package server

import (
	"context"
	"fmt"
	netmail "net/mail"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"mailsender/internal/core"
	"mailsender/internal/secure"
	"mailsender/internal/store"
)

// ---------- two-factor login (TOTP) ----------

var totpMu sync.Mutex // serialises replay-protection updates

func (s *Server) totpSecret(key string) (string, bool) {
	enc, ok := s.db.GetSetting(key)
	if !ok || enc == "" {
		return "", false
	}
	v, err := s.box.Decrypt(enc)
	return v, err == nil && v != ""
}

// checkTOTP verifies a code and records its time step so it cannot be used twice.
func (s *Server) checkTOTP(secret, code string) bool {
	totpMu.Lock()
	defer totpMu.Unlock()
	lastS, _ := s.db.GetSetting(keyTOTPLastStep)
	last, _ := strconv.ParseInt(lastS, 10, 64)
	step, ok := secure.VerifyTOTP(secret, code, last)
	if ok {
		_ = s.db.SetSetting(keyTOTPLastStep, strconv.FormatInt(step, 10))
	}
	return ok
}

func (s *Server) get2FA(c *gin.Context) {
	_, on := s.totpSecret(keyTOTP)
	c.JSON(200, gin.H{"enabled": on})
}

func (s *Server) setup2FA(c *gin.Context) {
	if _, on := s.totpSecret(keyTOTP); on {
		bad(c, "两步验证已开启，如需更换请先关闭")
		return
	}
	secret := secure.NewTOTPSecret()
	enc, err := s.box.Encrypt(secret)
	if err != nil {
		fail(c, err)
		return
	}
	if err := s.db.SetSetting(keyTOTPPending, enc); err != nil {
		fail(c, err)
		return
	}
	user, _ := s.db.GetSetting(keyAdminUser)
	issuer := "MailSender"
	if len(s.info.TLSDomains) > 0 {
		issuer += " (" + s.info.TLSDomains[0] + ")"
	}
	c.JSON(200, gin.H{"secret": secret, "uri": secure.TOTPURI(secret, user, issuer)})
}

func (s *Server) enable2FA(c *gin.Context) {
	var req struct {
		Code string `json:"code"`
	}
	_ = c.ShouldBindJSON(&req)
	secret, ok := s.totpSecret(keyTOTPPending)
	if !ok {
		bad(c, "请先生成二维码")
		return
	}
	if !s.checkTOTP(secret, req.Code) {
		bad(c, "验证码错误，请确认手机时间准确后重试")
		return
	}
	enc, _ := s.db.GetSetting(keyTOTPPending)
	if err := s.db.SetSetting(keyTOTP, enc); err != nil {
		fail(c, err)
		return
	}
	_ = s.db.SetSetting(keyTOTPPending, "")
	user, _ := s.db.GetSetting(keyAdminUser)
	// the token version includes the 2FA state: other sessions are logged out, this one gets a fresh token
	c.JSON(200, gin.H{"token": s.box.SignToken(user, s.auth.version(), tokenTTL)})
}

func (s *Server) disable2FA(c *gin.Context) {
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	_ = c.ShouldBindJSON(&req)
	hash, _ := s.db.GetSetting(keyAdminHash)
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		bad(c, "密码错误")
		return
	}
	secret, on := s.totpSecret(keyTOTP)
	if !on {
		bad(c, "两步验证未开启")
		return
	}
	if !s.checkTOTP(secret, req.Code) {
		bad(c, "验证码错误")
		return
	}
	if err := Disable2FA(s.db); err != nil {
		fail(c, err)
		return
	}
	user, _ := s.db.GetSetting(keyAdminUser)
	c.JSON(200, gin.H{"token": s.box.SignToken(user, s.auth.version(), tokenTTL)})
}

// Disable2FA turns off two-factor login (also used by MS_DISABLE_2FA at startup).
func Disable2FA(db *store.Store) error {
	if err := db.SetSetting(keyTOTP, ""); err != nil {
		return err
	}
	return db.SetSetting(keyTOTPPending, "")
}

// ---------- IP bans ----------

func (s *Server) unban(c *gin.Context) {
	var req struct {
		Scope string `json:"scope"`
		IP    string `json:"ip"`
	}
	if c.ShouldBindJSON(&req) != nil || req.IP == "" {
		bad(c, "ip 必填")
		return
	}
	s.svc.Guard.Unban(req.Scope, req.IP)
	c.JSON(200, gin.H{"ok": true})
}

// ---------- suppression list ----------

func (s *Server) listSuppressions(c *gin.Context) {
	q := s.db.DB.Model(&store.Suppression{}).Where("expires_at IS NULL OR expires_at > ?", time.Now())
	if v := strings.TrimSpace(c.Query("q")); v != "" {
		q = q.Where("addr LIKE ?", "%"+strings.ToLower(v)+"%")
	}
	if v := c.Query("source"); v != "" {
		q = q.Where("source = ?", v)
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	page, size = max(page, 1), min(max(size, 1), 200)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		fail(c, err)
		return
	}
	items := []store.Suppression{}
	if err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&items).Error; err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"items": items, "total": total})
}

func (s *Server) addSuppression(c *gin.Context) {
	var req struct {
		Addrs  string `json:"addrs"` // one or more, separated by commas / newlines
		Reason string `json:"reason"`
		Days   int    `json:"days"` // 0 = forever
	}
	if c.ShouldBindJSON(&req) != nil {
		bad(c, "invalid body")
		return
	}
	addrs := core.SplitList(req.Addrs)
	if len(addrs) == 0 || len(addrs) > 1000 {
		bad(c, "请输入 1-1000 个邮箱地址")
		return
	}
	for _, a := range addrs {
		if p, err := netmail.ParseAddress(a); err != nil || p.Address != a {
			bad(c, "地址无效："+a)
			return
		}
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "手动添加"
	}
	for _, a := range addrs {
		if err := s.svc.Suppress(a, reason, "manual", 0, max(req.Days, 0)); err != nil {
			fail(c, err)
			return
		}
	}
	c.JSON(200, gin.H{"added": len(addrs)})
}

func (s *Server) deleteSuppression(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := s.db.DB.Delete(&store.Suppression{}, id).Error; err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// ---------- groups / alerts / callbacks ----------

// listGroups returns every group tag used by an account, with how many accounts carry it.
func (s *Server) listGroups(c *gin.Context) {
	var tags []string
	s.db.DB.Model(&store.Account{}).Where("group_tags <> ''").Pluck("group_tags", &tags)
	count := map[string]int{}
	for _, t := range tags {
		for _, g := range core.SplitList(t) {
			count[g]++
		}
	}
	type group struct {
		Name     string `json:"name"`
		Accounts int    `json:"accounts"`
	}
	out := []group{}
	for g, n := range count {
		out = append(out, group{g, n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	c.JSON(200, out)
}

func (s *Server) testAlert(c *gin.Context) {
	set := s.svc.Cfg.Get()
	if err := c.ShouldBindJSON(&set); err != nil {
		bad(c, fmt.Sprintf("invalid body: %v", err))
		return
	}
	if err := set.Validate(); err != nil {
		bad(c, err.Error())
		return
	}
	if set.AlertChannel == "" {
		bad(c, "请先选择告警渠道")
		return
	}
	err := s.svc.Notify.SendWith(context.Background(), set, core.Alert{Event: "test", Title: "测试告警", Text: "收到这条消息说明告警配置正确。"})
	if err != nil {
		c.JSON(200, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) rotateCallbackSecret(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	enc, plain, err := s.newCallbackSecret()
	if err != nil {
		fail(c, err)
		return
	}
	res := s.db.DB.Model(&store.APIKey{}).Where("id = ?", id).UpdateColumn("callback_secret", enc)
	if res.Error != nil {
		fail(c, res.Error)
		return
	}
	c.JSON(200, gin.H{"callback_secret": plain})
}

func (s *Server) testCallback(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var k store.APIKey
	if err := s.db.DB.First(&k, id).Error; err != nil {
		notFoundOr(c, err)
		return
	}
	start := time.Now()
	if err := s.svc.TestCallback(&k); err != nil {
		c.JSON(200, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true, "duration_ms": time.Since(start).Milliseconds()})
}
