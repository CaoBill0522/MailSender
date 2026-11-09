package server

import (
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"mailsender/internal/core"
	"mailsender/internal/secure"
	"mailsender/internal/smtpd"
	"mailsender/internal/store"
)

type Server struct {
	db   *store.Store
	svc  *core.Service
	box  *secure.Box
	web  fs.FS
	auth *adminAuth
	info *RuntimeInfo
}

func New(db *store.Store, svc *core.Service, box *secure.Box, web fs.FS, info *RuntimeInfo) *Server {
	return &Server{db: db, svc: svc, box: box, web: web, info: info, auth: &adminAuth{db: db, box: box}}
}

func (s *Server) Handler(trustedProxies []string) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	_ = r.SetTrustedProxies(trustedProxies)

	// Admin API for the web console.
	ad := r.Group("/admin/api")
	ad.POST("/login", s.handleLogin)
	ad.Use(s.adminRequired)
	ad.GET("/me", func(c *gin.Context) { c.JSON(200, gin.H{"username": c.GetString("admin")}) })
	ad.POST("/password", s.handleChangePassword)
	ad.GET("/2fa", s.get2FA)
	ad.POST("/2fa/setup", s.setup2FA)
	ad.POST("/2fa/enable", s.enable2FA)
	ad.POST("/2fa/disable", s.disable2FA)
	ad.GET("/security/bans", func(c *gin.Context) { c.JSON(200, s.svc.Guard.List()) })
	ad.POST("/security/unban", s.unban)

	ad.GET("/accounts", s.listAccounts)
	ad.POST("/accounts", s.createAccount)
	ad.PUT("/accounts/:id", s.updateAccount)
	ad.DELETE("/accounts/:id", s.deleteAccount)
	ad.POST("/accounts/:id/test", s.testAccount)
	ad.POST("/accounts/:id/test-send", s.testSendAccount)
	ad.POST("/accounts/:id/reset", s.resetAccount)
	ad.POST("/accounts/check-all", s.checkAllAccounts)
	ad.GET("/groups", s.listGroups)

	ad.GET("/proxies", s.listProxies)
	ad.POST("/proxies", s.saveProxy)
	ad.PUT("/proxies/:id", s.saveProxy)
	ad.DELETE("/proxies/:id", s.deleteProxy)
	ad.POST("/proxies/:id/check", s.checkProxy)
	ad.POST("/proxies/direct-ip", s.directIP)

	ad.GET("/keys", s.listKeys)
	ad.POST("/keys", s.createKey)
	ad.PUT("/keys/:id", s.updateKey)
	ad.DELETE("/keys/:id", s.deleteKey)
	ad.POST("/keys/:id/callback-secret", s.rotateCallbackSecret)
	ad.POST("/keys/:id/callback-test", s.testCallback)

	ad.GET("/suppressions", s.listSuppressions)
	ad.POST("/suppressions", s.addSuppression)
	ad.DELETE("/suppressions/:id", s.deleteSuppression)

	ad.GET("/messages", s.listMessages)
	ad.GET("/messages/:id", s.getMessage)

	ad.GET("/stats/overview", s.statsOverview)
	ad.GET("/stats/trend", s.statsTrend)
	ad.GET("/stats/accounts", s.statsAccounts)
	ad.GET("/stats/keys", s.statsKeys)

	ad.GET("/settings", s.getSettings)
	ad.GET("/server-info", func(c *gin.Context) { c.JSON(200, s.info) })
	ad.PUT("/settings", s.putSettings)
	ad.POST("/alerts/test", s.testAlert)

	r.NoRoute(s.serveSPA)
	return r
}

// serveSPA serves the embedded Vue build, falling back to index.html for client-side routes.
func (s *Server) serveSPA(c *gin.Context) {
	p := strings.TrimPrefix(c.Request.URL.Path, "/")
	if strings.HasPrefix(p, "api/") {
		c.JSON(410, gin.H{"code": "gone", "message": "the HTTP send API has been removed; send mail over SMTP with your API key as the password"})
		return
	}
	if strings.HasPrefix(p, "admin/api/") {
		c.JSON(404, gin.H{"code": "not_found", "message": "not found"})
		return
	}
	if p != "" {
		if st, err := fs.Stat(s.web, p); err == nil && !st.IsDir() {
			if strings.HasPrefix(p, "assets/") {
				c.Header("Cache-Control", "public, max-age=31536000, immutable")
			}
			c.FileFromFS(p, http.FS(s.web))
			return
		}
	}
	b, err := fs.ReadFile(s.web, "index.html")
	if err != nil {
		c.String(500, "web console not built (run `make web`)")
		return
	}
	c.Header("Cache-Control", "no-cache")
	c.Data(200, "text/html; charset=utf-8", b)
}

func fail(c *gin.Context, err error) {
	c.AbortWithStatusJSON(500, gin.H{"code": "internal", "message": err.Error()})
}

func bad(c *gin.Context, msg string) {
	c.AbortWithStatusJSON(400, gin.H{"code": "bad_request", "message": msg})
}

func bearer(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// ---------- admin auth ----------

type adminAuth struct {
	db  *store.Store
	box *secure.Box
}

const (
	keyAdminUser    = "admin_user"
	keyAdminHash    = "admin_password_hash"
	keyTOTP         = "admin_totp"         // encrypted secret, present = 2FA on
	keyTOTPPending  = "admin_totp_pending" // encrypted secret awaiting its first code
	keyTOTPLastStep = "admin_totp_last"    // last accepted time step (replay protection)
	tokenTTL        = 7 * 24 * time.Hour
)

// EnsureAdmin creates the admin account on first run. Returns the generated password if one was made.
func EnsureAdmin(db *store.Store, initial string) (string, error) {
	if _, ok := db.GetSetting(keyAdminHash); ok {
		return "", nil
	}
	pw, generated := initial, ""
	if pw == "" {
		pw = secure.RandomString(16)
		generated = pw
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	if err := db.SetSetting(keyAdminUser, "admin"); err != nil {
		return "", err
	}
	return generated, db.SetSetting(keyAdminHash, string(h))
}

// version ties tokens to the current password hash and 2FA state, so changing either logs everyone out.
func (a *adminAuth) version() string {
	h, _ := a.db.GetSetting(keyAdminHash)
	if len(h) > 12 {
		h = h[len(h)-12:]
	}
	if t, ok := a.db.GetSetting(keyTOTP); ok && t != "" {
		h += "." + secure.HashAPIKey(t)[:8]
	}
	return h
}

func (s *Server) handleLogin(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		OTP      string `json:"otp"`
	}
	if c.ShouldBindJSON(&req) != nil {
		bad(c, "invalid body")
		return
	}
	ip := c.ClientIP()
	banned := func(until time.Time) {
		c.AbortWithStatusJSON(429, gin.H{"code": "banned", "message": "登录失败次数过多，该 IP 已被封禁到 " + until.Format("15:04")})
	}
	if until, ok := s.svc.Guard.Banned("admin", ip); ok {
		banned(until)
		return
	}
	failed := func(msg string) {
		time.Sleep(500 * time.Millisecond) // slow down guessing
		if s.svc.Guard.Fail("admin", ip, msg) {
			until, _ := s.svc.Guard.Banned("admin", ip)
			banned(until)
			return
		}
		c.AbortWithStatusJSON(401, gin.H{"code": "unauthorized", "message": msg})
	}
	user, _ := s.db.GetSetting(keyAdminUser)
	hash, _ := s.db.GetSetting(keyAdminHash)
	if req.Username != user || bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		failed("用户名或密码错误")
		return
	}
	if secret, on := s.totpSecret(keyTOTP); on {
		if strings.TrimSpace(req.OTP) == "" {
			c.AbortWithStatusJSON(401, gin.H{"code": "otp_required", "message": "请输入两步验证码"})
			return
		}
		if !s.checkTOTP(secret, req.OTP) {
			failed("两步验证码错误")
			return
		}
	}
	s.svc.Guard.Success("admin", ip)
	c.JSON(200, gin.H{"token": s.box.SignToken(user, s.auth.version(), tokenTTL), "username": user})
}

func (s *Server) adminRequired(c *gin.Context) {
	user, ok := s.box.VerifyToken(bearer(c), s.auth.version())
	if !ok {
		c.AbortWithStatusJSON(401, gin.H{"code": "unauthorized", "message": "login required"})
		return
	}
	c.Set("admin", user)
	c.Next()
}

func (s *Server) handleChangePassword(c *gin.Context) {
	var req struct {
		Old string `json:"old_password"`
		New string `json:"new_password"`
	}
	if c.ShouldBindJSON(&req) != nil || len(req.New) < 8 {
		bad(c, "新密码至少 8 位")
		return
	}
	hash, _ := s.db.GetSetting(keyAdminHash)
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Old)) != nil {
		bad(c, "原密码错误")
		return
	}
	h, err := bcrypt.GenerateFromPassword([]byte(req.New), bcrypt.DefaultCost)
	if err != nil {
		fail(c, err)
		return
	}
	if err := s.db.SetSetting(keyAdminHash, string(h)); err != nil {
		fail(c, err)
		return
	}
	user, _ := s.db.GetSetting(keyAdminUser)
	c.JSON(200, gin.H{"token": s.box.SignToken(user, s.auth.version(), tokenTTL)})
}

// RuntimeInfo describes listeners and TLS, shown in the console so users know how to connect.
type RuntimeInfo struct {
	TLSMode      string      `json:"tls_mode"` // none | files | acme
	TLSDomains   []string    `json:"tls_domains"`
	HTTPListen   string      `json:"http_listen"`
	HTTPSListen  string      `json:"https_listen"`
	HTTPRedirect bool        `json:"http_redirect"` // plain HTTP only redirects to HTTPS
	SMTP         *smtpd.Info `json:"smtp"`          // nil when the SMTP ingress is disabled
}
