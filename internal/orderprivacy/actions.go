package orderprivacy

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/orders"
)

type ActionProtector struct {
	Vault *credentials.Vault
	Store orders.ActionSecretStore
}

func actionRecord(scope orders.Scope, number string, action orders.Action, ref orders.ProtectedActionInput) credentials.RecordScope {
	return credentials.RecordScope{Scope: credentials.Scope{Tenant: scope.Tenant, Provider: "ozon", Account: scope.OzonAccount},
		Purpose: "order-action", RecordID: identityDigest(scope.MoyskladAccount, number, string(action), ref.Reference), Revision: ref.Revision}
}

func (p ActionProtector) ProtectOrderActionInput(ctx context.Context, config orders.Config, number string, action orders.Action, input orders.ActionParameters, now time.Time) (orders.ActionParameters, *orders.ProtectedActionInput, error) {
	if !input.HasPrivateValues() {
		return input.Public(), nil, nil
	}
	if p.Vault == nil || p.Store == nil || config.Scope.Validate() != nil || config.Revision < 1 {
		return orders.ActionParameters{}, nil, orders.ErrBlocked
	}
	plain, err := json.Marshal(input)
	if err != nil {
		return orders.ActionParameters{}, nil, orders.ErrInvalid
	}
	digest := p.Vault.Digest(plain)
	ref := orders.ProtectedActionInput{Reference: hex.EncodeToString(digest), Revision: config.Revision}
	ciphertext, err := p.Vault.EncryptRecord(actionRecord(config.Scope, number, action, ref), plain)
	if err != nil {
		return orders.ActionParameters{}, nil, err
	}
	_, err = p.Store.SaveOrderActionSecret(ctx, orders.ActionSecret{Scope: config.Scope, PostingNumber: number, Action: action,
		Reference: ref.Reference, ConfigRevision: ref.Revision, Ciphertext: ciphertext, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour)})
	return input.Public(), &ref, err
}

func (p ActionProtector) ReadOrderActionInput(ctx context.Context, scope orders.Scope, number string, action orders.Action, ref orders.ProtectedActionInput) (orders.ActionParameters, error) {
	if p.Vault == nil || p.Store == nil {
		return orders.ActionParameters{}, orders.ErrBlocked
	}
	secret, err := p.Store.OrderActionSecret(ctx, scope, number, action, ref)
	if err != nil {
		return orders.ActionParameters{}, err
	}
	plain, err := p.Vault.DecryptRecord(actionRecord(scope, number, action, ref), secret.Ciphertext)
	if err != nil {
		return orders.ActionParameters{}, err
	}
	digest := p.Vault.Digest(plain)
	var input orders.ActionParameters
	if hex.EncodeToString(digest) != ref.Reference || json.Unmarshal(plain, &input) != nil {
		return input, orders.ErrStale
	}
	return input, nil
}
