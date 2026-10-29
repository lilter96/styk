package postgres

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lilter96/styk/internal/credentials"
)

const credentialColumns = `tenant_id,provider,account_id,name,revision,enabled,updated_at,checked_at,last_request_id`

func scanCredential(row pgx.Row) (credentials.Metadata, error) {
	var m credentials.Metadata
	err := row.Scan(&m.Tenant, &m.Provider, &m.Account, &m.Name, &m.Revision, &m.Enabled, &m.UpdatedAt, &m.CheckedAt, &m.LastRequestID)
	return m, err
}
func (s *Store) LoadCredential(ctx context.Context, scope credentials.Scope) (credentials.Record, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	var r credentials.Record
	err := s.Pool.QueryRow(ctx, `SELECT `+credentialColumns+`,ciphertext FROM connection_credentials WHERE tenant_id=$1 AND provider=$2 AND account_id=$3`, scope.Tenant, scope.Provider, scope.Account).Scan(&r.Tenant, &r.Provider, &r.Account, &r.Name, &r.Revision, &r.Enabled, &r.UpdatedAt, &r.CheckedAt, &r.LastRequestID, &r.Ciphertext)
	var pgerr *pgconn.PgError
	if errors.Is(err, pgx.ErrNoRows) || errors.As(err, &pgerr) && pgerr.Code == "42P01" {
		return r, credentials.ErrNotFound
	}
	if err != nil {
		return r, credentials.ErrUnavailable
	}
	return r, nil
}
func (s *Store) CredentialConnectionOwned(ctx context.Context, scope credentials.Scope) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	var owned bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_connections WHERE tenant_id=$1 AND source=$2 AND account_id=$3)`, scope.Tenant, scope.Provider, scope.Account).Scan(&owned)
	return owned, err
}
func (s *Store) ListCredentialMetadata(ctx context.Context, tenant string) ([]credentials.Metadata, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	rows, err := s.Pool.Query(ctx, `SELECT `+credentialColumns+` FROM connection_credentials WHERE tenant_id=$1 ORDER BY updated_at DESC,provider,account_id LIMIT 100`, tenant)
	if err != nil {
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) && pgerr.Code == "42P01" {
			return nil, credentials.ErrNotFound
		}
		return nil, credentials.ErrUnavailable
	}
	defer rows.Close()
	out := []credentials.Metadata{}
	for rows.Next() {
		m, err := scanCredential(rows)
		if err != nil {
			return nil, credentials.ErrUnavailable
		}
		out = append(out, m)
	}
	if rows.Err() != nil {
		return nil, credentials.ErrUnavailable
	}
	return out, nil
}
func (s *Store) CredentialRequest(ctx context.Context, tenant, provider, id string, digest []byte) (credentials.Metadata, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	return credentialRequest(ctx, s.Pool, tenant, provider, id, digest)
}

type credentialQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func credentialRequest(ctx context.Context, q credentialQuerier, tenant, provider, id string, digest []byte) (credentials.Metadata, error) {
	var m credentials.Metadata
	var stored []byte
	err := q.QueryRow(ctx, `SELECT tenant_id,provider,account_id,name,revision,enabled,updated_at,checked_at,request_id,request_digest FROM credential_save_requests WHERE tenant_id=$1 AND provider=$2 AND request_id=$3`, tenant, provider, id).Scan(&m.Tenant, &m.Provider, &m.Account, &m.Name, &m.Revision, &m.Enabled, &m.UpdatedAt, &m.CheckedAt, &m.LastRequestID, &stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, credentials.ErrNotFound
	}
	if err != nil {
		return m, credentials.ErrUnavailable
	}
	if !hmac.Equal(stored, digest) {
		return m, credentials.ErrConflict
	}
	return m, nil
}

// SaveCredential commits registration, encrypted material and the replay ledger
// together. All provider reads occur before this method starts a transaction.
func (s *Store) SaveCredential(ctx context.Context, scope credentials.Scope, name string, expected int64, requestID string, digest, ciphertext []byte, checked time.Time, principal string) (credentials.Metadata, bool, error) {
	return s.changeCredential(ctx, scope, name, expected, requestID, digest, ciphertext, checked, principal, true)
}
func (s *Store) DisconnectCredential(ctx context.Context, scope credentials.Scope, expected int64, requestID string, digest []byte) (credentials.Metadata, bool, error) {
	return s.changeCredential(ctx, scope, "", expected, requestID, digest, nil, time.Time{}, "", false)
}
func (s *Store) changeCredential(ctx context.Context, scope credentials.Scope, name string, expected int64, requestID string, digest, ciphertext []byte, checked time.Time, principal string, enable bool) (credentials.Metadata, bool, error) {
	var zero credentials.Metadata
	if !scope.Valid() || expected < 0 || requestID == "" || len(digest) != 32 || enable && (name == "" || len(ciphertext) < 29 || checked.IsZero() || principal == "") {
		return zero, false, credentials.ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return zero, false, credentials.ErrUnavailable
	}
	defer tx.Rollback(context.Background())
	// Lock the request namespace first, then the source account. This serializes
	// same-key requests even when a caller tries to change the supplied account.
	for _, key := range []string{fmt.Sprintf("credential-request:%s:%s:%s", scope.Tenant, scope.Provider, requestID), credentials.ScopeKey(credentials.Scope{Provider: scope.Provider, Account: scope.Account})} {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
			return zero, false, credentials.ErrUnavailable
		}
	}
	if m, e := credentialRequest(ctx, tx, scope.Tenant, scope.Provider, requestID, digest); e == nil {
		return m, true, nil
	} else if !errors.Is(e, credentials.ErrNotFound) {
		return zero, false, e
	}
	if enable {
		if _, err = tx.Exec(ctx, `INSERT INTO stock_connections(tenant_id,source,account_id,provenance,include_import_history) VALUES($1,$2,$3,$4,false) ON CONFLICT(source,account_id) DO NOTHING`, scope.Tenant, scope.Provider, scope.Account, principal); err != nil {
			return zero, false, credentials.ErrUnavailable
		}
	}
	var owner string
	err = tx.QueryRow(ctx, `SELECT tenant_id FROM stock_connections WHERE source=$1 AND account_id=$2 FOR UPDATE`, scope.Provider, scope.Account).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) || owner != "" && owner != scope.Tenant {
		return zero, false, credentials.ErrOwnership
	}
	if err != nil {
		return zero, false, credentials.ErrUnavailable
	}
	var current credentials.Metadata
	current, err = scanCredential(tx.QueryRow(ctx, `SELECT `+credentialColumns+` FROM connection_credentials WHERE tenant_id=$1 AND provider=$2 AND account_id=$3 FOR UPDATE`, scope.Tenant, scope.Provider, scope.Account))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return zero, false, credentials.ErrUnavailable
	}
	if current.Revision != expected {
		return zero, false, credentials.ErrVersion
	}
	if !enable && current.Revision == 0 {
		return zero, false, credentials.ErrNotFound
	}
	if !enable {
		name = current.Name
		checked = current.CheckedAt
	}
	m, err := scanCredential(tx.QueryRow(ctx, `INSERT INTO connection_credentials(tenant_id,provider,account_id,name,revision,enabled,ciphertext,checked_at,last_request_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(tenant_id,provider,account_id) DO UPDATE SET name=EXCLUDED.name,revision=EXCLUDED.revision,enabled=EXCLUDED.enabled,ciphertext=EXCLUDED.ciphertext,checked_at=EXCLUDED.checked_at,last_request_id=EXCLUDED.last_request_id,updated_at=clock_timestamp() RETURNING `+credentialColumns, scope.Tenant, scope.Provider, scope.Account, name, expected+1, enable, ciphertext, checked, requestID))
	if err != nil {
		return zero, false, credentials.ErrUnavailable
	}
	if _, err = tx.Exec(ctx, `INSERT INTO credential_save_requests(tenant_id,provider,request_id,request_digest,account_id,name,revision,enabled,checked_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, scope.Tenant, scope.Provider, requestID, digest, scope.Account, m.Name, m.Revision, m.Enabled, m.CheckedAt, m.UpdatedAt); err != nil {
		return zero, false, credentials.ErrUnavailable
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, false, credentials.ErrUnavailable
	}
	return m, false, nil
}
