package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"mailsender/internal/core"
	"mailsender/internal/secure"
	"mailsender/internal/store"
)

func newTestServer(t *testing.T) (http.Handler, *core.Service) {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	box := secure.NewBox("s")
	if _, err := EnsureAdmin(db, "correct-horse"); err != nil {
		t.Fatal(err)
	}
	cfg := core.NewSettingsStore(db)
	sch, _ := core.NewScheduler(db, box, cfg)
	svc := core.NewService(db, sch, cfg, box)
	h := New(db, svc, box, fstest.MapFS{"index.html": {Data: []byte("x")}}, &RuntimeInfo{}).Handler(nil)
	return h, svc
}

func call(h http.Handler, method, path, token string, body any) (int, map[string]any) {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.RemoteAddr = "203.0.113.9:5555"
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestLoginBanAnd2FA(t *testing.T) {
	h, svc := newTestServer(t)
	login := func(pw, otp string) (int, map[string]any) {
		return call(h, "POST", "/admin/api/login", "", map[string]string{"username": "admin", "password": pw, "otp": otp})
	}

	code, r := login("correct-horse", "")
	if code != 200 {
		t.Fatalf("login: %d %v", code, r)
	}
	tok := r["token"].(string)

	// enable 2FA
	_, r = call(h, "POST", "/admin/api/2fa/setup", tok, nil)
	secret := r["secret"].(string)
	otp, _ := secure.TOTPCode(secret, time.Now())
	code, r = call(h, "POST", "/admin/api/2fa/enable", tok, map[string]string{"code": otp})
	if code != 200 {
		t.Fatalf("enable: %d %v", code, r)
	}
	if code, _ := call(h, "GET", "/admin/api/me", tok, nil); code != 401 {
		t.Fatal("old token must be invalid after enabling 2FA")
	}
	newTok := r["token"].(string)
	if code, _ := call(h, "GET", "/admin/api/me", newTok, nil); code != 200 {
		t.Fatal("token returned by enable must work")
	}

	if code, r = login("correct-horse", ""); code != 401 || r["code"] != "otp_required" {
		t.Fatalf("expected otp_required, got %d %v", code, r)
	}
	if code, _ = login("correct-horse", otp); code != 401 {
		t.Fatal("replayed code must be rejected")
	}
	next, _ := secure.TOTPCode(secret, time.Now().Add(30*time.Second))
	if code, r = login("correct-horse", next); code != 200 {
		t.Fatalf("login with next code: %d %v", code, r)
	}

	// 5 wrong passwords from one IP → banned, even the right password is refused
	for i := 0; i < 5; i++ {
		login("wrong", "")
	}
	if code, r = login("correct-horse", next); code != 429 {
		t.Fatalf("expected ban, got %d %v", code, r)
	}
	if bans := svc.Guard.List(); len(bans) != 1 || bans[0].Scope != "admin" || bans[0].IP != "203.0.113.9" {
		t.Fatalf("bans: %+v", bans)
	}
}
