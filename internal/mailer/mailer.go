// Package mailer talks SMTP to one account.
package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"
	"time"

	mail "github.com/wneessen/go-mail"

	"mailsender/internal/secure"
	"mailsender/internal/store"
)

type Content struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	HTML    string `json:"html,omitempty"`
	Text    string `json:"text,omitempty"`
	Raw     []byte `json:"raw,omitempty"` // complete message received by the SMTP ingress; relayed as-is

	MsgIDLocal string `json:"-"` // local part of the generated Message-ID (raw mail without one)
}

// Kind classifies a delivery failure so the scheduler knows how to react.
type Kind int

const (
	KindOK        Kind = iota
	KindAuth           // credentials rejected: cool the account down immediately
	KindRecipient      // recipient address permanently rejected: not the account's fault, do not retry
	KindAccount        // anything else (network, throttling, spam block): count a failure and try another account
)

func (k Kind) String() string {
	switch k {
	case KindOK:
		return "ok"
	case KindAuth:
		return "auth"
	case KindRecipient:
		return "recipient"
	default:
		return "account"
	}
}

func newClient(acc *store.Account, password string, timeout time.Duration) (*mail.Client, error) {
	opts := []mail.Option{
		mail.WithPort(acc.Port),
		mail.WithTimeout(timeout),
		mail.WithTLSConfig(&tls.Config{ServerName: acc.Host, MinVersion: tls.VersionTLS12}),
	}
	switch acc.Security {
	case "ssl":
		opts = append(opts, mail.WithSSL())
	case "starttls":
		opts = append(opts, mail.WithTLSPolicy(mail.TLSMandatory))
	default:
		opts = append(opts, mail.WithTLSPolicy(mail.NoTLS))
	}
	if acc.AuthType != "none" {
		var at mail.SMTPAuthType
		plainText := acc.Security == "none"
		switch acc.AuthType {
		case "plain":
			at = mail.SMTPAuthPlain
			if plainText {
				at = mail.SMTPAuthPlainNoEnc
			}
		case "login":
			at = mail.SMTPAuthLogin
			if plainText {
				at = mail.SMTPAuthLoginNoEnc
			}
		case "cram-md5":
			at = mail.SMTPAuthCramMD5
		default:
			at = mail.SMTPAuthAutoDiscover
			if plainText {
				at = mail.SMTPAuthPlainNoEnc
			}
		}
		opts = append(opts, mail.WithSMTPAuth(at), mail.WithUsername(acc.Username), mail.WithPassword(password))
	}
	return mail.NewClient(acc.Host, opts...)
}

// Send delivers one message through the account.
func Send(ctx context.Context, acc *store.Account, password string, c Content, timeout time.Duration) error {
	if len(c.Raw) > 0 {
		return sendRaw(ctx, acc, password, c, timeout)
	}
	m := mail.NewMsg()
	if err := m.FromFormat(acc.FromName, acc.FromAddr); err != nil {
		return fmt.Errorf("invalid from address: %w", err)
	}
	if err := m.To(c.To); err != nil {
		return &recipientError{err}
	}
	m.Subject(c.Subject)
	// Use the sender's domain rather than the local hostname, which go-mail would otherwise leak.
	domain := acc.FromAddr[strings.LastIndex(acc.FromAddr, "@")+1:]
	m.SetMessageIDWithValue(secure.RandomString(24) + "@" + domain)
	m.SetDate()
	switch {
	case c.Text != "" && c.HTML != "":
		m.SetBodyString(mail.TypeTextPlain, c.Text)
		m.AddAlternativeString(mail.TypeTextHTML, c.HTML)
	case c.HTML != "":
		m.SetBodyString(mail.TypeTextHTML, c.HTML)
	default:
		m.SetBodyString(mail.TypeTextPlain, c.Text)
	}
	cl, err := newClient(acc, password, timeout)
	if err != nil {
		return err
	}
	return cl.DialAndSendWithContext(ctx, m)
}

// Test connects and authenticates without sending anything.
func Test(ctx context.Context, acc *store.Account, password string, timeout time.Duration) error {
	cl, err := newClient(acc, password, timeout)
	if err != nil {
		return err
	}
	if err := cl.DialWithContext(ctx); err != nil {
		return err
	}
	return cl.Close()
}

type recipientError struct{ err error }

func (e *recipientError) Error() string { return "invalid recipient: " + e.err.Error() }
func (e *recipientError) Unwrap() error { return e.err }

var codeRe = regexp.MustCompile(`\b([45]\d\d)\b`)

// Code extracts the SMTP reply code from an error, or 0.
func Code(err error) int {
	var se *mail.SendError
	if errors.As(err, &se) && se.ErrorCode() > 0 {
		return se.ErrorCode()
	}
	var te *textproto.Error
	if errors.As(err, &te) {
		return te.Code
	}
	if m := codeRe.FindStringSubmatch(err.Error()); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// Classify decides how a send error should be treated.
func Classify(err error) Kind {
	if err == nil {
		return KindOK
	}
	var re *recipientError
	if errors.As(err, &re) {
		return KindRecipient
	}
	code := Code(err)
	msg := strings.ToLower(err.Error())
	var se *mail.SendError
	if errors.As(err, &se) {
		// A permanent rejection at RCPT TO with a "no such mailbox / bad address" style code
		// is the recipient's problem; any other account would get the same answer.
		if se.Reason == mail.ErrSMTPRcptTo && !se.IsTemp() && (code == 550 || code == 551 || code == 553 || code == 501) {
			return KindRecipient
		}
	}
	if code == 535 || code == 534 || code == 530 || (strings.Contains(msg, "auth") && (strings.Contains(msg, "fail") || strings.Contains(msg, "invalid"))) {
		return KindAuth
	}
	return KindAccount
}

// Short trims long multi-line SMTP errors for storage and display.
func Short(err error) string {
	if err == nil {
		return ""
	}
	s := strings.Join(strings.Fields(err.Error()), " ")
	if len(s) > 500 {
		s = s[:500] + "…"
	}
	return s
}
