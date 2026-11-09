package server

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"mailsender/internal/core"
	"mailsender/internal/store"
)

type sendResp struct {
	ID         uint       `json:"id"`
	Status     string     `json:"status"`
	To         string     `json:"to"`
	From       string     `json:"from,omitempty"`
	Attempts   int        `json:"attempts"`
	Error      string     `json:"error,omitempty"`
	DurationMs int64      `json:"duration_ms"`
	CreatedAt  time.Time  `json:"created_at"`
	SentAt     *time.Time `json:"sent_at,omitempty"`
	Code       string     `json:"code,omitempty"`
}

func toResp(m *store.Message) sendResp {
	return sendResp{ID: m.ID, Status: m.Status, To: m.To, From: m.FromAddr, Attempts: m.Attempts, Error: m.Error,
		DurationMs: m.DurationMs, CreatedAt: m.CreatedAt, SentAt: m.SentAt}
}

func writeSend(c *gin.Context, r sendResp) {
	if r.Status == store.StatusFailed && r.Attempts == 0 {
		c.JSON(503, gin.H{"code": "no_available_account", "message": r.Error, "data": r})
		return
	}
	if r.Status == store.StatusFailed {
		c.JSON(502, gin.H{"code": "send_failed", "message": r.Error, "data": r})
		return
	}
	c.JSON(200, gin.H{"code": "ok", "data": r})
}

func (s *Server) handleSend(c *gin.Context) {
	var req core.SendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		bad(c, "invalid JSON body: "+err.Error())
		return
	}
	key := c.MustGet("apikey").(*store.APIKey)
	m, err := s.svc.Submit(key, c.ClientIP(), req)
	if err != nil {
		fail(c, err)
		return
	}
	writeSend(c, toResp(m))
}

func (s *Server) handleSendCode(c *gin.Context) {
	var req core.CodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		bad(c, "invalid JSON body: "+err.Error())
		return
	}
	key := c.MustGet("apikey").(*store.APIKey)
	m, code, err := s.svc.SendCode(key, c.ClientIP(), req)
	if err != nil {
		fail(c, err)
		return
	}
	r := toResp(m)
	r.Code = code
	writeSend(c, r)
}

func (s *Server) handleGetMessage(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	key := c.MustGet("apikey").(*store.APIKey)
	var m store.Message
	if err := s.db.DB.Where("id = ? AND api_key_id = ?", id, key.ID).First(&m).Error; err != nil {
		c.AbortWithStatusJSON(404, gin.H{"code": "not_found", "message": "message not found"})
		return
	}
	c.JSON(200, gin.H{"code": "ok", "data": toResp(&m)})
}
