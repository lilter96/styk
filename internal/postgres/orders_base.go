package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/orders"
)

var _ orders.ServiceStore = (*Store)(nil)
var _ orders.RunnerStore = (*Store)(nil)
var _ orders.Ingestor = (*Store)(nil)
var _ orders.SecretStore = (*Store)(nil)
var _ orders.MetadataRegistrar = (*Store)(nil)

type ordersQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func ordersID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", errors.New("order identifier unavailable")
	}
	return hex.EncodeToString(value[:]), nil
}

func ordersJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, errors.New("order evidence encoding unavailable")
	}
	return raw, nil
}

func ordersHash(value any) (string, error) {
	raw, err := ordersJSON(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func ordersDecode(raw []byte, value any) error {
	if err := json.Unmarshal(raw, value); err != nil {
		return errors.New("stored order evidence is invalid")
	}
	return nil
}

func ordersError(action string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", action, orders.ErrNotFound)
	}
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) {
		switch pgerr.Code {
		case "23505":
			return fmt.Errorf("%s: %w", action, orders.ErrConflict)
		case "23503", "23514", "55000":
			return fmt.Errorf("%s: %w", action, orders.ErrStale)
		case "42501":
			return fmt.Errorf("%s: %w", action, orders.ErrBlocked)
		}
		return fmt.Errorf("%s: database operation unavailable", action)
	}
	return fmt.Errorf("%s: %w", action, err)
}

func ordersScopeArgs(scope orders.Scope) []any {
	return []any{scope.Tenant, scope.MoyskladAccount, scope.OzonAccount}
}

func ordersLock(ctx context.Context, tx pgx.Tx, scope orders.Scope) error {
	key := "orders:" + scope.Tenant + ":" + scope.MoyskladAccount + ":" + scope.OzonAccount
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
		return ordersError("lock order scope", err)
	}
	return nil
}

func ordersCredentialLocks(ctx context.Context, tx pgx.Tx, scope orders.Scope) error {
	stock := domain.StockSyncScope{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount}
	for _, key := range pairedCredentialLockKeys(stock) {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
			return ordersError("lock order credential revisions", err)
		}
	}
	return nil
}

func ordersMappingLocks(ctx context.Context, tx pgx.Tx, scope orders.Scope) error {
	stock := domain.StockSyncScope{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount}
	for _, key := range stockReviewLockKeys(stock) {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
			return ordersError("lock order mapping evidence", err)
		}
	}
	return nil
}

func ordersActor(ctx context.Context, tx pgx.Tx, tenant, actor string) (string, error) {
	session, ok := identity.SessionFromContext(ctx)
	if !ok || actor == "" || actor != session.AuditID {
		return "", orders.ErrBlocked
	}
	if _, err := tx.Exec(ctx, `SELECT saas_require_actor($1,$2,true)`, tenant, actor); err != nil {
		return "", ordersError("authorize order merchant", err)
	}
	var owner string
	if err := tx.QueryRow(ctx, `SELECT a.user_id FROM saas_merchant_accounts a JOIN saas_sessions s ON s.user_id=a.user_id WHERE a.tenant_id=$1 AND s.audit_id=$2`, tenant, actor).Scan(&owner); err != nil {
		return "", ordersError("read order owner", err)
	}
	return owner, nil
}

func ordersCredentials(ctx context.Context, q ordersQuerier, scope orders.Scope) (orders.CredentialRevisions, error) {
	var revisions orders.CredentialRevisions
	err := q.QueryRow(ctx, `SELECT COALESCE(max(revision) FILTER(WHERE provider='ozon'),0),COALESCE(max(revision) FILTER(WHERE provider='moysklad'),0) FROM connection_credentials WHERE tenant_id=$1 AND enabled AND ((provider='moysklad' AND account_id=$2) OR (provider='ozon' AND account_id=$3))`, ordersScopeArgs(scope)...).Scan(&revisions.Ozon, &revisions.Moysklad)
	if err != nil {
		return revisions, ordersError("read order credential revisions", err)
	}
	return revisions, nil
}

func ordersClock(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return now, ordersError("read order database clock", err)
	}
	return now, nil
}

func ordersSafeCode(code string) string {
	if len(code) > 128 {
		return "PROVIDER_ERROR"
	}
	for _, char := range code {
		if char != '_' && (char < 'A' || char > 'Z') && (char < '0' || char > '9') {
			return "PROVIDER_ERROR"
		}
	}
	return code
}

func ordersRequestKey(key string) bool {
	return key != "" && len(key) <= 128 && strings.TrimSpace(key) == key && !strings.ContainsAny(key, "\r\n\x00")
}
