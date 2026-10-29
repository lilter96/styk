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
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/testpublication"
)

var _ catalogsync.ServiceStore = (*Store)(nil)
var _ catalogsync.RunnerStore = (*Store)(nil)

type catalogQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func catalogID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("create catalog ID: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func catalogJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode catalog evidence: %w", err)
	}
	return raw, nil
}

func catalogHash(value any) (string, error) {
	raw, err := catalogJSON(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func catalogDecode(raw []byte, value any) error {
	if err := json.Unmarshal(raw, value); err != nil {
		return fmt.Errorf("decode catalog evidence: %w", err)
	}
	return nil
}

func catalogError(action string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", action, catalogsync.ErrNotFound)
	}
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.Code == "23505" {
		return fmt.Errorf("%s: %w", action, catalogsync.ErrConflict)
	}
	return fmt.Errorf("%s: %w", action, err)
}

func catalogLock(ctx context.Context, tx pgx.Tx, scope catalog.Scope) error {
	key := "catalog-sync:" + scope.Tenant + ":" + scope.MoyskladAccount + ":" + scope.OzonAccount
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
		return fmt.Errorf("lock catalog scope: %w", err)
	}
	return nil
}

func catalogActor(ctx context.Context, tx pgx.Tx, tenant, actor string) error {
	if actor == "" || len(actor) > 128 {
		return catalogsync.ErrInvalid
	}
	if session, ok := identity.SessionFromContext(ctx); ok && session.AuditID != actor {
		return catalogsync.ErrBlocked
	}
	if err := requireContextActor(ctx, tx, tenant, true); err != nil {
		return err
	}
	if strings.HasPrefix(actor, "user.") {
		return requireMerchantActor(ctx, tx, tenant, "MERCHANT_SESSION:"+actor, true)
	}
	return requireMerchantActor(ctx, tx, tenant, actor, true)
}

func catalogTestScope(scope catalog.Scope) bool {
	allowed := testpublication.Scope()
	return scope.Tenant == allowed.Tenant && scope.MoyskladAccount == allowed.MoyskladAccount && scope.OzonAccount == allowed.OzonAccount
}

func catalogScopeArgs(scope catalog.Scope) []any {
	return []any{scope.Tenant, scope.MoyskladAccount, scope.OzonAccount}
}

func catalogCredentials(ctx context.Context, q catalogQuerier, scope catalog.Scope) (catalogsync.CredentialRevisions, error) {
	var revisions catalogsync.CredentialRevisions
	err := q.QueryRow(ctx, `SELECT COALESCE(max(revision) FILTER(WHERE provider='moysklad'),0),COALESCE(max(revision) FILTER(WHERE provider='ozon'),0) FROM connection_credentials WHERE tenant_id=$1 AND enabled AND ((provider='moysklad' AND account_id=$2) OR (provider='ozon' AND account_id=$3))`, catalogScopeArgs(scope)...).Scan(&revisions.Moysklad, &revisions.Ozon)
	if err != nil {
		return revisions, fmt.Errorf("read catalog credential revisions: %w", err)
	}
	return revisions, nil
}

func catalogCredentialLocks(ctx context.Context, tx pgx.Tx, scope catalog.Scope) error {
	stock := domain.StockSyncScope{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount}
	for _, key := range pairedCredentialLockKeys(stock) {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
			return fmt.Errorf("lock catalog credentials: %w", err)
		}
	}
	return nil
}

func catalogClock(ctx context.Context, tx pgx.Tx, now time.Time) (time.Time, error) {
	return dispatchClock(ctx, tx, now)
}
