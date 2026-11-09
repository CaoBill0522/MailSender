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
	data, err := RewriteHeaders(c.Raw, acc.FromAddr, acc.FromName, c.FromName)
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
	if err := sc.Mail(acc.FromAddr); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err := sc.Rcpt(c.To); err != nil {
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

// headers removed from relayed mail: replaced by us, or invalid once the sender changes
var dropHeaders = map[string]bool{
	"from": true, "sender": true, "return-path": true, "bcc": true,
	"dkim-signature": true, "arc-seal": true, "arc-message-signature": true, "arc-authentication-results": true,
}

// RewriteHeaders sets From to the sending account, keeps the client's display name, moves the client's
// address to Reply-To, drops headers that would be wrong after relaying, and adds Message-ID/Date if missing.
// nameOverride (if set) wins over both the client's and the account's display name.
func RewriteHeaders(raw []byte, fromAddr, accountName, nameOverride string) ([]byte, error) {
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
	if nameOverride != "" {
		displayName = nameOverride
	}

	out := []string{"From: " + (&netmail.Address{Name: displayName, Address: fromAddr}).String()}
	if !hasReplyTo && orig != nil && !strings.EqualFold(orig.Address, fromAddr) {
		out = append(out, "Reply-To: "+orig.String())
	}
	if !hasMsgID {
		domain := fromAddr[strings.LastIndex(fromAddr, "@")+1:]
		out = append(out, "Message-ID: <"+secure.RandomString(24)+"@"+domain+">")
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

// SubjectOf returns the decoded Subject header of a raw message.
func SubjectOf(raw []byte) string {
	m, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return ""
	}
	s := m.Header.Get("Subject")
	if d, err := new(mime.WordDecoder).DecodeHeader(s); err == nil {
		s = d
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
