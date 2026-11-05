package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/orders"
)

// OrderNativeRetrySecret restores only a proved no-application native attempt.
// Ordinary action-secret reads still require the current configuration revision.
func (s *Store) OrderNativeRetrySecret(ctx context.Context, request orders.NativeRetrySecretRequest) (orders.ActionSecret, error) {
	session, ok := identity.SessionFromContext(ctx)
	if !ok || request.Validate() != nil {
		return orders.ActionSecret{}, orders.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return orders.ActionSecret{}, ordersError("begin exact native retry event read", err)
	}
	defer rollbackMerchantMutation(tx)
	if _, err = ordersActor(ctx, tx, request.Scope.Tenant, session.AuditID); err != nil {
		return orders.ActionSecret{}, orders.ErrNotFound
	}
	secret, err := readNativeRetrySecret(ctx, tx, request)
	if err != nil {
		return orders.ActionSecret{}, err
	}
	return secret, ordersRowsError("finish exact native retry event read", tx.Commit(ctx))
}

func readNativeRetrySecret(ctx context.Context, tx pgx.Tx, request orders.NativeRetrySecretRequest) (orders.ActionSecret, error) {
	secret := orders.ActionSecret{Scope: request.Scope, PostingNumber: request.PostingNumber, Action: request.Action, Reference: request.ProtectedInput.Reference, ConfigRevision: request.ProtectedInput.Revision}
	args := append(ordersScopeArgs(request.Scope), request.WorkID, request.CommandFingerprint, request.PostingNumber, request.Action, request.ProtectedInput.Reference, request.ProtectedInput.Revision)
	var raw []byte
	err := tx.QueryRow(ctx, nativeRetrySecretSQL, args...).Scan(&secret.Ciphertext, &secret.CreatedAt, &secret.ExpiresAt, &raw)
	if err != nil {
		return orders.ActionSecret{}, ordersError("read exact rejected native event", err)
	}
	var work orders.Work
	if !validOrderActionSecret(secret) || ordersDecode(raw, &work) != nil || !nativeRetrySecretWorkMatches(request, work) {
		return orders.ActionSecret{}, orders.ErrStale
	}
	return secret, nil
}

func nativeRetrySecretWorkMatches(request orders.NativeRetrySecretRequest, work orders.Work) bool {
	m := work.Command.NativeMovement
	if work.ID != request.WorkID || work.Scope != request.Scope || work.Command.Scope != request.Scope || work.CommandFingerprint != request.CommandFingerprint || work.Command.PostingNumber != request.PostingNumber || work.Command.Action != request.Action || work.Command.Validate() != nil || m == nil || m.Event.ProtectedInput != request.ProtectedInput {
		return false
	}
	fingerprint, err := orders.CommandFingerprint(work.Command)
	return err == nil && fingerprint == request.CommandFingerprint
}

const nativeRetrySecretSQL = `SELECT s.ciphertext,s.created_at,s.expires_at,w.work
 FROM orders_work w
 JOIN orders_native_document_intents i ON i.work_id=w.id AND i.tenant_id=w.tenant_id
 AND i.moysklad_account=w.moysklad_account AND i.ozon_account=w.ozon_account
 AND i.posting_number=w.posting_number AND i.kind||'_create'=w.action
 AND i.customer_order_id=w.work#>>'{command,correlation,customer_order_id}'
 AND i.event_digest=w.work#>>'{command,native_movement,event,digest}'
 AND i.sync_id::text=w.work#>>'{command,native_movement,correlation,sync_id}'
 AND i.marker=w.work#>>'{command,native_movement,correlation,marker}'
 JOIN orders_native_document_events e ON e.tenant_id=i.tenant_id AND e.moysklad_account=i.moysklad_account
 AND e.ozon_account=i.ozon_account AND e.kind=i.kind AND e.event_digest=i.event_digest
 AND e.sync_id=i.sync_id AND e.marker=i.marker AND e.posting_number=i.posting_number
 AND e.customer_order_id=i.customer_order_id
 JOIN orders_projection_bindings b ON b.tenant_id=i.tenant_id AND b.moysklad_account=i.moysklad_account
 AND b.ozon_account=i.ozon_account AND b.posting_number=i.posting_number AND b.customer_order_id=i.customer_order_id
 AND b.sync_id=w.work#>>'{command,correlation,sync_id}' AND b.marker=w.work#>>'{command,correlation,marker}'
 JOIN orders_action_secrets s ON s.tenant_id=w.tenant_id AND s.moysklad_account=w.moysklad_account
 AND s.ozon_account=w.ozon_account AND s.posting_number=w.posting_number AND s.action=w.action
 JOIN orders_configs c ON c.tenant_id=w.tenant_id AND c.moysklad_account=w.moysklad_account AND c.ozon_account=w.ozon_account
 JOIN orders_config_revisions cfg ON cfg.tenant_id=c.tenant_id AND cfg.moysklad_account=c.moysklad_account
 AND cfg.ozon_account=c.ozon_account AND cfg.revision=c.revision
 WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND w.id=$4
 AND w.command_fingerprint=$5 AND w.work->>'command_fingerprint'=w.command_fingerprint
 AND w.work->>'state'=w.state AND (w.work->>'dispatch_count')::int=w.dispatch_count
 AND w.posting_number=$6 AND w.action=$7 AND s.reference=$8 AND s.config_revision=$9
 AND w.config_revision=$9 AND w.work#>>'{command,native_movement,event,protected_input,reference}'=$8
 AND w.work#>>'{command,native_movement,event,protected_input,revision}'=$9::text
 AND c.enabled AND cfg.config->'test_enabled'='true'::jsonb AND c.revision>=s.config_revision
 AND s.created_at<=clock_timestamp() AND s.expires_at>clock_timestamp()
 AND s.expires_at<=s.created_at+interval '1 day' AND orders_native_retry_no_effect(w.id)
 AND EXISTS(SELECT 1 FROM connection_credentials k WHERE k.tenant_id=w.tenant_id AND k.provider='moysklad' AND k.account_id=w.moysklad_account AND k.enabled)
 AND EXISTS(SELECT 1 FROM connection_credentials k WHERE k.tenant_id=w.tenant_id AND k.provider='ozon' AND k.account_id=w.ozon_account AND k.enabled)`
