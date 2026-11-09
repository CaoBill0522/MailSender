package core

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-smtp"

	"mailsender/internal/mailer"
	"mailsender/internal/secure"
	"mailsender/internal/store"
)

// ---- fake upstream SMTP server: rejects RCPT for addresses starting with "bad" with 550 ----

type upstream struct {
	mu   sync.Mutex
	data []string
}

func (u *upstream) NewSession(*smtp.Conn) (smtp.Session, error) { return &upSession{u: u}, nil }

type upSession struct{ u *upstream }

func (s *upSession) Mail(string, *smtp.MailOptions) error { return nil }
func (s *upSession) Rcpt(to string, _ *smtp.RcptOptions) error {
	if strings.HasPrefix(to, "bad") {
		return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 1, 1}, Message: "Mailbox not found"}
	}
	return nil
}
func (s *upSession) Data(r io.Reader) error {
	b, _ := io.ReadAll(r)
	s.u.mu.Lock()
	s.u.data = append(s.u.data, string(b))
	s.u.mu.Unlock()
	return nil
}
func (s *upSession) Reset()        {}
func (s *upSession) Logout() error { return nil }

func startUpstream(t *testing.T) (*upstream, int) {
	t.Helper()
	u := &upstream{}
	srv := smtp.NewServer(u)
	srv.Domain = "upstream.test"
	srv.AllowInsecureAuth = true
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return u, ln.Addr().(*net.TCPAddr).Port
}

type env struct {
	db  *store.Store
	box *secure.Box
	cfg *SettingsStore
	sch *Scheduler
	svc *Service
}

func newEnv(t *testing.T, accs ...store.Account) *env {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	box := secure.NewBox("test-secret")
	cfg := NewSettingsStore(db)
	set := cfg.Get()
	set.RecipientIntervalSec, set.RecipientDailyMax, set.MaxAttempts = 0, 0, 2
	if err := cfg.Set(set); err != nil {
		t.Fatal(err)
	}
	for i := range accs {
		accs[i].Enabled = true
		if accs[i].Security == "" {
			accs[i].Security, accs[i].AuthType = "none", "none"
		}
		if err := db.DB.Create(&accs[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	sch, err := NewScheduler(db, box, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &env{db: db, box: box, cfg: cfg, sch: sch, svc: NewService(db, sch, cfg, box)}
}

func TestRoutingGroupsAndDomains(t *testing.T) {
	e := newEnv(t,
		store.Account{Name: "A", Host: "x", Port: 1, FromAddr: "a@x.com", Groups: "verify"},
		store.Account{Name: "B", Host: "x", Port: 1, FromAddr: "b@x.com", Groups: "verify,notify", PreferDomains: "qq.com"},
		store.Account{Name: "C", Host: "x", Port: 1, FromAddr: "c@x.com", Groups: "notify"},
	)
	pick := func(rt Route) string {
		l, err := e.sch.Acquire(nil, rt)
		if err != nil {
			t.Fatalf("acquire %+v: %v", rt, err)
		}
		return l.Account.Name
	}
	for i := 0; i < 4; i++ {
		if got := pick(Route{Groups: []string{"verify"}, Domain: "qq.com"}); got != "B" {
			t.Fatalf("qq.com should prefer B, got %s", got)
		}
		if got := pick(Route{Groups: []string{"verify"}, Domain: "vip.qq.com"}); got != "B" {
			t.Fatalf("subdomain should prefer B, got %s", got)
		}
	}
	seen := map[string]bool{}
	for i := 0; i < 6; i++ {
		seen[pick(Route{Groups: []string{"verify"}, Domain: "gmail.com"})] = true
	}
	if !seen["A"] || !seen["B"] || seen["C"] {
		t.Fatalf("verify group should rotate A and B only, saw %v", seen)
	}
	// the preferred account is excluded after failing: fall back to the rest of the group
	l, err := e.sch.Acquire(map[uint]bool{2: true}, Route{Groups: []string{"verify"}, Domain: "qq.com"})
	if err != nil || l.Account.Name != "A" {
		t.Fatalf("fallback: %v %v", l, err)
	}
	if _, err := e.sch.Acquire(nil, Route{Groups: []string{"nope"}}); err == nil || !strings.Contains(err.Error(), "no accounts in group") {
		t.Fatalf("unknown group: %v", err)
	}
	seen = map[string]bool{}
	for i := 0; i < 6; i++ {
		seen[pick(Route{})] = true
	}
	if len(seen) != 3 {
		t.Fatalf("no group should use every account, saw %v", seen)
	}
}

func TestBounceSuppressionAndCallback(t *testing.T) {
	up, port := startUpstream(t)
	e := newEnv(t, store.Account{Name: "up", Host: "127.0.0.1", Port: port, FromAddr: "sender@pool.test"})

	type hit struct {
		p   CallbackPayload
		sig string
		ts  string
		raw []byte
	}
	hits := make(chan hit, 4)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var p CallbackPayload
		_ = json.Unmarshal(b, &p)
		hits <- hit{p, r.Header.Get("X-MailSender-Signature"), r.Header.Get("X-MailSender-Timestamp"), b}
	}))
	defer hook.Close()

	enc, _ := e.box.Encrypt("whsec_test")
	key := &store.APIKey{Name: "app", Enabled: true, CallbackURL: hook.URL, CallbackSecret: enc}
	e.db.DB.Create(key)

	raw := []byte("From: App <app@myapp.com>\r\nTo: x\r\nSubject: code\r\nX-MS-Ref: order-42\r\n\r\n123456\r\n")
	msgs, err := e.svc.Relay(key, "10.0.0.1", []string{"bad@example.com", "good@example.com"}, raw)
	if err != nil {
		t.Fatal(err)
	}
	bad, good := msgs[0], msgs[1]
	if bad.Status != store.StatusFailed || !bad.Permanent {
		t.Fatalf("bad recipient: status=%s permanent=%v err=%s", bad.Status, bad.Permanent, bad.Error)
	}
	if good.Status != store.StatusSent || !strings.HasSuffix(good.MsgID, "@pool.test>") || good.Ref != "order-42" {
		t.Fatalf("good recipient: %+v", good)
	}
	if strings.Contains(up.data[0], "X-MS-Ref") {
		t.Fatal("X-MS-Ref must be stripped before relaying")
	}
	if !strings.Contains(up.data[0], "Message-ID: "+good.MsgID) {
		t.Fatalf("delivered Message-ID differs from recorded one %s:\n%s", good.MsgID, up.data[0])
	}

	// the bounced address is suppressed: the next RCPT is refused permanently, the good one is not
	if err := e.svc.CheckRecipient(key, "BAD@example.com"); !errors.Is(err, ErrSuppressed) {
		t.Fatalf("expected suppression, got %v", err)
	}
	if err := e.svc.CheckRecipient(key, "good@example.com"); err != nil {
		t.Fatalf("good address blocked: %v", err)
	}
	var sup store.Suppression
	e.db.DB.First(&sup, "addr = ?", "bad@example.com")
	if sup.Source != "bounce" || sup.Hits != 1 || sup.MessageID != bad.ID || sup.ExpiresAt == nil {
		t.Fatalf("suppression row: %+v", sup)
	}

	// one signed callback per message
	got := map[string]hit{}
	for i := 0; i < 2; i++ {
		select {
		case h := <-hits:
			got[h.p.To] = h
		case <-time.After(5 * time.Second):
			t.Fatal("callback not received")
		}
	}
	for _, h := range got {
		if h.sig != "sha256="+SignCallback("whsec_test", h.ts, h.raw) {
			t.Fatalf("bad signature for %s", h.p.To)
		}
	}
	if g := got["good@example.com"].p; g.Event != "message.sent" || g.Ref != "order-42" || g.MessageID != good.MsgID {
		t.Fatalf("sent payload: %+v", g)
	}
	if b := got["bad@example.com"].p; b.Event != "message.failed" || !b.Permanent {
		t.Fatalf("failed payload: %+v", b)
	}
	time.Sleep(100 * time.Millisecond)
	var m store.Message
	e.db.DB.First(&m, good.ID)
	if m.Callback != "ok" {
		t.Fatalf("callback status = %q", m.Callback)
	}
}

func TestAuthGuard(t *testing.T) {
	e := newEnv(t)
	set := e.cfg.Get()
	set.AuthMaxFails, set.AuthBanMinutes = 3, 10
	_ = e.cfg.Set(set)
	var bans []string
	e.svc.Guard.OnBan = func(scope, ip string, _ int, _ time.Time) { bans = append(bans, scope+" "+ip) }
	g := e.svc.Guard

	g.Fail("smtp", "1.2.3.4", "bad key")
	g.Fail("smtp", "1.2.3.4", "bad key")
	g.Success("smtp", "1.2.3.4") // a success resets the count
	g.Fail("smtp", "1.2.3.4", "bad key")
	g.Fail("smtp", "1.2.3.4", "bad key")
	if _, b := g.Banned("smtp", "1.2.3.4"); b {
		t.Fatal("banned too early")
	}
	if !g.Fail("smtp", "1.2.3.4", "bad key") {
		t.Fatal("third failure should ban")
	}
	if _, b := g.Banned("smtp", "1.2.3.4"); !b {
		t.Fatal("not banned")
	}
	if _, b := g.Banned("admin", "1.2.3.4"); b {
		t.Fatal("bans are per scope")
	}
	for i := 0; i < 5; i++ {
		g.Fail("smtp", "127.0.0.1", "local")
	}
	if _, b := g.Banned("smtp", "127.0.0.1"); b {
		t.Fatal("loopback must not be banned")
	}
	if l := g.List(); len(l) != 1 || l[0].IP != "1.2.3.4" {
		t.Fatalf("list: %+v", l)
	}
	g.Unban("smtp", "1.2.3.4")
	if _, b := g.Banned("smtp", "1.2.3.4"); b || len(bans) != 1 {
		t.Fatalf("unban failed, bans=%v", bans)
	}
}

func TestCooldownHooks(t *testing.T) {
	e := newEnv(t, store.Account{Name: "A", Host: "x", Port: 1, FromAddr: "a@x.com"})
	var cooled, recovered int
	e.sch.OnCooldown = func(store.Account, time.Time) { cooled++ }
	e.sch.OnRecover = func(store.Account) { recovered++ }
	e.sch.Report(1, mailer.KindAccount, "421 busy")
	e.sch.Report(1, mailer.KindAccount, "421 busy")
	if cooled != 0 {
		t.Fatal("cooldown before threshold")
	}
	e.sch.Report(1, mailer.KindAccount, "421 busy") // threshold 3
	e.sch.Report(1, mailer.KindAuth, "535 auth")    // already cooling: no second alert
	if cooled != 1 {
		t.Fatalf("cooled = %d", cooled)
	}
	e.sch.Report(1, mailer.KindOK, "")
	if recovered != 1 {
		t.Fatalf("recovered = %d", recovered)
	}
}

func TestMatchHelpers(t *testing.T) {
	if !MatchDomain("qq.com, *.edu.cn", "mail.pku.edu.cn") || MatchDomain("qq.com", "fakeqq.com") || !MatchDomain("QQ.com", "qq.com") {
		t.Fatal("MatchDomain")
	}
	if !MatchGroups("A, b", []string{"b"}) || MatchGroups("", []string{"b"}) || !MatchGroups("", nil) {
		t.Fatal("MatchGroups")
	}
	if got := strings.Join(SplitList(" a，B;a\nc "), ","); got != "a,b,c" {
		t.Fatalf("SplitList = %s", got)
	}
}
