package console

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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
type orderRefreshResult struct {
	status   string
	batchID  *int64
	attempts int
}
type orderRefreshResponse struct {
	From            time.Time `json:"from"`
	To              time.Time `json:"to"`
	SourceStatus    string    `json:"source_status"`
	SourceBatchID   *int64    `json:"source_batch_id,omitempty"`
	SourceAttempts  int       `json:"source_attempts"`
	PostingStatus   string    `json:"posting_status"`
	PostingBatchID  *int64    `json:"posting_batch_id,omitempty"`
	PostingAttempts int       `json:"posting_attempts"`
}

var errOrderCheckBusy = errors.New("order evidence check already running")

func registerOrderEvidenceRoutes(mux *http.ServeMux, a *auth, db *postgres.Store, c *connections) {
	api := &orderEvidenceAPI{db: db, connections: c}
	mux.HandleFunc("GET /api/stock-sync/order-evidence", a.authorized(api.get))
	mux.HandleFunc("POST /api/stock-sync/order-evidence/refresh", a.authorized(a.connectionMutation(api.refresh)))
}

func (a *orderEvidenceAPI) get(w http.ResponseWriter, r *http.Request) {
	e, err := a.db.StockOrderEvidence(r.Context(), syncScope(r), r.URL.Query().Get("product"), r.URL.Query().Get("warehouse"))
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
	}{e.BatchID, e.WindowFrom, e.WindowTo, e.FinishedAt, e.Positions, e.PostingBatchID, e.PostingWindowFrom, e.PostingWindowTo, e.Postings, e.Truncated})
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
	// Resolve both scoped credentials before network I/O; neither registered
	// account can be borrowed from another workspace.
	moy, market, err := a.orderReaders(r.Context(), req.StockSyncScope)
	if err != nil {
		writeError(w, 404, "SCOPE_NOT_REGISTERED")
		return
	}
	key := "order-evidence:" + req.Tenant + ":" + req.MoyskladAccount + ":" + req.OzonAccount
	release, err := lockOrderEvidence(r.Context(), a.db.Pool, key)
	if errors.Is(err, errOrderCheckBusy) {
		writeError(w, 409, "ORDER_CHECK_IN_PROGRESS")
		return
	}
	if err != nil {
		writeError(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	defer release()
	until := time.Now().UTC().Truncate(time.Millisecond)
	since := until.Add(-time.Duration(req.WindowHours) * time.Hour)
	source := a.refreshSourceOrders(r.Context(), moy, req.StockSyncScope, since, until)
	posting := a.refreshMarketPostings(r.Context(), market, req.StockSyncScope, since, until)
	writeJSON(w, orderRefreshResponse{since, until, source.status, source.batchID, source.attempts, posting.status, posting.batchID, posting.attempts})
}

func (a *orderEvidenceAPI) orderReaders(ctx context.Context, scope domain.StockSyncScope) (*moysklad.Client, *ozon.Client, error) {
	moy, err := a.connections.resolveMoy(ctx, scope.Tenant, scope.MoyskladAccount)
	if err != nil {
		return nil, nil, err
	}
	market, err := a.connections.resolveOzon(ctx, scope.Tenant, scope.OzonAccount)
	return moy, market, err
}

// A session advisory lock serializes on-demand checks without keeping a SQL
// transaction open over network I/O. Failed unlocks are never pooled.
func lockOrderEvidence(ctx context.Context, pool *pgxpool.Pool, key string) (func(), error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, key).Scan(&locked); err != nil {
		conn.Release()
		return nil, err
	}
	if !locked {
		conn.Release()
		return nil, errOrderCheckBusy
	}
	return func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var released bool
		if err := conn.QueryRow(cleanup, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, key).Scan(&released); err != nil || !released {
			if closeErr := conn.Hijack().Close(cleanup); closeErr != nil {
				slog.Warn("could not close order-check database session")
			}
			return
		}
		conn.Release()
	}, nil
}

func (a *orderEvidenceAPI) refreshSourceOrders(ctx context.Context, c *moysklad.Client, scope domain.StockSyncScope, since, until time.Time) orderRefreshResult {
	limits := domain.DefaultMoyskladImportLimits()
	limits.Requests = 15
	limits.Attempts = 15
	limits.Pages = 15
	limits.ResponseBytes = 2 << 20
	limits.DurationNS = int64(25 * time.Second)
	snapshot, counts, err := c.ReadCustomerOrdersBounded(ctx, scope.Tenant, scope.MoyskladAccount, since, until, limits)
	result := orderRefreshResult{attempts: counts.Attempts}
	if err == nil {
		var id int64
		id, err = a.db.SaveCustomerOrders(ctx, snapshot)
		if err == nil {
			result.batchID = &id
		}
	}
	result.status = orderCheckStatus(err)
	return result
}

func (a *orderEvidenceAPI) refreshMarketPostings(ctx context.Context, c *ozon.Client, scope domain.StockSyncScope, since, until time.Time) orderRefreshResult {
	limits := ozon.Limits{Requests: 15, Attempts: 15, Pages: 15, Entities: 1000, Products: 1000, Warehouses: 1000, Postings: 1000, Observations: 1000, ResponseBytes: 2 << 20, Duration: 25 * time.Second}
	snapshot, counts, err := c.ReadPostingsBounded(ctx, scope.Tenant, since, until, limits)
	result := orderRefreshResult{attempts: counts.Attempts}
	if err == nil {
		var id int64
		id, err = a.db.SavePostings(ctx, snapshot)
		if err == nil {
			result.batchID = &id
		}
	}
	result.status = orderCheckStatus(err)
	return result
}

func orderCheckStatus(err error) string {
	if err == nil {
		return "COMPLETED"
	}
	var sourceLimit *moysklad.LimitError
	var marketLimit *ozon.LimitError
	if errors.As(err, &sourceLimit) || errors.As(err, &marketLimit) {
		return "LIMIT_REACHED"
	}
	var sourceAPI *moysklad.APIError
	var marketAPI *ozon.APIError
	if errors.As(err, &sourceAPI) {
		return orderHTTPStatus(sourceAPI.Status)
	}
	if errors.As(err, &marketAPI) {
		return orderHTTPStatus(marketAPI.Status)
	}
	return "UNAVAILABLE"
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
