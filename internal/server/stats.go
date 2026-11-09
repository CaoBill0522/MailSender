package server

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"mailsender/internal/store"
)

type StatSum struct {
	MsgSent     int64 `json:"msg_sent"`
	MsgFailed   int64 `json:"msg_failed"`
	MsgRejected int64 `json:"msg_rejected"`
	AttOK       int64 `json:"att_ok"`
	AttFail     int64 `json:"att_fail"`
	AttMsSum    int64 `json:"att_ms_sum"`
}

const sumCols = "COALESCE(SUM(msg_sent),0) AS msg_sent, COALESCE(SUM(msg_failed),0) AS msg_failed, COALESCE(SUM(msg_rejected),0) AS msg_rejected, " +
	"COALESCE(SUM(att_ok),0) AS att_ok, COALESCE(SUM(att_fail),0) AS att_fail, COALESCE(SUM(att_ms_sum),0) AS att_ms_sum"

func daysParam(c *gin.Context, def int) (int, string) {
	d, _ := strconv.Atoi(c.DefaultQuery("days", strconv.Itoa(def)))
	d = min(max(d, 1), 366)
	return d, store.Day(time.Now().AddDate(0, 0, -(d - 1)))
}

func (s *Server) sumDay(day string) StatSum {
	var r StatSum
	s.db.DB.Model(&store.DailyStat{}).Select(sumCols).Where("day = ?", day).Scan(&r)
	return r
}

func (s *Server) statsOverview(c *gin.Context) {
	now := time.Now()
	today := s.sumDay(store.Day(now))
	yesterday := s.sumDay(store.Day(now.AddDate(0, 0, -1)))
	var total StatSum
	s.db.DB.Model(&store.DailyStat{}).Select(sumCols).Scan(&total)

	rt := s.svc.Sched.Snapshot()
	var enabled int64
	s.db.DB.Model(&store.Account{}).Where("enabled = ?", true).Count(&enabled)
	available := 0
	for _, r := range rt {
		if r.Available {
			available++
		}
	}
	var queued int64
	s.db.DB.Model(&store.Message{}).Where("status = ?", store.StatusQueued).Count(&queued)
	c.JSON(200, gin.H{
		"today": today, "yesterday": yesterday, "total": total,
		"accounts_total": len(rt), "accounts_enabled": enabled, "accounts_available": available,
		"queued": queued,
	})
}

func (s *Server) statsTrend(c *gin.Context) {
	days, since := daysParam(c, 7)
	type row struct {
		Day string `json:"day"`
		StatSum
	}
	rows := []row{}
	s.db.DB.Model(&store.DailyStat{}).Select("day, "+sumCols).Where("day >= ?", since).Group("day").Scan(&rows)
	byDay := map[string]row{}
	for _, r := range rows {
		byDay[r.Day] = r
	}
	out := make([]row, 0, days)
	for i := days - 1; i >= 0; i-- {
		d := store.Day(time.Now().AddDate(0, 0, -i))
		r := byDay[d]
		r.Day = d
		out = append(out, r)
	}
	c.JSON(200, out)
}

func (s *Server) statsAccounts(c *gin.Context) {
	_, since := daysParam(c, 1)
	type row struct {
		AccountID uint   `json:"account_id"`
		Name      string `json:"name"`
		FromAddr  string `json:"from_addr"`
		StatSum
	}
	rows := []row{}
	s.db.DB.Table("daily_stats").
		Select("daily_stats.account_id, accounts.name, accounts.from_addr, "+sumCols).
		Joins("LEFT JOIN accounts ON accounts.id = daily_stats.account_id").
		Where("daily_stats.day >= ? AND daily_stats.account_id > 0", since).
		Group("daily_stats.account_id").Order("att_ok DESC").Scan(&rows)
	c.JSON(200, rows)
}

func (s *Server) statsKeys(c *gin.Context) {
	_, since := daysParam(c, 1)
	type row struct {
		APIKeyID uint   `json:"api_key_id"`
		Name     string `json:"name"`
		StatSum
	}
	rows := []row{}
	s.db.DB.Table("daily_stats").
		Select("daily_stats.api_key_id, api_keys.name, "+sumCols).
		Joins("LEFT JOIN api_keys ON api_keys.id = daily_stats.api_key_id").
		Where("daily_stats.day >= ?", since).
		Group("daily_stats.api_key_id").Order("msg_sent DESC").Scan(&rows)
	c.JSON(200, rows)
}
