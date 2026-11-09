package core

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	netmail "net/mail"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"mailsender/internal/mailer"
	"mailsender/internal/secure"
	"mailsender/internal/store"
)

// Service relays mail received by the SMTP ingress through the account pool.
type Service struct {
	DB     *store.Store
	Sched  *Scheduler
	Cfg    *SettingsStore
	Box    *secure.Box
	Guard  *AuthGuard
	Notify *Notifier
	rcpt   *RecipientLimiter
	Keys   *KeyLimiter
	hooks  *callbacks

	keyTouch sync.Map // api key id -> last time last_used_at was persisted
}

func NewService(db *store.Store, sched *Scheduler, cfg *SettingsStore, box *secure.Box) *Service {
	s := &Service{DB: db, Sched: sched, Cfg: cfg, Box: box, Guard: NewAuthGuard(cfg), Notify: NewNotifier(cfg),
		rcpt: NewRecipientLimiter(), Keys: NewKeyLimiter()}
	s.hooks = newCallbacks(s)
	sched.OnCooldown = func(a store.Account, until time.Time) {
		s.Notify.Notify(fmt.Sprintf("cool:%d", a.ID), 30*time.Minute, Alert{
			Event: "account_cooldown", Title: "账号进入冷却：" + a.Name,
			Text: fmt.Sprintf("%s <%s> 连续失败 %d 次，暂停使用到 %s。\n最近错误：%s", a.Name, a.FromAddr, a.ConsecutiveFails, until.Format("15:04"), a.LastError),
			Data: map[string]any{"account_id": a.ID, "account": a.Name, "from": a.FromAddr, "until": until, "error": a.LastError},
		})
	}
	sched.OnRecover = func(a store.Account) {
		s.Notify.Notify(fmt.Sprintf("recover:%d", a.ID), 30*time.Minute, Alert{
			Event: "account_recovered", Title: "账号已恢复：" + a.Name,
			Text: fmt.Sprintf("%s <%s> 冷却后已成功发信，恢复正常轮询。", a.Name, a.FromAddr),
			Data: map[string]any{"account_id": a.ID, "account": a.Name, "from": a.FromAddr},
		})
	}
	s.Guard.OnBan = func(scope, ip string, fails int, until time.Time) {
		where := map[string]string{"smtp": "SMTP 登录", "admin": "后台登录"}[scope]
		log.Printf("banned %s on %s after %d failed logins until %s", ip, scope, fails, until.Format(time.DateTime))
		s.Notify.Notify("ban:"+scope, 30*time.Minute, Alert{
			Event: "ip_banned", Title: "IP 已封禁：" + ip,
			Text: fmt.Sprintf("%s 在 15 分钟内%s失败 %d 次，封禁到 %s。30 分钟内的其他封禁不再单独提醒，可在后台「安全」页查看。", ip, where, fails, until.Format("15:04")),
			Data: map[string]any{"scope": scope, "ip": ip, "fails": fails, "until": until},
		})
	}
	return s
}

// Start runs background jobs (health checks, log cleanup, limiter GC, failure-rate alerts).
func (s *Service) Start(ctx context.Context) {
	// Delivery is synchronous, so anything still queued was interrupted by a restart. The SMTP client
	// never got a success reply and will retry, so mark these failed instead of sending duplicates.
	res := s.DB.DB.Model(&store.Message{}).Where("status = ?", store.StatusQueued).
		Updates(map[string]any{"status": store.StatusFailed, "error": "interrupted by service restart (client was not told it succeeded)"})
	if res.RowsAffected > 0 {
		log.Printf("marked %d interrupted messages as failed", res.RowsAffected)
	}
	go s.maintenance(ctx)
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
		s.Guard.GC()
		s.checkFailureRate(set)
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
			s.DB.DB.Where("expires_at IS NOT NULL AND expires_at < ?", time.Now()).Delete(&store.Suppression{})
		}
	}
}

// checkFailureRate alerts when most recent messages fail (at least 10 in the last 15 minutes).
func (s *Service) checkFailureRate(set Settings) {
	if set.AlertChannel == "" || set.AlertFailRatePct <= 0 {
		return
	}
	var r struct{ Total, Failed int64 }
	s.DB.DB.Model(&store.Message{}).Select("COUNT(*) AS total, SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS failed", store.StatusFailed).
		Where("created_at >= ? AND status <> ?", time.Now().Add(-15*time.Minute), store.StatusQueued).Scan(&r)
	if r.Total < 10 || r.Failed*100 < r.Total*int64(set.AlertFailRatePct) {
		return
	}
	var last store.Message
	s.DB.DB.Where("status = ?", store.StatusFailed).Order("id DESC").First(&last)
	s.Notify.Notify("failrate", time.Hour, Alert{
		Event: "high_failure_rate", Title: fmt.Sprintf("失败率过高：%d%%", r.Failed*100/r.Total),
		Text: fmt.Sprintf("最近 15 分钟 %d 封中失败 %d 封。\n最近一次错误：%s", r.Total, r.Failed, trimText(last.Error, 300)),
		Data: map[string]any{"total": r.Total, "failed": r.Failed, "last_error": last.Error},
	})
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
	dial, err := s.DialerFor(&lease.Account)
	if err == nil {
		err = mailer.Test(ctx, &lease.Account, lease.Password, dial, timeout)
	}
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
	dial, err := s.DialerFor(&lease.Account)
	if err != nil {
		return err
	}
	return mailer.Send(ctx, &lease.Account, lease.Password, dial, mailer.Content{
		To:      addr.Address,
		Subject: "MailSender 测试邮件",
		Text:    fmt.Sprintf("这是一封来自 MailSender 的测试邮件。\n发件账号：%s <%s>\n时间：%s", lease.Account.Name, lease.Account.FromAddr, time.Now().Format(time.DateTime)),
	}, timeout)
}

// ErrSuppressed means the recipient is on the suppression list (permanent).
var ErrSuppressed = errors.New("recipient is on the suppression list")

// CheckRecipient rejects suppressed addresses, then applies the per-recipient frequency limits and reserves a slot.
func (s *Service) CheckRecipient(key *store.APIKey, to string) error {
	if sup, ok := s.Suppressed(to); ok {
		s.DB.DB.Model(&store.Suppression{}).Where("id = ?", sup.ID).UpdateColumn("hits", gorm.Expr("hits + 1"))
		_ = s.DB.IncStat(store.Day(time.Now()), key.ID, 0, store.DailyStat{MsgRejected: 1})
		return fmt.Errorf("%w (%s)", ErrSuppressed, trimText(sup.Reason, 120))
	}
	set := s.Cfg.Get()
	if ok, wait := s.rcpt.Allow(to, time.Duration(set.RecipientIntervalSec)*time.Second, set.RecipientDailyMax); !ok {
		_ = s.DB.IncStat(store.Day(time.Now()), key.ID, 0, store.DailyStat{MsgRejected: 1})
		if wait < 0 {
			return errors.New("daily limit for this recipient reached")
		}
		return fmt.Errorf("too frequent for this recipient, retry in %ds", int(wait.Seconds())+1)
	}
	return nil
}

// Suppressed returns the active suppression entry for addr, if any.
func (s *Service) Suppressed(addr string) (*store.Suppression, bool) {
	var sup store.Suppression
	err := s.DB.DB.Where("addr = ? AND (expires_at IS NULL OR expires_at > ?)", strings.ToLower(strings.TrimSpace(addr)), time.Now()).
		Limit(1).Find(&sup).Error
	return &sup, err == nil && sup.ID != 0
}

// Suppress adds (or refreshes) a suppression entry. days <= 0 means forever.
func (s *Service) Suppress(addr, reason, source string, msgID uint, days int) error {
	sup := store.Suppression{Addr: strings.ToLower(strings.TrimSpace(addr)), Reason: trimText(reason, 500), Source: source, MessageID: msgID}
	if days > 0 {
		t := time.Now().AddDate(0, 0, days)
		sup.ExpiresAt = &t
	}
	var old store.Suppression
	if s.DB.DB.Where("addr = ?", sup.Addr).Limit(1).Find(&old); old.ID != 0 {
		return s.DB.DB.Model(&old).Select("reason", "source", "message_id", "expires_at", "created_at").
			Updates(&store.Suppression{Reason: sup.Reason, Source: sup.Source, MessageID: msgID, ExpiresAt: sup.ExpiresAt, CreatedAt: time.Now()}).Error
	}
	return s.DB.DB.Create(&sup).Error
}

// Relay delivers a message received over SMTP to each recipient (one tracked message per recipient)
// and waits for the results. Recipient limits must already have been checked at RCPT time.
func (s *Service) Relay(key *store.APIKey, clientIP string, rcpts []string, raw []byte) ([]*store.Message, error) {
	meta := mailer.MetaOf(raw)
	msgs := make([]*store.Message, len(rcpts))
	for i, to := range rcpts {
		m := &store.Message{
			APIKeyID: key.ID, APIKeyName: key.Name, Source: "smtp",
			To: to, Subject: meta.Subject, Status: store.StatusQueued, ClientIP: clientIP, Ref: meta.Ref, MsgID: meta.MsgID,
		}
		if err := s.DB.DB.Create(m).Error; err != nil {
			return nil, err
		}
		msgs[i] = m
	}
	groups := SplitList(key.Groups)
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, m := range msgs {
		wg.Add(1)
		sem <- struct{}{}
		go func(m *store.Message) {
			defer wg.Done()
			defer func() { <-sem }()
			c := mailer.Content{To: m.To, Subject: meta.Subject, Raw: raw}
			if meta.MsgID == "" {
				c.MsgIDLocal = secure.RandomString(24)
			}
			s.deliver(m, c, Route{Groups: groups, Domain: domainOf(m.To)})
			s.hooks.fire(key, m)
		}(m)
	}
	wg.Wait()
	return msgs, nil
}

// AuthKey resolves a plaintext API key and checks that it is enabled and allowed from clientIP.
func (s *Service) AuthKey(raw, clientIP string) (*store.APIKey, error) {
	if raw == "" {
		return nil, errors.New("missing API key")
	}
	var k store.APIKey
	if err := s.DB.DB.Where("key_hash = ?", secure.HashAPIKey(raw)).First(&k).Error; err != nil || !k.Enabled {
		return nil, errors.New("invalid or disabled API key")
	}
	if !IPAllowed(k.IPWhitelist, clientIP) {
		return nil, fmt.Errorf("client IP not in whitelist: %s", clientIP)
	}
	now := time.Now()
	if v, ok := s.keyTouch.Load(k.ID); !ok || now.Sub(v.(time.Time)) > time.Minute {
		s.keyTouch.Store(k.ID, now)
		s.DB.DB.Model(&store.APIKey{}).Where("id = ?", k.ID).UpdateColumn("last_used_at", now)
	}
	return &k, nil
}

// AllowKeyRate applies the key's mails-per-minute limit.
func (s *Service) AllowKeyRate(k *store.APIKey) error {
	if !s.Keys.Allow(k.ID, k.RateLimitPerMin) {
		return errors.New("API key rate limit exceeded")
	}
	return nil
}

// IPAllowed reports whether ip matches a comma/space/newline separated list of IPs and CIDRs (empty = any).
func IPAllowed(list, ip string) bool {
	list = strings.TrimSpace(list)
	if list == "" {
		return true
	}
	addr := net.ParseIP(ip)
	for _, item := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' }) {
		if strings.Contains(item, "/") {
			if _, n, err := net.ParseCIDR(item); err == nil && addr != nil && n.Contains(addr) {
				return true
			}
		} else if p := net.ParseIP(item); p != nil && addr != nil && p.Equal(addr) {
			return true
		}
	}
	return false
}

// deliver tries up to MaxAttempts different accounts until one succeeds.
func (s *Service) deliver(m *store.Message, c mailer.Content, rt Route) {
	set := s.Cfg.Get()
	timeout := time.Duration(set.SendTimeoutSec) * time.Second
	start := time.Now()
	exclude := map[uint]bool{}
	var errs []string
	var lastAcc uint
	var from string
	ok := false
	for i := 0; i < set.MaxAttempts; i++ {
		lease, err := s.Sched.Acquire(exclude, rt)
		if err != nil {
			if len(errs) == 0 || !errors.Is(err, ErrNoAccount) || !strings.Contains(err.Error(), "already tried") {
				errs = append(errs, err.Error())
			}
			if i == 0 && errors.Is(err, ErrNoAccount) {
				title, scope := "没有可用的发件账号", ""
				if len(rt.Groups) > 0 {
					scope = strings.Join(rt.Groups, ",")
					title += "（分组 " + scope + "）"
				}
				s.Notify.Notify("pool:"+scope, 30*time.Minute, Alert{
					Event: "pool_unavailable", Title: title,
					Text: fmt.Sprintf("发给 %s 的邮件无法发送：%s。\n客户端收到 451，会稍后重试。", m.To, err),
					Data: map[string]any{"to": m.To, "error": err.Error(), "groups": rt.Groups},
				})
			}
			break
		}
		acc := lease.Account
		exclude[acc.ID] = true
		t0 := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), timeout+5*time.Second)
		var dial mailer.Dialer
		if dial, err = s.DialerFor(&acc); err == nil {
			err = mailer.Send(ctx, &acc, lease.Password, dial, c, timeout)
		}
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
			if m.MsgID == "" {
				m.MsgID = mailer.MessageID(c.MsgIDLocal, acc.FromAddr)
			}
			break
		}
		errs = append(errs, fmt.Sprintf("[%s] %s", acc.Name, mailer.Short(err)))
		if kind == mailer.KindRecipient {
			m.Permanent = true
			if set.SuppressBounces {
				if e := s.Suppress(m.To, mailer.Short(err), "bounce", m.ID, set.SuppressDays); e != nil {
					log.Printf("suppress %s: %v", m.To, e)
				}
			}
			break
		}
	}

	now := time.Now()
	m.AccountID, m.FromAddr, m.DurationMs = lastAcc, from, now.Sub(start).Milliseconds()
	st := store.DailyStat{MsgFailed: 1}
	if ok {
		m.Status, m.SentAt, m.Error = store.StatusSent, &now, ""
		st = store.DailyStat{MsgSent: 1}
	} else {
		m.Status, m.Error = store.StatusFailed, strings.Join(errs, " | ")
	}
	if err := s.DB.DB.Model(m).Select("account_id", "from_addr", "status", "attempts", "error", "duration_ms", "sent_at", "msg_id").Updates(m).Error; err != nil {
		log.Printf("save message %d: %v", m.ID, err)
	}
	_ = s.DB.IncStat(store.Day(start), m.APIKeyID, m.AccountID, st)
}

func domainOf(addr string) string { return strings.ToLower(addr[strings.LastIndex(addr, "@")+1:]) }

func trimText(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
