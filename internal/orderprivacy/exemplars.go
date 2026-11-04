package orderprivacy

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/orders"
)

type ExemplarProtector struct {
	Vault *credentials.Vault
	Store orders.ExemplarSecretReader
}

func exemplarRecord(s orders.ExemplarSecret) credentials.RecordScope {
	return credentials.RecordScope{Scope: credentials.Scope{Tenant: s.Scope.Tenant, Provider: "ozon", Account: s.Scope.OzonAccount}, Purpose: "order-exemplar",
		RecordID: identityDigest(s.Scope.MoyskladAccount, s.WorkID, s.PostingNumber, s.TargetFingerprint, s.Digest), Revision: s.CredentialRevision}
}

func (p ExemplarProtector) SealOrderExemplars(work orders.Work, snapshot orders.ExemplarSnapshot, now time.Time) (orders.ExemplarSecret, error) {
	s := orders.ExemplarSecret{Scope: work.Scope, WorkID: work.ID, PostingNumber: work.Command.PostingNumber, CredentialRevision: work.Credentials.Ozon, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour)}
	if p.Vault == nil || work.Command.PostingAction == nil || snapshot.PostingNumber != s.PostingNumber {
		return s, orders.ErrBlocked
	}
	s.TargetFingerprint = work.Command.PostingAction.PackingTargetFingerprint
	raw, err := orders.ExemplarSnapshotDigest(snapshot)
	if err != nil {
		return s, err
	}
	s.Digest = hex.EncodeToString(p.Vault.Digest(raw))
	s.Ciphertext, err = p.Vault.EncryptRecord(exemplarRecord(s), raw)
	if err != nil {
		return s, err
	}
	s.Public, err = snapshot.Public(work, now)
	s.Public.TargetFingerprint = s.TargetFingerprint
	return s, err
}

func (p ExemplarProtector) ReadOrderExemplars(ctx context.Context, scope orders.Scope, workID string) (orders.ExemplarSnapshot, orders.ExemplarSecret, error) {
	if p.Vault == nil || p.Store == nil {
		return orders.ExemplarSnapshot{}, orders.ExemplarSecret{}, orders.ErrBlocked
	}
	s, err := p.Store.OrderExemplarSecret(ctx, scope, workID)
	if err != nil {
		return orders.ExemplarSnapshot{}, s, err
	}
	if s.Scope != scope || s.WorkID != workID {
		return orders.ExemplarSnapshot{}, s, orders.ErrStale
	}
	raw, err := p.Vault.DecryptRecord(exemplarRecord(s), s.Ciphertext)
	if err != nil {
		return orders.ExemplarSnapshot{}, s, err
	}
	if hex.EncodeToString(p.Vault.Digest(raw)) != s.Digest {
		return orders.ExemplarSnapshot{}, s, orders.ErrStale
	}
	var snapshot orders.ExemplarSnapshot
	if json.Unmarshal(raw, &snapshot) != nil || snapshot.PostingNumber != s.PostingNumber {
		return snapshot, s, orders.ErrStale
	}
	return snapshot, s, nil
}
