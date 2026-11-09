// Package smtpd lets client programs submit mail over SMTP instead of HTTP.
// They authenticate with an API key as the password; accepted mail goes through the same
// rotation, limits, retries and statistics as the HTTP API.
package smtpd

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	netmail "net/mail"
	"os"
	"strings"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"mailsender/internal/core"
	"mailsender/internal/store"
)

const maxRecipients = 50

type Options struct {
	Listen            string      // SMTP with STARTTLS (if TLS != nil); "" = off
	ListenTLS         string      // implicit TLS; "" = off
	Hostname          string      // greeting name
	TLS               *tls.Config // nil = no TLS available
	AllowInsecureAuth bool        // allow AUTH before STARTTLS even when TLS is available
	MaxMessageBytes   int64
}

// Info describes the running listeners for the admin console.
type Info struct {
	Listen       string `json:"listen"`
	ListenTLS    string `json:"listen_tls"`
	Hostname     string `json:"hostname"`
	STARTTLS     bool   `json:"starttls"`
	InsecureAuth bool   `json:"insecure_auth"`
	MaxMiB       int64  `json:"max_mib"`
}

type Ingress struct {
	servers []*smtp.Server
	Info    Info
}

func Start(svc *core.Service, o Options) (*Ingress, error) {
	if o.ListenTLS != "" && o.TLS == nil {
		return nil, errors.New("SMTPS listener requires TLS (set MS_TLS_CERT/MS_TLS_KEY or MS_TLS_DOMAIN)")
	}
	host := o.Hostname
	if host == "" {
		host, _ = os.Hostname()
	}
	insecure := o.TLS == nil || o.AllowInsecureAuth
	in := &Ingress{Info: Info{Listen: o.Listen, ListenTLS: o.ListenTLS, Hostname: host, STARTTLS: o.TLS != nil && o.Listen != "",
		InsecureAuth: insecure, MaxMiB: o.MaxMessageBytes >> 20}}
	be := &backend{svc: svc}
	mk := func(addr string) *smtp.Server {
		s := smtp.NewServer(be)
		s.Addr = addr
		s.Domain = host
		s.TLSConfig = o.TLS
		s.AllowInsecureAuth = insecure
		s.MaxMessageBytes = o.MaxMessageBytes
		s.MaxRecipients = maxRecipients
		s.ReadTimeout = 5 * time.Minute
		s.WriteTimeout = 5 * time.Minute // includes waiting for delivery after DATA
		return s
	}
	if o.Listen != "" {
		s := mk(o.Listen)
		ln, err := net.Listen("tcp", o.Listen)
		if err != nil {
			return nil, fmt.Errorf("smtp listen %s: %w", o.Listen, err)
		}
		in.servers = append(in.servers, s)
		go func() {
			if err := s.Serve(ln); err != nil && !errors.Is(err, smtp.ErrServerClosed) {
				log.Printf("smtp %s: %v", o.Listen, err)
			}
		}()
		mode := "STARTTLS"
		if o.TLS == nil {
			mode = "no TLS — AUTH is sent in clear text, use only on trusted networks"
		}
		log.Printf("SMTP ingress listening on %s (%s)", o.Listen, mode)
	}
	if o.ListenTLS != "" {
		s := mk(o.ListenTLS)
		ln, err := tls.Listen("tcp", o.ListenTLS, o.TLS)
		if err != nil {
			in.Close()
			return nil, fmt.Errorf("smtps listen %s: %w", o.ListenTLS, err)
		}
		in.servers = append(in.servers, s)
		go func() {
			if err := s.Serve(ln); err != nil && !errors.Is(err, smtp.ErrServerClosed) {
				log.Printf("smtps %s: %v", o.ListenTLS, err)
			}
		}()
		log.Printf("SMTPS ingress listening on %s (implicit TLS)", o.ListenTLS)
	}
	return in, nil
}

func (in *Ingress) Close() {
	for _, s := range in.servers {
		_ = s.Close()
	}
}

type backend struct{ svc *core.Service }

func (b *backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	ip, _, _ := net.SplitHostPort(c.Conn().RemoteAddr().String())
	if until, banned := b.svc.Guard.Banned("smtp", ip); banned {
		return nil, bannedErr(until)
	}
	return &session{svc: b.svc, ip: ip}, nil
}

func bannedErr(until time.Time) *smtp.SMTPError {
	return smtpErr(554, smtp.EnhancedCode{5, 7, 1}, "too many failed logins from your IP, blocked until %s", until.Format(time.RFC3339))
}

type session struct {
	svc      *core.Service
	ip       string
	key      *store.APIKey
	rcpts    []string
	failedPW map[string]bool // passwords already counted as failed on this connection
}

func smtpErr(code int, enh smtp.EnhancedCode, format string, a ...any) *smtp.SMTPError {
	return &smtp.SMTPError{Code: code, EnhancedCode: enh, Message: fmt.Sprintf(format, a...)}
}

func (s *session) AuthMechanisms() []string { return []string{sasl.Plain, sasl.Login} }

func (s *session) Auth(mech string) (sasl.Server, error) {
	// The API key is the password. For clients that put it in the username field instead, accept that too.
	verify := func(username, password string) error {
		if until, banned := s.svc.Guard.Banned("smtp", s.ip); banned {
			return bannedErr(until)
		}
		cands := []string{password}
		if strings.HasPrefix(username, "ms_") {
			cands = append(cands, username)
		}
		var last error
		for _, c := range cands {
			k, aerr := s.svc.AuthKey(c, s.ip)
			if aerr == nil {
				s.key = k
				s.svc.Guard.Success("smtp", s.ip)
				return nil
			}
			last = aerr
		}
		time.Sleep(time.Second) // slow down guessing
		// clients such as Python's smtplib retry the same password with every mechanism; count it once
		if s.failedPW == nil {
			s.failedPW = map[string]bool{}
		}
		counted := s.failedPW[password]
		s.failedPW[password] = true
		if !counted && s.svc.Guard.Fail("smtp", s.ip, last.Error()) {
			until, _ := s.svc.Guard.Banned("smtp", s.ip)
			return bannedErr(until)
		}
		return smtpErr(535, smtp.EnhancedCode{5, 7, 8}, "authentication failed: %s", last)
	}
	switch mech {
	case sasl.Plain:
		return sasl.NewPlainServer(func(_, u, p string) error { return verify(u, p) }), nil
	case sasl.Login:
		return &loginServer{verify: verify}, nil
	}
	return nil, smtp.ErrAuthUnsupported
}

func (s *session) Mail(_ string, _ *smtp.MailOptions) error {
	if s.key == nil {
		return smtp.ErrAuthRequired
	}
	if err := s.svc.AllowKeyRate(s.key); err != nil {
		return smtpErr(450, smtp.EnhancedCode{4, 7, 1}, "%s", err)
	}
	s.rcpts = nil
	return nil
}

func (s *session) Rcpt(to string, _ *smtp.RcptOptions) error {
	if s.key == nil {
		return smtp.ErrAuthRequired
	}
	a, err := netmail.ParseAddress(to)
	if err != nil {
		return smtpErr(501, smtp.EnhancedCode{5, 1, 3}, "bad recipient address")
	}
	if len(s.rcpts) >= maxRecipients {
		return smtpErr(452, smtp.EnhancedCode{4, 5, 3}, "too many recipients")
	}
	if err := s.svc.CheckRecipient(s.key, a.Address); err != nil {
		if errors.Is(err, core.ErrSuppressed) {
			return smtpErr(550, smtp.EnhancedCode{5, 1, 1}, "%s", err)
		}
		return smtpErr(450, smtp.EnhancedCode{4, 2, 1}, "%s", err)
	}
	s.rcpts = append(s.rcpts, a.Address)
	return nil
}

func (s *session) Data(r io.Reader) error {
	if s.key == nil {
		return smtp.ErrAuthRequired
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return err // includes smtp.ErrDataTooLarge
	}
	msgs, err := s.svc.Relay(s.key, s.ip, s.rcpts, raw)
	if err != nil {
		log.Printf("smtp ingress: %v", err)
		return smtpErr(451, smtp.EnhancedCode{4, 3, 0}, "internal error, try again later")
	}
	sent, permanent := 0, 0
	var firstErr string
	for _, m := range msgs {
		switch {
		case m.Status == store.StatusSent:
			sent++
		case m.Permanent:
			permanent++
			fallthrough
		default:
			if firstErr == "" {
				firstErr = fmt.Sprintf("%s: %s", m.To, m.Error)
			}
		}
	}
	switch {
	case sent > 0:
		// SMTP cannot report per-recipient results after DATA; failures are visible in the message log.
		return nil
	case permanent == len(msgs):
		return smtpErr(554, smtp.EnhancedCode{5, 0, 0}, "%s", trim(firstErr))
	default:
		// nothing delivered for a possibly temporary reason: let the client queue and retry
		return smtpErr(451, smtp.EnhancedCode{4, 4, 5}, "%s", trim(firstErr))
	}
}

func (s *session) Reset()        { s.rcpts = nil }
func (s *session) Logout() error { return nil }

func trim(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 400 {
		s = s[:400] + "..."
	}
	return s
}

// loginServer implements the (non-standard but widely used) AUTH LOGIN mechanism.
type loginServer struct {
	step   int
	user   string
	verify func(user, pass string) error
}

func (l *loginServer) Next(resp []byte) ([]byte, bool, error) {
	switch l.step {
	case 0:
		if len(resp) > 0 { // initial response carries the username
			l.user, l.step = string(resp), 2
			return []byte("Password:"), false, nil
		}
		l.step = 1
		return []byte("Username:"), false, nil
	case 1:
		l.user, l.step = string(resp), 2
		return []byte("Password:"), false, nil
	case 2:
		l.step = 3
		return nil, true, l.verify(l.user, string(resp))
	}
	return nil, true, errors.New("unexpected AUTH LOGIN step")
}
