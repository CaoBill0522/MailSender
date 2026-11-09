package mailer

import (
	"bufio"
	"context"
	"net"
	"regexp"
	"strings"
	"testing"
	"time"

	"mailsender/internal/store"
)

// strictServer is a minimal SMTP server that, like smtp.163.com, rejects envelope commands
// whose address is not enclosed in angle brackets. It records every command it receives.
func strictServer(t *testing.T) (port int, cmds chan string, data chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	cmds, data = make(chan string, 32), make(chan string, 1)
	envelope := regexp.MustCompile(`^(MAIL FROM|RCPT TO):<[^<>\s]+@[^<>\s]+>( |$)`)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r, w := bufio.NewReader(c), bufio.NewWriter(c)
		reply := func(s string) { w.WriteString(s + "\r\n"); w.Flush() }
		reply("220 strict ESMTP")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			cmds <- line
			up := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
				reply("250-strict\r\n250 8BITMIME")
			case strings.HasPrefix(up, "MAIL"), strings.HasPrefix(up, "RCPT"):
				if !envelope.MatchString(line) {
					reply("500 Error: bad syntax")
				} else {
					reply("250 OK")
				}
			case up == "DATA":
				reply("354 go ahead")
				var b strings.Builder
				for {
					l, err := r.ReadString('\n')
					if err != nil || l == ".\r\n" {
						break
					}
					b.WriteString(l)
				}
				data <- b.String()
				reply("250 queued")
			case up == "QUIT":
				reply("221 bye")
				return
			default:
				reply("250 OK")
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, cmds, data
}

func TestSendRawUsesBracketedEnvelope(t *testing.T) {
	port, cmds, data := strictServer(t)
	acc := &store.Account{Host: "127.0.0.1", Port: port, Security: "none", AuthType: "none",
		FromAddr: "sender@163.com", FromName: "Pool"}
	raw := []byte("From: App <app@myapp.com>\r\nTo: user@example.com\r\nSubject: hi\r\n\r\nbody\r\n")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := Send(ctx, acc, "", Content{To: "user@example.com", Raw: raw}, 5*time.Second); err != nil {
		t.Fatalf("send failed: %v", err)
	}

	var got []string
	for len(cmds) > 0 {
		got = append(got, <-cmds)
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "MAIL FROM:<sender@163.com>") || !strings.Contains(joined, "RCPT TO:<user@example.com>") {
		t.Fatalf("unexpected envelope commands:\n%s", joined)
	}

	body := <-data
	// the client's display name is kept, the address becomes the sending account
	if !strings.Contains(body, "From: \"App\" <sender@163.com>") {
		t.Errorf("From not rewritten to the account:\n%s", body)
	}
	if !strings.Contains(body, "Reply-To: \"App\" <app@myapp.com>") {
		t.Errorf("original sender not moved to Reply-To:\n%s", body)
	}
}

func TestEnvelopeAddr(t *testing.T) {
	for in, want := range map[string]string{
		"a@b.com":     "<a@b.com>",
		"<a@b.com>":   "<a@b.com>",
		" a@b.com ":   "<a@b.com>",
		"x+y@163.com": "<x+y@163.com>",
	} {
		if got := envelopeAddr(in); got != want {
			t.Errorf("envelopeAddr(%q) = %q, want %q", in, got, want)
		}
	}
}
