package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/domain"
)

var (
	ErrImportScope             = errors.New("import account not registered")
	ErrImportConflict          = errors.New("import already active")
	ErrImportIdempotency       = errors.New("import idempotency conflict")
	ErrImportNotFound          = errors.New("import operation not found")
	ErrImportClaimLost         = errors.New("import claim not owned")
	ErrImportRunning           = errors.New("active cancellation unsupported")
	ErrImportCommitUnknown     = errors.New("import commit outcome unknown")
	ErrImportCredentialChanged = errors.New("import credential changed during traversal")
)
var importKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

func validImportScope(tenant, account string) bool {
	return tenant != "" && account != "" && len(tenant) <= 256 && len(account) <= 256 && strings.TrimSpace(tenant) == tenant && strings.TrimSpace(account) == account
}

// SubmitOzonImport stores submission identity and the outbox task atomically.
func (s *Store) SubmitOzonImport(ctx context.Context, tenant, account, key, session string, limits domain.ImportLimits) (domain.ImportOperation, bool, error) {
	return s.SubmitCatalogImport(ctx, tenant, "ozon", account, key, session, limits)
}

func (s *Store) SubmitCatalogImport(ctx context.Context, tenant, provider, account, key, session string, limits domain.ImportLimits) (domain.ImportOperation, bool, error) {
	return s.submitCatalogImport(ctx, tenant, provider, account, key, session, auditPrincipal(session), limits)
}

// SubmitScheduledCatalogImport is called only by the scheduler after loading an
// enabled, audited policy. Its principal identifies the policy rather than a
// fictitious new operator action.
func (s *Store) SubmitScheduledCatalogImport(ctx context.Context, tenant, provider, account, key, policy string, limits domain.ImportLimits) (domain.ImportOperation, bool, error) {
	return s.submitCatalogImport(ctx, tenant, provider, account, key, policy, "AUTOMATIC_POLICY", limits)
}

func (s *Store) submitCatalogImport(ctx context.Context, tenant, provider, account, key, session, principal string, limits domain.ImportLimits) (domain.ImportOperation, bool, error) {
	operationType, taskType := domain.OzonCatalogImport, domain.OzonCatalogTask
	if provider == "moysklad" {
		operationType, taskType = domain.MoyskladCatalogImport, domain.MoyskladCatalogTask
	}
	if !validImportScope(tenant, account) || !importKeyPattern.MatchString(key) || session == "" || len(session) > 128 {
		return domain.ImportOperation{}, false, errors.New("invalid import request")
	}
	if !limits.ValidFor(provider) {
		return domain.ImportOperation{}, false, errors.New("invalid server limits")
	}
	payload, err := json.Marshal(struct {
		Tenant, Account, Type string
		Limits                domain.ImportLimits
	}{tenant, account, operationType, limits})
	if err != nil {
		return domain.ImportOperation{}, false, err
	}
	sum := sha256.Sum256(payload)
	fingerprint := hex.EncodeToString(sum[:])
	rawLimits, _ := json.Marshal(limits)
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return domain.ImportOperation{}, false, err
	}
	id := hex.EncodeToString(random[:])
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return domain.ImportOperation{}, false, err
	}
	defer rollbackTransaction(tx)
	var registered bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_connections WHERE tenant_id=$1 AND source=$3 AND account_id=$2)`, tenant, account, provider).Scan(&registered); err != nil {
		return domain.ImportOperation{}, false, err
	}
	if !registered {
		return domain.ImportOperation{}, false, ErrImportScope
	}
	var inserted string
	err = tx.QueryRow(ctx, `INSERT INTO ozon_import_operations(id,tenant_id,account_id,operation_type,idempotency_key,request_fingerprint,principal_type,session_audit_id,limits,provider) VALUES($1,$2,$3,$4,$5,$6,$10,$7,$8,$9) ON CONFLICT(tenant_id,idempotency_key) DO NOTHING RETURNING id`, id, tenant, account, operationType, key, fingerprint, session, rawLimits, provider, principal).Scan(&inserted)
	replay := false
	if errors.Is(err, pgx.ErrNoRows) {
		var existingFingerprint, existingAccount string
		if err = tx.QueryRow(ctx, `SELECT id,request_fingerprint,account_id FROM ozon_import_operations WHERE tenant_id=$1 AND idempotency_key=$2`, tenant, key).Scan(&id, &existingFingerprint, &existingAccount); err != nil {
			return domain.ImportOperation{}, false, err
		}
		if existingFingerprint != fingerprint || existingAccount != account {
			return domain.ImportOperation{}, false, ErrImportIdempotency
		}
		replay = true
	} else if err != nil {
		return domain.ImportOperation{}, false, err
	}
	if !replay {
		_, err = tx.Exec(ctx, `INSERT INTO outbox_tasks(operation_key,observation_id,max_attempts,task_type,import_operation_id,import_tenant_id,import_account_id) VALUES($1,NULL,3,$5,$2,$3,$4)`, provider+"-import:"+id, id, tenant, account, taskType)
		if err != nil {
			var pg *pgconn.PgError
			if errors.As(err, &pg) && (pg.ConstraintName == "outbox_one_active_catalog" || pg.ConstraintName == "outbox_one_active_ozon_catalog") {
				return domain.ImportOperation{}, false, ErrImportConflict
			}
			return domain.ImportOperation{}, false, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.ImportOperation{}, false, err
	}
	op, err := s.GetCatalogOperation(ctx, tenant, provider, account, id)
	return op, replay, err
}

const importColumns = `o.id,o.tenant_id,o.provider,o.account_id,o.operation_type,t.state,o.phase,o.outcome,o.limiting_resource,o.created_at,o.started_at,o.attempt_started_at,o.progress_at,o.finished_at,t.attempts,t.max_attempts,o.limits,o.counters,o.batch_id,o.batch_created,t.lease_until,COALESCE(t.state='running' AND t.lease_until<=clock_timestamp(),false)`

func scanImport(row pgx.Row) (domain.ImportOperation, error) {
	var v domain.ImportOperation
	var limits, counters []byte
	var leaseExpired bool
	err := row.Scan(&v.ID, &v.Tenant, &v.Provider, &v.Account, &v.Type, &v.State, &v.Phase, &v.Outcome, &v.LimitingResource, &v.CreatedAt, &v.StartedAt, &v.AttemptStartedAt, &v.ProgressAt, &v.FinishedAt, &v.Attempt, &v.MaxAttempts, &limits, &counters, &v.BatchID, &v.BatchCreated, &v.LeaseExpiresAt, &leaseExpired)
	if err != nil {
		return v, err
	}
	if err = json.Unmarshal(limits, &v.Limits); err != nil {
		return v, err
	}
	if err = json.Unmarshal(counters, &v.Counts); err != nil {
		return v, err
	}
	switch v.State {
	case "pending":
		v.State = "QUEUED"
	case "running":
		v.State = "RUNNING"
		if leaseExpired && v.Phase == "PERSISTING" {
			v.State = "OUTCOME_UNKNOWN"
		}
	case "succeeded":
		v.State = "SUCCEEDED"
	case "failed":
		v.State = "FAILED"
	case "cancelled":
		v.State = "CANCELLED"
	}
	if v.AttemptStartedAt != nil {
		end := time.Now()
		if v.FinishedAt != nil {
			end = *v.FinishedAt
		}
		if d := end.Sub(*v.AttemptStartedAt); d > 0 {
			v.ElapsedMS = d.Milliseconds()
		}
	}
	return v, nil
}
func (s *Store) GetImportOperation(ctx context.Context, tenant, account, id string) (domain.ImportOperation, error) {
	return s.GetCatalogOperation(ctx, tenant, "ozon", account, id)
}
func (s *Store) GetCatalogOperation(ctx context.Context, tenant, provider, account, id string) (domain.ImportOperation, error) {
	if !validImportScope(tenant, account) || len(id) != 32 {
		return domain.ImportOperation{}, ErrImportNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	v, err := scanImport(s.Pool.QueryRow(ctx, `SELECT `+importColumns+` FROM ozon_import_operations o JOIN outbox_tasks t ON t.import_operation_id=o.id JOIN stock_connections c ON c.tenant_id=o.tenant_id AND c.source=o.provider AND c.account_id=o.account_id WHERE o.tenant_id=$1 AND o.account_id=$2 AND o.id=$3 AND o.provider=$4`, tenant, account, id, provider))
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrImportNotFound
	}
	return v, err
}
func (s *Store) ListImportOperations(ctx context.Context, tenant, account string, limit, offset int) ([]domain.ImportOperation, bool, error) {
	return s.ListCatalogOperations(ctx, tenant, "ozon", account, limit, offset)
}
func (s *Store) ListCatalogOperations(ctx context.Context, tenant, provider, account string, limit, offset int) ([]domain.ImportOperation, bool, error) {
	if !validImportScope(tenant, account) || limit < 1 || limit > 50 || offset < 0 || offset > 10000 {
		return nil, false, errors.New("invalid import scope")
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	var registered bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_connections WHERE tenant_id=$1 AND source=$3 AND account_id=$2)`, tenant, account, provider).Scan(&registered); err != nil {
		return nil, false, err
	}
	if !registered {
		return nil, false, ErrImportScope
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+importColumns+` FROM ozon_import_operations o JOIN outbox_tasks t ON t.import_operation_id=o.id WHERE o.tenant_id=$1 AND o.account_id=$2 AND o.provider=$5 ORDER BY o.created_at DESC,o.id DESC LIMIT $3 OFFSET $4`, tenant, account, limit+1, offset, provider)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	list := []domain.ImportOperation{}
	for rows.Next() {
		v, e := scanImport(rows)
		if e != nil {
			return nil, false, e
		}
		list = append(list, v)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(list) > limit
	if more {
		list = list[:limit]
	}
	return list, more, nil
}
func (s *Store) CancelQueuedImport(ctx context.Context, tenant, account, id string) error {
	return s.CancelCatalogImport(ctx, tenant, "ozon", account, id)
}
func (s *Store) CancelCatalogImport(ctx context.Context, tenant, provider, account, id string) error {
	if !validImportScope(tenant, account) || len(id) != 32 {
		return ErrImportNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTransaction(tx)
	if err = requireContextActor(ctx, tx, tenant, false); err != nil {
		return err
	}
	var state string
	err = tx.QueryRow(ctx, `SELECT t.state FROM outbox_tasks t JOIN ozon_import_operations o ON o.id=t.import_operation_id WHERE o.id=$1 AND o.tenant_id=$2 AND o.account_id=$3 AND o.provider=$4 FOR UPDATE OF t`, id, tenant, account, provider).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrImportNotFound
	}
	if err != nil {
		return err
	}
	if state == "cancelled" {
		return nil
	}
	if state != "pending" {
		return ErrImportRunning
	}
	if _, err = tx.Exec(ctx, `UPDATE outbox_tasks SET state='cancelled',updated_at=clock_timestamp() WHERE import_operation_id=$1 AND state='pending'`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE ozon_import_operations SET phase='CANCELLED',outcome='CANCELLED',finished_at=clock_timestamp() WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// lockImportClaim serializes claim replacement, progress and final persistence.
func lockImportClaim(ctx context.Context, tx pgx.Tx, t domain.Task) error {
	var id int64
	err := tx.QueryRow(ctx, `SELECT id FROM outbox_tasks WHERE id=$1 AND import_operation_id=$2 AND task_type IN ('ozon_catalog_import','moysklad_catalog_import') AND state='running' AND claim_token=$3 FOR UPDATE`, t.ID, t.ImportOperationID, t.ClaimToken).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrImportClaimLost
	}
	if err != nil {
		return fmt.Errorf("lock import claim: %w", err)
	}
	return checkLockedImportLease(ctx, tx, t.ID)
}

// The row is already locked; evaluate the database clock after any lock wait.
func checkLockedImportLease(ctx context.Context, tx pgx.Tx, id int64) error {
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT COALESCE(lease_until>clock_timestamp(),false) FROM outbox_tasks WHERE id=$1`, id).Scan(&valid); err != nil {
		return fmt.Errorf("check import lease: %w", err)
	}
	if !valid {
		return ErrImportClaimLost
	}
	return nil
}
func (s *Store) ImportForClaim(ctx context.Context, t domain.Task) (domain.ImportOperation, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return domain.ImportOperation{}, err
	}
	defer rollbackTransaction(tx)
	if err = lockImportClaim(ctx, tx, t); err != nil {
		return domain.ImportOperation{}, err
	}
	v, err := scanImport(tx.QueryRow(ctx, `SELECT `+importColumns+` FROM ozon_import_operations o JOIN outbox_tasks t ON t.import_operation_id=o.id JOIN stock_connections c ON c.tenant_id=o.tenant_id AND c.source=o.provider AND c.account_id=o.account_id WHERE o.id=$1`, t.ImportOperationID))
	if err != nil {
		return v, err
	}
	if err = tx.Commit(ctx); err != nil {
		return v, err
	}
	return v, nil
}
func (s *Store) ImportProgress(ctx context.Context, t domain.Task, phase string, counts domain.ImportCounts) error {
	switch phase {
	case "READING_PRODUCTS", "READING_VARIANTS", "READING_WAREHOUSES", "READING_STOCK", "READING_AGGREGATE_STOCK", "READING_WAREHOUSE_STOCK", "PERSISTING":
	default:
		return errors.New("invalid import phase")
	}
	raw, err := json.Marshal(counts)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTransaction(tx)
	if err = lockImportClaim(ctx, tx, t); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE ozon_import_operations SET phase=$2,counters=$3,progress_at=clock_timestamp() WHERE id=$1`, t.ImportOperationID, phase, raw); err != nil {
		return err
	}
	if err = checkLockedImportLease(ctx, tx, t.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) FailImport(ctx context.Context, t domain.Task, outcome, resource string, counts domain.ImportCounts) error {
	switch outcome {
	case "LIMIT_REACHED", "CANCELLED", "API_FAILURE", "INVALID_RESPONSE", "PERSISTENCE_FAILURE", "CONFIGURATION_UNAVAILABLE":
	default:
		return errors.New("invalid import outcome")
	}
	raw, err := json.Marshal(counts)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTransaction(tx)
	if err = lockImportClaim(ctx, tx, t); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE ozon_import_operations SET phase='FAILED',outcome=$2,limiting_resource=NULLIF($3,''),counters=$4,progress_at=clock_timestamp(),finished_at=clock_timestamp() WHERE id=$1`, t.ImportOperationID, outcome, resource, raw); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE outbox_tasks SET state='failed',last_error=$3,claim_token=NULL,lease_until=NULL,updated_at=clock_timestamp() WHERE id=$1 AND claim_token=$2 AND lease_until>clock_timestamp()`, t.ID, t.ClaimToken, outcome)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrImportClaimLost
	}
	return tx.Commit(ctx)
}

// CommitClaimedOzonImport commits source evidence, operation linkage and task
// completion together while the task row is locked and the lease is valid.
func (s *Store) CommitClaimedOzonImport(ctx context.Context, t domain.Task, snap domain.OzonImport, fingerprint string, counts domain.ImportCounts, timeout time.Duration) error {
	return s.commitClaimedOzon(ctx, t, snap, fingerprint, counts, timeout, nil)
}
func (s *Store) CommitClaimedOzonImportWithRevision(ctx context.Context, t domain.Task, snap domain.OzonImport, fingerprint string, counts domain.ImportCounts, timeout time.Duration, revision int64) error {
	return s.commitClaimedOzon(ctx, t, snap, fingerprint, counts, timeout, &revision)
}
func (s *Store) commitClaimedOzon(ctx context.Context, t domain.Task, snap domain.OzonImport, fingerprint string, counts domain.ImportCounts, timeout time.Duration, revision *int64) error {
	return s.commitCatalog(ctx, t, "ozon", snap.Account, snap.StartedAt, counts, timeout, revision, func(ctx context.Context, tx pgx.Tx, notBefore *time.Time) (int64, bool, error) {
		return saveOzonImportTx(ctx, tx, snap, fingerprint, notBefore)
	})
}
func (s *Store) CommitClaimedMoyskladImport(ctx context.Context, t domain.Task, snap domain.InventoryImport, fingerprint string, counts domain.ImportCounts, timeout time.Duration) error {
	return s.commitClaimedMoysklad(ctx, t, snap, fingerprint, counts, timeout, nil)
}
func (s *Store) CommitClaimedMoyskladImportWithRevision(ctx context.Context, t domain.Task, snap domain.InventoryImport, fingerprint string, counts domain.ImportCounts, timeout time.Duration, revision int64) error {
	return s.commitClaimedMoysklad(ctx, t, snap, fingerprint, counts, timeout, &revision)
}
func (s *Store) commitClaimedMoysklad(ctx context.Context, t domain.Task, snap domain.InventoryImport, fingerprint string, counts domain.ImportCounts, timeout time.Duration, revision *int64) error {
	return s.commitCatalog(ctx, t, "moysklad", snap.Account, snap.StartedAt, counts, timeout, revision, func(ctx context.Context, tx pgx.Tx, notBefore *time.Time) (int64, bool, error) {
		return saveInventoryImportTx(ctx, tx, snap, fingerprint, notBefore)
	})
}
func (s *Store) commitCatalog(ctx context.Context, t domain.Task, provider, snapAccount string, started time.Time, counts domain.ImportCounts, timeout time.Duration, revision *int64, save func(context.Context, pgx.Tx, *time.Time) (int64, bool, error)) error {
	if timeout <= 0 || timeout > 10*time.Second {
		return errors.New("invalid persistence timeout")
	}
	raw, err := json.Marshal(counts)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTransaction(tx)
	if err = lockImportClaim(ctx, tx, t); err != nil {
		return err
	}
	if revision != nil {
		key := credentials.ScopeKey(credentials.Scope{Provider: provider, Account: snapAccount})
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
			return err
		}
	}
	var account, tenant string
	var includeHistory bool
	var registered time.Time
	err = tx.QueryRow(ctx, `SELECT o.account_id,o.tenant_id,c.include_import_history,c.registered_at FROM ozon_import_operations o JOIN stock_connections c ON c.tenant_id=o.tenant_id AND c.source=o.provider AND c.account_id=o.account_id WHERE o.id=$1 AND o.provider=$2 FOR SHARE OF c`, t.ImportOperationID, provider).Scan(&account, &tenant, &includeHistory, &registered)
	if err != nil {
		return err
	}
	if account != snapAccount {
		return ErrImportScope
	}
	if revision != nil {
		if err = checkImportCredential(ctx, tx, credentials.Scope{Tenant: tenant, Provider: provider, Account: account}, *revision); err != nil {
			return err
		}
	}
	var notBefore *time.Time
	if !includeHistory {
		notBefore = &registered
		if started.Before(registered) {
			return errors.New("import predates the account registration")
		}
	}
	id, created, err := save(ctx, tx, notBefore)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE ozon_import_operations SET phase='COMPLETED',outcome='COMPLETE_TRAVERSAL',counters=$2,progress_at=clock_timestamp(),finished_at=clock_timestamp(),batch_id=$3,batch_created=$4 WHERE id=$1`, t.ImportOperationID, raw, id, created); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE outbox_tasks SET state='succeeded',result='COMPLETE_TRAVERSAL',last_error=NULL,claim_token=NULL,lease_until=NULL,updated_at=clock_timestamp() WHERE id=$1 AND claim_token=$2 AND lease_until>clock_timestamp()`, t.ID, t.ClaimToken)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrImportClaimLost
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: %w", ErrImportCommitUnknown, err)
	}
	return nil
}

func checkImportCredential(ctx context.Context, tx pgx.Tx, scope credentials.Scope, expected int64) error {
	var revision int64
	var enabled bool
	err := tx.QueryRow(ctx, `SELECT revision,enabled FROM connection_credentials WHERE tenant_id=$1 AND provider=$2 AND account_id=$3 FOR SHARE`, scope.Tenant, scope.Provider, scope.Account).Scan(&revision, &enabled)
	if errors.Is(err, pgx.ErrNoRows) && expected == 0 {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (!enabled || revision != expected)) {
		return ErrImportCredentialChanged
	}
	return err
}

// ResolveImportCommit waits for any prior transaction's task lock before
// interpreting persisted state; false means no success was committed.
func (s *Store) ResolveImportCommit(ctx context.Context, t domain.Task) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer rollbackTransaction(tx)
	var state, token string
	var batch *int64
	err = tx.QueryRow(ctx, `SELECT t.state,COALESCE(t.claim_token,''),o.batch_id FROM outbox_tasks t JOIN ozon_import_operations o ON o.id=t.import_operation_id WHERE t.id=$1 AND o.id=$2 FOR UPDATE OF t`, t.ID, t.ImportOperationID).Scan(&state, &token, &batch)
	if err != nil {
		return false, err
	}
	if state == "succeeded" && batch != nil {
		return true, nil
	}
	if state != "running" || token != t.ClaimToken {
		return false, ErrImportClaimLost
	}
	return false, nil
}

// CatalogImportExists allows recovery of an immutable accepted intent even when
// its credential was subsequently disabled or removed. The subsequent submission
// still validates the exact frozen scope, type and limits before replaying it.
func (s *Store) CatalogImportExists(ctx context.Context, tenant, provider, account, key string) (bool, error) {
	if !validImportScope(tenant, account) || !importKeyPattern.MatchString(key) || (provider != "moysklad" && provider != "ozon") {
		return false, ErrImportScope
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	var exists bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ozon_import_operations WHERE tenant_id=$1 AND provider=$2 AND account_id=$3 AND idempotency_key=$4)`, tenant, provider, account, key).Scan(&exists)
	return exists, err
}
