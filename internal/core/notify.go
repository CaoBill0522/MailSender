package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

// Notifier sends operational alerts (account cooldown, pool exhausted, high failure rate, IP bans)
// to the channel configured in settings. Repeated alerts with the same key are throttled.
type Notifier struct {
	cfg    *SettingsStore
	client *http.Client
	mu     sync.Mutex
	last   map[string]time.Time
}

// Alert is one notification.
type Alert struct {
	Event string         `json:"event"` // account_cooldown | account_recovered | pool_unavailable | high_failure_rate | ip_banned | test
	Title string         `json:"title"`
	Text  string         `json:"text"`
	Data  map[string]any `json:"data,omitempty"`
	Time  time.Time      `json:"time"`
}

func NewNotifier(cfg *SettingsStore) *Notifier {
	return &Notifier{cfg: cfg, client: &http.Client{Timeout: 10 * time.Second}, last: map[string]time.Time{}}
}

// Notify sends a in the background unless an alert with the same key was sent within every.
func (n *Notifier) Notify(key string, every time.Duration, a Alert) {
	if n == nil || n.cfg.Get().AlertChannel == "" {
		return
	}
	n.mu.Lock()
	if t, ok := n.last[key]; ok && time.Since(t) < every {
		n.mu.Unlock()
		return
	}
	n.last[key] = time.Now()
	n.mu.Unlock()
	go func() {
		if err := n.Send(context.Background(), a); err != nil {
			log.Printf("alert %s: %v", a.Event, err)
		}
	}()
}

// Send delivers one alert synchronously with the saved settings.
func (n *Notifier) Send(ctx context.Context, a Alert) error { return n.SendWith(ctx, n.cfg.Get(), a) }

// SendWith delivers one alert using the given (possibly unsaved) settings — the console's test button.
func (n *Notifier) SendWith(ctx context.Context, set Settings, a Alert) error {
	if a.Time.IsZero() {
		a.Time = time.Now()
	}
	text := fmt.Sprintf("【MailSender】%s\n%s\n%s", a.Title, a.Text, a.Time.Format(time.DateTime))
	var target string
	var body any
	switch set.AlertChannel {
	case "":
		return fmt.Errorf("告警未开启")
	case "webhook":
		target, body = set.AlertURL, a
	case "wecom", "dingtalk":
		target, body = set.AlertURL, map[string]any{"msgtype": "text", "text": map[string]string{"content": text}}
	case "feishu":
		target, body = set.AlertURL, map[string]any{"msg_type": "text", "content": map[string]string{"text": text}}
	case "telegram":
		target = "https://api.telegram.org/bot" + set.AlertTGToken + "/sendMessage"
		body = map[string]any{"chat_id": set.AlertTGChat, "text": text, "disable_web_page_preview": true}
	default:
		return fmt.Errorf("unknown alert channel %q", set.AlertChannel)
	}
	b, _ := json.Marshal(body)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "MailSender-Alert/1")
	resp, err := n.client.Do(req)
	if err != nil {
		return redactErr(err, set.AlertTGToken)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(rb))
	}
	// WeCom / DingTalk / Feishu answer 200 with an error code in the body
	var r struct {
		ErrCode *int   `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
		Code    *int   `json:"code"`
		Msg     string `json:"msg"`
	}
	if json.Unmarshal(rb, &r) == nil {
		if r.ErrCode != nil && *r.ErrCode != 0 {
			return fmt.Errorf("errcode %d: %s", *r.ErrCode, r.ErrMsg)
		}
		if set.AlertChannel == "feishu" && r.Code != nil && *r.Code != 0 {
			return fmt.Errorf("code %d: %s", *r.Code, r.Msg)
		}
	}
	return nil
}

// redactErr keeps a bot token out of logs and API responses (url.Error includes the request URL).
func redactErr(err error, secret string) error {
	if secret == "" {
		return err
	}
	return fmt.Errorf("%s", bytes.ReplaceAll([]byte(err.Error()), []byte(secret), []byte("***")))
}
