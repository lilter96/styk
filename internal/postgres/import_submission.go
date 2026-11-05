package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orders"
)

type catalogImportSubmission struct {
	tenant, provider, account, key, session, principal string
	id, fingerprint, operationType, taskType           string
	rawLimits                                          []byte
	source                                             *orders.Scope
}

func newCatalogImportSubmission(tenant, provider, account, key, session, principal string, limits domain.ImportLimits, source *orders.Scope) (catalogImportSubmission, error) {
	r := catalogImportSubmission{tenant: tenant, provider: provider, account: account, key: key, session: session, principal: principal, operationType: domain.OzonCatalogImport, taskType: domain.OzonCatalogTask}
	if !validImportScope(tenant, account) || !importKeyPattern.MatchString(key) || session == "" || len(session) > 128 || !limits.ValidFor(provider) {
		return r, errors.New("invalid import request or server limits")
	}
	if source != nil {
		if provider != "ozon" || source.Tenant != tenant || source.OzonAccount != account {
			return r, orders.ErrInvalid
		}
		frozen := *source
		r.source = &frozen
	}
	if provider == "moysklad" {
		r.operationType, r.taskType = domain.MoyskladCatalogImport, domain.MoyskladCatalogTask
	}
	payload, err := json.Marshal(struct {
		Tenant, Account, Type string
		Limits                domain.ImportLimits
		OrdersMoyskladAccount string `json:",omitempty"`
	}{tenant, account, r.operationType, limits, orderSourceAccount(r.source)})
	if err != nil {
		return r, err
	}
	sum := sha256.Sum256(payload)
	r.fingerprint = hex.EncodeToString(sum[:])
	r.rawLimits, err = json.Marshal(limits)
	if err != nil {
		return r, err
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return r, err
	}
	r.id = hex.EncodeToString(random[:])
	return r, nil
}

func (r *catalogImportSubmission) persist(ctx context.Context, tx pgx.Tx) (bool, error) {
	owner := ""
	var err error
	if r.source != nil {
		owner, err = prepareOrderSource(ctx, tx, *r.source, r.session)
		if err != nil {
			return false, err
		}
	}
	var registered bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_connections WHERE tenant_id=$1 AND source=$3 AND account_id=$2)`, r.tenant, r.account, r.provider).Scan(&registered); err != nil {
		return false, err
	}
	if !registered {
		return false, ErrImportScope
	}
	replay, err := r.insert(ctx, tx)
	if err != nil || replay {
		return replay, err
	}
	if err = r.queue(ctx, tx); err != nil {
		return false, err
	}
	if r.source != nil {
		if err = queueOrderSource(ctx, tx, *r.source, r.id, owner, r.session); err != nil {
			return false, err
		}
	}
	return false, nil
}

func (r *catalogImportSubmission) insert(ctx context.Context, tx pgx.Tx) (bool, error) {
	var inserted string
	err := tx.QueryRow(ctx, `INSERT INTO ozon_import_operations(id,tenant_id,account_id,operation_type,idempotency_key,request_fingerprint,principal_type,session_audit_id,limits,provider) VALUES($1,$2,$3,$4,$5,$6,$10,$7,$8,$9) ON CONFLICT(tenant_id,idempotency_key) DO NOTHING RETURNING id`, r.id, r.tenant, r.account, r.operationType, r.key, r.fingerprint, r.session, r.rawLimits, r.provider, r.principal).Scan(&inserted)
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	var fingerprint, account string
	if err = tx.QueryRow(ctx, `SELECT id,request_fingerprint,account_id FROM ozon_import_operations WHERE tenant_id=$1 AND idempotency_key=$2`, r.tenant, r.key).Scan(&r.id, &fingerprint, &account); err != nil {
		return false, err
	}
	if fingerprint != r.fingerprint || account != r.account {
		return false, ErrImportIdempotency
	}
	return true, nil
}

func (r *catalogImportSubmission) queue(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `INSERT INTO outbox_tasks(operation_key,observation_id,max_attempts,task_type,import_operation_id,import_tenant_id,import_account_id) VALUES($1,NULL,3,$5,$2,$3,$4)`, r.provider+"-import:"+r.id, r.id, r.tenant, r.account, r.taskType)
	var pg *pgconn.PgError
	if errors.As(err, &pg) && (pg.ConstraintName == "outbox_one_active_catalog" || pg.ConstraintName == "outbox_one_active_ozon_catalog") {
		return ErrImportConflict
	}
	return err
}
