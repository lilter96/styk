package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func orderPostingHash(posting orders.Posting) (string, error) {
	return orders.PostingFingerprint(posting)
}

func insertOrderPosting(ctx context.Context, tx pgx.Tx, posting orders.Posting, now time.Time) (string, error) {
	if err := posting.Validate(); err != nil {
		return "", err
	}
	if err := insertOrderPostingIdentity(ctx, tx, posting, now); err != nil {
		return "", err
	}
	hash, err := orderPostingHash(posting)
	if err != nil {
		return "", err
	}
	id, err := replayOrderPosting(ctx, tx, posting, hash)
	if err == nil {
		return id, refreshOrderPostingHead(ctx, tx, posting, id, now)
	}
	if !errors.Is(err, orders.ErrNotFound) {
		return "", err
	}
	return writeOrderPosting(ctx, tx, posting, hash, now)
}

func insertOrderPostingIdentity(ctx context.Context, tx pgx.Tx, posting orders.Posting, now time.Time) error {
	if _, err := tx.Exec(ctx, `INSERT INTO orders_commercial_orders(tenant_id,ozon_account,commercial_order_id,created_at) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, posting.Scope.Tenant, posting.Scope.OzonAccount, posting.CommercialOrderID, now); err != nil {
		return ordersError("save commercial order identity", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO orders_posting_identities(tenant_id,ozon_account,posting_number,commercial_order_id,created_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, posting.Scope.Tenant, posting.Scope.OzonAccount, posting.PostingNumber, posting.CommercialOrderID, now); err != nil {
		return ordersError("save order posting identity", err)
	}
	var orderID string
	if err := tx.QueryRow(ctx, `SELECT commercial_order_id FROM orders_posting_identities WHERE tenant_id=$1 AND ozon_account=$2 AND posting_number=$3`, posting.Scope.Tenant, posting.Scope.OzonAccount, posting.PostingNumber).Scan(&orderID); err != nil {
		return ordersError("read commercial posting identity", err)
	}
	if orderID != posting.CommercialOrderID {
		return orders.ErrConflict
	}
	return insertOrderLineIdentities(ctx, tx, posting)
}

func insertOrderLineIdentities(ctx context.Context, tx pgx.Tx, posting orders.Posting) error {
	for _, line := range posting.Lines {
		identity := line.Identity
		if _, err := tx.Exec(ctx, `INSERT INTO orders_line_identities(tenant_id,ozon_account,posting_number,line_key,provider_line_id,sku,offer_id) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, posting.Scope.Tenant, posting.Scope.OzonAccount, posting.PostingNumber, identity.Key(), identity.ProviderLineID, identity.SKU, identity.OfferID); err != nil {
			return ordersError("save posting line identity", err)
		}
		var found bool
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders_line_identities WHERE tenant_id=$1 AND ozon_account=$2 AND posting_number=$3 AND line_key=$4 AND provider_line_id=$5 AND sku=$6 AND offer_id=$7)`, posting.Scope.Tenant, posting.Scope.OzonAccount, posting.PostingNumber, identity.Key(), identity.ProviderLineID, identity.SKU, identity.OfferID).Scan(&found)
		if err != nil {
			return ordersError("verify posting line identity", err)
		}
		if !found {
			return orders.ErrConflict
		}
	}
	return nil
}

func replayOrderPosting(ctx context.Context, tx pgx.Tx, posting orders.Posting, hash string) (string, error) {
	var id, previous string
	args := append(ordersScopeArgs(posting.Scope), posting.PostingNumber, posting.Evidence.Revision)
	err := tx.QueryRow(ctx, `SELECT id,observation_hash FROM orders_posting_observations WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND posting_number=$4 AND source_revision=$5`, args...).Scan(&id, &previous)
	if err != nil {
		return "", ordersError("read order observation replay", err)
	}
	if hash != previous {
		return "", orders.ErrConflict
	}
	return id, nil
}

func writeOrderPosting(ctx context.Context, tx pgx.Tx, posting orders.Posting, hash string, now time.Time) (string, error) {
	id, err := ordersID()
	if err != nil {
		return "", err
	}
	raw, err := ordersJSON(posting)
	if err != nil {
		return "", err
	}
	var updated *time.Time
	if posting.Evidence.ProviderUpdatedAt.IsKnown() {
		updated = posting.Evidence.ProviderUpdatedAt.Value
	}
	_, err = tx.Exec(ctx, `INSERT INTO orders_posting_observations(id,tenant_id,moysklad_account,ozon_account,posting_number,commercial_order_id,source_revision,observation_hash,posting,observed_at,provider_updated_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, id, posting.Scope.Tenant, posting.Scope.MoyskladAccount, posting.Scope.OzonAccount, posting.PostingNumber, posting.CommercialOrderID, posting.Evidence.Revision, hash, raw, posting.Evidence.ObservedAt, updated, now)
	if err != nil {
		return "", ordersError("save immutable order observation", err)
	}
	if err = refreshOrderPostingHead(ctx, tx, posting, id, now); err != nil {
		return "", err
	}
	return id, reserveOrderProjection(ctx, tx, posting.Scope, posting.PostingNumber, now)
}

func refreshOrderPostingHead(ctx context.Context, tx pgx.Tx, posting orders.Posting, id string, now time.Time) error {
	previous, err := readOrderPosting(ctx, tx, posting.Scope, posting.PostingNumber)
	if err != nil && !errors.Is(err, orders.ErrNotFound) {
		return err
	}
	ordering := orders.EvidenceUnknown
	if err == nil {
		same, compareErr := sameOrderPostingFacts(previous, posting)
		if compareErr != nil {
			return compareErr
		}
		if same {
			return touchOrderPosting(ctx, tx, posting.Scope, posting.PostingNumber, now)
		}
		ordering = orders.CompareEvidence(previous.Evidence, posting.Evidence)
		if ordering == orders.EvidenceSame {
			return touchOrderPosting(ctx, tx, posting.Scope, posting.PostingNumber, now)
		}
		if ordering == orders.EvidenceConflict {
			return quarantineOrderPosting(ctx, tx, posting, id, "ORDERING_PROVIDER_REVISION_CONFLICT", now)
		}
		if ordering == orders.EvidenceOlder {
			return nil
		}
		if ordering == orders.EvidenceUnknown {
			return advanceUnversionedOrderPosting(ctx, tx, posting, id, now)
		}
	}
	return writeOrderPostingHead(ctx, tx, posting, id, ordering, now)
}

func writeOrderPostingHead(ctx context.Context, tx pgx.Tx, posting orders.Posting, id string, ordering orders.EvidenceOrder, now time.Time) error {
	previous, err := readOrderPosting(ctx, tx, posting.Scope, posting.PostingNumber)
	if err == nil {
		previous.Drift = withoutOrderChronologyDrift(previous.Drift)
		posting, err = mergeOrderPosting(previous, posting)
	}
	if err != nil && !errors.Is(err, orders.ErrNotFound) {
		return err
	}
	raw, err := ordersJSON(posting)
	if err != nil {
		return err
	}
	args := append(ordersScopeArgs(posting.Scope), posting.PostingNumber, id, posting.Active(), ordering, now, raw)
	_, err = tx.Exec(ctx, `INSERT INTO orders_posting_heads(tenant_id,moysklad_account,ozon_account,posting_number,observation_id,active,ordering,checked_at,updated_at,projection) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8,$9) ON CONFLICT(tenant_id,moysklad_account,ozon_account,posting_number) DO UPDATE SET observation_id=EXCLUDED.observation_id,active=EXCLUDED.active,ordering=EXCLUDED.ordering,checked_at=EXCLUDED.checked_at,updated_at=EXCLUDED.updated_at,projection=EXCLUDED.projection`, args...)
	if err != nil {
		return ordersError("advance dated order posting facts", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO orders_posting_head_events(tenant_id,moysklad_account,ozon_account,posting_number,observation_id,ordering,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, append(ordersScopeArgs(posting.Scope), posting.PostingNumber, id, ordering, now)...)
	return ordersRowsError("journal accepted order observation", err)
}

func sameOrderPostingFacts(previous, next orders.Posting) (bool, error) {
	previous.Drift = withoutOrderChronologyDrift(previous.Drift)
	before, err := orderPostingHash(previous)
	if err != nil {
		return false, err
	}
	after, err := orderPostingHash(next)
	return before == after, err
}

func withoutOrderChronologyDrift(drift []orders.Drift) []orders.Drift {
	result := make([]orders.Drift, 0, len(drift))
	for _, entry := range drift {
		if entry.Code != "ORDERING_SUPERSEDED_OBSERVATION" && entry.Code != "ORDERING_PROVIDER_REVISION_CONFLICT" {
			result = append(result, entry)
		}
	}
	return result
}

func touchOrderPosting(ctx context.Context, tx pgx.Tx, scope orders.Scope, number string, now time.Time) error {
	args := append(ordersScopeArgs(scope), number, now)
	_, err := tx.Exec(ctx, `UPDATE orders_posting_heads SET checked_at=$5 WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND posting_number=$4`, args...)
	return ordersRowsError("refresh current order observation check", err)
}

func advanceUnversionedOrderPosting(ctx context.Context, tx pgx.Tx, posting orders.Posting, id string, now time.Time) error {
	if posting.Evidence.CollectionKind != orders.CollectionSerialDetail || posting.Evidence.LocalSequence < 1 {
		return nil
	}
	var prior bool
	args := append(ordersScopeArgs(posting.Scope), posting.PostingNumber, id)
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders_posting_observations o JOIN orders_posting_head_events e ON e.observation_id=o.id WHERE o.tenant_id=$1 AND o.moysklad_account=$2 AND o.ozon_account=$3 AND o.posting_number=$4 AND o.observation_hash=(SELECT observation_hash FROM orders_posting_observations WHERE id=$5) AND e.created_at<$6)`, append(args, now)...).Scan(&prior)
	if err != nil {
		return ordersError("check superseded order observation", err)
	}
	if prior {
		return quarantineOrderPosting(ctx, tx, posting, id, "ORDERING_SUPERSEDED_OBSERVATION", now)
	}
	return writeOrderPostingHead(ctx, tx, posting, id, orders.EvidenceUnknown, now)
}

func quarantineOrderPosting(ctx context.Context, tx pgx.Tx, posting orders.Posting, incoming, code string, now time.Time) error {
	id, err := ordersID()
	if err != nil {
		return err
	}
	args := append(ordersScopeArgs(posting.Scope), posting.PostingNumber, id, incoming, posting.Evidence.LocalSequence, now, code)
	_, err = tx.Exec(ctx, `INSERT INTO orders_posting_conflicts(id,tenant_id,moysklad_account,ozon_account,posting_number,current_observation_id,incoming_observation_id,collection_sequence,code,created_at) SELECT $5,h.tenant_id,h.moysklad_account,h.ozon_account,h.posting_number,h.observation_id,$6,$7,$9,$8 FROM orders_posting_heads h WHERE h.tenant_id=$1 AND h.moysklad_account=$2 AND h.ozon_account=$3 AND h.posting_number=$4`, args...)
	if err != nil {
		return ordersError("journal order chronology conflict", err)
	}
	_, err = tx.Exec(ctx, `UPDATE orders_posting_heads SET quarantined=true,checked_at=$5 WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND posting_number=$4`, append(ordersScopeArgs(posting.Scope), posting.PostingNumber, now)...)
	return ordersRowsError("quarantine superseded order observation", err)
}

func readOrderPosting(ctx context.Context, q ordersQuerier, scope orders.Scope, number string) (orders.Posting, error) {
	args := append(ordersScopeArgs(scope), number)
	return scanOrderPosting(q.QueryRow(ctx, `SELECT h.projection,h.checked_at,h.quarantined FROM orders_posting_heads h JOIN orders_posting_observations o ON o.id=h.observation_id AND o.tenant_id=h.tenant_id AND o.moysklad_account=h.moysklad_account AND o.ozon_account=h.ozon_account AND o.posting_number=h.posting_number WHERE h.tenant_id=$1 AND h.moysklad_account=$2 AND h.ozon_account=$3 AND h.posting_number=$4`, args...))
}

func reserveOrderProjection(ctx context.Context, tx pgx.Tx, scope orders.Scope, number string, now time.Time) error {
	correlation, err := orders.CorrelationFor(scope, number)
	if err != nil {
		return err
	}
	args := append(ordersScopeArgs(scope), number, correlation.SyncID, correlation.Marker, now)
	_, err = tx.Exec(ctx, `INSERT INTO orders_projection_bindings(tenant_id,moysklad_account,ozon_account,posting_number,sync_id,marker,created_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, args...)
	return ordersRowsError("reserve scoped order projection", err)
}
