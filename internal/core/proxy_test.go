package core

import (
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"

	"mailsender/internal/mailer"
	"mailsender/internal/store"
)

// socks5 is a minimal RFC 1928/1929 proxy (CONNECT, optional username/password) that records targets.
type socks5 struct {
	user, pass string
	mu         sync.Mutex
	targets    []string
}

func startSOCKS5(t *testing.T, user, pass string) (*socks5, int) {
	t.Helper()
	p := &socks5{user: user, pass: pass}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go p.serve(c)
		}
	}()
	return p, ln.Addr().(*net.TCPAddr).Port
}

func (p *socks5) serve(c net.Conn) {
	defer c.Close()
	b := make([]byte, 262)
	if _, err := io.ReadFull(c, b[:2]); err != nil || b[0] != 5 {
		return
	}
	methods := b[2 : 2+int(b[1])]
	if _, err := io.ReadFull(c, methods); err != nil {
		return
	}
	want := byte(0)
	if p.user != "" {
		want = 2
	}
	if !strings.ContainsRune(string(methods), rune(want)) {
		c.Write([]byte{5, 0xff})
		return
	}
	c.Write([]byte{5, want})
	if want == 2 {
		io.ReadFull(c, b[:2])
		u := make([]byte, b[1])
		io.ReadFull(c, u)
		io.ReadFull(c, b[:1])
		pw := make([]byte, b[0])
		io.ReadFull(c, pw)
		if string(u) != p.user || string(pw) != p.pass {
			c.Write([]byte{1, 1})
			return
		}
		c.Write([]byte{1, 0})
	}
	if _, err := io.ReadFull(c, b[:4]); err != nil || b[1] != 1 {
		return
	}
	var host string
	switch b[3] {
	case 1:
		io.ReadFull(c, b[:4])
		host = net.IP(b[:4]).String()
	case 3:
		io.ReadFull(c, b[:1])
		h := make([]byte, b[0])
		io.ReadFull(c, h)
		host = string(h)
	case 4:
		io.ReadFull(c, b[:16])
		host = net.IP(b[:16]).String()
	}
	io.ReadFull(c, b[:2])
	target := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(b[:2]))))
	p.mu.Lock()
	p.targets = append(p.targets, target)
	p.mu.Unlock()
	up, err := net.Dial("tcp", target)
	if err != nil {
		c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer up.Close()
	c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	go io.Copy(up, c)
	io.Copy(c, up)
}

func TestSendThroughSOCKS5(t *testing.T) {
	up, port := startUpstream(t)
	px, pxPort := startSOCKS5(t, "u", "p@ss")
	e := newEnv(t)

	enc, _ := e.box.Encrypt("p@ss")
	good := store.Proxy{Name: "home", Host: "127.0.0.1", Port: pxPort, Username: "u", PasswordEnc: enc}
	encBad, _ := e.box.Encrypt("wrong")
	bad := store.Proxy{Name: "broken", Host: "127.0.0.1", Port: pxPort, Username: "u", PasswordEnc: encBad}
	e.db.DB.Create(&good)
	e.db.DB.Create(&bad)

	// "localhost" must reach the proxy as a name: DNS is resolved on the proxy side
	a := store.Account{Name: "via-proxy", Host: "localhost", Port: port, FromAddr: "a@pool.test", ProxyID: good.ID, Enabled: true, Security: "none", AuthType: "none"}
	e.db.DB.Create(&a)
	e.sch.Reload()
	key := &store.APIKey{Name: "app", Enabled: true}
	e.db.DB.Create(key)
	raw := []byte("From: x@app.test\r\nSubject: hi\r\n\r\nbody\r\n")

	msgs, err := e.svc.Relay(key, "10.0.0.1", []string{"one@example.com"}, raw)
	if err != nil || msgs[0].Status != store.StatusSent {
		t.Fatalf("send via proxy: %v %+v", err, msgs)
	}
	if len(px.targets) != 1 || px.targets[0] != "localhost:"+strconv.Itoa(port) || len(up.data) != 1 {
		t.Fatalf("proxy targets %v, upstream got %d", px.targets, len(up.data))
	}

	// rejected proxy credentials: classified as an account problem (not SMTP auth), and the
	// message fails over to the direct account instead of leaving from the wrong IP silently.
	e.db.DB.Model(&a).Update("proxy_id", bad.ID)
	direct := store.Account{Name: "direct", Host: "127.0.0.1", Port: port, FromAddr: "b@pool.test", Enabled: true, Security: "none", AuthType: "none"}
	e.db.DB.Create(&direct)
	e.sch.Reload()
	for i := 0; i < 2; i++ {
		msgs, err = e.svc.Relay(key, "10.0.0.1", []string{"two@example.com"}, raw)
		if err != nil || msgs[0].Status != store.StatusSent || msgs[0].AccountID != direct.ID {
			t.Fatalf("failover: %v %+v", err, msgs[0])
		}
	}
	var atts []store.SendAttempt
	e.db.DB.Where("account_id = ? AND ok = ?", a.ID, false).Find(&atts)
	if len(atts) == 0 || !strings.Contains(atts[0].Error, "出口代理 broken") {
		t.Fatalf("attempt through broken proxy: %+v", atts)
	}
	var acc store.Account
	e.db.DB.First(&acc, a.ID)
	if acc.CooldownUntil != nil && acc.ConsecutiveFails < 2 {
		t.Fatal("a proxy failure must not cool the account down like bad SMTP credentials")
	}

	// a dangling proxy id is an error, never a direct connection
	acc.ProxyID = 9999
	if _, err := e.svc.DialerFor(&acc); err == nil || mailer.Classify(err) != mailer.KindAccount {
		t.Fatalf("missing proxy: %v", err)
	}
	if d, err := e.svc.DialerFor(&direct); d != nil || err != nil {
		t.Fatalf("direct account: %v %v", d, err)
	}
}
