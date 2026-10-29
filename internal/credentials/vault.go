// Package credentials protects provider keys at rest. Plaintext is returned only
// to an internal provider-client constructor, never in console metadata.
package credentials

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

var (
	ErrNotFound    = errors.New("credential not found")
	ErrUnavailable = errors.New("credential storage unavailable")
	ErrConflict    = errors.New("credential request conflict")
	ErrVersion     = errors.New("credential version conflict")
	ErrOwnership   = errors.New("credential ownership conflict")
)

type Scope struct{ Tenant, Provider, Account string }

func (s Scope) Valid() bool {
	return validID(s.Tenant) && validID(s.Account) && (s.Provider == "moysklad" || s.Provider == "ozon")
}
func validID(s string) bool {
	return s != "" && len(s) <= 256 && strings.TrimSpace(s) == s && !strings.ContainsAny(s, "\r\n\x00")
}

type Metadata struct {
	Tenant        string    `json:"tenant"`
	Provider      string    `json:"provider"`
	Account       string    `json:"account"`
	Name          string    `json:"name"`
	Revision      int64     `json:"revision"`
	Enabled       bool      `json:"enabled"`
	UpdatedAt     time.Time `json:"updated_at"`
	CheckedAt     time.Time `json:"checked_at"`
	LastRequestID string    `json:"last_request_id"`
}

func (m Metadata) Scope() Scope { return Scope{m.Tenant, m.Provider, m.Account} }

// Record deliberately does not implement a printable plaintext representation.
type Record struct {
	Metadata
	Ciphertext []byte `json:"-"`
}

type Vault struct {
	aead      cipher.AEAD
	digestKey [32]byte
}

func New(key []byte) (*Vault, error) {
	if len(key) != 32 {
		return nil, errors.New("credential encryption key must contain 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("credential encryption unavailable")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("credential encryption unavailable")
	}
	return &Vault{aead: aead, digestKey: sha256.Sum256(append([]byte("styk credential request digest v1\x00"), key...))}, nil
}

// FromEnvironment does not create or persist a key automatically. A malformed
// supplied key fails startup; an absent key leaves legacy environment imports usable.
func FromEnvironment() (*Vault, error) {
	raw := os.Getenv("STYK_CREDENTIAL_ENCRYPTION_KEY")
	if raw == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, errors.New("invalid STYK_CREDENTIAL_ENCRYPTION_KEY")
	}
	v, err := New(key)
	if err != nil {
		return nil, errors.New("STYK_CREDENTIAL_ENCRYPTION_KEY must encode 32 bytes")
	}
	return v, nil
}
func aad(scope Scope, revision int64) ([]byte, error) {
	if !scope.Valid() || revision < 1 {
		return nil, errors.New("invalid credential scope or revision")
	}
	return json.Marshal(struct {
		Version                   int
		Tenant, Provider, Account string
		Revision                  int64
	}{1, scope.Tenant, scope.Provider, scope.Account, revision})
}
func (v *Vault) Encrypt(scope Scope, revision int64, secret string) ([]byte, error) {
	if v == nil {
		return nil, ErrUnavailable
	}
	data, err := aad(scope, revision)
	if err != nil {
		return nil, err
	}
	if len(secret) == 0 || len(secret) > 8192 || strings.TrimSpace(secret) != secret || strings.ContainsAny(secret, "\r\n\x00") {
		return nil, errors.New("invalid credential")
	}
	nonce := make([]byte, v.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, errors.New("credential nonce unavailable")
	}
	return v.aead.Seal(nonce, nonce, []byte(secret), data), nil
}
func (v *Vault) Decrypt(record Record) (string, error) {
	if v == nil || !record.Enabled {
		return "", ErrUnavailable
	}
	data, err := aad(record.Scope(), record.Revision)
	if err != nil {
		return "", ErrUnavailable
	}
	n := v.aead.NonceSize()
	if len(record.Ciphertext) < n+v.aead.Overhead() {
		return "", ErrUnavailable
	}
	plain, err := v.aead.Open(nil, record.Ciphertext[:n], record.Ciphertext[n:], data)
	if err != nil {
		return "", ErrUnavailable
	}
	return string(plain), nil
}

// Digest uses a separate derived key so a database-only disclosure does not let
// an attacker verify guessed low-entropy credentials against a request digest.
func (v *Vault) Digest(body []byte) []byte {
	if v == nil {
		return nil
	}
	h := hmac.New(sha256.New, v.digestKey[:])
	_, _ = h.Write(body)
	return h.Sum(nil)
}

type Store interface {
	LoadCredential(context.Context, Scope) (Record, error)
	CredentialConnectionOwned(context.Context, Scope) (bool, error)
}
type Resolver struct {
	Store Store
	Vault *Vault
}

func (r Resolver) Resolve(ctx context.Context, scope Scope) (string, error) {
	if r.Store == nil || !scope.Valid() {
		return "", ErrUnavailable
	}
	record, err := r.Store.LoadCredential(ctx, scope)
	if err == nil {
		return r.Vault.Decrypt(record)
	}
	if !errors.Is(err, ErrNotFound) {
		return "", ErrUnavailable
	}
	owned, err := r.Store.CredentialConnectionOwned(ctx, scope)
	if err != nil || !owned {
		return "", ErrOwnership
	}
	var account, secret string
	if scope.Provider == "ozon" {
		account, secret = os.Getenv("OZON_CLIENT_ID"), os.Getenv("OZON_API_KEY")
	} else {
		account, secret = os.Getenv("MOYSKLAD_ACCOUNT_ID"), os.Getenv("MOYSKLAD_TOKEN")
	}
	if account != scope.Account || strings.TrimSpace(secret) == "" || strings.ContainsAny(secret, "\r\n\x00") {
		return "", ErrUnavailable
	}
	return secret, nil
}
func ScopeKey(s Scope) string { b, _ := json.Marshal(s); return fmt.Sprintf("credential:%s", b) }
