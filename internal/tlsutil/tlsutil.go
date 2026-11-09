// Package tlsutil builds the TLS configuration shared by HTTPS and the SMTP ingress:
// either certificate files (reloaded when they change on disk) or automatic Let's Encrypt certificates.
package tlsutil

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

type Options struct {
	CertFile  string
	KeyFile   string
	Domains   []string // ACME (Let's Encrypt) domains
	ACMEEmail string
	DataDir   string
}

// Provider supplies certificates. A nil *Provider means TLS is not configured.
type Provider struct {
	Mode    string // "files" | "acme"
	Domains []string
	cfg     *tls.Config
	acme    *autocert.Manager
}

func New(o Options) (*Provider, error) {
	switch {
	case o.CertFile != "" || o.KeyFile != "":
		if o.CertFile == "" || o.KeyFile == "" {
			return nil, errors.New("MS_TLS_CERT and MS_TLS_KEY must be set together")
		}
		r := &fileReloader{certFile: o.CertFile, keyFile: o.KeyFile}
		if err := r.load(); err != nil {
			return nil, fmt.Errorf("load certificate: %w", err)
		}
		go r.watch()
		cfg := &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: r.get}
		return &Provider{Mode: "files", Domains: r.names(), cfg: cfg}, nil
	case len(o.Domains) > 0:
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(o.Domains...),
			Cache:      autocert.DirCache(filepath.Join(o.DataDir, "acme")),
			Email:      o.ACMEEmail,
		}
		cfg := m.TLSConfig()
		cfg.MinVersion = tls.VersionTLS12
		inner := m.GetCertificate
		// SMTP clients often omit SNI; fall back to the first configured domain.
		cfg.GetCertificate = func(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if h.ServerName == "" {
				h.ServerName = o.Domains[0]
			}
			return inner(h)
		}
		return &Provider{Mode: "acme", Domains: o.Domains, cfg: cfg, acme: m}, nil
	}
	return nil, nil
}

// Config returns a TLS config for HTTPS (includes the ACME ALPN protocol when in ACME mode).
func (p *Provider) Config() *tls.Config { return p.cfg.Clone() }

// SMTPConfig returns a TLS config for SMTP listeners (no HTTP ALPN protocols).
func (p *Provider) SMTPConfig() *tls.Config {
	c := p.cfg.Clone()
	c.NextProtos = nil
	return c
}

// HTTPHandler wraps the plain-HTTP handler so ACME HTTP-01 challenges are answered.
func (p *Provider) HTTPHandler(h http.Handler) http.Handler {
	if p == nil || p.acme == nil {
		return h
	}
	return p.acme.HTTPHandler(h)
}

// Hostname is the name to announce (SMTP greeting), or "" if unknown.
func (p *Provider) Hostname() string {
	if p == nil || len(p.Domains) == 0 {
		return ""
	}
	return p.Domains[0]
}

type fileReloader struct {
	certFile, keyFile string
	mu                sync.RWMutex
	cert              *tls.Certificate
	mtime             time.Time
}

func (r *fileReloader) load() error {
	c, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return err
	}
	st, _ := os.Stat(r.certFile)
	r.mu.Lock()
	r.cert = &c
	if st != nil {
		r.mtime = st.ModTime()
	}
	r.mu.Unlock()
	return nil
}

// watch reloads the certificate when the file changes (e.g. after certbot renews it).
func (r *fileReloader) watch() {
	for range time.Tick(time.Minute) {
		st, err := os.Stat(r.certFile)
		if err != nil {
			continue
		}
		r.mu.RLock()
		changed := st.ModTime().After(r.mtime)
		r.mu.RUnlock()
		if changed {
			if err := r.load(); err != nil {
				log.Printf("reload certificate: %v", err)
			} else {
				log.Printf("certificate reloaded from %s", r.certFile)
			}
		}
	}
}

func (r *fileReloader) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cert, nil
}

func (r *fileReloader) names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.cert == nil || r.cert.Leaf == nil {
		return nil
	}
	return r.cert.Leaf.DNSNames
}
