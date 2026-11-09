package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	htmltpl "html/template"
	"log"
	netmail "net/mail"
	"strings"
	"sync"
	texttpl "text/template"
	"time"

	"gorm.io/gorm"

	"mailsender/internal/mailer"
	"mailsender/internal/secure"
	"mailsender/internal/store"
)

// APIError is returned to API clients with an HTTP status.
type APIError struct {
	Status int    `json:"-"`
	Code   string `json:"code"`
	Msg    string `json:"message"`
}

func (e *APIError) Error() string { return e.Msg }

func apiErr(status int, code, format string, a ...any) *APIError {
	return &APIError{Status: status, Code: code, Msg: fmt.Sprintf(format, a...)}
}

type SendRequest struct {
	To       string         `json:"to"`
	Subject  string         `json:"subject"`
	HTML     string         `json:"html"`
	Text     string         `json:"text"`
	Template string         `json:"template"`
	Vars     map[string]any `json:"vars"`
	FromName string         `json:"from_name"`
	Async    bool           `json:"async"`

	secret string // value masked out of the stored subject (verification code)
}

type CodeRequest struct {
	To       string         `json:"to"`
	Code     string         `json:"code"`     // optional; generated when empty
	Length   int            `json:"length"`   // generated code length, default 6
	Minutes  int            `json:"minutes"`  // validity shown in the mail, default 10
	Template string         `json:"template"` // default "verify_code"
	Vars     map[string]any `json:"vars"`
	FromName string         `json:"from_name"`
	Async    bool           `json:"async"`
}

type Service struct {
	DB    *store.Store
	Sched *Scheduler
	Cfg   *SettingsStore
	rcpt  *RecipientLimiter
	Keys  *KeyLimiter

	queue    chan uint
	inflight sync.Map // message id -> struct{}
}

const DefaultCodeTemplate = "verify_code"

func NewService(db *store.Store, sched *Scheduler, cfg *SettingsStore) *Service {
	return &Service{
		DB: db, Sched: sched, Cfg: cfg,
		rcpt:  NewRecipientLimiter(),
		Keys:  NewKeyLimiter(),
		queue: make(chan uint, 10000),
	}
}

// Start launches async workers and background jobs.
func (s *Service) Start(ctx context.Context, workers int) {
	for i := 0; i < workers; i++ {
		go s.worker(ctx)
	}
	go s.sweeper(ctx)
	go s.maintenance(ctx)
}

func (s *Service) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-s.queue:
			var m store.Message
			if err := s.DB.DB.First(&m, id).Error; err == nil && m.Status == store.StatusQueued {
				s.deliver(&m, true)
			}
			s.inflight.Delete(id)
		}
	}
}

func (s *Service) enqueue(id uint) bool {
	if _, loaded := s.inflight.LoadOrStore(id, struct{}{}); loaded {
		return true
	}
	select {
	case s.queue <- id:
		return true
	default:
		s.inflight.Delete(id)
		return false // stays queued in DB; the sweeper retries
	}
}

// sweeper re-enqueues queued messages left over from a restart or a full queue.
func (s *Service) sweeper(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		var ids []uint
		s.DB.DB.Model(&store.Message{}).Where("status = ? AND created_at < ?", store.StatusQueued, time.Now().Add(-20*time.Second)).
			Order("id").Limit(1000).Pluck("id", &ids)
		for _, id := range ids {
			if !s.enqueue(id) {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Service) maintenance(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	var lastCheck, lastClean time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		set := s.Cfg.Get()
		s.rcpt.GC(time.Duration(set.RecipientIntervalSec) * time.Second)
		if set.HealthCheckMinutes > 0 && time.Since(lastCheck) >= time.Duration(set.HealthCheckMinutes)*time.Minute {
			lastCheck = time.Now()
			s.HealthCheckAll()
		}
		if time.Since(lastClean) >= 6*time.Hour {
			lastClean = time.Now()
			if n, err := s.DB.Cleanup(set.LogRetentionDays); err != nil {
				log.Printf("cleanup: %v", err)
			} else if n > 0 {
				log.Printf("cleanup: removed %d old messages", n)
			}
		}
	}
}

// HealthCheckAll login-tests every enabled account (max 4 at a time).
func (s *Service) HealthCheckAll() {
	var ids []uint
	s.DB.DB.Model(&store.Account{}).Where("enabled = ?", true).Pluck("id", &ids)
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func(id uint) {
			defer wg.Done()
			defer func() { <-sem }()
			_ = s.TestAccount(id)
		}(id)
	}
	wg.Wait()
}

// TestAccount verifies login for one account and records the result.
func (s *Service) TestAccount(id uint) error {
	lease, err := s.Sched.Credentials(id)
	if err != nil {
		return err
	}
	timeout := time.Duration(s.Cfg.Get().SendTimeoutSec) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout+5*time.Second)
	defer cancel()
	err = mailer.Test(ctx, &lease.Account, lease.Password, timeout)
	s.Sched.RecordCheck(id, err)
	return err
}

// TestSend sends a test message through one specific account, bypassing rotation.
func (s *Service) TestSend(id uint, to string) error {
	lease, err := s.Sched.Credentials(id)
	if err != nil {
		return err
	}
	addr, err := netmail.ParseAddress(to)
	if err != nil {
		return fmt.Errorf("invalid recipient: %w", err)
	}
	timeout := time.Duration(s.Cfg.Get().SendTimeoutSec) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout+5*time.Second)
	defer cancel()
	return mailer.Send(ctx, &lease.Account, lease.Password, mailer.Content{
		To:      addr.Address,
		Subject: "MailSender 测试邮件",
		Text:    fmt.Sprintf("这是一封来自 MailSender 的测试邮件。\n发件账号：%s <%s>\n时间：%s", lease.Account.Name, lease.Account.FromAddr, time.Now().Format(time.DateTime)),
	}, timeout)
}

// Render builds mail content from a template or raw fields.
func (s *Service) Render(req SendRequest) (mailer.Content, string, error) {
	c := mailer.Content{FromName: req.FromName, Subject: req.Subject, HTML: req.HTML, Text: req.Text}
	if req.Template == "" {
		if strings.TrimSpace(c.Subject) == "" {
			return c, "", apiErr(400, "bad_request", "subject is required")
		}
		if c.HTML == "" && c.Text == "" {
			return c, "", apiErr(400, "bad_request", "html or text is required")
		}
		return c, "", nil
	}
	var t store.Template
	if err := s.DB.DB.Where("name = ?", req.Template).First(&t).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c, "", apiErr(400, "template_not_found", "template %q not found", req.Template)
		}
		return c, "", err
	}
	return RenderTemplate(t, req.Vars, req.FromName)
}

// RenderTemplate executes a template with vars. Subject/text use text/template, HTML is auto-escaped.
func RenderTemplate(t store.Template, vars map[string]any, fromName string) (mailer.Content, string, error) {
	if vars == nil {
		vars = map[string]any{}
	}
	c := mailer.Content{FromName: fromName}
	var err error
	if c.Subject, err = execText(t.Subject, vars); err != nil {
		return c, t.Name, apiErr(400, "template_error", "subject: %v", err)
	}
	if c.Text, err = execText(t.Text, vars); err != nil {
		return c, t.Name, apiErr(400, "template_error", "text: %v", err)
	}
	if t.HTML != "" {
		tp, err := htmltpl.New("h").Option("missingkey=zero").Parse(t.HTML)
		if err != nil {
			return c, t.Name, apiErr(400, "template_error", "html: %v", err)
		}
		var b bytes.Buffer
		if err := tp.Execute(&b, vars); err != nil {
			return c, t.Name, apiErr(400, "template_error", "html: %v", err)
		}
		c.HTML = b.String()
	}
	return c, t.Name, nil
}

func execText(src string, vars map[string]any) (string, error) {
	if src == "" {
		return "", nil
	}
	tp, err := texttpl.New("t").Option("missingkey=zero").Parse(src)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := tp.Execute(&b, vars); err != nil {
		return "", err
	}
	return strings.ReplaceAll(b.String(), "<no value>", ""), nil
}

// Submit validates, rate-limits and records a send request, then delivers it (sync) or queues it (async).
func (s *Service) Submit(key *store.APIKey, clientIP string, req SendRequest) (*store.Message, error) {
	addr, err := netmail.ParseAddress(strings.TrimSpace(req.To))
	if err != nil || strings.ContainsAny(req.To, ",;") {
		return nil, apiErr(400, "bad_request", "to must be a single valid email address")
	}
	req.To = addr.Address

	content, tplName, err := s.Render(req)
	if err != nil {
		return nil, err
	}
	content.To = req.To
	if strings.TrimSpace(content.Subject) == "" {
		return nil, apiErr(400, "bad_request", "rendered subject is empty")
	}

	set := s.Cfg.Get()
	if ok, wait := s.rcpt.Allow(req.To, time.Duration(set.RecipientIntervalSec)*time.Second, set.RecipientDailyMax); !ok {
		_ = s.DB.IncStat(store.Day(time.Now()), key.ID, 0, store.DailyStat{MsgRejected: 1})
		if wait < 0 {
			return nil, apiErr(429, "recipient_daily_limit", "daily limit for this recipient reached")
		}
		return nil, apiErr(429, "recipient_too_frequent", "too frequent for this recipient, retry in %ds", int(wait.Seconds())+1)
	}

	payload, _ := json.Marshal(content)
	logSubject := content.Subject
	if req.secret != "" {
		logSubject = strings.ReplaceAll(logSubject, req.secret, strings.Repeat("*", len(req.secret)))
	}
	m := &store.Message{
		APIKeyID: key.ID, APIKeyName: key.Name,
		To: req.To, Subject: logSubject, Template: tplName,
		Status: store.StatusQueued, ClientIP: clientIP, Payload: string(payload),
	}
	if err := s.DB.DB.Create(m).Error; err != nil {
		return nil, err
	}
	if req.Async {
		s.enqueue(m.ID)
		return m, nil
	}
	s.inflight.Store(m.ID, struct{}{})
	defer s.inflight.Delete(m.ID)
	s.deliver(m, false)
	return m, nil
}

// SendCode is a convenience wrapper for verification-code mails.
func (s *Service) SendCode(key *store.APIKey, clientIP string, req CodeRequest) (*store.Message, string, error) {
	code := strings.TrimSpace(req.Code)
	if code == "" {
		n := req.Length
		if n <= 0 {
			n = 6
		}
		if n > 12 {
			n = 12
		}
		code = secure.RandomDigits(n)
	}
	minutes := req.Minutes
	if minutes <= 0 {
		minutes = 10
	}
	vars := map[string]any{}
	for k, v := range req.Vars {
		vars[k] = v
	}
	vars["code"], vars["minutes"], vars["email"] = code, minutes, req.To
	tpl := req.Template
	if tpl == "" {
		tpl = DefaultCodeTemplate
	}
	m, err := s.Submit(key, clientIP, SendRequest{To: req.To, Template: tpl, Vars: vars, FromName: req.FromName, Async: req.Async, secret: code})
	return m, code, err
}

// MaxQueueWait is how long an async message may wait for a free account before it is failed.
const MaxQueueWait = 30 * time.Minute

// deliver tries up to MaxAttempts different accounts until one succeeds.
// With canDefer, a message that finds every account busy (quota/cooldown) stays queued for the sweeper.
func (s *Service) deliver(m *store.Message, canDefer bool) {
	var c mailer.Content
	if err := json.Unmarshal([]byte(m.Payload), &c); err != nil {
		s.finish(m, false, "corrupt payload: "+err.Error(), 0, "", time.Now())
		return
	}
	set := s.Cfg.Get()
	timeout := time.Duration(set.SendTimeoutSec) * time.Second
	start := time.Now()
	exclude := map[uint]bool{}
	var errs []string
	var lastAcc uint
	var from string
	ok := false
	for i := 0; i < set.MaxAttempts; i++ {
		lease, err := s.Sched.Acquire(exclude)
		if err != nil {
			if i == 0 && canDefer && time.Since(m.CreatedAt) < MaxQueueWait {
				s.DB.DB.Model(m).UpdateColumn("error", "waiting: "+err.Error())
				return
			}
			if len(errs) == 0 || !errors.Is(err, ErrNoAccount) || !strings.Contains(err.Error(), "already tried") {
				errs = append(errs, err.Error())
			}
			break
		}
		acc := lease.Account
		exclude[acc.ID] = true
		t0 := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), timeout+5*time.Second)
		err = mailer.Send(ctx, &acc, lease.Password, c, timeout)
		cancel()
		dur := time.Since(t0).Milliseconds()
		kind := mailer.Classify(err)
		m.Attempts++
		lastAcc, from = acc.ID, acc.FromAddr

		att := store.SendAttempt{MessageID: m.ID, AccountID: acc.ID, OK: err == nil, Error: mailer.Short(err), DurationMs: dur, CreatedAt: t0}
		if e := s.DB.DB.Create(&att).Error; e != nil {
			log.Printf("save attempt: %v", e)
		}
		st := store.DailyStat{AttOK: 1, AttMsSum: dur}
		if err != nil {
			st = store.DailyStat{AttFail: 1}
		}
		_ = s.DB.IncStat(store.Day(t0), m.APIKeyID, acc.ID, st)
		s.Sched.Report(acc.ID, kind, mailer.Short(err))

		if err == nil {
			ok = true
			break
		}
		errs = append(errs, fmt.Sprintf("[%s] %s", acc.Name, mailer.Short(err)))
		if kind == mailer.KindRecipient {
			break
		}
	}
	m.AccountID, m.FromAddr = lastAcc, from
	errMsg := ""
	if !ok {
		errMsg = strings.Join(errs, " | ")
	}
	s.finish(m, ok, errMsg, time.Since(start).Milliseconds(), from, start)
}

func (s *Service) finish(m *store.Message, ok bool, errMsg string, durMs int64, from string, start time.Time) {
	now := time.Now()
	m.Status, m.Error, m.DurationMs, m.Payload = store.StatusFailed, errMsg, durMs, ""
	st := store.DailyStat{MsgFailed: 1}
	if ok {
		m.Status, m.SentAt = store.StatusSent, &now
		st = store.DailyStat{MsgSent: 1}
	}
	if err := s.DB.DB.Model(m).Select("account_id", "from_addr", "status", "attempts", "error", "duration_ms", "payload", "sent_at").Updates(m).Error; err != nil {
		log.Printf("save message %d: %v", m.ID, err)
	}
	_ = s.DB.IncStat(store.Day(start), m.APIKeyID, m.AccountID, st)
}

// SeedDefaults creates the built-in verification-code template on first run.
func SeedDefaults(db *store.Store) error {
	var n int64
	db.DB.Model(&store.Template{}).Where("name = ?", DefaultCodeTemplate).Count(&n)
	if n > 0 {
		return nil
	}
	return db.DB.Create(&store.Template{
		Name:    DefaultCodeTemplate,
		Subject: "您的验证码：{{.code}}",
		Text:    "您的验证码是：{{.code}}\n{{.minutes}} 分钟内有效，请勿泄露给他人。\n如非本人操作，请忽略本邮件。",
		HTML: `<div style="max-width:480px;margin:0 auto;padding:24px;font-family:-apple-system,'PingFang SC','Microsoft YaHei',sans-serif;color:#222">
  <p style="font-size:15px">您好，</p>
  <p style="font-size:15px">您的验证码是：</p>
  <p style="font-size:32px;font-weight:700;letter-spacing:6px;margin:16px 0">{{.code}}</p>
  <p style="font-size:13px;color:#666">{{.minutes}} 分钟内有效，请勿泄露给他人。如非本人操作，请忽略本邮件。</p>
</div>`,
		Remark: "内置验证码模板，变量：code、minutes、email",
	}).Error
}
