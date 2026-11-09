package secure

import (
	"testing"
	"time"
)

func nowStep() int64 { return time.Now().Unix() / 30 }

// RFC 6238 appendix B test vector (SHA1, secret "12345678901234567890").
func TestTOTPVector(t *testing.T) {
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	got, err := totpAt(secret, 59/30)
	if err != nil || got != "287082" { // last 6 digits of 94287082
		t.Fatalf("totp = %q, %v", got, err)
	}
	got, _ = totpAt(secret, 1111111109/30)
	if got != "081804" { // 07081804
		t.Fatalf("totp = %q", got)
	}
}

func TestVerifyTOTPReplay(t *testing.T) {
	s := NewTOTPSecret()
	code, _ := totpAt(s, nowStep())
	step, ok := VerifyTOTP(s, code, 0)
	if !ok {
		t.Fatal("valid code rejected")
	}
	if _, ok := VerifyTOTP(s, code, step); ok {
		t.Fatal("replayed code accepted")
	}
}
