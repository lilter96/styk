package orderprivacy

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

func (p ActionProtector) ReadNativeRetryInput(ctx context.Context, request orders.NativeRetrySecretRequest) (orders.ActionParameters, error) {
	store, ok := p.Store.(orders.NativeRetrySecretStore)
	if p.Vault == nil || !ok || request.Validate() != nil {
		return orders.ActionParameters{}, orders.ErrBlocked
	}
	secret, err := store.OrderNativeRetrySecret(ctx, request)
	if err != nil {
		return orders.ActionParameters{}, err
	}
	if secret.Scope != request.Scope || secret.PostingNumber != request.PostingNumber || secret.Action != request.Action || secret.Reference != request.ProtectedInput.Reference || secret.ConfigRevision != request.ProtectedInput.Revision {
		return orders.ActionParameters{}, orders.ErrStale
	}
	return p.decryptActionInput(request.Scope, request.PostingNumber, request.Action, request.ProtectedInput, secret)
}
