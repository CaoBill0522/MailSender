// Package secure provides encryption of stored secrets, API key generation and admin token signing.
package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Box struct {
	encKey  []byte
	signKey []byte
}

// LoadOrCreateSecret returns the configured secret, or reads/creates <dataDir>/secret.key.
func LoadOrCreateSecret(configured, dataDir string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	p := filepath.Join(dataDir, "secret.key")
	if b, err := os.ReadFile(p); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s, nil
		}
	}
	s := RandomString(48)
	if err := os.WriteFile(p, []byte(s+"\n"), 0o600); err != nil {
		return "", err
	}
	return s, nil
}

func NewBox(secret string) *Box {
	e := sha256.Sum256([]byte("mailsender-enc:" + secret))
	s := sha256.Sum256([]byte("mailsender-sign:" + secret))
	return &Box{encKey: e[:], signKey: s[:]}
}

func (b *Box) Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	block, err := aes.NewCipher(b.encKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

func (b *Box) Decrypt(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(b.encKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt failed (secret key changed?): %w", err)
	}
	return string(plain), nil
}

type tokenClaims struct {
	User string `json:"u"`
	Exp  int64  `json:"e"`
	Ver  string `json:"v"` // changes when the admin password changes, invalidating old tokens
}

func (b *Box) SignToken(user, ver string, ttl time.Duration) string {
	payload, _ := json.Marshal(tokenClaims{User: user, Exp: time.Now().Add(ttl).Unix(), Ver: ver})
	p := base64.RawURLEncoding.EncodeToString(payload)
	return p + "." + b.mac(p)
}

func (b *Box) VerifyToken(tok, ver string) (string, bool) {
	p, sig, ok := strings.Cut(tok, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(b.mac(p))) {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return "", false
	}
	var c tokenClaims
	if json.Unmarshal(raw, &c) != nil || time.Now().Unix() > c.Exp || c.Ver != ver {
		return "", false
	}
	return c.User, true
}

func (b *Box) mac(s string) string {
	m := hmac.New(sha256.New, b.signKey)
	m.Write([]byte(s))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

const alnum = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789"

func RandomString(n int) string {
	out := make([]byte, n)
	max := big.NewInt(int64(len(alnum)))
	for i := range out {
		v, _ := rand.Int(rand.Reader, max)
		out[i] = alnum[v.Int64()]
	}
	return string(out)
}

func RandomDigits(n int) string {
	out := make([]byte, n)
	for i := range out {
		v, _ := rand.Int(rand.Reader, big.NewInt(10))
		out[i] = byte('0' + v.Int64())
	}
	return string(out)
}

// NewAPIKey returns a new plaintext key; only its hash is stored.
func NewAPIKey() string { return "ms_" + RandomString(40) }

func HashAPIKey(k string) string {
	h := sha256.Sum256([]byte(k))
	return hex.EncodeToString(h[:])
}
