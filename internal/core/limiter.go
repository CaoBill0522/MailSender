package core

import (
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"mailsender/internal/store"
)

// RecipientLimiter stops one address from being mailed too often (anti verification-code bombing).
type RecipientLimiter struct {
	mu sync.Mutex
	m  map[string]*rcptState
}

type rcptState struct {
	last  time.Time
	day   string
	count int
}

func NewRecipientLimiter() *RecipientLimiter { return &RecipientLimiter{m: map[string]*rcptState{}} }

// Allow reserves a send to addr, or returns how long the caller must wait (-1 = daily cap reached).
func (l *RecipientLimiter) Allow(addr string, interval time.Duration, dailyMax int) (bool, time.Duration) {
	if interval <= 0 && dailyMax <= 0 {
		return true, 0
	}
	addr = strings.ToLower(addr)
	now := time.Now()
	day := store.Day(now)
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.m[addr]
	if st == nil {
		st = &rcptState{day: day}
		l.m[addr] = st
	}
	if st.day != day {
		st.day, st.count = day, 0
	}
	if dailyMax > 0 && st.count >= dailyMax {
		return false, -1
	}
	if interval > 0 && !st.last.IsZero() {
		if wait := st.last.Add(interval).Sub(now); wait > 0 {
			return false, wait
		}
	}
	st.last = now
	st.count++
	return true, 0
}

// GC drops entries that can no longer affect a decision.
func (l *RecipientLimiter) GC(interval time.Duration) {
	now := time.Now()
	day := store.Day(now)
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, st := range l.m {
		if st.day != day && now.Sub(st.last) > interval {
			delete(l.m, k)
		}
	}
}

// KeyLimiter applies a per-API-key requests-per-minute limit.
type KeyLimiter struct {
	mu sync.Mutex
	m  map[uint]*keyLim
}

type keyLim struct {
	perMin int
	lim    *rate.Limiter
}

func NewKeyLimiter() *KeyLimiter { return &KeyLimiter{m: map[uint]*keyLim{}} }

func (l *KeyLimiter) Allow(id uint, perMin int) bool {
	if perMin <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	k := l.m[id]
	if k == nil || k.perMin != perMin {
		k = &keyLim{perMin: perMin, lim: rate.NewLimiter(rate.Limit(float64(perMin)/60), perMin)}
		l.m[id] = k
	}
	return k.lim.Allow()
}
