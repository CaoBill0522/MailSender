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
	TrustedProxies []string
}

func Load() *Config {
	c := &Config{}
	flag.StringVar(&c.Listen, "listen", env("MS_LISTEN", ":8080"), "HTTP listen address")
	flag.StringVar(&c.DataDir, "data", env("MS_DATA_DIR", "./data"), "data directory (database, secret key)")
	proxies := flag.String("trusted-proxies", env("MS_TRUSTED_PROXIES", ""), "comma separated trusted proxy IPs/CIDRs")
	flag.Parse()
	c.Secret = os.Getenv("MS_SECRET")
	c.AdminPassword = os.Getenv("MS_ADMIN_PASSWORD")
	for _, p := range strings.Split(*proxies, ",") {
		if p = strings.TrimSpace(p); p != "" {
			c.TrustedProxies = append(c.TrustedProxies, p)
		}
	}
	return c
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
