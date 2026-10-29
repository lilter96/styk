package credentials

import (
	"crypto/rand"
	"encoding/json"
)

// RecordScope domain-separates protected order data from provider credentials
// and binds ciphertext to one tenant/account/record revision. Existing credential
// encryption and its key format remain unchanged.
type RecordScope struct {
	Scope
	Purpose  string
	RecordID string
	Revision int64
}

func recordAAD(scope RecordScope) ([]byte, error) {
	if !scope.Valid() || !validID(scope.RecordID) || scope.Purpose != "order-customer" || scope.Revision < 1 {
		return nil, ErrOwnership
	}
	return json.Marshal(struct {
		Version                             int
		Purpose                             string
		Tenant, Provider, Account, RecordID string
		Revision                            int64
	}{1, scope.Purpose, scope.Tenant, scope.Provider, scope.Account, scope.RecordID, scope.Revision})
}

func (v *Vault) EncryptRecord(scope RecordScope, plain []byte) ([]byte, error) {
	if v == nil || len(plain) == 0 || len(plain) > 128<<10 {
		return nil, ErrUnavailable
	}
	aad, err := recordAAD(scope)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, v.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, ErrUnavailable
	}
	return v.aead.Seal(nonce, nonce, plain, aad), nil
}

func (v *Vault) DecryptRecord(scope RecordScope, ciphertext []byte) ([]byte, error) {
	if v == nil || len(ciphertext) > 129<<10 {
		return nil, ErrUnavailable
	}
	aad, err := recordAAD(scope)
	if err != nil {
		return nil, err
	}
	n := v.aead.NonceSize()
	if len(ciphertext) < n+v.aead.Overhead() {
		return nil, ErrUnavailable
	}
	plain, err := v.aead.Open(nil, ciphertext[:n], ciphertext[n:], aad)
	if err != nil {
		return nil, ErrUnavailable
	}
	return plain, nil
}
