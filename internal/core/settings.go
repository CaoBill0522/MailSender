package core

import (
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"mailsender/internal/store"
)

// Settings are runtime-tunable and stored in the DB.
type Settings struct {
	Strategy             string `json:"strategy"`               // weighted | round_robin | least_used | random
	MaxAttempts          int    `json:"max_attempts"`           // accounts tried per message
	FailThreshold        int    `json:"fail_threshold"`         // consecutive failures before cooldown
	CooldownMinutes      int    `json:"cooldown_minutes"`       // cooldown length
	RecipientIntervalSec int    `json:"recipient_interval_sec"` // min seconds between mails to the same recipient, 0 = off
	RecipientDailyMax    int    `json:"recipient_daily_max"`    // max mails per recipient per day, 0 = off
	HealthCheckMinutes   int    `json:"health_check_minutes"`   // periodic login test interval, 0 = off
	LogRetentionDays     int    `json:"log_retention_days"`     // message log retention, 0 = keep forever
	SendTimeoutSec       int    `json:"send_timeout_sec"`       // SMTP timeout per attempt

	// login protection (SMTP AUTH and the admin console)
	AuthMaxFails   int `json:"auth_max_fails"`   // failed logins from one IP within 15 minutes before it is banned, 0 = off
	AuthBanMinutes int `json:"auth_ban_minutes"` // ban length

	// bounce suppression
	SuppressBounces bool `json:"suppress_bounces"` // block recipients that permanently bounced
	SuppressDays    int  `json:"suppress_days"`    // how long an automatic entry lasts, 0 = forever

	// alerts
	AlertChannel     string `json:"alert_channel"`       // "" (off) | webhook | wecom | dingtalk | feishu | telegram
	AlertURL         string `json:"alert_url"`           // webhook / bot URL
	AlertTGToken     string `json:"alert_tg_token"`      // telegram bot token
	AlertTGChat      string `json:"alert_tg_chat"`       // telegram chat id
	AlertFailRatePct int    `json:"alert_fail_rate_pct"` // alert when the 15-minute failure rate exceeds this, 0 = off
}

func DefaultSettings() Settings {
	return Settings{
		Strategy:             "weighted",
		MaxAttempts:          3,
		FailThreshold:        3,
		CooldownMinutes:      30,
		RecipientIntervalSec: 60,
		RecipientDailyMax:    10,
		HealthCheckMinutes:   30,
		LogRetentionDays:     90,
		SendTimeoutSec:       20,
		AuthMaxFails:         5,
		AuthBanMinutes:       60,
		SuppressBounces:      true,
		SuppressDays:         180,
		AlertFailRatePct:     50,
	}
}

func (s Settings) Validate() error {
	switch s.Strategy {
	case "weighted", "round_robin", "least_used", "random":
	default:
		return errors.New("strategy must be weighted, round_robin, least_used or random")
	}
	if s.MaxAttempts < 1 || s.MaxAttempts > 10 {
		return errors.New("max_attempts must be 1-10")
	}
	if s.FailThreshold < 1 {
		return errors.New("fail_threshold must be >= 1")
	}
	if s.CooldownMinutes < 1 {
		return errors.New("cooldown_minutes must be >= 1")
	}
	if s.SendTimeoutSec < 3 || s.SendTimeoutSec > 120 {
		return errors.New("send_timeout_sec must be 3-120")
	}
	if s.RecipientIntervalSec < 0 || s.RecipientDailyMax < 0 || s.HealthCheckMinutes < 0 || s.LogRetentionDays < 0 ||
		s.AuthMaxFails < 0 || s.SuppressDays < 0 || s.AlertFailRatePct < 0 || s.AlertFailRatePct > 100 {
		return errors.New("values must not be negative")
	}
	if s.AuthMaxFails > 0 && s.AuthBanMinutes < 1 {
		return errors.New("auth_ban_minutes must be >= 1")
	}
	switch s.AlertChannel {
	case "":
	case "webhook", "wecom", "dingtalk", "feishu":
		if !validHTTPURL(s.AlertURL) {
			return errors.New("告警地址必须是 http(s):// 开头的 URL")
		}
	case "telegram":
		if !tgToken.MatchString(s.AlertTGToken) || strings.TrimSpace(s.AlertTGChat) == "" {
			return errors.New("Telegram 需要填写有效的 Bot Token（形如 123456:ABC-xyz）和 Chat ID")
		}
	default:
		return errors.New("alert_channel 无效")
	}
	return nil
}

type SettingsStore struct {
	mu  sync.RWMutex
	cur Settings
	db  *store.Store
}

const settingsKey = "settings"

func NewSettingsStore(db *store.Store) *SettingsStore {
	s := &SettingsStore{db: db, cur: DefaultSettings()}
	if v, ok := db.GetSetting(settingsKey); ok {
		_ = json.Unmarshal([]byte(v), &s.cur) // missing fields keep defaults
	}
	return s
}

func (s *SettingsStore) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

func (s *SettingsStore) Set(v Settings) error {
	if err := v.Validate(); err != nil {
		return err
	}
	b, _ := json.Marshal(v)
	if err := s.db.SetSetting(settingsKey, string(b)); err != nil {
		return err
	}
	s.mu.Lock()
	s.cur = v
	s.mu.Unlock()
	return nil
}

var tgToken = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)

func validHTTPURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
