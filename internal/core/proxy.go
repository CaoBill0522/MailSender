package core

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"mailsender/internal/mailer"
	"mailsender/internal/store"
)

// DialerFor returns how the account reaches its SMTP server: nil for direct, or its SOCKS5 proxy.
// A missing proxy is an error rather than a fallback to the server's own IP.
func (s *Service) DialerFor(acc *store.Account) (mailer.Dialer, error) {
	if acc.ProxyID == 0 {
		return nil, nil
	}
	var p store.Proxy
	if err := s.DB.DB.First(&p, acc.ProxyID).Error; err != nil {
		return nil, &mailer.ProxyError{Name: fmt.Sprintf("#%d", acc.ProxyID), Err: fmt.Errorf("not found: %w", err)}
	}
	return s.ProxyDialer(&p)
}

// ProxyDialer builds the dialer for one proxy.
func (s *Service) ProxyDialer(p *store.Proxy) (mailer.Dialer, error) {
	pw, err := s.Box.Decrypt(p.PasswordEnc)
	if err != nil {
		return nil, &mailer.ProxyError{Name: p.Name, Err: fmt.Errorf("decrypt password: %w", err)}
	}
	return mailer.SOCKS5(p.Name, net.JoinHostPort(p.Host, strconv.Itoa(p.Port)), p.Username, pw)
}

// ProxyCheck is the result of looking up a proxy's egress IP.
type ProxyCheck struct {
	OK        bool   `json:"ok"`
	IP        string `json:"ip,omitempty"`
	Error     string `json:"error,omitempty"`
	LatencyMs int64  `json:"latency_ms"`
}

// CheckProxy connects through the proxy to an IP echo service and records the egress IP.
func (s *Service) CheckProxy(p *store.Proxy) ProxyCheck {
	start := time.Now()
	r := ProxyCheck{}
	dial, err := s.ProxyDialer(p)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		r.IP, err = mailer.EgressIP(ctx, dial)
		cancel()
	}
	r.LatencyMs = time.Since(start).Milliseconds()
	r.OK = err == nil
	r.Error = mailer.Short(err)
	now := time.Now()
	upd := map[string]any{"last_check_at": &now, "last_check_ok": r.OK, "last_error": r.Error, "last_latency_ms": r.LatencyMs}
	if r.OK {
		upd["last_ip"] = r.IP
	}
	s.DB.DB.Model(&store.Proxy{}).Where("id = ?", p.ID).Updates(upd)
	return r
}

// DirectIP reports the server's own egress IP, for comparison with the proxies.
func (s *Service) DirectIP() ProxyCheck {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ip, err := mailer.EgressIP(ctx, nil)
	return ProxyCheck{OK: err == nil, IP: ip, Error: mailer.Short(err), LatencyMs: time.Since(start).Milliseconds()}
}
