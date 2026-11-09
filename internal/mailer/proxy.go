package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// Dialer opens the TCP connection to the SMTP server. nil means a direct connection.
type Dialer func(ctx context.Context, network, addr string) (net.Conn, error)

// ProxyError marks a failure of the egress proxy itself (unreachable, bad proxy credentials),
// so it is never mistaken for an SMTP authentication or recipient error.
type ProxyError struct {
	Name string
	Err  error
}

func (e *ProxyError) Error() string { return "出口代理 " + e.Name + ": " + e.Err.Error() }
func (e *ProxyError) Unwrap() error { return e.Err }

// SOCKS5 returns a Dialer that connects through a SOCKS5 proxy. Host names are resolved by the
// proxy, so DNS lookups also leave from the proxy's network. A failing proxy is an error, never
// a silent fallback to a direct connection.
func SOCKS5(name, addr, username, password string) (Dialer, error) {
	var auth *proxy.Auth
	if username != "" || password != "" {
		auth = &proxy.Auth{User: username, Password: password}
	}
	d, err := proxy.SOCKS5("tcp", addr, auth, &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second})
	if err != nil {
		return nil, &ProxyError{name, err}
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		return nil, &ProxyError{name, errors.New("dialer does not support contexts")}
	}
	return func(ctx context.Context, network, target string) (net.Conn, error) {
		c, err := cd.DialContext(ctx, network, target)
		if err != nil {
			return nil, &ProxyError{name, err}
		}
		return c, nil
	}, nil
}

// tlsDialer wraps dial with implicit TLS (port 465 style) for go-mail, which only adds TLS itself
// when it owns the dialer.
func tlsDialer(dial Dialer, cfg *tls.Config) Dialer {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		raw, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		tc := tls.Client(raw, cfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			raw.Close()
			return nil, err
		}
		return tc, nil
	}
}

// IPEchoURLs answer with the caller's public IP as plain text. Tried in order.
var IPEchoURLs = []string{"https://api.ipify.org", "https://ifconfig.me/ip", "https://icanhazip.com"}

// EgressIP reports the public IP address that connections made through dial leave from.
func EgressIP(ctx context.Context, dial Dialer) (string, error) {
	tr := &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 10 * time.Second, DisableKeepAlives: true}
	if dial != nil {
		tr.DialContext = dial
	}
	defer tr.CloseIdleConnections()
	cl := &http.Client{Transport: tr, Timeout: 20 * time.Second}
	var errs []string
	for _, u := range IPEchoURLs {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		req.Header.Set("User-Agent", "MailSender")
		resp, err := cl.Do(req)
		if err != nil {
			var pe *ProxyError
			if errors.As(err, &pe) {
				return "", pe // the proxy is down; other echo services won't help
			}
			errs = append(errs, err.Error())
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		resp.Body.Close()
		ip := strings.TrimSpace(string(body))
		if resp.StatusCode == 200 && net.ParseIP(ip) != nil {
			return ip, nil
		}
		errs = append(errs, fmt.Sprintf("%s: HTTP %d", u, resp.StatusCode))
		if ctx.Err() != nil {
			break
		}
	}
	return "", errors.New("无法获取出口 IP：" + strings.Join(errs, "; "))
}
