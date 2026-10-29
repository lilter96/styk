package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/identity"
)

func auditPrincipal(session string) string {
	if strings.HasPrefix(session, "user.") {
		return "MERCHANT_SESSION"
	}
	return "LOCAL_OPERATOR_SESSION"
}

func rollbackMerchantMutation(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		slog.Error("merchant mutation rollback unavailable")
	}
}

func requireContextActor(ctx context.Context, tx pgx.Tx, tenant string, admin bool) error {
	session, ok := identity.SessionFromContext(ctx)
	if !ok {
		return nil
	}
	return requireMerchantActor(ctx, tx, tenant, "MERCHANT_SESSION:"+session.AuditID, admin)
}

func ensureConnectionWorkspace(ctx context.Context, tx pgx.Tx, tenant, provenance string) error {
	if strings.HasPrefix(provenance, "MERCHANT_SESSION:") {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO saas_workspaces(id,name,legacy_unclaimed) VALUES($1,rtrim(left($1,120)),true) ON CONFLICT(id) DO NOTHING`, tenant)
	if err != nil {
		return fmt.Errorf("reserve local workspace: %w", err)
	}
	return nil
}

func requireMerchantActor(ctx context.Context, tx pgx.Tx, tenant, provenance string, admin bool) error {
	const prefix = "MERCHANT_SESSION:"
	if !strings.HasPrefix(provenance, prefix) {
		return nil
	}
	if _, err := tx.Exec(ctx, `SELECT saas_require_actor($1,$2,$3)`, tenant, strings.TrimPrefix(provenance, prefix), admin); err != nil {
		return fmt.Errorf("merchant permission no longer valid: %w", err)
	}
	return nil
}
