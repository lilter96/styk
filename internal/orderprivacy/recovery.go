package orderprivacy

import (
	"encoding/hex"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/orders"
)

type RecoveryProtector struct{ Vault *credentials.Vault }

func (p RecoveryProtector) SealOrderRecoveryReason(scope orders.Scope, workID, id, reason string) ([]byte, string, error) {
	if p.Vault == nil || scope.Validate() != nil || workID == "" || id == "" {
		return nil, "", orders.ErrInvalid
	}
	record := credentials.RecordScope{Scope: credentials.Scope{Tenant: scope.Tenant, Provider: "moysklad", Account: scope.MoyskladAccount}, Purpose: "order-recovery-reason", RecordID: identityDigest(workID, id), Revision: 1}
	cipher, err := p.Vault.EncryptRecord(record, []byte(reason))
	if err != nil {
		return nil, "", err
	}
	digest := p.Vault.Digest([]byte(reason))
	return cipher, hex.EncodeToString(digest), nil
}
