package store

import "time"

// Account is one SMTP sender account in the rotation pool.
type Account struct {
	ID             uint   `gorm:"primaryKey" json:"id"`
	Name           string `json:"name"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Security       string `json:"security"`  // ssl | starttls | none
	AuthType       string `json:"auth_type"` // auto | plain | login | cram-md5 | none
	Username       string `json:"username"`
	PasswordEnc    string `json:"-"`
	FromAddr       string `json:"from_addr"`
	FromName       string `json:"from_name"`
	Weight         int    `json:"weight"`
	LimitPerMinute int    `json:"limit_per_minute"` // 0 = unlimited
	LimitPerHour   int    `json:"limit_per_hour"`
	LimitPerDay    int    `json:"limit_per_day"`
	Enabled        bool   `json:"enabled"`
	Remark         string `json:"remark"`
	Groups         string `gorm:"column:group_tags" json:"groups"` // comma separated tags; API keys bound to groups only use matching accounts
	PreferDomains  string `json:"prefer_domains"`                  // comma separated recipient domains this account is tried first for

	// health state, maintained by the scheduler
	ConsecutiveFails int        `json:"consecutive_fails"`
	CooldownUntil    *time.Time `json:"cooldown_until"`
	LastError        string     `json:"last_error"`
	LastUsedAt       *time.Time `json:"last_used_at"`
	LastCheckAt      *time.Time `json:"last_check_at"`
	LastCheckOK      bool       `json:"last_check_ok"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// APIKey authenticates a client program calling the send API.
type APIKey struct {
	ID              uint       `gorm:"primaryKey" json:"id"`
	Name            string     `json:"name"`
	Prefix          string     `json:"prefix"` // first chars of the key, for display
	KeyHash         string     `gorm:"uniqueIndex" json:"-"`
	Enabled         bool       `json:"enabled"`
	RateLimitPerMin int        `json:"rate_limit_per_min"`              // 0 = unlimited
	IPWhitelist     string     `json:"ip_whitelist"`                    // comma separated IPs / CIDRs, empty = any
	Groups          string     `gorm:"column:group_tags" json:"groups"` // comma separated account groups this key may use, empty = all
	CallbackURL     string     `json:"callback_url"`                    // delivery status webhook, empty = off
	CallbackSecret  string     `json:"-"`                               // encrypted HMAC secret for signing callbacks
	Remark          string     `json:"remark"`
	LastUsedAt      *time.Time `json:"last_used_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

const (
	StatusQueued = "queued"
	StatusSent   = "sent"
	StatusFailed = "failed"
)

// Message is one mail send request (one recipient).
type Message struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	APIKeyID   uint       `gorm:"index" json:"api_key_id"`
	APIKeyName string     `json:"api_key_name"`
	AccountID  uint       `gorm:"index" json:"account_id"` // account that finally delivered (or last tried)
	FromAddr   string     `json:"from_addr"`
	To         string     `gorm:"index" json:"to"`
	Subject    string     `json:"subject"`
	Source     string     `json:"source"` // api | smtp
	Status     string     `gorm:"index" json:"status"`
	Attempts   int        `json:"attempts"`
	Error      string     `json:"error"`
	DurationMs int64      `json:"duration_ms"`
	ClientIP   string     `json:"client_ip"`
	Ref        string     `json:"ref"`        // client reference from the X-MS-Ref header
	MsgID      string     `json:"msg_id"`     // Message-ID header as delivered
	Callback   string     `json:"callback"`   // delivery callback result: "", ok, or the error
	Permanent  bool       `gorm:"-" json:"-"` // set by delivery when the failure is permanent (bad recipient)
	CreatedAt  time.Time  `gorm:"index" json:"created_at"`
	SentAt     *time.Time `json:"sent_at"`
}

// SendAttempt is one try of a message on one account.
type SendAttempt struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	MessageID  uint      `gorm:"index" json:"message_id"`
	AccountID  uint      `gorm:"index:idx_att_acc_time" json:"account_id"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error"`
	DurationMs int64     `json:"duration_ms"`
	CreatedAt  time.Time `gorm:"index:idx_att_acc_time" json:"created_at"`
}

// DailyStat is a pre-aggregated counter row per (day, api key, account).
type DailyStat struct {
	Day         string `gorm:"primaryKey;size:10" json:"day"` // YYYY-MM-DD, server local time
	APIKeyID    uint   `gorm:"primaryKey;autoIncrement:false" json:"api_key_id"`
	AccountID   uint   `gorm:"primaryKey;autoIncrement:false" json:"account_id"`
	MsgSent     int64  `json:"msg_sent"`
	MsgFailed   int64  `json:"msg_failed"`
	MsgRejected int64  `json:"msg_rejected"` // blocked by rate limit before sending
	AttOK       int64  `json:"att_ok"`
	AttFail     int64  `json:"att_fail"`
	AttMsSum    int64  `json:"att_ms_sum"` // sum of successful attempt durations
}

// Suppression blocks a recipient address, normally after a permanent bounce, to protect account reputation.
type Suppression struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	Addr      string     `gorm:"uniqueIndex" json:"addr"` // lower case
	Reason    string     `json:"reason"`
	Source    string     `json:"source"` // bounce | manual
	MessageID uint       `json:"message_id"`
	Hits      int        `json:"hits"` // mails blocked since
	ExpiresAt *time.Time `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// Setting is a key/value row for runtime settings and admin credentials.
type Setting struct {
	Key   string `gorm:"primaryKey"`
	Value string
}
