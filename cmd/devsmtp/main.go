// devsmtp runs several fake SMTP servers for local testing of MailSender's rotation.
//
//	go run ./cmd/devsmtp -base 2525 -n 3 -fail 2527
//
// Each server accepts AUTH PLAIN with user "test" / password "test", prints received
// mail headers, and the port given by -fail rejects every message with a 451.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	netmail "net/mail"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

type backend struct {
	port int
	fail bool
}

func (b *backend) NewSession(_ *smtp.Conn) (smtp.Session, error) { return &session{b: b}, nil }

type session struct {
	b      *backend
	authed bool
	from   string
	to     []string
}

func (s *session) AuthMechanisms() []string { return []string{sasl.Plain} }

func (s *session) Auth(mech string) (sasl.Server, error) {
	check := func(user, pass string) error {
		if user != "test" || pass != "test" {
			return errors.New("invalid credentials")
		}
		s.authed = true
		return nil
	}
	if mech != sasl.Plain {
		return nil, smtp.ErrAuthUnsupported
	}
	return sasl.NewPlainServer(func(_, u, p string) error { return check(u, p) }), nil
}

func (s *session) Mail(from string, _ *smtp.MailOptions) error {
	if !s.authed {
		return smtp.ErrAuthRequired
	}
	s.from = from
	return nil
}

func (s *session) Rcpt(to string, _ *smtp.RcptOptions) error {
	if strings.HasPrefix(to, "nouser@") {
		return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 1, 1}, Message: "mailbox does not exist"}
	}
	s.to = append(s.to, to)
	return nil
}

func (s *session) Data(r io.Reader) error {
	if s.b.fail {
		io.Copy(io.Discard, r)
		return &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 7, 1}, Message: "simulated throttling"}
	}
	msg, err := netmail.ReadMessage(bufio.NewReader(r))
	if err != nil {
		return err
	}
	io.Copy(io.Discard, msg.Body)
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil {
		subject = msg.Header.Get("Subject")
	}
	log.Printf("[:%d] %s -> %s  subject=%q", s.b.port, s.from, strings.Join(s.to, ","), subject)
	return nil
}

func (s *session) Reset()        { s.from, s.to = "", nil }
func (s *session) Logout() error { return nil }

func main() {
	base := flag.Int("base", 2525, "first port")
	n := flag.Int("n", 3, "number of servers")
	failPort := flag.Int("fail", 0, "port that rejects all mail (0 = none)")
	flag.Parse()
	var wg sync.WaitGroup
	for i := 0; i < *n; i++ {
		port := *base + i
		srv := smtp.NewServer(&backend{port: port, fail: port == *failPort})
		srv.Addr = fmt.Sprintf("127.0.0.1:%d", port)
		srv.Domain = "localhost"
		srv.AllowInsecureAuth = true
		srv.ReadTimeout = 30 * time.Second
		srv.WriteTimeout = 30 * time.Second
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Printf("fake SMTP on %s (fail=%v)", srv.Addr, port == *failPort)
			if err := srv.ListenAndServe(); err != nil {
				log.Printf("%s: %v", srv.Addr, err)
			}
		}()
	}
	wg.Wait()
}
