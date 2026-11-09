package config

import (
	"flag"
	"os"
	"strings"
)

// Config holds process-level settings. Runtime (hot-reloadable) settings live in the DB.
type Config struct {
	Listen         string
	DataDir        string
	Secret         string // master key for encrypting SMTP passwords and signing admin tokens
	AdminPassword  string // initial admin password (only used when no admin exists yet)
	Disable2FA     bool   // turn off admin two-factor login at startup (lost authenticator)
	TrustedProxies []string

	// TLS: either certificate files or automatic Let's Encrypt certificates for the given domains.
	TLSCert     string
	TLSKey      string
	TLSDomains  []string
	ACMEEmail   string
	HTTPSListen string // HTTPS address, used only when TLS is configured

	// SMTP ingress: client programs submit mail over SMTP, authenticating with an API key.
	SMTPListen        string // plain SMTP with STARTTLS (when TLS configured); "" disables
	SMTPSListen       string // implicit TLS (SMTPS); requires TLS; "" disables
	SMTPHostname      string
	SMTPInsecureAuth  bool // allow AUTH on unencrypted connections even when TLS is available
	SMTPMaxMessageMiB int
}

func Load() *Config {
	c := &Config{}
	flag.StringVar(&c.Listen, "listen", env("MS_LISTEN", ":8080"), "HTTP listen address")
	flag.StringVar(&c.DataDir, "data", env("MS_DATA_DIR", "./data"), "data directory (database, secret key)")
	proxies := flag.String("trusted-proxies", env("MS_TRUSTED_PROXIES", ""), "comma separated trusted proxy IPs/CIDRs")
	flag.StringVar(&c.TLSCert, "tls-cert", env("MS_TLS_CERT", ""), "TLS certificate file (PEM, full chain)")
	flag.StringVar(&c.TLSKey, "tls-key", env("MS_TLS_KEY", ""), "TLS private key file (PEM)")
	domains := flag.String("tls-domain", env("MS_TLS_DOMAIN", ""), "comma separated domains for automatic Let's Encrypt certificates")
	flag.StringVar(&c.ACMEEmail, "acme-email", env("MS_ACME_EMAIL", ""), "contact email for Let's Encrypt (optional)")
	flag.StringVar(&c.HTTPSListen, "https-listen", env("MS_HTTPS_LISTEN", ":443"), "HTTPS listen address (when TLS is configured)")
	flag.StringVar(&c.SMTPListen, "smtp-listen", env("MS_SMTP_LISTEN", ":2525"), `SMTP submission address with STARTTLS, "off" to disable`)
	flag.StringVar(&c.SMTPSListen, "smtps-listen", env("MS_SMTPS_LISTEN", ""), "SMTPS (implicit TLS) address, e.g. :465; requires TLS")
	flag.StringVar(&c.SMTPHostname, "smtp-hostname", env("MS_SMTP_HOSTNAME", ""), "hostname announced by the SMTP server")
	flag.BoolVar(&c.SMTPInsecureAuth, "smtp-insecure-auth", env("MS_SMTP_INSECURE_AUTH", "") == "true", "allow SMTP AUTH without TLS even when TLS is configured")
	flag.IntVar(&c.SMTPMaxMessageMiB, "smtp-max-mib", 25, "maximum accepted SMTP message size in MiB")
	flag.Parse()
	c.Secret = os.Getenv("MS_SECRET")
	c.AdminPassword = os.Getenv("MS_ADMIN_PASSWORD")
	c.Disable2FA = os.Getenv("MS_DISABLE_2FA") == "true"
	c.TrustedProxies = split(*proxies)
	c.TLSDomains = split(*domains)
	for _, p := range []*string{&c.SMTPListen, &c.SMTPSListen, &c.HTTPSListen} {
		if strings.EqualFold(*p, "off") {
			*p = ""
		}
	}
	return c
}

func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
