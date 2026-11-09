package mailer

import (
	"bytes"
	"context"
	"fmt"
	"mime"
	netmail "net/mail"
	"strings"
	"time"

	"mailsender/internal/secure"
	"mailsender/internal/store"
)

// sendRaw relays a complete RFC 5322 message (from the SMTP ingress) through the account,
// rewriting the sender headers so SPF/DMARC align with the account actually sending.
func sendRaw(ctx context.Context, acc *store.Account, password string, c Content, timeout time.Duration) error {
	data, err := RewriteHeaders(c.Raw, acc.FromAddr, acc.FromName, c.MsgIDLocal)
	if err != nil {
		return fmt.Errorf("invalid message: %w", err)
	}
	cl, err := newClient(acc, password, timeout)
	if err != nil {
		return err
	}
	sc, err := cl.DialToSMTPClientWithContext(ctx)
	if err != nil {
		return err
	}
	defer cl.CloseWithSMTPClient(sc)
	// The low-level client sends the argument verbatim; RFC 5321 requires <addr>, and strict servers
	// (163.com: "500 Error: bad syntax") reject bare addresses.
	if err := sc.Mail(envelopeAddr(acc.FromAddr)); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err := sc.Rcpt(envelopeAddr(c.To)); err != nil {
		e := fmt.Errorf("RCPT TO: %w", err)
		if code := Code(err); code == 550 || code == 551 || code == 553 || code == 501 {
			return &recipientError{e}
		}
		return e
	}
	w, err := sc.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		w.Close()
		return fmt.Errorf("DATA: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	_ = sc.Quit() // the message is accepted; a QUIT failure does not matter
	return nil
}

func envelopeAddr(addr string) string { return "<" + strings.Trim(strings.TrimSpace(addr), "<>") + ">" }

// headers removed from relayed mail: replaced by us, or invalid once the sender changes
var dropHeaders = map[string]bool{
	"from": true, "sender": true, "return-path": true, "bcc": true,
	"dkim-signature": true, "arc-seal": true, "arc-message-signature": true, "arc-authentication-results": true,
	RefHeader: true,
}

// RefHeader carries the client's own reference for delivery callbacks; it is stripped before relaying.
const RefHeader = "x-ms-ref"

// MessageID builds the Message-ID used when the client did not set one: the local part is fixed per
// message (so retries on other accounts keep it), the domain is the sending account's.
func MessageID(local, fromAddr string) string {
	if local == "" {
		local = secure.RandomString(24)
	}
	return "<" + local + "@" + fromAddr[strings.LastIndex(fromAddr, "@")+1:] + ">"
}

// RewriteHeaders sets From to the sending account, keeps the client's display name, moves the client's
// address to Reply-To, drops headers that would be wrong after relaying, and adds Message-ID/Date if missing
// (msgIDLocal is the local part for a generated Message-ID, random when empty).
func RewriteHeaders(raw []byte, fromAddr, accountName, msgIDLocal string) ([]byte, error) {
	head, body, sep := raw, []byte{}, -1
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		head, body, sep = raw[:i], raw[i+4:], i
	} else if i := bytes.Index(raw, []byte("\n\n")); i >= 0 {
		head, body, sep = raw[:i], raw[i+2:], i
	}
	if sep < 0 && !bytes.Contains(raw, []byte(":")) {
		return nil, fmt.Errorf("no header section")
	}

	// unfold into header fields, each kept with its original continuation lines
	var fields []string
	for _, line := range strings.Split(string(head), "\n") {
		line = strings.TrimRight(line, "\r")
		if (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) && len(fields) > 0 {
			fields[len(fields)-1] += "\r\n" + line
			continue
		}
		if line != "" {
			fields = append(fields, line)
		}
	}

	var origFrom string
	hasReplyTo, hasMsgID, hasDate := false, false, false
	kept := make([]string, 0, len(fields)+3)
	for _, f := range fields {
		name, value, _ := strings.Cut(f, ":")
		key := strings.ToLower(strings.TrimSpace(name))
		switch key {
		case "from":
			origFrom = strings.TrimSpace(strings.ReplaceAll(value, "\r\n", ""))
		case "reply-to":
			hasReplyTo = true
		case "message-id":
			hasMsgID = true
		case "date":
			hasDate = true
		}
		if !dropHeaders[key] {
			kept = append(kept, f)
		}
	}

	displayName := accountName
	var orig *netmail.Address
	if origFrom != "" {
		if list, err := netmail.ParseAddressList(origFrom); err == nil && len(list) > 0 {
			orig = list[0]
			if orig.Name != "" {
				displayName = orig.Name
			}
		}
	}

	out := []string{"From: " + (&netmail.Address{Name: displayName, Address: fromAddr}).String()}
	if !hasReplyTo && orig != nil && !strings.EqualFold(orig.Address, fromAddr) {
		out = append(out, "Reply-To: "+orig.String())
	}
	if !hasMsgID {
		out = append(out, "Message-ID: "+MessageID(msgIDLocal, fromAddr))
	}
	if !hasDate {
		out = append(out, "Date: "+time.Now().Format(time.RFC1123Z))
	}
	out = append(out, kept...)

	var b bytes.Buffer
	b.Grow(len(raw) + 256)
	b.WriteString(strings.Join(out, "\r\n"))
	b.WriteString("\r\n\r\n")
	b.Write(body)
	return b.Bytes(), nil
}

// Meta is what the service records about a raw message.
type Meta struct {
	Subject string // decoded
	MsgID   string // client-set Message-ID, "" if none
	Ref     string // X-MS-Ref value
}

// MetaOf extracts the decoded Subject, Message-ID and X-MS-Ref of a raw message.
func MetaOf(raw []byte) Meta {
	m, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return Meta{}
	}
	s := m.Header.Get("Subject")
	if d, err := new(mime.WordDecoder).DecodeHeader(s); err == nil {
		s = d
	}
	return Meta{Subject: clip(s, 300), MsgID: clip(strings.TrimSpace(m.Header.Get("Message-Id")), 250), Ref: clip(strings.TrimSpace(m.Header.Get(RefHeader)), 200)}
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
