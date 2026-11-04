package orderprivacy

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/orders"
)

func TestRecoveryReasonEncryptedWithExactScopeAndIntent(t *testing.T) {
	t.Parallel()
	vault, err := credentials.New(bytes.Repeat([]byte{71}, 32))
	if err != nil {
		t.Fatal(err)
	}
	scope := orders.Scope{Tenant: "merchant", MoyskladAccount: "moy", OzonAccount: "ozon"}
	p := RecoveryProtector{Vault: vault}
	cipher, digest, err := p.SealOrderRecoveryReason(scope, "work", "resolution", "PRIVATE_BUYER_REASON_SENTINEL")
	if err != nil || len(digest) != 64 || bytes.Contains(cipher, []byte("PRIVATE_")) {
		t.Fatal("reason not protected")
	}
	record := credentials.RecordScope{Scope: credentials.Scope{Tenant: scope.Tenant, Provider: "moysklad", Account: scope.MoyskladAccount}, Purpose: "order-recovery-reason", RecordID: identityDigest("work", "resolution"), Revision: 1}
	plain, err := vault.DecryptRecord(record, cipher)
	if err != nil || string(plain) != "PRIVATE_BUYER_REASON_SENTINEL" {
		t.Fatal("reason did not decrypt")
	}
	for _, change := range []func(*credentials.RecordScope){func(r *credentials.RecordScope) { r.Tenant = "other" }, func(r *credentials.RecordScope) { r.Account = "other" }, func(r *credentials.RecordScope) { r.RecordID = identityDigest("other-work", "resolution") }, func(r *credentials.RecordScope) { r.Purpose = "order-customer" }} {
		changed := record
		change(&changed)
		if _, err = vault.DecryptRecord(changed, cipher); err == nil {
			t.Fatal("reason decrypted outside exact recovery")
		}
	}
	raw, err := json.Marshal(orders.RecoveryResolution{ReasonCiphertext: cipher, ReasonDigest: digest})
	if err != nil || strings.Contains(string(raw), "PRIVATE_") || bytes.Contains(raw, cipher) {
		t.Fatal("private reason escaped resolution JSON")
	}
}
