package communications

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

func localMailTLS(t *testing.T) (net.Listener, *tls.Config) {
	t.Helper()
	source := httptest.NewTLSServer(http.NotFoundHandler())
	cert := source.TLS.Certificates[0]
	roots := x509.NewCertPool()
	roots.AddCert(source.Certificate())
	source.Close()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln, &tls.Config{ServerName: "example.com", RootCAs: roots, MinVersion: tls.VersionTLS12}
}
func TestHostSMTPSendsUsingTLSAndRejectsHeaderInjection(t *testing.T) {
	ln, tlsConfig := localMailTLS(t)
	result := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			result <- ""
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		reader := bufio.NewReader(conn)
		fmt.Fprint(conn, "220 example.com ESMTP\r\n")
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				fmt.Fprint(conn, "250-example.com\r\n250 AUTH PLAIN\r\n")
			case strings.HasPrefix(line, "AUTH"):
				fmt.Fprint(conn, "235 authenticated\r\n")
			case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
				fmt.Fprint(conn, "250 OK\r\n")
			case strings.HasPrefix(line, "DATA"):
				fmt.Fprint(conn, "354 continue\r\n")
				var body strings.Builder
				for {
					line, err = reader.ReadString('\n')
					if err != nil {
						return
					}
					if line == ".\r\n" {
						break
					}
					body.WriteString(line)
				}
				result <- body.String()
				fmt.Fprint(conn, "250 accepted\r\n")
			default:
				return
			}
		}
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	sender, err := NewSMTPSender(SMTPConfig{Host: host, Port: port, Username: "sender@example.test", Password: "test-only", From: "sender@example.test", ImplicitTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	tlsConfig.ServerName = "example.com"
	sender.tlsConfig = tlsConfig
	// smtp.PlainAuth binds to the configured host, independently of TLS name.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sender.Send(ctx, Mail{Recipient: "target@example.test", Subject: "测试", Body: "hello"}); err != nil {
		t.Fatal(err)
	}
	select {
	case body := <-result:
		if !strings.Contains(body, "hello") || !strings.Contains(body, "From: sender@example.test") {
			t.Fatalf("message=%q", body)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := sender.Send(ctx, Mail{Recipient: "target@example.test", Subject: "x\r\nBcc:y", Body: "hello"}); err != ErrInvalidRequest {
		t.Fatalf("header injection error=%v", err)
	}
}

type literal struct {
	*strings.Reader
	size int64
}

func (l literal) Size() int64 { return l.size }
func TestHostIMAPPaginatesUnreadWithoutMarkingSeen(t *testing.T) {
	ln, tlsConfig := localMailTLS(t)
	backend := imapmemserver.New()
	user := imapmemserver.NewUser("agent@example.test", "test-only")
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		text := fmt.Sprintf("From: alice@example.test\r\nTo: agent@example.test\r\nSubject: Message %d\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nhello %d", i, i)
		if _, err := user.Append("INBOX", literal{strings.NewReader(text), int64(len(text))}, &imap.AppendOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	backend.AddUser(user)
	server := imapserver.New(&imapserver.Options{NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		return backend.NewSession(), nil, nil
	}})
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(ln) }()
	t.Cleanup(func() { _ = server.Close(); <-done })
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(port)
	receiver, err := NewIMAPReceiver(IMAPConfig{Host: host, Port: p, Username: "agent@example.test", Password: "test-only", Mailbox: "INBOX"})
	if err != nil {
		t.Fatal(err)
	}
	receiver.tlsConfig = tlsConfig
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	page, err := receiver.ListUnread(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 20 || page.NextBeforeUID == 0 {
		t.Fatalf("page=%+v", page)
	}
	next, err := receiver.ListUnread(ctx, page.NextBeforeUID)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Messages) != 5 {
		t.Fatalf("next=%+v", next)
	}
	again, err := receiver.ListUnread(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Messages) != 20 {
		t.Fatal("reading changed unread flags")
	}
	for _, m := range again.Messages {
		if !strings.HasPrefix(m.BodyText, "hello") {
			t.Fatalf("body=%q", m.BodyText)
		}
	}
}

func TestIMAPCancellationClosesBlockedConnection(t *testing.T) {
	ln, tlsConfig := localMailTLS(t)
	ready := make(chan struct{})
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprint(conn, "* OK [CAPABILITY IMAP4rev1] ready\r\n")
		reader := bufio.NewReader(conn)
		if _, err := reader.ReadString('\n'); err != nil {
			return
		}
		close(ready)
		_, _ = reader.ReadString('\n')
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(port)
	receiver, err := NewIMAPReceiver(IMAPConfig{Host: host, Port: p, Username: "u", Password: "p", Mailbox: "INBOX", Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	receiver.tlsConfig = tlsConfig
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := receiver.ListUnread(ctx, 0); result <- err }()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("IMAP did not connect")
	}
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("canceled read returned success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not abort IMAP")
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("connection not closed")
	}
}
