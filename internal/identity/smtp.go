package identity

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

var ErrDelivery = errors.New("identity mail delivery unavailable")

// Sender delivers a single verification or reset link. Implementations must
// not log addresses, message bodies or tokens.
type Sender interface {
	Send(ctx context.Context, recipient, purpose, link string) error
}

type SMTP struct {
	address  string
	host     string
	from     string
	username string
	password string
}

func NewSMTP(address, from, username, password string) (*SMTP, error) {
	host, _, err := net.SplitHostPort(address)
	parsed, parseErr := mail.ParseAddress(from)
	if err != nil || host == "" || parseErr != nil || parsed.Name != "" || parsed.Address != from || strings.ContainsAny(from, "\r\n") || (username == "") != (password == "") {
		return nil, ErrDelivery
	}
	return &SMTP{address: address, host: host, from: from, username: username, password: password}, nil
}

func (s *SMTP) Send(ctx context.Context, recipient, purpose, link string) error {
	canonical, err := canonicalEmail(recipient)
	if err != nil || canonical != recipient || strings.ContainsAny(link, "\r\n") {
		return ErrDelivery
	}
	subject, text, ok := mailContent(purpose, link)
	if !ok {
		return ErrDelivery
	}
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", s.address)
	if err != nil {
		return ErrDelivery
	}
	defer closeConnection(conn)
	stop := context.AfterFunc(ctx, func() { closeConnection(conn) })
	defer stop()
	if err = conn.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
		return ErrDelivery
	}
	client, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return ErrDelivery
	}
	if err = s.secure(client); err != nil {
		return ErrDelivery
	}
	if err = s.deliver(client, recipient, subject, text); err != nil {
		return ErrDelivery
	}
	return nil
}

func closeConnection(conn net.Conn) {
	if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		slog.Error("identity mail connection close unavailable")
	}
}

func (s *SMTP) secure(client *smtp.Client) error {
	if ok, _ := client.Extension("STARTTLS"); !ok {
		return ErrDelivery
	}
	if err := client.StartTLS(&tls.Config{ServerName: s.host, MinVersion: tls.VersionTLS12}); err != nil {
		return ErrDelivery
	}
	if s.username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.username, s.password, s.host)); err != nil {
			return ErrDelivery
		}
	}
	return nil
}

func (s *SMTP) deliver(client *smtp.Client, recipient, subject, text string) error {
	if err := client.Mail(s.from); err != nil {
		return err
	}
	if err := client.Rcpt(recipient); err != nil {
		return err
	}
	data, err := client.Data()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(data, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n", s.from, recipient, mime.QEncoding.Encode("utf-8", subject), text)
	if err != nil {
		return err
	}
	if err = data.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func mailContent(purpose, link string) (string, string, bool) {
	switch purpose {
	case "VERIFY_EMAIL":
		return "Подтвердите почту STYK", "Подтвердите адрес электронной почты:\r\n" + link + "\r\nСсылка действует 24 часа.", true
	case "RESET_PASSWORD":
		return "Восстановление пароля STYK", "Чтобы задать новый пароль, откройте:\r\n" + link + "\r\nСсылка действует 30 минут.", true
	default:
		return "", "", false
	}
}
