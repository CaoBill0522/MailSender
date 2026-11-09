package server

import (
	"errors"
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

	// Public send API for client programs.
	api := r.Group("/api/v1", s.apiKeyAuth)
	api.POST("/send", s.handleSend)
	api.POST("/send/code", s.handleSendCode)
	api.GET("/messages/:id", s.handleGetMessage)

	// Admin API for the web console.
	ad := r.Group("/admin/api")
	ad.POST("/login", s.handleLogin)
	ad.Use(s.adminRequired)
	ad.GET("/me", func(c *gin.Context) { c.JSON(200, gin.H{"username": c.GetString("admin")}) })
	ad.POST("/password", s.handleChangePassword)

	ad.GET("/accounts", s.listAccounts)
	ad.POST("/accounts", s.createAccount)
	ad.PUT("/accounts/:id", s.updateAccount)
	ad.DELETE("/accounts/:id", s.deleteAccount)
	ad.POST("/accounts/:id/test", s.testAccount)
	ad.POST("/accounts/:id/test-send", s.testSendAccount)
	ad.POST("/accounts/:id/reset", s.resetAccount)
	ad.POST("/accounts/check-all", s.checkAllAccounts)

	ad.GET("/keys", s.listKeys)
	ad.POST("/keys", s.createKey)
	ad.PUT("/keys/:id", s.updateKey)
	ad.DELETE("/keys/:id", s.deleteKey)

	ad.GET("/templates", s.listTemplates)
	ad.POST("/templates", s.createTemplate)
	ad.PUT("/templates/:id", s.updateTemplate)
	ad.DELETE("/templates/:id", s.deleteTemplate)
	ad.POST("/templates/preview", s.previewTemplate)

	ad.GET("/messages", s.listMessages)
	ad.GET("/messages/:id", s.getMessage)

	ad.GET("/stats/overview", s.statsOverview)
	ad.GET("/stats/trend", s.statsTrend)
	ad.GET("/stats/accounts", s.statsAccounts)
	ad.GET("/stats/keys", s.statsKeys)

	ad.GET("/settings", s.getSettings)
	ad.GET("/server-info", func(c *gin.Context) { c.JSON(200, s.info) })
	ad.PUT("/settings", s.putSettings)

	r.NoRoute(s.serveSPA)
	return r
}

// serveSPA serves the embedded Vue build, falling back to index.html for client-side routes.
func (s *Server) serveSPA(c *gin.Context) {
	p := strings.TrimPrefix(c.Request.URL.Path, "/")
	if strings.HasPrefix(p, "api/") || strings.HasPrefix(p, "admin/api/") {
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
	var ae *core.APIError
	if errors.As(err, &ae) {
		c.AbortWithStatusJSON(ae.Status, ae)
		return
	}
	c.AbortWithStatusJSON(500, gin.H{"code": "internal", "message": err.Error()})
}

func bad(c *gin.Context, msg string) {
	c.AbortWithStatusJSON(400, gin.H{"code": "bad_request", "message": msg})
}

// ---------- API key auth ----------

func bearer(c *gin.Context) string {
	if k := c.GetHeader("X-API-Key"); k != "" {
		return k
	}
	h := c.GetHeader("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func (s *Server) apiKeyAuth(c *gin.Context) {
	k, aerr := s.svc.AuthKey(bearer(c), c.ClientIP())
	if aerr == nil {
		aerr = s.svc.AllowKeyRate(k)
	}
	if aerr != nil {
		c.AbortWithStatusJSON(aerr.Status, aerr)
		return
	}
	c.Set("apikey", k)
	c.Next()
}

// ---------- admin auth ----------

type adminAuth struct {
	db  *store.Store
	box *secure.Box
}

const (
	keyAdminUser = "admin_user"
	keyAdminHash = "admin_password_hash"
	tokenTTL     = 7 * 24 * time.Hour
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

// version ties tokens to the current password hash so changing it logs everyone out.
func (a *adminAuth) version() string {
	h, _ := a.db.GetSetting(keyAdminHash)
	if len(h) > 12 {
		return h[len(h)-12:]
	}
	return h
}

func (s *Server) handleLogin(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if c.ShouldBindJSON(&req) != nil {
		bad(c, "invalid body")
		return
	}
	user, _ := s.db.GetSetting(keyAdminUser)
	hash, _ := s.db.GetSetting(keyAdminHash)
	if req.Username != user || bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		time.Sleep(500 * time.Millisecond) // slow down guessing
		c.AbortWithStatusJSON(401, gin.H{"code": "unauthorized", "message": "用户名或密码错误"})
		return
	}
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
	TLSMode     string      `json:"tls_mode"` // none | files | acme
	TLSDomains  []string    `json:"tls_domains"`
	HTTPListen  string      `json:"http_listen"`
	HTTPSListen string      `json:"https_listen"`
	SMTP        *smtpd.Info `json:"smtp"` // nil when the SMTP ingress is disabled
}
