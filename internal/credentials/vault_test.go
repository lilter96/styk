package credentials

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestVaultAuthenticatesScopeRevisionAndCiphertext(t *testing.T) {
	v, err := New([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	scope := Scope{"tenant", "moysklad", "account"}
	secret := "synthetic-token-not-printed"
	encrypted, err := v.Encrypt(scope, 1, secret)
	if err != nil {
		t.Fatal(err)
	}
	record := Record{Metadata: Metadata{Tenant: scope.Tenant, Provider: scope.Provider, Account: scope.Account, Revision: 1, Enabled: true}, Ciphertext: encrypted}
	got, err := v.Decrypt(record)
	if err != nil || got != secret {
		t.Fatal("round trip failed")
	}
	second, _ := v.Encrypt(scope, 1, secret)
	if string(encrypted) == string(second) {
		t.Fatal("nonce reused")
	}
	for _, mutate := range []func(*Record){func(r *Record) { r.Tenant = "other" }, func(r *Record) { r.Provider = "ozon" }, func(r *Record) { r.Account = "other" }, func(r *Record) { r.Revision = 2 }, func(r *Record) { r.Enabled = false }, func(r *Record) {
		r.Ciphertext = append([]byte(nil), encrypted...)
		r.Ciphertext[len(r.Ciphertext)-1] ^= 1
	}} {
		r := record
		mutate(&r)
		if _, err = v.Decrypt(r); !errors.Is(err, ErrUnavailable) {
			t.Fatal("unauthenticated record decrypted")
		}
	}
	raw, _ := json.Marshal(record)
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), base64.StdEncoding.EncodeToString(encrypted)) {
		t.Fatal("private material was serialized")
	}
}
func TestVaultConfigurationAndDigest(t *testing.T) {
	for _, key := range []string{"bad", base64.StdEncoding.EncodeToString([]byte("short"))} {
		t.Setenv("STYK_CREDENTIAL_ENCRYPTION_KEY", key)
		if _, err := FromEnvironment(); err == nil {
			t.Fatal("invalid key accepted")
		}
	}
	t.Setenv("STYK_CREDENTIAL_ENCRYPTION_KEY", "")
	v, err := FromEnvironment()
	if v != nil || err != nil {
		t.Fatal("missing configuration should remain disabled")
	}
	a, _ := New([]byte(strings.Repeat("a", 32)))
	b, _ := New([]byte(strings.Repeat("b", 32)))
	if string(a.Digest([]byte("request"))) == string(b.Digest([]byte("request"))) {
		t.Fatal("request digest not keyed")
	}
	if string(a.Digest([]byte("one"))) == string(a.Digest([]byte("two"))) {
		t.Fatal("request digest not bound")
	}
}

type memoryStore struct {
	record Record
	err    error
	owned  bool
}

func (s memoryStore) LoadCredential(context.Context, Scope) (Record, error) { return s.record, s.err }
func (s memoryStore) CredentialConnectionOwned(context.Context, Scope) (bool, error) {
	return s.owned, nil
}
func TestResolverRequiresOriginalOwnerAndHonorsDisabledStoredKey(t *testing.T) {
	t.Setenv("OZON_CLIENT_ID", "account")
	t.Setenv("OZON_API_KEY", "synthetic-env-key")
	scope := Scope{"tenant", "ozon", "account"}
	resolver := Resolver{Store: memoryStore{err: ErrNotFound, owned: true}}
	if key, err := resolver.Resolve(context.Background(), scope); err != nil || key != "synthetic-env-key" {
		t.Fatal("matching legacy fallback unavailable")
	}
	resolver.Store = memoryStore{err: ErrNotFound, owned: false}
	if _, err := resolver.Resolve(context.Background(), scope); !errors.Is(err, ErrOwnership) {
		t.Fatal("unowned environment key resolved")
	}
	resolver.Store = memoryStore{record: Record{Metadata: Metadata{Tenant: "tenant", Provider: "ozon", Account: "account", Revision: 1, Enabled: false}}, owned: true}
	if _, err := resolver.Resolve(context.Background(), scope); !errors.Is(err, ErrUnavailable) {
		t.Fatal("disabled stored key fell back to environment")
	}
	resolver.Store = memoryStore{err: ErrNotFound, owned: true}
	scope.Account = "other"
	if _, err := resolver.Resolve(context.Background(), scope); !errors.Is(err, ErrUnavailable) {
		t.Fatal("environment key retargeted")
	}
}
