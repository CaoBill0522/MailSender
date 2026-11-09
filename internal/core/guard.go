package core

import (
	"sort"
	"sync"
	"time"
)

// AuthGuard bans client IPs that fail to log in too often (SMTP AUTH brute force, admin password guessing).
// State is in memory: a restart lifts all bans.
type AuthGuard struct {
	mu     sync.Mutex
	cfg    *SettingsStore
	m      map[string]*guardEntry // scope + "|" + ip
	OnBan  func(scope, ip string, fails int, until time.Time)
	window time.Duration
}

type guardEntry struct {
	fails       []time.Time
	bannedUntil time.Time
	lastReason  string
}

// Ban is one active ban, for the admin console.
type Ban struct {
	Scope  string    `json:"scope"` // smtp | admin
	IP     string    `json:"ip"`
	Until  time.Time `json:"until"`
	Fails  int       `json:"fails"`
	Reason string    `json:"reason"`
}

func NewAuthGuard(cfg *SettingsStore) *AuthGuard {
	return &AuthGuard{cfg: cfg, m: map[string]*guardEntry{}, window: 15 * time.Minute}
}

// Banned reports whether ip may not try to log in right now.
func (g *AuthGuard) Banned(scope, ip string) (time.Time, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.m[scope+"|"+ip]
	if e == nil || !time.Now().Before(e.bannedUntil) {
		return time.Time{}, false
	}
	return e.bannedUntil, true
}

// Fail records a failed login and returns true if it caused a ban.
func (g *AuthGuard) Fail(scope, ip, reason string) bool {
	set := g.cfg.Get()
	if set.AuthMaxFails <= 0 || isLoopback(ip) {
		return false
	}
	now := time.Now()
	g.mu.Lock()
	k := scope + "|" + ip
	e := g.m[k]
	if e == nil {
		e = &guardEntry{}
		g.m[k] = e
	}
	cut := now.Add(-g.window)
	i := 0
	for i < len(e.fails) && e.fails[i].Before(cut) {
		i++
	}
	e.fails = append(e.fails[i:], now)
	e.lastReason = reason
	banned := false
	if len(e.fails) >= set.AuthMaxFails && !now.Before(e.bannedUntil) {
		e.bannedUntil = now.Add(time.Duration(set.AuthBanMinutes) * time.Minute)
		banned = true
	}
	fails, until := len(e.fails), e.bannedUntil
	g.mu.Unlock()
	if banned && g.OnBan != nil {
		g.OnBan(scope, ip, fails, until)
	}
	return banned
}

// Success clears the failure history of ip.
func (g *AuthGuard) Success(scope, ip string) {
	g.mu.Lock()
	delete(g.m, scope+"|"+ip)
	g.mu.Unlock()
}

// Unban lifts a ban and forgets the failures.
func (g *AuthGuard) Unban(scope, ip string) { g.Success(scope, ip) }

// List returns the active bans, most recent first.
func (g *AuthGuard) List() []Ban {
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	out := []Ban{}
	for k, e := range g.m {
		if !now.Before(e.bannedUntil) {
			continue
		}
		scope, ip := splitKey(k)
		out = append(out, Ban{Scope: scope, IP: ip, Until: e.bannedUntil, Fails: len(e.fails), Reason: e.lastReason})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Until.After(out[j].Until) })
	return out
}

// GC drops entries with no recent failures and no active ban.
func (g *AuthGuard) GC() {
	now := time.Now()
	cut := now.Add(-g.window)
	g.mu.Lock()
	defer g.mu.Unlock()
	for k, e := range g.m {
		if now.After(e.bannedUntil) && (len(e.fails) == 0 || e.fails[len(e.fails)-1].Before(cut)) {
			delete(g.m, k)
		}
	}
}

func splitKey(k string) (string, string) {
	for i := 0; i < len(k); i++ {
		if k[i] == '|' {
			return k[:i], k[i+1:]
		}
	}
	return "", k
}

// loopback clients (local tools, health probes) are never banned
func isLoopback(ip string) bool {
	return ip == "127.0.0.1" || ip == "::1" || len(ip) > 4 && ip[:4] == "127."
}
