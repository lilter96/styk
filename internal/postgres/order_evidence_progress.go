package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/domain"
)

var (
	ErrOrderEvidenceChanged      = errors.New("order evidence scope or credentials changed")
	ErrOrderEvidenceStagingLimit = errors.New("order evidence staging limit reached")
	ErrOrderEvidenceExpired      = errors.New("order evidence progress expired")
)

const orderEvidenceMaxAge = 30 * time.Minute

// OrderEvidenceRun carries only normalized evidence; it is never sent to the browser.
type OrderEvidenceRun struct {
	ID, Generation, Version                                                                      int64
	Scope                                                                                        domain.StockSyncScope
	WindowHours                                                                                  int
	From, To, StartedAt, UpdatedAt                                                               time.Time
	MoyskladRevision, OzonRevision                                                               int64
	SourceStatus, PostingStatus, SourceResource, PostingResource                                 string
	SourceBatchID, PostingBatchID                                                                *int64
	SourceCompletedWindows, SourcePendingWindows, PostingCompletedWindows, PostingPendingWindows int
	Source                                                                                       application.WindowProgress[domain.CustomerOrderImport]
	Posting                                                                                      application.WindowProgress[domain.PostingImport]
}

func (r OrderEvidenceRun) ContinuationSupported() bool {
	return r.MoyskladRevision > 0 && r.OzonRevision > 0
}

const orderEvidenceColumns = `id,generation,version,window_hours,window_from,window_to,started_at,updated_at,moysklad_revision,ozon_revision,source_status,posting_status,source_resource,posting_resource,source_batch_id,posting_batch_id,source_completed_windows,source_pending_windows,posting_completed_windows,posting_pending_windows,source_progress,posting_progress`

func scanOrderEvidence(row pgx.Row, scope domain.StockSyncScope) (OrderEvidenceRun, error) {
	r := OrderEvidenceRun{Scope: scope}
	var source, posting []byte
	err := row.Scan(&r.ID, &r.Generation, &r.Version, &r.WindowHours, &r.From, &r.To, &r.StartedAt, &r.UpdatedAt, &r.MoyskladRevision, &r.OzonRevision, &r.SourceStatus, &r.PostingStatus, &r.SourceResource, &r.PostingResource, &r.SourceBatchID, &r.PostingBatchID, &r.SourceCompletedWindows, &r.SourcePendingWindows, &r.PostingCompletedWindows, &r.PostingPendingWindows, &source, &posting)
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(source, &r.Source); err != nil {
		return r, fmt.Errorf("decode source progress: %w", err)
	}
	if err = json.Unmarshal(posting, &r.Posting); err != nil {
		return r, fmt.Errorf("decode posting progress: %w", err)
	}
	if r.SourceStatus != "COMPLETED" && (!r.Source.Valid(r.From, r.To) || len(r.Source.Pending) == 0 || len(r.Source.Completed) != r.SourceCompletedWindows || len(r.Source.Pending) != r.SourcePendingWindows) {
		return r, errors.New("invalid source progress")
	}
	if r.PostingStatus != "COMPLETED" && (!r.Posting.Valid(r.From, r.To) || len(r.Posting.Pending) == 0 || len(r.Posting.Completed) != r.PostingCompletedWindows || len(r.Posting.Pending) != r.PostingPendingWindows) {
		return r, errors.New("invalid posting progress")
	}
	return r, nil
}

func lockOrderCredentialRevisions(ctx context.Context, tx pgx.Tx, scope domain.StockSyncScope) (int64, int64, error) {
	for _, key := range pairedCredentialLockKeys(scope) {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
			return 0, 0, fmt.Errorf("lock credential revision: %w", err)
		}
	}
	var moy, ozon int64
	err := tx.QueryRow(ctx, `SELECT
	 COALESCE((SELECT CASE WHEN enabled THEN revision ELSE -1 END FROM connection_credentials WHERE tenant_id=$1 AND provider='moysklad' AND account_id=$2),0),
	 COALESCE((SELECT CASE WHEN enabled THEN revision ELSE -1 END FROM connection_credentials WHERE tenant_id=$1 AND provider='ozon' AND account_id=$3),0)
	 WHERE EXISTS(SELECT 1 FROM stock_connections WHERE tenant_id=$1 AND source='moysklad' AND account_id=$2)
	 AND EXISTS(SELECT 1 FROM stock_connections WHERE tenant_id=$1 AND source='ozon' AND account_id=$3)`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&moy, &ozon)
	if errors.Is(err, pgx.ErrNoRows) || moy < 0 || ozon < 0 {
		return 0, 0, ErrOrderEvidenceChanged
	}
	if err != nil {
		return 0, 0, fmt.Errorf("read credential revisions: %w", err)
	}
	return moy, ozon, nil
}

// StartOrderEvidence freezes the original window and resumes only its own
// account pair and current credential revisions. No provider I/O occurs here.
func (s *Store) StartOrderEvidence(ctx context.Context, scope domain.StockSyncScope, hours int, now time.Time) (OrderEvidenceRun, error) {
	return s.startOrderEvidence(ctx, scope, hours, now, nil)
}

// StartOrderEvidenceForRevisions fences the exact credentials used to create
// the provider clients, before any new or resumed progress is written.
func (s *Store) StartOrderEvidenceForRevisions(ctx context.Context, scope domain.StockSyncScope, hours int, now time.Time, moy, ozon int64) (OrderEvidenceRun, error) {
	if moy < 0 || ozon < 0 {
		return OrderEvidenceRun{}, ErrOrderEvidenceChanged
	}
	return s.startOrderEvidence(ctx, scope, hours, now, &[2]int64{moy, ozon})
}

func (s *Store) startOrderEvidence(ctx context.Context, scope domain.StockSyncScope, hours int, now time.Time, expected *[2]int64) (OrderEvidenceRun, error) {
	if !scope.Valid() || (hours != 1 && hours != 24 && hours != 168) || now.IsZero() {
		return OrderEvidenceRun{}, errors.New("invalid order evidence scope")
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return OrderEvidenceRun{}, fmt.Errorf("begin order evidence progress: %w", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = requireContextActor(ctx, tx, scope.Tenant, false); err != nil {
		return OrderEvidenceRun{}, err
	}
	moy, ozon, err := lockOrderCredentialRevisions(ctx, tx, scope)
	if err != nil {
		return OrderEvidenceRun{}, err
	}
	if expected != nil && (moy != expected[0] || ozon != expected[1]) {
		return OrderEvidenceRun{}, ErrOrderEvidenceChanged
	}
	r, err := scanOrderEvidence(tx.QueryRow(ctx, `SELECT `+orderEvidenceColumns+` FROM order_evidence_progress WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 FOR UPDATE`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount), scope)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return r, fmt.Errorf("read order evidence progress: %w", err)
	}
	reset := errors.Is(err, pgx.ErrNoRows) || r.WindowHours != hours || r.MoyskladRevision != moy || r.OzonRevision != ozon || (r.SourceStatus == "COMPLETED" && r.PostingStatus == "COMPLETED") || r.SourceStatus == "CONFLICT" || r.PostingStatus == "CONFLICT" || r.SourceStatus == "LIMIT_REACHED" || r.PostingStatus == "LIMIT_REACHED" || ((moy == 0 || ozon == 0) && r.ID != 0) || !now.Before(r.StartedAt.Add(orderEvidenceMaxAge))
	if reset {
		r, err = resetOrderEvidence(ctx, tx, scope, hours, now, moy, ozon)
		if err != nil {
			return r, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return r, fmt.Errorf("commit order evidence progress: %w", err)
	}
	return r, nil
}

func resetOrderEvidence(ctx context.Context, tx pgx.Tx, scope domain.StockSyncScope, hours int, now time.Time, moy, ozon int64) (OrderEvidenceRun, error) {
	to := now.UTC().Truncate(time.Millisecond)
	from := to.Add(-time.Duration(hours) * time.Hour)
	source, err := json.Marshal(application.NewWindowProgress[domain.CustomerOrderImport](from, to))
	if err != nil {
		return OrderEvidenceRun{}, err
	}
	posting, err := json.Marshal(application.NewWindowProgress[domain.PostingImport](from, to))
	if err != nil {
		return OrderEvidenceRun{}, err
	}
	row := tx.QueryRow(ctx, `INSERT INTO order_evidence_progress(tenant_id,moysklad_account,ozon_account,window_hours,window_from,window_to,started_at,updated_at,moysklad_revision,ozon_revision,generation,version,source_status,posting_status,source_progress,posting_progress)
	 VALUES($1,$2,$3,$4,$5,$6,$6,$6,$7,$8,1,1,'IN_PROGRESS','IN_PROGRESS',$9::jsonb,$10::jsonb)
	 ON CONFLICT(tenant_id,moysklad_account,ozon_account) DO UPDATE SET window_hours=EXCLUDED.window_hours,window_from=EXCLUDED.window_from,window_to=EXCLUDED.window_to,started_at=EXCLUDED.started_at,updated_at=EXCLUDED.updated_at,moysklad_revision=EXCLUDED.moysklad_revision,ozon_revision=EXCLUDED.ozon_revision,generation=order_evidence_progress.generation+1,version=order_evidence_progress.version+1,source_status='IN_PROGRESS',posting_status='IN_PROGRESS',source_resource='',posting_resource='',source_batch_id=NULL,posting_batch_id=NULL,source_completed_windows=0,source_pending_windows=1,posting_completed_windows=0,posting_pending_windows=1,source_progress=EXCLUDED.source_progress,posting_progress=EXCLUDED.posting_progress
	 RETURNING `+orderEvidenceColumns, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, hours, from, to, moy, ozon, source, posting)
	r, err := scanOrderEvidence(row, scope)
	if err != nil {
		return r, fmt.Errorf("reset order evidence progress: %w", err)
	}
	return r, nil
}

// OrderEvidenceSummary contains no staged provider observations.
type OrderEvidenceSummary struct {
	WindowHours             int       `json:"window_hours"`
	From                    time.Time `json:"from"`
	To                      time.Time `json:"to"`
	UpdatedAt               time.Time `json:"updated_at"`
	ExpiresAt               time.Time `json:"expires_at"`
	Expired                 bool      `json:"expired"`
	SourceStatus            string    `json:"source_status"`
	SourceBatchID           *int64    `json:"source_batch_id,omitempty"`
	SourceCompletedWindows  int       `json:"source_completed_windows"`
	SourcePendingWindows    int       `json:"source_pending_windows"`
	SourceLimitResource     string    `json:"source_limit_resource,omitempty"`
	PostingStatus           string    `json:"posting_status"`
	PostingBatchID          *int64    `json:"posting_batch_id,omitempty"`
	PostingCompletedWindows int       `json:"posting_completed_windows"`
	PostingPendingWindows   int       `json:"posting_pending_windows"`
	PostingLimitResource    string    `json:"posting_limit_resource,omitempty"`
	CredentialChanged       bool      `json:"credential_changed"`
	ContinuationSupported   bool      `json:"continuation_supported"`
}

// CurrentOrderEvidence reads bounded metadata without loading staged records.
func (s *Store) CurrentOrderEvidence(ctx context.Context, scope domain.StockSyncScope) (*OrderEvidenceSummary, error) {
	if !scope.Valid() {
		return nil, domain.ErrStockSyncNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	return currentOrderEvidence(ctx, s.Pool, scope)
}

type orderEvidenceQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func currentOrderEvidence(ctx context.Context, q orderEvidenceQuerier, scope domain.StockSyncScope) (*OrderEvidenceSummary, error) {
	var out OrderEvidenceSummary
	var started time.Time
	var storedMoy, storedOzon, currentMoy, currentOzon int64
	err := q.QueryRow(ctx, `SELECT p.window_hours,p.window_from,p.window_to,p.started_at,p.updated_at,p.source_status,p.source_completed_windows,p.source_pending_windows,p.source_resource,p.source_batch_id,p.posting_status,p.posting_completed_windows,p.posting_pending_windows,p.posting_resource,p.posting_batch_id,p.moysklad_revision,p.ozon_revision,
	 COALESCE((SELECT CASE WHEN enabled THEN revision ELSE -1 END FROM connection_credentials WHERE tenant_id=p.tenant_id AND provider='moysklad' AND account_id=p.moysklad_account),0),
	 COALESCE((SELECT CASE WHEN enabled THEN revision ELSE -1 END FROM connection_credentials WHERE tenant_id=p.tenant_id AND provider='ozon' AND account_id=p.ozon_account),0)
	 FROM order_evidence_progress p WHERE p.tenant_id=$1 AND p.moysklad_account=$2 AND p.ozon_account=$3`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&out.WindowHours, &out.From, &out.To, &started, &out.UpdatedAt, &out.SourceStatus, &out.SourceCompletedWindows, &out.SourcePendingWindows, &out.SourceLimitResource, &out.SourceBatchID, &out.PostingStatus, &out.PostingCompletedWindows, &out.PostingPendingWindows, &out.PostingLimitResource, &out.PostingBatchID, &storedMoy, &storedOzon, &currentMoy, &currentOzon)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read order evidence status: %w", err)
	}
	out.ContinuationSupported = storedMoy > 0 && storedOzon > 0
	out.ExpiresAt = started.Add(orderEvidenceMaxAge)
	out.Expired = (out.SourceStatus != "COMPLETED" || out.PostingStatus != "COMPLETED") && !time.Now().UTC().Before(out.ExpiresAt)
	out.CredentialChanged = storedMoy != currentMoy || storedOzon != currentOzon
	return &out, nil
}

// CheckpointOrderEvidence persists progress and atomically seals a batch only
// after the complete frozen interval has been checked without gaps.
func (s *Store) CheckpointOrderEvidence(ctx context.Context, run OrderEvidenceRun, provider, status, resource string) (OrderEvidenceRun, error) {
	if !time.Now().UTC().Before(run.StartedAt.Add(orderEvidenceMaxAge)) {
		return run, ErrOrderEvidenceExpired
	}
	if provider != "moysklad" && provider != "ozon" {
		return run, errors.New("invalid order evidence provider")
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return run, fmt.Errorf("begin order evidence checkpoint: %w", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = fenceOrderEvidence(ctx, tx, run); err != nil {
		return run, err
	}
	if provider == "moysklad" {
		run, err = checkpointOrderSource(ctx, tx, run, status, resource)
	} else {
		run, err = checkpointOrderPosting(ctx, tx, run, status, resource)
	}
	if err != nil {
		return run, err
	}
	if err = tx.Commit(ctx); err != nil {
		return run, fmt.Errorf("commit order evidence checkpoint: %w", err)
	}
	return run, nil
}

func fenceOrderEvidence(ctx context.Context, tx pgx.Tx, run OrderEvidenceRun) error {
	if err := requireContextActor(ctx, tx, run.Scope.Tenant, false); err != nil {
		return err
	}
	moy, ozon, err := lockOrderCredentialRevisions(ctx, tx, run.Scope)
	if err != nil {
		return err
	}
	if moy != run.MoyskladRevision || ozon != run.OzonRevision {
		return ErrOrderEvidenceChanged
	}
	current, err := scanOrderEvidence(tx.QueryRow(ctx, `SELECT `+orderEvidenceColumns+` FROM order_evidence_progress WHERE id=$1 AND tenant_id=$2 AND moysklad_account=$3 AND ozon_account=$4 FOR UPDATE`, run.ID, run.Scope.Tenant, run.Scope.MoyskladAccount, run.Scope.OzonAccount), run.Scope)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrOrderEvidenceChanged
	}
	if err != nil {
		return fmt.Errorf("read checkpoint progress: %w", err)
	}
	if current.Generation != run.Generation || current.Version != run.Version {
		return ErrOrderEvidenceChanged
	}
	if !time.Now().UTC().Before(run.StartedAt.Add(orderEvidenceMaxAge)) {
		return ErrOrderEvidenceExpired
	}
	return nil
}

func evidenceProgressJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(b) > 4<<20 {
		return nil, ErrOrderEvidenceStagingLimit
	}
	return b, nil
}

func checkpointOrderSource(ctx context.Context, tx pgx.Tx, run OrderEvidenceRun, status, resource string) (OrderEvidenceRun, error) {
	p := run.Source
	if !p.Valid(run.From, run.To) || (status == "COMPLETED") != (len(p.Pending) == 0) {
		return run, errors.New("invalid source evidence checkpoint")
	}
	payload, err := evidenceProgressJSON(p)
	if err != nil {
		return run, err
	}
	var batch *int64
	if status == "COMPLETED" {
		merged, e := application.CombineCustomerOrderSlices(p, run.Scope.Tenant, run.Scope.MoyskladAccount, run.From, run.To, run.StartedAt, time.Now().UTC())
		if e != nil {
			return run, e
		}
		id, e := saveCustomerOrdersTx(ctx, tx, merged)
		if e != nil {
			return run, e
		}
		batch = &id
		payload = []byte(`{}`)
	}
	row := tx.QueryRow(ctx, `UPDATE order_evidence_progress SET source_status=$1,source_resource=$2,source_batch_id=$3,source_progress=$4::jsonb,source_completed_windows=$5,source_pending_windows=$6,version=version+1,updated_at=clock_timestamp() WHERE id=$7 AND generation=$8 AND version=$9 RETURNING `+orderEvidenceColumns, status, resource, batch, payload, len(p.Completed), len(p.Pending), run.ID, run.Generation, run.Version)
	r, err := scanOrderEvidence(row, run.Scope)
	if err != nil {
		return run, fmt.Errorf("save source progress: %w", err)
	}
	return r, nil
}

func checkpointOrderPosting(ctx context.Context, tx pgx.Tx, run OrderEvidenceRun, status, resource string) (OrderEvidenceRun, error) {
	p := run.Posting
	if !p.Valid(run.From, run.To) || (status == "COMPLETED") != (len(p.Pending) == 0) {
		return run, errors.New("invalid posting evidence checkpoint")
	}
	payload, err := evidenceProgressJSON(p)
	if err != nil {
		return run, err
	}
	var batch *int64
	if status == "COMPLETED" {
		merged, e := application.CombinePostingSlices(p, run.Scope.Tenant, run.Scope.OzonAccount, run.From, run.To, run.StartedAt, time.Now().UTC())
		if e != nil {
			return run, e
		}
		id, e := savePostingsTx(ctx, tx, merged)
		if e != nil {
			return run, e
		}
		batch = &id
		payload = []byte(`{}`)
	}
	row := tx.QueryRow(ctx, `UPDATE order_evidence_progress SET posting_status=$1,posting_resource=$2,posting_batch_id=$3,posting_progress=$4::jsonb,posting_completed_windows=$5,posting_pending_windows=$6,version=version+1,updated_at=clock_timestamp() WHERE id=$7 AND generation=$8 AND version=$9 RETURNING `+orderEvidenceColumns, status, resource, batch, payload, len(p.Completed), len(p.Pending), run.ID, run.Generation, run.Version)
	r, err := scanOrderEvidence(row, run.Scope)
	if err != nil {
		return run, fmt.Errorf("save posting progress: %w", err)
	}
	return r, nil
}
