package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mailsender/internal/config"
	"mailsender/internal/core"
	"mailsender/internal/secure"
	"mailsender/internal/server"
	"mailsender/internal/smtpd"
	"mailsender/internal/store"
	"mailsender/internal/tlsutil"
	"mailsender/web"
)

func main() {
	cfg := config.Load()

	db, err := store.Open(cfg.DataDir)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	secret, err := secure.LoadOrCreateSecret(cfg.Secret, cfg.DataDir)
	if err != nil {
		log.Fatalf("secret: %v", err)
	}
	box := secure.NewBox(secret)

	pw, err := server.EnsureAdmin(db, cfg.AdminPassword)
	if err != nil {
		log.Fatalf("init admin: %v", err)
	}
	if pw != "" {
		log.Printf("==================================================")
		log.Printf(" 管理员账号已创建  用户名: admin  密码: %s", pw)
		log.Printf(" 请登录后台后立即修改密码")
		log.Printf("==================================================")
	}

	if cfg.Disable2FA {
		if err := server.Disable2FA(db); err != nil {
			log.Fatalf("disable 2FA: %v", err)
		}
		log.Printf("两步验证已关闭（MS_DISABLE_2FA）。登录后请重新设置，并去掉该环境变量")
	}

	tp, err := tlsutil.New(tlsutil.Options{CertFile: cfg.TLSCert, KeyFile: cfg.TLSKey, Domains: cfg.TLSDomains, ACMEEmail: cfg.ACMEEmail, DataDir: cfg.DataDir})
	if err != nil {
		log.Fatalf("tls: %v", err)
	}

	settings := core.NewSettingsStore(db)
	sched, err := core.NewScheduler(db, box, settings)
	if err != nil {
		log.Fatalf("scheduler: %v", err)
	}
	svc := core.NewService(db, sched, settings, box)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	svc.Start(ctx)

	info := &server.RuntimeInfo{TLSMode: "none", HTTPListen: cfg.Listen}
	if tp != nil {
		info.TLSMode, info.TLSDomains, info.HTTPSListen = tp.Mode, tp.Domains, cfg.HTTPSListen
	}

	// SMTP ingress
	if cfg.SMTPListen != "" || cfg.SMTPSListen != "" {
		o := smtpd.Options{
			Listen: cfg.SMTPListen, ListenTLS: cfg.SMTPSListen,
			Hostname: cfg.SMTPHostname, AllowInsecureAuth: cfg.SMTPInsecureAuth,
			MaxMessageBytes: int64(cfg.SMTPMaxMessageMiB) << 20,
		}
		if tp != nil {
			o.TLS = tp.SMTPConfig()
			if o.Hostname == "" {
				o.Hostname = tp.Hostname()
			}
		}
		ing, err := smtpd.Start(svc, o)
		if err != nil {
			log.Fatalf("smtp ingress: %v", err)
		}
		defer ing.Close()
		info.SMTP = &ing.Info
	}

	handler := server.New(db, svc, box, web.FS(), info).Handler(cfg.TrustedProxies)
	var servers []*http.Server
	serve := func(s *http.Server, tlsOn bool) {
		servers = append(servers, s)
		go func() {
			var err error
			if tlsOn {
				log.Printf("HTTPS listening on %s (%s)", s.Addr, info.TLSMode)
				err = s.ListenAndServeTLS("", "")
			} else {
				log.Printf("HTTP listening on %s (data: %s)", s.Addr, cfg.DataDir)
				err = s.ListenAndServe()
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatalf("listen %s: %v", s.Addr, err)
			}
		}()
	}
	if cfg.Listen != "" {
		// In ACME mode plain HTTP only answers Let's Encrypt HTTP-01 challenges and redirects to HTTPS,
		// so credentials are never sent in clear text. Otherwise it serves the app.
		h := tp.HTTPHandler(handler)
		if tp != nil && tp.Mode == "acme" && cfg.HTTPSListen != "" {
			h = tp.HTTPHandler(nil)
			info.HTTPRedirect = true
		}
		serve(&http.Server{Addr: cfg.Listen, Handler: h, ReadHeaderTimeout: 10 * time.Second}, false)
	}
	if tp != nil && cfg.HTTPSListen != "" {
		serve(&http.Server{Addr: cfg.HTTPSListen, Handler: handler, TLSConfig: tp.Config(), ReadHeaderTimeout: 10 * time.Second}, true)
	}

	<-ctx.Done()
	log.Printf("shutting down…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.Shutdown(shutdownCtx)
	}
}
