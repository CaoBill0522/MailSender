package core

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"mailsender/internal/store"
)

// callbacks posts the final delivery status of each message to the API key's callback URL.
type callbacks struct {
	svc    *Service
	client *http.Client
	delays []time.Duration // wait before each try
}

func newCallbacks(s *Service) *callbacks {
	return &callbacks{svc: s, client: &http.Client{Timeout: 10 * time.Second},
		delays: []time.Duration{0, 10 * time.Second, time.Minute}}
}

// CallbackPayload is the JSON body of a delivery callback.
type CallbackPayload struct {
	Event      string     `json:"event"` // message.sent | message.failed
	ID         uint       `json:"id"`
	Ref        string     `json:"ref"`        // X-MS-Ref header set by the client
	MessageID  string     `json:"message_id"` // Message-ID header as delivered
	To         string     `json:"to"`
	Subject    string     `json:"subject"`
	Status     string     `json:"status"`
	Permanent  bool       `json:"permanent"` // the recipient was rejected permanently (bad address)
	Attempts   int        `json:"attempts"`
	Error      string     `json:"error,omitempty"`
	From       string     `json:"from,omitempty"`
	DurationMs int64      `json:"duration_ms"`
	CreatedAt  time.Time  `json:"created_at"`
	SentAt     *time.Time `json:"sent_at,omitempty"`
}

func (cb *callbacks) fire(key *store.APIKey, m *store.Message) {
	if key.CallbackURL == "" {
		return
	}
	secret, err := cb.svc.Box.Decrypt(key.CallbackSecret)
	if err != nil {
		log.Printf("callback secret for key %d: %v", key.ID, err)
		return
	}
	ev := "message.sent"
	if m.Status != store.StatusSent {
		ev = "message.failed"
	}
	body, _ := json.Marshal(CallbackPayload{Event: ev, ID: m.ID, Ref: m.Ref, MessageID: m.MsgID, To: m.To, Subject: m.Subject,
		Status: m.Status, Permanent: m.Permanent, Attempts: m.Attempts, Error: m.Error, From: m.FromAddr,
		DurationMs: m.DurationMs, CreatedAt: m.CreatedAt, SentAt: m.SentAt})
	go func(url string, id uint) {
		var last error
		for i, d := range cb.delays {
			time.Sleep(d)
			if last = cb.post(url, secret, ev, body); last == nil {
				cb.svc.DB.DB.Model(&store.Message{}).Where("id = ?", id).UpdateColumn("callback", "ok")
				return
			}
			if i == 0 {
				cb.svc.DB.DB.Model(&store.Message{}).Where("id = ?", id).UpdateColumn("callback", "retrying: "+trimText(last.Error(), 200))
			}
		}
		log.Printf("callback for message %d failed: %v", id, last)
		cb.svc.DB.DB.Model(&store.Message{}).Where("id = ?", id).UpdateColumn("callback", "failed: "+trimText(last.Error(), 200))
	}(key.CallbackURL, m.ID)
}

func (cb *callbacks) post(url, secret, event string, body []byte) error {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "MailSender-Callback/1")
	req.Header.Set("X-MailSender-Event", event)
	req.Header.Set("X-MailSender-Timestamp", ts)
	req.Header.Set("X-MailSender-Signature", "sha256="+SignCallback(secret, ts, body))
	resp, err := cb.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// SignCallback returns hex(HMAC-SHA256(secret, timestamp + "." + body)).
func SignCallback(secret, ts string, body []byte) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(ts + "."))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// TestCallback posts a sample "message.test" event to the key's callback URL and returns the result.
func (s *Service) TestCallback(key *store.APIKey) error {
	if key.CallbackURL == "" {
		return fmt.Errorf("未设置回调地址")
	}
	secret, err := s.Box.Decrypt(key.CallbackSecret)
	if err != nil {
		return err
	}
	now := time.Now()
	body, _ := json.Marshal(CallbackPayload{Event: "message.test", Ref: "test", MessageID: "<test@mailsender>", To: "test@example.com",
		Subject: "MailSender 回调测试", Status: store.StatusSent, Attempts: 1, CreatedAt: now, SentAt: &now})
	return s.hooks.post(key.CallbackURL, secret, "message.test", body)
}
