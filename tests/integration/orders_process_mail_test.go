//go:build integration

package integration

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Three independent APIs deliver through one real STARTTLS SMTP listener. The
// disposable CA is trusted only in these child environments.
func ordersProcessMail(t *testing.T, mailbox *testMailbox) (string, string) {
	t.Helper()
	certificate, ca := ordersProcessMailCertificate(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("listen on synthetic SMTP fixture")
	}
	ctx, cancel := context.WithCancel(context.Background())
	var children sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			children.Add(1)
			go func() {
				defer children.Done()
				ordersProcessSMTP(ctx, conn, certificate, mailbox)
			}()
		}
	}()
	t.Cleanup(func() {
		cancel()
		if err := listener.Close(); err != nil {
			t.Error("close synthetic SMTP listener")
		}
		<-done
		children.Wait()
	})
	return listener.Addr().String(), ca
}

func ordersProcessMailCertificate(t *testing.T) (tls.Certificate, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal("generate fixture SMTP key")
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Orders process fixture"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	raw, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal("create fixture SMTP certificate")
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal("load fixture SMTP certificate")
	}
	path := filepath.Join(t.TempDir(), "smtp-fixture-ca.pem")
	if err = os.WriteFile(path, certPEM, 0o600); err != nil {
		t.Fatal("write fixture SMTP CA")
	}
	return pair, path
}

func ordersProcessSMTP(ctx context.Context, conn net.Conn, certificate tls.Certificate, mailbox *testMailbox) {
	defer func() {
		if err := conn.Close(); err != nil {
			return
		}
	}()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return
	}
	stop := context.AfterFunc(ctx, func() {
		if err := conn.Close(); err != nil {
			return
		}
	})
	defer stop()
	reader := bufio.NewReader(conn)
	if _, err := fmt.Fprint(conn, "220 localhost ESMTP fixture\r\n"); err != nil {
		return
	}
	recipient := ""
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		if !ordersProcessSMTPCommand(&conn, &reader, strings.TrimSpace(line), &recipient, certificate, mailbox) {
			return
		}
	}
}

func ordersProcessSMTPCommand(conn *net.Conn, reader **bufio.Reader, line string, recipient *string, certificate tls.Certificate, mailbox *testMailbox) bool {
	reply := "250 OK\r\n"
	switch {
	case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
		reply = "250-localhost\r\n250 STARTTLS\r\n"
	case line == "STARTTLS":
		if _, err := fmt.Fprint(*conn, "220 Ready\r\n"); err != nil {
			return false
		}
		secure := tls.Server(*conn, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
		if err := secure.Handshake(); err != nil {
			return false
		}
		*conn, *reader = secure, bufio.NewReader(secure)
		return true
	case strings.HasPrefix(line, "RCPT TO:"):
		*recipient = strings.Trim(strings.TrimPrefix(line, "RCPT TO:"), "<>")
	case line == "DATA":
		if _, err := fmt.Fprint(*conn, "354 Send message\r\n"); err != nil {
			return false
		}
		if !ordersProcessSMTPData(*reader, mailbox, *recipient) {
			return false
		}
	case line == "QUIT":
		reply = "221 Bye\r\n"
	}
	_, err := fmt.Fprint(*conn, reply)
	return err == nil && line != "QUIT"
}

func ordersProcessSMTPData(reader *bufio.Reader, mailbox *testMailbox, recipient string) bool {
	for range 100 {
		line, err := reader.ReadString('\n')
		if err != nil {
			return false
		}
		if strings.TrimSpace(line) == "." {
			return true
		}
		link := strings.TrimSpace(line)
		if strings.HasPrefix(link, ordersProcessOrigin+"/") {
			if err = mailbox.Send(context.Background(), recipient, "VERIFY_EMAIL", link); err != nil {
				return false
			}
		}
	}
	return false
}
