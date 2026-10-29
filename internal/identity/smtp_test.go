package identity

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSMTPRequiresStartTLSBeforeAnyMail(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close listener: %v", err)
		}
	}()
	commands := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			commands <- "accept failed"
			return
		}
		defer closeConnection(conn)
		if err = conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			commands <- "deadline failed"
			return
		}
		if _, err = fmt.Fprint(conn, "220 local-test\r\n"); err != nil {
			commands <- "greeting failed"
			return
		}
		reader := bufio.NewReader(conn)
		line, err := reader.ReadString('\n')
		if err != nil {
			commands <- "request failed"
			return
		}
		if _, err = fmt.Fprint(conn, "250 local-test\r\n"); err != nil {
			commands <- "reply failed"
			return
		}
		commands <- line
	}()
	sender, err := NewSMTP(listener.Addr().String(), "styk@example.test", "account", "synthetic-password")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = sender.Send(ctx, "seller@example.test", "VERIFY_EMAIL", "https://example.test/#verify=x"); !errors.Is(err, ErrDelivery) {
		t.Fatalf("SMTP without TLS was accepted: %v", err)
	}
	if command := <-commands; !strings.HasPrefix(command, "EHLO ") {
		t.Fatalf("unexpected SMTP command before TLS: %q", command)
	}
}

func TestSMTPRejectsInvalidRecipientsAndPurposes(t *testing.T) {
	sender, err := NewSMTP("127.0.0.1:25", "styk@example.test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ recipient, purpose, link string }{
		{"seller@example.test\r\nBcc: attacker@example.test", "VERIFY_EMAIL", "https://example.test/"},
		{"seller@example.test", "OTHER", "https://example.test/"},
		{"seller@example.test", "RESET_PASSWORD", "https://example.test/\r\nInjected"},
	} {
		if err := sender.Send(context.Background(), tc.recipient, tc.purpose, tc.link); !errors.Is(err, ErrDelivery) {
			t.Fatalf("invalid mail accepted: %v", err)
		}
	}
}
