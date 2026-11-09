package core

import (
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"

	"mailsender/internal/mailer"
	"mailsender/internal/secure"
	"mailsender/internal/store"
)

// ErrNoAccount means no account is currently able to send.
var ErrNoAccount = errors.New("no available SMTP account")

type accState struct {
	acc      store.Account
	password string
	decErr   error
	window   []time.Time // attempt timestamps within the last hour (sorted)
	day      string
	dayCount int
	cw       int         // smooth weighted round-robin current weight
	lastKind mailer.Kind // kind of the most recent failure
}

// Scheduler picks which account sends next, enforces per-account quotas and tracks health.
type Scheduler struct {
	mu     sync.Mutex
	db     *store.Store
	box    *secure.Box
	cfg    *SettingsStore
	states map[uint]*accState
	lastRR uint

	// health transition hooks (alerts); called without the lock held
	OnCooldown func(acc store.Account, until time.Time)
	OnRecover  func(acc store.Account)
}

type Lease struct {
	Account  store.Account
	Password string
}

// Runtime is the live view of an account for the admin UI.
type Runtime struct {
	MinuteCount int    `json:"minute_count"`
	HourCount   int    `json:"hour_count"`
	DayCount    int    `json:"day_count"`
	Available   bool   `json:"available"`
	Reason      string `json:"reason"` // why not available
}

func NewScheduler(db *store.Store, box *secure.Box, cfg *SettingsStore) (*Scheduler, error) {
	s := &Scheduler{db: db, box: box, cfg: cfg, states: map[uint]*accState{}}
	if err := s.Reload(); err != nil {
		return nil, err
	}
	s.rebuildCounters()
	return s, nil
}

// Reload re-reads account configuration from the DB, keeping live counters.
func (s *Scheduler) Reload() error {
	var accs []store.Account
	if err := s.db.DB.Find(&accs).Error; err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[uint]*accState, len(accs))
	for _, a := range accs {
		st := s.states[a.ID]
		if st == nil {
			st = &accState{}
		}
		st.acc = a
		st.password, st.decErr = s.box.Decrypt(a.PasswordEnc)
		if st.decErr != nil {
			log.Printf("account %d (%s): %v", a.ID, a.Name, st.decErr)
		}
		next[a.ID] = st
	}
	s.states = next
	return nil
}

// rebuildCounters restores hourly/daily usage from the attempt log after a restart.
func (s *Scheduler) rebuildCounters() {
	now := time.Now()
	y, m, d := now.Date()
	startOfDay := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	hourAgo := now.Add(-time.Hour)
	since := startOfDay
	if hourAgo.Before(since) {
		since = hourAgo
	}
	var rows []store.SendAttempt
	if err := s.db.DB.Select("account_id", "created_at").Where("created_at >= ?", since).Order("created_at").Find(&rows).Error; err != nil {
		log.Printf("rebuild counters: %v", err)
		return
	}
	today := store.Day(now)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range rows {
		st := s.states[r.AccountID]
		if st == nil {
			continue
		}
		if st.day != today {
			st.day, st.dayCount = today, 0
		}
		if !r.CreatedAt.Before(startOfDay) {
			st.dayCount++
		}
		if r.CreatedAt.After(hourAgo) {
			st.window = append(st.window, r.CreatedAt)
		}
	}
}

func (st *accState) prune(now time.Time) {
	cut := now.Add(-time.Hour)
	i := 0
	for i < len(st.window) && !st.window[i].After(cut) {
		i++
	}
	if i > 0 {
		st.window = append(st.window[:0], st.window[i:]...)
	}
	if d := store.Day(now); st.day != d {
		st.day, st.dayCount = d, 0
	}
}

func (st *accState) minuteCount(now time.Time) int {
	cut := now.Add(-time.Minute)
	n := 0
	for i := len(st.window) - 1; i >= 0 && st.window[i].After(cut); i-- {
		n++
	}
	return n
}

// check returns "" when the account may send now, otherwise the reason it may not.
func (st *accState) check(now time.Time) string {
	a := &st.acc
	switch {
	case !a.Enabled:
		return "disabled"
	case st.decErr != nil:
		return "password decrypt failed"
	case a.CooldownUntil != nil && now.Before(*a.CooldownUntil):
		return "cooldown"
	case a.LimitPerDay > 0 && st.dayCount >= a.LimitPerDay:
		return "daily limit"
	case a.LimitPerHour > 0 && len(st.window) >= a.LimitPerHour:
		return "hourly limit"
	case a.LimitPerMinute > 0 && st.minuteCount(now) >= a.LimitPerMinute:
		return "minute limit"
	}
	return ""
}

// Route restricts which accounts may send a message.
type Route struct {
	Groups []string // the account must carry one of these tags; empty = any account
	Domain string   // recipient domain; accounts listing it in PreferDomains are tried first
}

// Acquire picks an account (excluding already-tried ones) and reserves one send slot on it.
func (s *Scheduler) Acquire(exclude map[uint]bool, rt Route) (*Lease, error) {
	strategy := s.cfg.Get().Strategy
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	var cands, preferred []*accState
	reasons := map[string]int{}
	inGroup := 0
	for _, st := range s.states {
		st.prune(now)
		if !MatchGroups(st.acc.Groups, rt.Groups) {
			continue
		}
		inGroup++
		if exclude[st.acc.ID] {
			continue
		}
		if r := st.check(now); r != "" {
			reasons[r]++
			continue
		}
		cands = append(cands, st)
		if rt.Domain != "" && MatchDomain(st.acc.PreferDomains, rt.Domain) {
			preferred = append(preferred, st)
		}
	}
	if len(cands) == 0 {
		switch {
		case len(s.states) == 0:
			return nil, fmt.Errorf("%w: no accounts configured", ErrNoAccount)
		case inGroup == 0:
			return nil, fmt.Errorf("%w: no accounts in group %s", ErrNoAccount, strings.Join(rt.Groups, "/"))
		case len(reasons) == 0:
			return nil, fmt.Errorf("%w: all accounts already tried", ErrNoAccount)
		}
		return nil, fmt.Errorf("%w: %s", ErrNoAccount, summarize(reasons))
	}
	if len(preferred) > 0 {
		cands = preferred
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].acc.ID < cands[j].acc.ID })

	var pick *accState
	switch strategy {
	case "round_robin":
		pick = cands[0]
		for _, c := range cands {
			if c.acc.ID > s.lastRR {
				pick = c
				break
			}
		}
	case "least_used":
		pick = cands[0]
		for _, c := range cands[1:] {
			if c.dayCount < pick.dayCount ||
				(c.dayCount == pick.dayCount && usedBefore(c.acc.LastUsedAt, pick.acc.LastUsedAt)) {
				pick = c
			}
		}
	case "random":
		total := 0
		for _, c := range cands {
			total += weight(c)
		}
		r := rand.IntN(total)
		for _, c := range cands {
			if r -= weight(c); r < 0 {
				pick = c
				break
			}
		}
	default: // smooth weighted round-robin (nginx style): even spread, proportional to weight
		total := 0
		for _, c := range cands {
			c.cw += weight(c)
			total += weight(c)
			if pick == nil || c.cw > pick.cw {
				pick = c
			}
		}
		pick.cw -= total
	}

	s.lastRR = pick.acc.ID
	pick.window = append(pick.window, now)
	pick.dayCount++
	t := now
	pick.acc.LastUsedAt = &t
	return &Lease{Account: pick.acc, Password: pick.password}, nil
}

// SplitList splits a comma / space / newline separated list, lower-cased and de-duplicated.
func SplitList(v string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '，' || r == ' ' || r == '\n' || r == ';' }) {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// MatchGroups reports whether an account tagged with accGroups may serve a key limited to want (empty = any).
func MatchGroups(accGroups string, want []string) bool {
	if len(want) == 0 {
		return true
	}
	for _, g := range SplitList(accGroups) {
		for _, w := range want {
			if g == w {
				return true
			}
		}
	}
	return false
}

// MatchDomain reports whether domain equals, or is a subdomain of, an entry of list.
func MatchDomain(list, domain string) bool {
	domain = strings.ToLower(domain)
	for _, d := range SplitList(list) {
		d = strings.TrimPrefix(strings.TrimPrefix(d, "*"), ".")
		if d != "" && (domain == d || strings.HasSuffix(domain, "."+d)) {
			return true
		}
	}
	return false
}

func weight(st *accState) int {
	if st.acc.Weight < 1 {
		return 1
	}
	return st.acc.Weight
}

func usedBefore(a, b *time.Time) bool {
	if a == nil {
		return b != nil
	}
	return b != nil && a.Before(*b)
}

func summarize(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("%d %s", m[k], k)
	}
	return out
}

// Report records the outcome of an attempt and updates the account's health.
func (s *Scheduler) Report(id uint, kind mailer.Kind, errMsg string) {
	set := s.cfg.Get()
	now := time.Now()
	s.mu.Lock()
	st := s.states[id]
	if st == nil {
		s.mu.Unlock()
		return
	}
	a := &st.acc
	hadCooldown := a.CooldownUntil != nil // also an expired one: the account was benched until this success
	coolingNow := hadCooldown && now.Before(*a.CooldownUntil)
	switch kind {
	case mailer.KindOK:
		a.ConsecutiveFails, a.CooldownUntil, a.LastError = 0, nil, ""
	case mailer.KindRecipient:
		// not the account's fault
	case mailer.KindAuth:
		a.ConsecutiveFails++
		a.LastError = errMsg
		until := now.Add(time.Duration(set.CooldownMinutes) * time.Minute)
		a.CooldownUntil = &until
		st.lastKind = kind
	default:
		a.ConsecutiveFails++
		a.LastError = errMsg
		st.lastKind = kind
		if a.ConsecutiveFails >= set.FailThreshold {
			until := now.Add(time.Duration(set.CooldownMinutes) * time.Minute)
			a.CooldownUntil = &until
		}
	}
	upd := map[string]any{
		"consecutive_fails": a.ConsecutiveFails,
		"cooldown_until":    a.CooldownUntil,
		"last_error":        a.LastError,
		"last_used_at":      a.LastUsedAt,
	}
	snap := *a
	s.mu.Unlock()
	if !coolingNow && snap.CooldownUntil != nil && now.Before(*snap.CooldownUntil) && s.OnCooldown != nil {
		s.OnCooldown(snap, *snap.CooldownUntil)
	}
	if hadCooldown && kind == mailer.KindOK && s.OnRecover != nil {
		s.OnRecover(snap)
	}
	if err := s.db.DB.Model(&store.Account{}).Where("id = ?", id).UpdateColumns(upd).Error; err != nil {
		log.Printf("persist account %d health: %v", id, err)
	}
}

// RecordCheck stores a health-check result. A passing check lifts a cooldown caused by auth failure.
func (s *Scheduler) RecordCheck(id uint, checkErr error) {
	now := time.Now()
	s.mu.Lock()
	st := s.states[id]
	if st == nil {
		s.mu.Unlock()
		return
	}
	a := &st.acc
	a.LastCheckAt, a.LastCheckOK = &now, checkErr == nil
	authCooled := st.lastKind == mailer.KindAuth && a.CooldownUntil != nil
	s.mu.Unlock()

	s.db.DB.Model(&store.Account{}).Where("id = ?", id).UpdateColumns(map[string]any{"last_check_at": now, "last_check_ok": checkErr == nil})
	if checkErr != nil {
		kind := mailer.Classify(checkErr)
		if kind == mailer.KindRecipient {
			kind = mailer.KindAccount
		}
		s.Report(id, kind, "health check: "+mailer.Short(checkErr))
	} else if authCooled {
		s.ResetHealth(id)
	}
}

// ResetHealth clears failures and cooldown (admin action or recovered auth).
func (s *Scheduler) ResetHealth(id uint) {
	s.mu.Lock()
	if st := s.states[id]; st != nil {
		st.acc.ConsecutiveFails, st.acc.CooldownUntil, st.acc.LastError = 0, nil, ""
		st.lastKind = mailer.KindOK
	}
	s.mu.Unlock()
	s.db.DB.Model(&store.Account{}).Where("id = ?", id).UpdateColumns(map[string]any{"consecutive_fails": 0, "cooldown_until": nil, "last_error": ""})
}

// Snapshot returns live account state keyed by ID, including health fields not yet re-read from DB.
func (s *Scheduler) Snapshot() map[uint]Runtime {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[uint]Runtime, len(s.states))
	for id, st := range s.states {
		st.prune(now)
		r := st.check(now)
		out[id] = Runtime{
			MinuteCount: st.minuteCount(now),
			HourCount:   len(st.window),
			DayCount:    st.dayCount,
			Available:   r == "",
			Reason:      r,
		}
	}
	return out
}

// Credentials returns the decrypted account for direct admin operations (test/test-send).
func (s *Scheduler) Credentials(id uint) (*Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.states[id]
	if st == nil {
		return nil, store.ErrNotFound
	}
	if st.decErr != nil {
		return nil, st.decErr
	}
	return &Lease{Account: st.acc, Password: st.password}, nil
}
