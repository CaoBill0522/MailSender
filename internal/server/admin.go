package server

import (
	"errors"
	"fmt"
	netmail "net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"mailsender/internal/core"
	"mailsender/internal/mailer"
	"mailsender/internal/secure"
	"mailsender/internal/store"
)

func idParam(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		bad(c, "invalid id")
		return 0, false
	}
	return uint(id), true
}

func notFoundOr(c *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, store.ErrNotFound) {
		c.AbortWithStatusJSON(404, gin.H{"code": "not_found", "message": "not found"})
		return
	}
	fail(c, err)
}

// ---------- accounts ----------

type accountView struct {
	store.Account
	HasPassword bool         `json:"has_password"`
	Runtime     core.Runtime `json:"runtime"`
}

type accountInput struct {
	Name           string `json:"name"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Security       string `json:"security"`
	AuthType       string `json:"auth_type"`
	Username       string `json:"username"`
	Password       string `json:"password"` // empty on update = keep
	FromAddr       string `json:"from_addr"`
	FromName       string `json:"from_name"`
	Weight         int    `json:"weight"`
	LimitPerMinute int    `json:"limit_per_minute"`
	LimitPerHour   int    `json:"limit_per_hour"`
	LimitPerDay    int    `json:"limit_per_day"`
	Enabled        bool   `json:"enabled"`
	Remark         string `json:"remark"`
}

func (in *accountInput) normalize() error {
	in.Host = strings.TrimSpace(in.Host)
	in.FromAddr = strings.TrimSpace(in.FromAddr)
	in.Username = strings.TrimSpace(in.Username)
	if in.Host == "" {
		return errors.New("host 必填")
	}
	if in.Port <= 0 || in.Port > 65535 {
		return errors.New("端口无效")
	}
	if in.Security == "" {
		switch in.Port {
		case 465:
			in.Security = "ssl"
		case 587:
			in.Security = "starttls"
		default:
			in.Security = "none"
		}
	}
	if in.Security != "ssl" && in.Security != "starttls" && in.Security != "none" {
		return errors.New("security 必须是 ssl / starttls / none")
	}
	if in.AuthType == "" {
		in.AuthType = "auto"
	}
	switch in.AuthType {
	case "auto", "plain", "login", "cram-md5", "none":
	default:
		return errors.New("auth_type 无效")
	}
	a, err := netmail.ParseAddress(in.FromAddr)
	if err != nil {
		return errors.New("发件地址无效")
	}
	in.FromAddr = a.Address
	if in.Username == "" {
		in.Username = in.FromAddr
	}
	if in.Name == "" {
		in.Name = in.FromAddr
	}
	if in.Weight < 1 {
		in.Weight = 1
	}
	if in.LimitPerMinute < 0 || in.LimitPerHour < 0 || in.LimitPerDay < 0 {
		return errors.New("限额不能为负数")
	}
	return nil
}

func (in *accountInput) apply(a *store.Account) {
	a.Name, a.Host, a.Port, a.Security, a.AuthType = in.Name, in.Host, in.Port, in.Security, in.AuthType
	a.Username, a.FromAddr, a.FromName, a.Weight = in.Username, in.FromAddr, in.FromName, in.Weight
	a.LimitPerMinute, a.LimitPerHour, a.LimitPerDay = in.LimitPerMinute, in.LimitPerHour, in.LimitPerDay
	a.Enabled, a.Remark = in.Enabled, in.Remark
}

func (s *Server) listAccounts(c *gin.Context) {
	var accs []store.Account
	if err := s.db.DB.Order("id").Find(&accs).Error; err != nil {
		fail(c, err)
		return
	}
	rt := s.svc.Sched.Snapshot()
	out := make([]accountView, 0, len(accs))
	for _, a := range accs {
		out = append(out, accountView{Account: a, HasPassword: a.PasswordEnc != "", Runtime: rt[a.ID]})
	}
	c.JSON(200, out)
}

func (s *Server) createAccount(c *gin.Context) {
	var in accountInput
	if c.ShouldBindJSON(&in) != nil {
		bad(c, "invalid body")
		return
	}
	if err := in.normalize(); err != nil {
		bad(c, err.Error())
		return
	}
	var a store.Account
	in.apply(&a)
	enc, err := s.box.Encrypt(in.Password)
	if err != nil {
		fail(c, err)
		return
	}
	a.PasswordEnc = enc
	if err := s.db.DB.Create(&a).Error; err != nil {
		fail(c, err)
		return
	}
	_ = s.svc.Sched.Reload()
	c.JSON(200, a)
}

func (s *Server) updateAccount(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in accountInput
	if c.ShouldBindJSON(&in) != nil {
		bad(c, "invalid body")
		return
	}
	if err := in.normalize(); err != nil {
		bad(c, err.Error())
		return
	}
	var a store.Account
	if err := s.db.DB.First(&a, id).Error; err != nil {
		notFoundOr(c, err)
		return
	}
	in.apply(&a)
	cols := []string{"name", "host", "port", "security", "auth_type", "username", "from_addr", "from_name",
		"weight", "limit_per_minute", "limit_per_hour", "limit_per_day", "enabled", "remark"}
	if in.Password != "" {
		enc, err := s.box.Encrypt(in.Password)
		if err != nil {
			fail(c, err)
			return
		}
		a.PasswordEnc = enc
		cols = append(cols, "password_enc")
	}
	if err := s.db.DB.Model(&a).Select(cols).Updates(&a).Error; err != nil {
		fail(c, err)
		return
	}
	_ = s.svc.Sched.Reload()
	c.JSON(200, a)
}

func (s *Server) deleteAccount(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := s.db.DB.Delete(&store.Account{}, id).Error; err != nil {
		fail(c, err)
		return
	}
	_ = s.svc.Sched.Reload()
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) testAccount(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	start := time.Now()
	err := s.svc.TestAccount(id)
	if errors.Is(err, store.ErrNotFound) {
		notFoundOr(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": err == nil, "error": mailer.Short(err), "duration_ms": time.Since(start).Milliseconds()})
}

func (s *Server) testSendAccount(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req struct {
		To string `json:"to"`
	}
	if c.ShouldBindJSON(&req) != nil || req.To == "" {
		bad(c, "to 必填")
		return
	}
	start := time.Now()
	err := s.svc.TestSend(id, req.To)
	if errors.Is(err, store.ErrNotFound) {
		notFoundOr(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": err == nil, "error": mailer.Short(err), "duration_ms": time.Since(start).Milliseconds()})
}

func (s *Server) resetAccount(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	s.svc.Sched.ResetHealth(id)
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) checkAllAccounts(c *gin.Context) {
	s.svc.HealthCheckAll()
	s.listAccounts(c)
}

// ---------- API keys ----------

type keyInput struct {
	Name            string `json:"name"`
	Enabled         bool   `json:"enabled"`
	RateLimitPerMin int    `json:"rate_limit_per_min"`
	IPWhitelist     string `json:"ip_whitelist"`
	Remark          string `json:"remark"`
}

func (s *Server) listKeys(c *gin.Context) {
	var keys []store.APIKey
	if err := s.db.DB.Order("id").Find(&keys).Error; err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, keys)
}

func (s *Server) createKey(c *gin.Context) {
	var in keyInput
	if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.Name) == "" {
		bad(c, "名称必填")
		return
	}
	raw := secure.NewAPIKey()
	k := store.APIKey{Name: strings.TrimSpace(in.Name), Prefix: raw[:10], KeyHash: secure.HashAPIKey(raw), Enabled: true,
		RateLimitPerMin: max(in.RateLimitPerMin, 0), IPWhitelist: strings.TrimSpace(in.IPWhitelist), Remark: in.Remark}
	if err := s.db.DB.Create(&k).Error; err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"key": raw, "item": k})
}

func (s *Server) updateKey(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in keyInput
	if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.Name) == "" {
		bad(c, "名称必填")
		return
	}
	res := s.db.DB.Model(&store.APIKey{}).Where("id = ?", id).Select("name", "enabled", "rate_limit_per_min", "ip_whitelist", "remark").
		Updates(&store.APIKey{Name: strings.TrimSpace(in.Name), Enabled: in.Enabled, RateLimitPerMin: max(in.RateLimitPerMin, 0),
			IPWhitelist: strings.TrimSpace(in.IPWhitelist), Remark: in.Remark})
	if res.Error != nil {
		fail(c, res.Error)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) deleteKey(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := s.db.DB.Delete(&store.APIKey{}, id).Error; err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// ---------- templates ----------

func (s *Server) listTemplates(c *gin.Context) {
	var ts []store.Template
	if err := s.db.DB.Order("id").Find(&ts).Error; err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, ts)
}

func validateTemplate(t *store.Template) error {
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" || strings.ContainsAny(t.Name, " /") {
		return errors.New("模板名必填，且不能包含空格或 /")
	}
	if strings.TrimSpace(t.Subject) == "" {
		return errors.New("主题必填")
	}
	if t.HTML == "" && t.Text == "" {
		return errors.New("HTML 和纯文本至少填一个")
	}
	_, _, err := core.RenderTemplate(*t, map[string]any{}, "")
	return err
}

func (s *Server) createTemplate(c *gin.Context) {
	var t store.Template
	if c.ShouldBindJSON(&t) != nil {
		bad(c, "invalid body")
		return
	}
	t.ID = 0
	if err := validateTemplate(&t); err != nil {
		bad(c, err.Error())
		return
	}
	if err := s.db.DB.Create(&t).Error; err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			bad(c, "模板名已存在")
			return
		}
		fail(c, err)
		return
	}
	c.JSON(200, t)
}

func (s *Server) updateTemplate(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var t store.Template
	if c.ShouldBindJSON(&t) != nil {
		bad(c, "invalid body")
		return
	}
	t.ID = id
	if err := validateTemplate(&t); err != nil {
		bad(c, err.Error())
		return
	}
	if err := s.db.DB.Model(&t).Select("name", "subject", "html", "text", "remark").Updates(&t).Error; err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			bad(c, "模板名已存在")
			return
		}
		fail(c, err)
		return
	}
	c.JSON(200, t)
}

func (s *Server) deleteTemplate(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := s.db.DB.Delete(&store.Template{}, id).Error; err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) previewTemplate(c *gin.Context) {
	var req struct {
		store.Template
		Vars map[string]any `json:"vars"`
	}
	if c.ShouldBindJSON(&req) != nil {
		bad(c, "invalid body")
		return
	}
	out, _, err := core.RenderTemplate(req.Template, req.Vars, "")
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, out)
}

// ---------- messages ----------

func (s *Server) listMessages(c *gin.Context) {
	q := s.db.DB.Model(&store.Message{})
	if v := c.Query("status"); v != "" {
		q = q.Where("status = ?", v)
	}
	if v := strings.TrimSpace(c.Query("to")); v != "" {
		q = q.Where("\"to\" LIKE ?", "%"+v+"%")
	}
	if v := c.Query("account_id"); v != "" {
		q = q.Where("account_id = ?", v)
	}
	if v := c.Query("api_key_id"); v != "" {
		q = q.Where("api_key_id = ?", v)
	}
	if v := c.Query("start"); v != "" {
		if t, err := time.ParseInLocation("2006-01-02", v, time.Local); err == nil {
			q = q.Where("created_at >= ?", t)
		}
	}
	if v := c.Query("end"); v != "" {
		if t, err := time.ParseInLocation("2006-01-02", v, time.Local); err == nil {
			q = q.Where("created_at < ?", t.AddDate(0, 0, 1))
		}
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	page, size = max(page, 1), min(max(size, 1), 200)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		fail(c, err)
		return
	}
	items := []store.Message{}
	if err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&items).Error; err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"items": items, "total": total})
}

func (s *Server) getMessage(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var m store.Message
	if err := s.db.DB.First(&m, id).Error; err != nil {
		notFoundOr(c, err)
		return
	}
	type attView struct {
		store.SendAttempt
		AccountName string `json:"account_name"`
	}
	atts := []attView{}
	s.db.DB.Table("send_attempts").Select("send_attempts.*, accounts.name AS account_name").
		Joins("LEFT JOIN accounts ON accounts.id = send_attempts.account_id").
		Where("message_id = ?", id).Order("send_attempts.id").Scan(&atts)
	c.JSON(200, gin.H{"message": m, "attempts": atts})
}

// ---------- settings ----------

func (s *Server) getSettings(c *gin.Context) { c.JSON(200, s.svc.Cfg.Get()) }

func (s *Server) putSettings(c *gin.Context) {
	v := s.svc.Cfg.Get()
	if err := c.ShouldBindJSON(&v); err != nil {
		bad(c, fmt.Sprintf("invalid body: %v", err))
		return
	}
	if err := s.svc.Cfg.Set(v); err != nil {
		bad(c, err.Error())
		return
	}
	c.JSON(200, v)
}
