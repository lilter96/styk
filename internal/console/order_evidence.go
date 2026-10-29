package console

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/postgres"
)

// Order checks are read-only at the providers. Each successful bounded
// traversal is sealed in the existing source-specific evidence tables.
type orderEvidenceAPI struct {
	db          *postgres.Store
	connections *connections
}
type orderRefreshRequest struct {
	domain.StockSyncScope
	WindowHours int `json:"window_hours"`
}
type orderRefreshResponse struct {
	From                    time.Time `json:"from"`
	To                      time.Time `json:"to"`
	SourceStatus            string    `json:"source_status"`
	SourceBatchID           *int64    `json:"source_batch_id,omitempty"`
	SourceAttempts          int       `json:"source_attempts"`
	SourceCompletedWindows  int       `json:"source_completed_windows"`
	SourcePendingWindows    int       `json:"source_pending_windows"`
	SourceLimitResource     string    `json:"source_limit_resource,omitempty"`
	PostingStatus           string    `json:"posting_status"`
	PostingBatchID          *int64    `json:"posting_batch_id,omitempty"`
	PostingAttempts         int       `json:"posting_attempts"`
	PostingCompletedWindows int       `json:"posting_completed_windows"`
	PostingPendingWindows   int       `json:"posting_pending_windows"`
	PostingLimitResource    string    `json:"posting_limit_resource,omitempty"`
}

var (
	errOrderCheckBusy          = errors.New("order evidence check already running")
	errOrderCheckCapacity      = errors.New("order evidence check capacity reached")
	errOrderCredentialDisabled = errors.New("order evidence credential disabled")
	orderEvidenceSlots         = make(chan struct{}, 8)
)

func registerOrderEvidenceRoutes(mux *http.ServeMux, a *auth, db *postgres.Store, c *connections) {
	api := &orderEvidenceAPI{db: db, connections: c}
	mux.HandleFunc("GET /api/stock-sync/order-evidence", a.authorized(api.get))
	mux.HandleFunc("POST /api/stock-sync/order-evidence/refresh", a.authorized(a.connectionMutation(api.refresh)))
}

func (a *orderEvidenceAPI) get(w http.ResponseWriter, r *http.Request) {
	scope := syncScope(r)
	e, progress, err := a.db.StockOrderEvidenceWithProgress(r.Context(), scope, r.URL.Query().Get("product"), r.URL.Query().Get("warehouse"))
	if err != nil {
		stockSyncError(w, err)
		return
	}
	writeJSON(w, struct {
		SourceBatchID   *int64                              `json:"source_batch_id,omitempty"`
		SourceFrom      *time.Time                          `json:"source_from,omitempty"`
		SourceTo        *time.Time                          `json:"source_to,omitempty"`
		SourceCheckedAt *time.Time                          `json:"source_checked_at,omitempty"`
		Positions       []domain.StockObservedOrderPosition `json:"positions"`
		PostingBatchID  *int64                              `json:"posting_batch_id,omitempty"`
		PostingFrom     *time.Time                          `json:"posting_from,omitempty"`
		PostingTo       *time.Time                          `json:"posting_to,omitempty"`
		Postings        []domain.StockObservedPostingLine   `json:"postings"`
		Truncated       bool                                `json:"truncated"`
		Progress        *postgres.OrderEvidenceSummary      `json:"progress,omitempty"`
	}{e.BatchID, e.WindowFrom, e.WindowTo, e.FinishedAt, e.Positions, e.PostingBatchID, e.PostingWindowFrom, e.PostingWindowTo, e.Postings, e.Truncated, progress})
}

func (a *orderEvidenceAPI) refresh(w http.ResponseWriter, r *http.Request) {
	var req orderRefreshRequest
	if !decodeStockSync(w, r, &req) {
		return
	}
	if !req.Valid() || (req.WindowHours != 1 && req.WindowHours != 24 && req.WindowHours != 168) {
		writeError(w, 400, "INVALID_SCOPE")
		return
	}
	key := orderEvidenceLockKey(req.StockSyncScope)
	release, err := lockOrderEvidence(r.Context(), a.db.Pool, key)
	if errors.Is(err, errOrderCheckBusy) {
		writeError(w, 409, "ORDER_CHECK_IN_PROGRESS")
		return
	}
	if errors.Is(err, errOrderCheckCapacity) {
		writeError(w, 503, "ORDER_CHECK_CAPACITY")
		return
	}
	if err != nil {
		writeError(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	defer release()
	a.runOrderRefresh(w, r, req)
}

func (a *orderEvidenceAPI) runOrderRefresh(w http.ResponseWriter, r *http.Request, req orderRefreshRequest) {
	readers, err := a.orderReaders(r.Context(), req.StockSyncScope)
	if err != nil {
		orderReaderError(w, err)
		return
	}
	run, err := a.db.StartOrderEvidenceForRevisions(r.Context(), req.StockSyncScope, req.WindowHours, time.Now().UTC(), readers.moyRevision, readers.marketRevision)
	if err != nil {
		orderRefreshError(w, err)
		return
	}
	run, sourceAttempts, err := a.refreshSourceOrders(r.Context(), readers.moy, run)
	if err != nil {
		orderRefreshError(w, err)
		return
	}
	run, postingAttempts, err := a.refreshMarketPostings(r.Context(), readers.market, run)
	if err != nil {
		orderRefreshError(w, err)
		return
	}
	writeJSON(w, orderRefreshResponse{
		From: run.From, To: run.To,
		SourceStatus: run.SourceStatus, SourceBatchID: run.SourceBatchID, SourceAttempts: sourceAttempts,
		SourceCompletedWindows: run.SourceCompletedWindows, SourcePendingWindows: run.SourcePendingWindows, SourceLimitResource: run.SourceResource,
		PostingStatus: run.PostingStatus, PostingBatchID: run.PostingBatchID, PostingAttempts: postingAttempts,
		PostingCompletedWindows: run.PostingCompletedWindows, PostingPendingWindows: run.PostingPendingWindows, PostingLimitResource: run.PostingResource,
	})
}

func orderRefreshError(w http.ResponseWriter, err error) {
	if errors.Is(err, postgres.ErrOrderEvidenceChanged) || errors.Is(err, postgres.ErrOrderEvidenceExpired) {
		writeError(w, 409, "ORDER_CHECK_RESTART_REQUIRED")
		return
	}
	writeError(w, 503, "DATABASE_UNAVAILABLE")
}

type orderReaderPair struct {
	moy                         *moysklad.Client
	market                      *ozon.Client
	moyRevision, marketRevision int64
}

func (a *orderEvidenceAPI) orderReaders(ctx context.Context, scope domain.StockSyncScope) (orderReaderPair, error) {
	var out orderReaderPair
	c := a.connections
	moyScope := credentials.Scope{Tenant: scope.Tenant, Provider: "moysklad", Account: scope.MoyskladAccount}
	secret, revision, legacy, err := c.orderCredential(ctx, moyScope, c.moy != nil && c.moyAccount == scope.MoyskladAccount)
	if err != nil {
		return out, err
	}
	out.moyRevision = revision
	if legacy {
		out.moy = c.moy
	} else if out.moy, err = c.newMoy(secret); err != nil {
		return out, err
	}
	marketScope := credentials.Scope{Tenant: scope.Tenant, Provider: "ozon", Account: scope.OzonAccount}
	secret, revision, legacy, err = c.orderCredential(ctx, marketScope, c.ozon != nil && c.ozonAccount == scope.OzonAccount)
	if err != nil {
		return out, err
	}
	out.marketRevision = revision
	if legacy {
		out.market = c.ozon
	} else if out.market, err = c.newOzon(scope.OzonAccount, secret); err != nil {
		return out, err
	}
	return out, nil
}

func (c *connections) orderCredential(ctx context.Context, scope credentials.Scope, hasLegacyClient bool) (string, int64, bool, error) {
	record, err := c.db.LoadCredential(ctx, scope)
	if err == nil {
		if !record.Enabled {
			return "", 0, false, errOrderCredentialDisabled
		}
		secret, e := c.vault.Decrypt(record)
		return secret, record.Revision, false, e
	}
	if !errors.Is(err, credentials.ErrNotFound) {
		return "", 0, false, err
	}
	if hasLegacyClient {
		owned, e := c.db.CredentialConnectionOwned(ctx, scope)
		if e != nil {
			return "", 0, false, e
		}
		if !owned {
			return "", 0, false, credentials.ErrOwnership
		}
		return "", 0, true, nil
	}
	secret, revision, err := (credentials.Resolver{Store: c.db, Vault: c.vault}).ResolveWithRevision(ctx, scope)
	return secret, revision, false, err
}

func orderReaderError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, credentials.ErrOwnership):
		writeError(w, 404, "SCOPE_NOT_REGISTERED")
	case errors.Is(err, errOrderCredentialDisabled):
		writeError(w, 409, "ORDER_CREDENTIAL_DISABLED")
	default:
		writeError(w, 503, "CREDENTIALS_UNAVAILABLE")
	}
}

// Length prefixes keep account names containing colons from colliding.
func orderEvidenceLockKey(scope domain.StockSyncScope) string {
	return fmt.Sprintf("order-evidence:%d:%s%d:%s%d:%s", len(scope.Tenant), scope.Tenant, len(scope.MoyskladAccount), scope.MoyskladAccount, len(scope.OzonAccount), scope.OzonAccount)
}

// The lock has its own bounded session, leaving the application pool free for
// checkpoints and credential reads even when that pool has one connection.
func lockOrderEvidence(ctx context.Context, pool *pgxpool.Pool, key string) (func(), error) {
	select {
	case orderEvidenceSlots <- struct{}{}:
	default:
		return nil, errOrderCheckCapacity
	}
	connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(connectCtx, pool.Config().ConnConfig.Copy())
	if err != nil {
		<-orderEvidenceSlots
		return nil, err
	}
	var locked bool
	if err = conn.QueryRow(connectCtx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, key).Scan(&locked); err != nil || !locked {
		closeOrderEvidenceSession(conn)
		if err != nil {
			return nil, err
		}
		return nil, errOrderCheckBusy
	}
	var once sync.Once
	return func() { once.Do(func() { closeOrderEvidenceSession(conn) }) }, nil
}

func closeOrderEvidenceSession(conn *pgx.Conn) {
	cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := conn.Close(cleanup); err != nil {
		slog.Warn("could not close order-check database session")
	}
	<-orderEvidenceSlots
}

func (a *orderEvidenceAPI) refreshSourceOrders(ctx context.Context, c *moysklad.Client, run postgres.OrderEvidenceRun) (postgres.OrderEvidenceRun, int, error) {
	if run.SourceStatus == "COMPLETED" {
		return run, 0, nil
	}
	limits := domain.DefaultMoyskladImportLimits()
	limits.Requests, limits.Attempts, limits.Pages = 15, 15, 15
	limits.ResponseBytes, limits.DurationNS = 2<<20, int64(25*time.Second)
	window := run.Source.Pending[0]
	snapshot, counts, err := c.ReadCustomerOrdersBounded(ctx, run.Scope.Tenant, run.Scope.MoyskladAccount, window.From, window.To, limits)
	if ctx.Err() != nil {
		return run, counts.Attempts, ctx.Err()
	}
	original := run.Source
	status, resource := orderAttemptStatus(err, counts.RateLimitResponses)
	if err == nil {
		if snapshot.Tenant != run.Scope.Tenant || snapshot.Account != run.Scope.MoyskladAccount || !snapshot.Since.Equal(window.From) || !snapshot.Until.Equal(window.To) {
			status = "INVALID_RESPONSE"
		} else if err = run.Source.Add(snapshot); err != nil {
			return run, counts.Attempts, err
		} else if len(run.Source.Pending) == 0 {
			status = "COMPLETED"
		}
	} else if status == "LIMIT_REACHED" && run.ContinuationSupported() && run.Source.Split() {
		status = "IN_PROGRESS"
	}
	updated, err := a.db.CheckpointOrderEvidence(ctx, run, "moysklad", status, resource)
	if errors.Is(err, postgres.ErrOrderEvidenceStagingLimit) || errors.Is(err, application.ErrEvidenceConflict) {
		run.Source = original
		status, resource = orderCheckpointFailure(err)
		updated, err = a.db.CheckpointOrderEvidence(ctx, run, "moysklad", status, resource)
	}
	return updated, counts.Attempts, err
}

func (a *orderEvidenceAPI) refreshMarketPostings(ctx context.Context, c *ozon.Client, run postgres.OrderEvidenceRun) (postgres.OrderEvidenceRun, int, error) {
	if run.PostingStatus == "COMPLETED" {
		return run, 0, nil
	}
	limits := ozon.Limits{Requests: 15, Attempts: 15, Pages: 15, Entities: 1000, Products: 1000, Warehouses: 1000, Postings: 1000, Observations: 1000, ResponseBytes: 2 << 20, Duration: 25 * time.Second}
	window := run.Posting.Pending[0]
	snapshot, counts, err := c.ReadPostingsBounded(ctx, run.Scope.Tenant, window.From, window.To, limits)
	if ctx.Err() != nil {
		return run, counts.Attempts, ctx.Err()
	}
	original := run.Posting
	status, resource := orderAttemptStatus(err, counts.RateLimitResponses)
	if err == nil {
		if snapshot.Tenant != run.Scope.Tenant || snapshot.Account != run.Scope.OzonAccount || !snapshot.Since.Equal(window.From) || !snapshot.Until.Equal(window.To) {
			status = "INVALID_RESPONSE"
		} else if err = run.Posting.Add(snapshot); err != nil {
			return run, counts.Attempts, err
		} else if len(run.Posting.Pending) == 0 {
			status = "COMPLETED"
		}
	} else if status == "LIMIT_REACHED" && run.ContinuationSupported() && run.Posting.Split() {
		status = "IN_PROGRESS"
	}
	updated, err := a.db.CheckpointOrderEvidence(ctx, run, "ozon", status, resource)
	if errors.Is(err, postgres.ErrOrderEvidenceStagingLimit) || errors.Is(err, application.ErrEvidenceConflict) {
		run.Posting = original
		status, resource = orderCheckpointFailure(err)
		updated, err = a.db.CheckpointOrderEvidence(ctx, run, "ozon", status, resource)
	}
	return updated, counts.Attempts, err
}

func orderCheckpointFailure(err error) (string, string) {
	if errors.Is(err, postgres.ErrOrderEvidenceStagingLimit) {
		return "LIMIT_REACHED", "staging_bytes"
	}
	return "CONFLICT", ""
}

func orderAttemptStatus(err error, rateLimitResponses int) (string, string) {
	if err == nil {
		return "IN_PROGRESS", ""
	}
	if rateLimitResponses > 0 {
		return "RATE_LIMITED", ""
	}
	var sourceLimit *moysklad.LimitError
	var marketLimit *ozon.LimitError
	if errors.As(err, &sourceLimit) {
		return "LIMIT_REACHED", orderLimitResource(sourceLimit.Resource)
	}
	if errors.As(err, &marketLimit) {
		return "LIMIT_REACHED", orderLimitResource(marketLimit.Resource)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "LIMIT_REACHED", "duration"
	}
	var sourceAPI *moysklad.APIError
	var marketAPI *ozon.APIError
	if errors.As(err, &sourceAPI) {
		return orderHTTPStatus(sourceAPI.Status), ""
	}
	if errors.As(err, &marketAPI) {
		return orderHTTPStatus(marketAPI.Status), ""
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, moysklad.ErrTransport) || errors.Is(err, ozon.ErrTransport) {
		return "UNAVAILABLE", ""
	}
	return "INVALID_RESPONSE", ""
}

func orderLimitResource(resource string) string {
	switch resource {
	case "requests", "attempts", "pages", "products", "variants", "warehouses", "observations", "warehouse_observations", "entities", "postings", "response_bytes", "duration", "staging_bytes":
		return resource
	default:
		return "other"
	}
}

func orderHTTPStatus(status int) string {
	if status == 429 {
		return "RATE_LIMITED"
	}
	if status == 401 || status == 403 {
		return "AUTHORIZATION_REQUIRED"
	}
	return "UNAVAILABLE"
}
