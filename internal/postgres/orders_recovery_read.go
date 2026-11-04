package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) OrderRecoveryContext(ctx context.Context, request orders.RecoveryRequest) (orders.RecoveryContext, error) {
	if request.Scope.Validate() != nil || !ordersRequestKey(request.WorkID) || request.Actor == "" {
		return orders.RecoveryContext{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return orders.RecoveryContext{}, ordersError("begin order recovery inspection", err)
	}
	defer rollbackMerchantMutation(tx)
	if _, err = ordersActor(ctx, tx, request.Scope.Tenant, request.Actor); err != nil {
		return orders.RecoveryContext{}, orders.ErrNotFound
	}
	current, err := readOrderRecoveryContext(ctx, tx, request.Scope, request.WorkID)
	if err != nil {
		return current, err
	}
	return current, ordersRowsError("finish order recovery inspection", tx.Commit(ctx))
}

func readOrderRecoveryContext(ctx context.Context, tx pgx.Tx, scope orders.Scope, workID string) (orders.RecoveryContext, error) {
	var current orders.RecoveryContext
	var raw []byte
	args := append(ordersScopeArgs(scope), workID)
	err := tx.QueryRow(ctx, `SELECT work FROM orders_work WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND id=$4`, args...).Scan(&raw)
	if err != nil {
		return current, ordersError("read owned unknown order", err)
	}
	if err = ordersDecode(raw, &current.View.Work); err != nil {
		return current, err
	}
	current.Config, err = readOrderConfig(ctx, tx, scope)
	if err != nil {
		return current, err
	}
	current.Credentials, err = ordersCredentials(ctx, tx, scope)
	if err != nil {
		return current, err
	}
	current.Posting, err = readOrderPosting(ctx, tx, scope, current.View.Work.Command.PostingNumber)
	if err != nil {
		return current, err
	}
	if err = readOrderRecoveryRecords(ctx, tx, &current); err != nil {
		return current, err
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return current, err
	}
	current.View.Blockers = recoveryBlockers(current, now)
	if err = addRecoveryDispatchBlockers(ctx, tx, &current.View); err != nil {
		return current, err
	}
	current.View.Eligible = len(current.View.Blockers) == 0
	return current, nil
}

func readOrderRecoveryRecords(ctx context.Context, tx pgx.Tx, current *orders.RecoveryContext) error {
	work := current.View.Work
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT evidence FROM orders_recovery_evidence WHERE work_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, work.ID).Scan(&raw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ordersError("read latest order recovery evidence", err)
	}
	if err == nil {
		var evidence orders.RecoveryEvidence
		if err = ordersDecode(raw, &evidence); err != nil {
			return err
		}
		current.View.Evidence = &evidence
	}
	return readOrderResolution(ctx, tx, &current.View)
}

func readOrderResolution(ctx context.Context, tx pgx.Tx, view *orders.RecoveryView) error {
	var resolution orders.RecoveryResolution
	err := tx.QueryRow(ctx, `SELECT r.id,r.evidence_id,r.native_id,r.native_kind,r.actor,r.created_at,COALESCE((SELECT l.operation_id FROM orders_recovery_links l WHERE l.resolution_id=r.id ORDER BY l.created_at,l.work_id LIMIT 1),'') FROM orders_recovery_resolutions r WHERE r.work_id=$1`, view.Work.ID).Scan(&resolution.ID, &resolution.EvidenceID, &resolution.NativeID, &resolution.NativeKind, &resolution.Actor, &resolution.CreatedAt, &resolution.NewOperationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return ordersError("read immutable order resolution", err)
	}
	resolution.Scope, resolution.WorkID = view.Work.Scope, view.Work.ID
	resolution.AcceptedUncertainty, resolution.WorkerStopped = true, true
	view.Resolution = &resolution
	return readRecoveryAttestation(ctx, tx, view.Resolution)
}

func recoveryBlockers(current orders.RecoveryContext, now time.Time) []orders.Validation {
	result := make([]orders.Validation, 0, 4)
	work := current.View.Work
	if issue := recoveryRenewalBlocker(current, now); issue != "" {
		result = append(result, orders.Validation{Code: issue})
	}
	if !work.RecoveryUpdateEligible() {
		result = append(result, orders.Validation{Code: "UNKNOWN_ACTION_DISCOVERY_ONLY"})
	}
	if !current.Config.Enabled || !current.Config.TestEnabled {
		result = append(result, orders.Validation{Code: "ORDER_WORKFLOW_DISABLED"})
	}
	e := current.View.Evidence
	if e == nil {
		return append(result, orders.Validation{Code: "RECOVERY_CHECK_REQUIRED"})
	}
	if e.ConfigRevision != current.Config.Revision || e.Credentials != current.Credentials || !e.ExpiresAt.After(now) || e.CreatedAt.After(now) {
		result = append(result, orders.Validation{Code: "RECOVERY_EVIDENCE_STALE"})
	}
	if !recoverySourceCurrent(current, now) {
		result = append(result, orders.Validation{Code: "RECOVERY_SOURCE_REFRESH_REQUIRED"})
	}
	if !recoveryNativeCurrent(work, e) {
		result = append(result, orders.Validation{Code: "RECOVERY_EXACT_NATIVE_REQUIRED"})
	}
	return result
}

func recoverySourceCurrent(current orders.RecoveryContext, now time.Time) bool {
	evidence := current.View.Evidence
	if evidence == nil || evidence.Source == nil || evidence.StoredSourceRevision != current.Posting.Evidence.Revision || ordersQuarantined(current.Posting) {
		return false
	}
	actual, err := orders.NewRecoverySource(current.Posting)
	checked := current.Posting.Evidence.CheckedAt
	if checked.IsZero() {
		checked = current.Posting.Evidence.ObservedAt
	}
	return err == nil && evidence.Source.Fingerprint == actual.Fingerprint && !checked.After(now.Add(time.Minute)) && now.Sub(checked) <= time.Duration(current.Config.SourceFreshnessSeconds)*time.Second
}

func recoveryNativeCurrent(work orders.Work, evidence *orders.RecoveryEvidence) bool {
	if evidence == nil || evidence.Target == nil || evidence.Target.Kind != "customerorder" || !evidence.Target.Complete || evidence.Target.Revision == "" {
		return false
	}
	target := evidence.Target.Correlation
	original := work.Command.Correlation
	return target.Scope == original.Scope && target.PostingNumber == original.PostingNumber && target.Marker == original.Marker && target.SyncID == original.SyncID && target.CustomerOrderID == original.CustomerOrderID && original.CustomerOrderID != ""
}

func addRecoveryDispatchBlockers(ctx context.Context, tx pgx.Tx, view *orders.RecoveryView) error {

	var active, providerActive bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM outbox_tasks WHERE orders_work_id=$1 AND task_type='orders_sync' AND ((state<>'pending' AND NOT(state='failed' AND EXISTS(SELECT 1 FROM orders_recovery_resolutions WHERE work_id=$1))) OR lease_until IS NOT NULL)) OR EXISTS(SELECT 1 FROM orders_target_fences f JOIN orders_work old ON old.id=$1 WHERE f.tenant_id=$2 AND f.moysklad_account=$3 AND f.ozon_account=$4 AND f.posting_number=old.posting_number AND f.work_id<>old.id),EXISTS(SELECT 1 FROM provider_call_permits WHERE tenant_id=$2 AND ((provider='moysklad' AND account_id=$3) OR (provider='ozon' AND account_id=$4)) AND state IN ('reserved','sending') AND lease_until>clock_timestamp())`, view.Work.ID, view.Work.Scope.Tenant, view.Work.Scope.MoyskladAccount, view.Work.Scope.OzonAccount).Scan(&active, &providerActive)
	if err != nil {
		return ordersError("check recovery dispatch absence", err)
	}
	if active || providerActive {
		view.Blockers = append(view.Blockers, orders.Validation{Code: "RECOVERY_ACTIVE_CLAIM_OR_PROVIDER_CALL"})
	}
	return nil
}

func readRecoveryAttestation(ctx context.Context, tx pgx.Tx, resolution *orders.RecoveryResolution) error {
	var a orders.RecoveryAttestation
	err := tx.QueryRow(ctx, `SELECT a.id,a.evidence_id,a.created_at,a.valid_until,COALESCE(l.operation_id,''),COALESCE(w.state,''),COALESCE(w.dispatch_count,0) FROM orders_recovery_attestations a LEFT JOIN orders_recovery_links l ON l.attestation_id=a.id LEFT JOIN orders_work w ON w.id=l.work_id WHERE a.resolution_id=$1 ORDER BY a.created_at DESC,a.id DESC LIMIT 1`, resolution.ID).Scan(&a.ID, &a.EvidenceID, &a.CreatedAt, &a.ValidUntil, &a.NewOperationID, &a.NewOperationState, &a.NewDispatchCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return ordersError("read scoped recovery attestation", err)
	}
	resolution.Attestation = &a
	return nil
}

func recoveryRenewalBlocker(current orders.RecoveryContext, now time.Time) string {
	if current.View.Resolution == nil {
		return ""
	}
	a := current.View.Resolution.Attestation
	if a == nil {
		return "RECOVERY_ATTESTATION_REQUIRED"
	}
	if a.NewOperationID != "" && (a.NewDispatchCount > 0 || a.NewOperationState != orders.StateBlocked) {
		return "RECOVERY_INSPECT_NEW_OPERATION"
	}
	if a.NewOperationID == "" && a.ValidUntil.After(now) {
		return "RECOVERY_ATTESTATION_STILL_ACTIVE"
	}
	e := current.View.Evidence
	if e == nil || e.ID == a.EvidenceID || !e.CreatedAt.After(a.CreatedAt) {
		return "RECOVERY_CHECK_REQUIRED"
	}
	return ""
}
