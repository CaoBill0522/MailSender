package secure

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a random 160-bit base32 secret for authenticator apps.
func NewTOTPSecret() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return b32.EncodeToString(b)
}

// TOTPURI is the otpauth:// link encoded in the QR code.
func TOTPURI(secret, account, issuer string) string {
	v := url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + v.Encode()
}

func totpAt(secret string, counter int64) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimRight(strings.ReplaceAll(secret, " ", ""), "=")))
	if err != nil {
		return "", err
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	code := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", code%1000000), nil
}

// VerifyTOTP checks a 6-digit code (RFC 6238, 30 s steps, ±1 step of clock drift) and returns the matched
// time step. Codes at or before notAfter are rejected so a code cannot be replayed.
func VerifyTOTP(secret, code string, notAfter int64) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return 0, false
	}
	now := time.Now().Unix() / 30
	for _, c := range []int64{now, now - 1, now + 1} {
		if c <= notAfter {
			continue
		}
		want, err := totpAt(secret, c)
		if err == nil && hmac.Equal([]byte(want), []byte(code)) {
			return c, true
		}
	}
	return 0, false
}

// TOTPCode returns the code for time t (tests and tooling).
func TOTPCode(secret string, t time.Time) (string, error) { return totpAt(secret, t.Unix()/30) }
