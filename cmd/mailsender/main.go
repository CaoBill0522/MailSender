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
	"mailsender/internal/store"
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
	if err := core.SeedDefaults(db); err != nil {
		log.Fatalf("seed: %v", err)
	}

	settings := core.NewSettingsStore(db)
	sched, err := core.NewScheduler(db, box, settings)
	if err != nil {
		log.Fatalf("scheduler: %v", err)
	}
	svc := core.NewService(db, sched, settings)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	svc.Start(ctx, 4)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           server.New(db, svc, box, web.FS()).Handler(cfg.TrustedProxies),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Printf("MailSender listening on %s (data: %s)", cfg.Listen, cfg.DataDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()
	<-ctx.Done()
	log.Printf("shutting down…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
