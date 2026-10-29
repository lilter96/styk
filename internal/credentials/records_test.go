package credentials

import (
	"bytes"
	"testing"
)

func TestOrderRecordEnvelopeRejectsScopeAndCredentialSubstitution(t *testing.T) {
	t.Parallel()
	v, err := New(bytes.Repeat([]byte{49}, 32))
	if err != nil {
		t.Fatal(err)
	}
	scope := RecordScope{Scope: Scope{Tenant: "merchant-a", Provider: "ozon", Account: "seller-a"}, Purpose: "order-customer", RecordID: "posting-1", Revision: 1}
	encrypted, err := v.EncryptRecord(scope, []byte(`{"name":"Synthetic customer"}`))
	if err != nil {
		t.Fatal(err)
	}
	actual, err := v.DecryptRecord(scope, encrypted)
	if err != nil || !bytes.Equal(actual, []byte(`{"name":"Synthetic customer"}`)) {
		t.Fatal("protected record did not round trip")
	}
	cases := []struct {
		name   string
		change func(*RecordScope)
	}{
		{"tenant", func(s *RecordScope) { s.Tenant = "merchant-b" }},
		{"account", func(s *RecordScope) { s.Account = "seller-b" }},
		{"provider", func(s *RecordScope) { s.Provider = "moysklad" }},
		{"posting", func(s *RecordScope) { s.RecordID = "posting-2" }},
		{"revision", func(s *RecordScope) { s.Revision = 2 }},
		{"purpose", func(s *RecordScope) { s.Purpose = "credential" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changed := scope
			tc.change(&changed)
			if _, err := v.DecryptRecord(changed, encrypted); err == nil {
				t.Fatal("substituted record scope decrypted")
			}
		})
	}
	record := Record{Metadata: Metadata{Tenant: scope.Tenant, Provider: scope.Provider, Account: scope.Account, Revision: 1, Enabled: true}, Ciphertext: encrypted}
	if _, err := v.Decrypt(record); err == nil {
		t.Fatal("order ciphertext was accepted as a provider credential")
	}
	credential, err := v.Encrypt(scope.Scope, 1, "synthetic-api-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.DecryptRecord(scope, credential); err == nil {
		t.Fatal("credential ciphertext was accepted as an order record")
	}
}
