// Package orderprivacy keeps customer details outside normal order records.
package orderprivacy

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/orders"
)

type Protector struct {
	Vault *credentials.Vault
	Store orders.SecretStore
}

func record(scope orders.Scope, posting string, revision int64) credentials.RecordScope {
	data, _ := json.Marshal([]string{scope.MoyskladAccount, posting})
	sum := sha256.Sum256(data)
	return credentials.RecordScope{Scope: credentials.Scope{Tenant: scope.Tenant, Provider: "ozon", Account: scope.OzonAccount}, Purpose: "order-customer", RecordID: hex.EncodeToString(sum[:]), Revision: revision}
}

func (p Protector) ProtectOrderPage(ctx context.Context, config orders.Config, page *orders.SourcePage) error {
	if page == nil || page.Scope != config.Scope {
		return orders.ErrInvalid
	}
	for i := range page.Postings {
		posting := &page.Postings[i]
		if !config.PIIEnabled {
			posting.PII = nil
			posting.Customer = orders.ProtectedCustomer{Presence: orders.PresenceMissing}
			continue
		}
		if posting.PII == nil {
			continue
		}
		if err := p.protect(ctx, config, posting, page); err != nil {
			return err
		}
	}
	return nil
}

func (p Protector) protect(ctx context.Context, config orders.Config, posting *orders.Posting, page *orders.SourcePage) error {
	if p.Vault == nil || p.Store == nil || config.PIIRetentionSeconds < 1 {
		return orders.ErrBlocked
	}
	plain, err := json.Marshal(posting.PII)
	if err != nil {
		return orders.ErrInvalid
	}
	scopeData, _ := json.Marshal([]string{config.Scope.Tenant, config.Scope.OzonAccount, config.Scope.MoyskladAccount, posting.PostingNumber, string(plain)})
	digest := p.Vault.Digest(scopeData)
	revision := int64(binary.BigEndian.Uint64(digest[:8]) & 0x7fffffffffffffff)
	if revision == 0 {
		revision = 1
	}
	secret, err := p.Store.OrderSecret(ctx, config.Scope, posting.PostingNumber, revision)
	if err != nil && !errors.Is(err, orders.ErrNotFound) {
		return err
	}
	if err == nil {
		existing, decryptErr := p.Vault.DecryptRecord(record(config.Scope, posting.PostingNumber, revision), secret.Ciphertext)
		if decryptErr != nil || string(existing) != string(plain) {
			return orders.ErrConflict
		}
	}
	if errors.Is(err, orders.ErrNotFound) {
		cipher, encryptErr := p.Vault.EncryptRecord(record(config.Scope, posting.PostingNumber, revision), plain)
		if encryptErr != nil {
			return encryptErr
		}
		now := page.FinishedAt
		secret = orders.PostingSecret{Scope: config.Scope, PostingNumber: posting.PostingNumber, Revision: revision, Purpose: "order-customer", Ciphertext: cipher, CreatedAt: now, ExpiresAt: now.Add(time.Duration(config.PIIRetentionSeconds) * time.Second)}
		page.Secrets = append(page.Secrets, secret)
	}
	posting.PII = nil
	posting.Customer = orders.ProtectedCustomer{Reference: hex.EncodeToString(digest), Schema: "customer-v1", Revision: revision, Presence: orders.PresenceValue}
	posting.Evidence.Revision, err = orders.PostingFingerprint(*posting)
	return err
}

// Customer is called only for an authorized exact-target dispatch/read view.
func (p Protector) Customer(ctx context.Context, scope orders.Scope, posting string, ref orders.ProtectedCustomer) (*orders.Customer, error) {
	if p.Vault == nil || p.Store == nil || ref.Revision < 1 {
		return nil, orders.ErrBlocked
	}
	secret, err := p.Store.OrderSecret(ctx, scope, posting, ref.Revision)
	if err != nil {
		return nil, err
	}
	plain, err := p.Vault.DecryptRecord(record(scope, posting, ref.Revision), secret.Ciphertext)
	if err != nil {
		return nil, err
	}
	var customer orders.Customer
	if json.Unmarshal(plain, &customer) != nil {
		return nil, orders.ErrInvalid
	}
	return &customer, nil
}
