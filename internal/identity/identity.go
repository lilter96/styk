// Package identity implements isolated merchant identities and sessions.
// It does not grant permission to publish marketplace stock.
package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalid   = errors.New("invalid identity input")
	ErrAuth      = errors.New("authentication failed")
	ErrConflict  = errors.New("identity already exists")
	ErrForbidden = errors.New("merchant account unavailable")
)

const passwordCost = 12

type Store struct {
	pool      *pgxpool.Pool
	timeout   time.Duration
	dummyHash []byte
	hashSlots chan struct{}
}

type User struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	AuthVersion int64  `json:"-"`
}

type Workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type Session struct {
	AuditID string
	User    User
	CSRF    string
	Expires time.Time
}

type sessionContextKey struct{}

func WithSession(ctx context.Context, session Session) context.Context {
	return context.WithValue(ctx, sessionContextKey{}, session)
}

func SessionFromContext(ctx context.Context) (Session, bool) {
	session, ok := ctx.Value(sessionContextKey{}).(Session)
	return session, ok && session.AuditID != ""
}

func New(pool *pgxpool.Pool, timeout time.Duration) (*Store, error) {
	if pool == nil || timeout <= 0 {
		return nil, ErrInvalid
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("styk-dummy-password-never-used-for-login"), passwordCost)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool, timeout: timeout, dummyHash: hash, hashSlots: make(chan struct{}, 4)}, nil
}

func canonicalEmail(value string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Name != "" || address.Address != email || len(email) > 254 {
		return "", ErrInvalid
	}
	return email, nil
}

func validPassword(password string) bool {
	return len(password) >= 12 && len(password) <= 72 && !strings.ContainsRune(password, 0)
}

func validName(name string) bool {
	return name != "" && strings.TrimSpace(name) == name && len(name) <= 120 && !strings.ContainsAny(name, "\r\n\x00")
}

func identifier(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(value[:]), nil
}

func randomToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func tokenHash(value string) ([]byte, error) {
	bytes, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(bytes) != 32 {
		return nil, ErrAuth
	}
	sum := sha256.Sum256(bytes)
	return sum[:], nil
}

func (s *Store) passwordHash(ctx context.Context, password string) ([]byte, error) {
	select {
	case s.hashSlots <- struct{}{}:
		defer func() { <-s.hashSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return bcrypt.GenerateFromPassword([]byte(password), passwordCost)
}

func (s *Store) passwordMatches(ctx context.Context, hash []byte, password string) bool {
	select {
	case s.hashSlots <- struct{}{}:
		defer func() { <-s.hashSlots }()
	case <-ctx.Done():
		return false
	}
	return bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
}

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		slog.Error("identity transaction rollback unavailable")
	}
}
