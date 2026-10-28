package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"example.com/styk/internal/commitment"
	"example.com/styk/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// RegisterStockConnection records an explicit trusted-administrator attestation.
// It does not authenticate an operator or verify ownership with an external API.
func (s *Store) RegisterStockConnection(ctx context.Context, tenant, source, account, provenance string, includeImportHistory bool) error {
	if strings.TrimSpace(tenant) == "" || strings.TrimSpace(tenant) != tenant || strings.TrimSpace(account) == "" || strings.TrimSpace(account) != account || strings.TrimSpace(provenance) == "" || (source != "moysklad" && source != "ozon") {
		return errors.New("tenant, source, account and provenance are required")
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin connection registration: %w", err)
	}
	defer tx.Rollback(context.Background())
	_, err = tx.Exec(ctx, `INSERT INTO stock_connections(tenant_id,source,account_id,provenance,include_import_history) VALUES($1,$2,$3,$4,$5) ON CONFLICT(source,account_id) DO NOTHING`, tenant, source, account, provenance, includeImportHistory)
	if err != nil {
		return fmt.Errorf("register stock connection: %w", err)
	}
	var owner, existingProvenance string
	var existingHistory bool
	if err = tx.QueryRow(ctx, `SELECT tenant_id,provenance,include_import_history FROM stock_connections WHERE source=$1 AND account_id=$2 FOR SHARE`, source, account).Scan(&owner, &existingProvenance, &existingHistory); err != nil {
		return fmt.Errorf("read stock connection ownership: %w", err)
	}
	if owner != tenant {
		return errors.New("source account is already registered to another tenant")
	}
	if existingHistory != includeImportHistory || existingProvenance != provenance {
		return errors.New("existing registration has different history scope or provenance")
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit connection registration: %w", err)
	}
	return nil
}

func validateStockRequest(r domain.StockCalculationRequest) error {
	for _, value := range []string{r.Pool.Tenant, r.Pool.MoyskladAccount, r.Pool.WarehouseID, r.Pool.ItemID, r.Target.OzonAccount, r.Target.WarehouseID, r.Target.ProductID} {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
			return errors.New("tenant, account, warehouse and item identities are required")
		}
	}
	if r.Pool.ItemKind != "product" && r.Pool.ItemKind != "variant" {
		return errors.New("item kind must be product or variant")
	}
	if r.Policy.Version <= 0 || r.Policy.MaxObservationAge <= 0 || r.Policy.DecisionLifetime <= 0 {
		return errors.New("policy version and durations must be positive")
	}
	return nil
}

// CalculateStock uses only persisted observations. In particular, it never
// constructs the cross-system proofs needed to make a READY decision.
func (s *Store) CalculateStock(ctx context.Context, request domain.StockCalculationRequest, now time.Time) (domain.StockDecision, error) {
	if err := validateStockRequest(request); err != nil {
		return domain.StockDecision{}, err
	}
	if now.IsZero() {
		return domain.StockDecision{}, errors.New("calculation time is required")
	}
	// PostgreSQL timestamptz has microsecond precision; keep the frozen JSON
	// and indexed timestamp identical instead of silently losing nanoseconds.
	now = now.UTC().Truncate(time.Microsecond)
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	for attempt := 0; attempt < 5; attempt++ {
		d, err := s.calculateStock(ctx, request, now)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || (pgerr.Code != "40001" && pgerr.Code != "40P01") {
			return d, err
		}
		if ctx.Err() != nil {
			return domain.StockDecision{}, ctx.Err()
		}
		timer := time.NewTimer(time.Duration(1<<attempt) * 5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return domain.StockDecision{}, ctx.Err()
		case <-timer.C:
		}
	}
	return domain.StockDecision{}, errors.New("stock calculation exceeded serialization retry budget")
}

func (s *Store) calculateStock(ctx context.Context, r domain.StockCalculationRequest, now time.Time) (domain.StockDecision, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return domain.StockDecision{}, fmt.Errorf("begin stock calculation: %w", err)
	}
	defer tx.Rollback(context.Background())
	// Match the import and confirmation lock order. Serializable isolation also
	// protects the read from changes that do not use these application locks.
	for _, key := range []string{"ozon:" + r.Target.OzonAccount, "moysklad:" + r.Pool.MoyskladAccount, "matching:" + r.Pool.Tenant, "warehouse-mapping:" + r.Pool.Tenant} {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
			return domain.StockDecision{}, fmt.Errorf("lock stock scope: %w", err)
		}
	}
	e := domain.StockEvidence{Proofs: map[string]domain.StockProof{}}
	for _, connection := range []domain.StockConnectionReference{{Source: "moysklad", Account: r.Pool.MoyskladAccount}, {Source: "ozon", Account: r.Target.OzonAccount}} {
		err = tx.QueryRow(ctx, `SELECT provenance,registered_at,include_import_history FROM stock_connections WHERE tenant_id=$1 AND source=$2 AND account_id=$3 FOR SHARE`, r.Pool.Tenant, connection.Source, connection.Account).Scan(&connection.Provenance, &connection.RegisteredAt, &connection.IncludeImportHistory)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.StockDecision{}, errors.New("both source accounts require explicit trusted registration for this tenant")
		}
		if err != nil {
			return domain.StockDecision{}, fmt.Errorf("read stock connection: %w", err)
		}
		e.Connections = append(e.Connections, connection)
	}
	registeredAt := e.Connections[0].RegisteredAt
	if e.Connections[1].RegisteredAt.After(registeredAt) {
		registeredAt = e.Connections[1].RegisteredAt
	}
	e.Proofs[commitment.ProofOwnership] = domain.StockProof{State: domain.ProofVerified, Reference: "trusted administrator account registrations", AsOf: registeredAt, ValidUntil: now.Add(r.Policy.DecisionLifetime)}
	_, err = tx.Exec(ctx, `INSERT INTO stock_policies(tenant_id,version,unit,safety_stock,max_observation_age_ns,decision_lifetime_ns) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(tenant_id,version) DO NOTHING`, r.Pool.Tenant, r.Policy.Version, r.Policy.Unit, r.Policy.SafetyStock, int64(r.Policy.MaxObservationAge), int64(r.Policy.DecisionLifetime))
	if err != nil {
		return domain.StockDecision{}, fmt.Errorf("register stock policy: %w", err)
	}
	var unit, safety string
	var maxAge, lifetime int64
	err = tx.QueryRow(ctx, `SELECT unit,safety_stock,max_observation_age_ns,decision_lifetime_ns FROM stock_policies WHERE tenant_id=$1 AND version=$2 FOR SHARE`, r.Pool.Tenant, r.Policy.Version).Scan(&unit, &safety, &maxAge, &lifetime)
	if err != nil {
		return domain.StockDecision{}, fmt.Errorf("read stock policy: %w", err)
	}
	if unit != r.Policy.Unit || safety != r.Policy.SafetyStock || maxAge != int64(r.Policy.MaxObservationAge) || lifetime != int64(r.Policy.DecisionLifetime) {
		return domain.StockDecision{}, errors.New("policy version already exists with different settings")
	}
	_, err = tx.Exec(ctx, `INSERT INTO stock_pools(tenant_id,account_id,warehouse_id,item_kind,item_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT(source,account_id,warehouse_id,item_kind,item_id) DO NOTHING`, r.Pool.Tenant, r.Pool.MoyskladAccount, r.Pool.WarehouseID, r.Pool.ItemKind, r.Pool.ItemID)
	if err != nil {
		return domain.StockDecision{}, fmt.Errorf("create stock pool: %w", err)
	}
	var poolID int64
	err = tx.QueryRow(ctx, `SELECT id FROM stock_pools WHERE tenant_id=$1 AND account_id=$2 AND warehouse_id=$3 AND item_kind=$4 AND item_id=$5 FOR SHARE`, r.Pool.Tenant, r.Pool.MoyskladAccount, r.Pool.WarehouseID, r.Pool.ItemKind, r.Pool.ItemID).Scan(&poolID)
	if err != nil {
		return domain.StockDecision{}, fmt.Errorf("read scoped stock pool: %w", err)
	}
	if err = loadStockMappings(ctx, tx, r, now, &e); err != nil {
		return domain.StockDecision{}, err
	}
	if err = loadStockObservations(ctx, tx, r, &e); err != nil {
		return domain.StockDecision{}, err
	}
	d := commitment.Evaluate(r, e, now)
	if d.Status == domain.StockReady {
		return domain.StockDecision{}, errors.New("current imports cannot establish the required readiness proofs")
	}
	if err = tx.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence('stock_decisions','id'))`).Scan(&d.ID); err != nil {
		return domain.StockDecision{}, fmt.Errorf("allocate decision ID: %w", err)
	}
	if err = persistStockDecision(ctx, tx, poolID, d); err != nil {
		return domain.StockDecision{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.StockDecision{}, fmt.Errorf("commit stock decision: %w", err)
	}
	return d, nil
}

func loadStockMappings(ctx context.Context, tx pgx.Tx, r domain.StockCalculationRequest, now time.Time, e *domain.StockEvidence) error {
	var pm domain.MappingReference
	var targetAccount, targetKind, targetID string
	err := tx.QueryRow(ctx, `SELECT id,version,ozon_offer_id,provenance,confirmed_at,moysklad_account,target_kind,target_id FROM product_mappings WHERE tenant_id=$1 AND ozon_account=$2 AND ozon_product_id=$3 FOR SHARE`, r.Pool.Tenant, r.Target.OzonAccount, r.Target.ProductID).Scan(&pm.ID, &pm.Version, &pm.OfferID, &pm.Provenance, &pm.ConfirmedAt, &targetAccount, &targetKind, &targetID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read product mapping: %w", err)
	}
	if err == nil {
		if targetAccount != r.Pool.MoyskladAccount || targetKind != r.Pool.ItemKind || targetID != r.Pool.ItemID {
			e.ProductConflict = true
		} else {
			var provenance string
			var confirmed time.Time
			err = tx.QueryRow(ctx, `SELECT provenance,confirmed_at FROM product_mapping_history WHERE mapping_id=$1 AND version=$2 AND tenant_id=$3 AND ozon_account=$4 AND moysklad_account=$5 AND target_kind=$6 AND target_id=$7 AND ozon_product_id=$8 AND ozon_offer_id=$9 FOR SHARE`, pm.ID, pm.Version, r.Pool.Tenant, r.Target.OzonAccount, r.Pool.MoyskladAccount, r.Pool.ItemKind, r.Pool.ItemID, r.Target.ProductID, pm.OfferID).Scan(&provenance, &confirmed)
			if errors.Is(err, pgx.ErrNoRows) {
				e.ProductConflict = true
			} else if err != nil {
				return fmt.Errorf("read product mapping history: %w", err)
			} else if provenance != pm.Provenance || !confirmed.Equal(pm.ConfirmedAt) {
				e.ProductConflict = true
			} else {
				pm.Tenant, pm.OzonAccount, pm.MoyskladAccount = r.Pool.Tenant, r.Target.OzonAccount, r.Pool.MoyskladAccount
				pm.OzonProductID, pm.ItemKind, pm.ItemID = r.Target.ProductID, r.Pool.ItemKind, r.Pool.ItemID
				e.ProductMapping = &pm
				e.Proofs[commitment.ProofProductMapping] = domain.StockProof{State: domain.ProofVerified, Reference: fmt.Sprintf("product_mapping:%d:%d", pm.ID, pm.Version), AsOf: pm.ConfirmedAt, ValidUntil: now.Add(r.Policy.DecisionLifetime)}
			}
		}
	}
	var wm domain.MappingReference
	var warehouseAccount, warehouseID, status string
	err = tx.QueryRow(ctx, `SELECT id,version,provenance,confirmed_at,moysklad_account,moysklad_warehouse_id,status FROM warehouse_mappings WHERE tenant_id=$1 AND ozon_account=$2 AND ozon_warehouse_id=$3 FOR SHARE`, r.Pool.Tenant, r.Target.OzonAccount, r.Target.WarehouseID).Scan(&wm.ID, &wm.Version, &wm.Provenance, &wm.ConfirmedAt, &warehouseAccount, &warehouseID, &status)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read warehouse mapping: %w", err)
	}
	if err == nil {
		if warehouseAccount != r.Pool.MoyskladAccount || warehouseID != r.Pool.WarehouseID || status != "active" {
			e.WarehouseConflict = true
		} else {
			var provenance, historyStatus string
			var confirmed time.Time
			err = tx.QueryRow(ctx, `SELECT provenance,confirmed_at,status FROM warehouse_mapping_history WHERE mapping_id=$1 AND version=$2 AND tenant_id=$3 AND ozon_account=$4 AND moysklad_account=$5 AND moysklad_warehouse_id=$6 AND ozon_warehouse_id=$7 FOR SHARE`, wm.ID, wm.Version, r.Pool.Tenant, r.Target.OzonAccount, r.Pool.MoyskladAccount, r.Pool.WarehouseID, r.Target.WarehouseID).Scan(&provenance, &confirmed, &historyStatus)
			if errors.Is(err, pgx.ErrNoRows) {
				e.WarehouseConflict = true
			} else if err != nil {
				return fmt.Errorf("read warehouse mapping history: %w", err)
			} else if provenance != wm.Provenance || !confirmed.Equal(wm.ConfirmedAt) || historyStatus != "active" {
				e.WarehouseConflict = true
			} else {
				wm.Tenant, wm.OzonAccount, wm.MoyskladAccount = r.Pool.Tenant, r.Target.OzonAccount, r.Pool.MoyskladAccount
				wm.MoyskladWarehouseID, wm.OzonWarehouseID = r.Pool.WarehouseID, r.Target.WarehouseID
				e.WarehouseMapping = &wm
				e.Proofs[commitment.ProofWarehouseMapping] = domain.StockProof{State: domain.ProofVerified, Reference: fmt.Sprintf("warehouse_mapping:%d:%d", wm.ID, wm.Version), AsOf: wm.ConfirmedAt, ValidUntil: now.Add(r.Policy.DecisionLifetime)}
			}
		}
	}
	// Legacy mappings can predate ownership registration. Detect conflicts
	// across all scopes without exposing the other tenant's identities.
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_mappings WHERE moysklad_account=$1 AND target_kind=$2 AND target_id=$3 AND id<>$4)`, r.Pool.MoyskladAccount, r.Pool.ItemKind, r.Pool.ItemID, pm.ID).Scan(&e.SharedPool)
	if err != nil {
		return fmt.Errorf("check shared stock pool: %w", err)
	}
	var sharedWarehouse bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM warehouse_mappings WHERE ((moysklad_account=$1 AND moysklad_warehouse_id=$2) OR (ozon_account=$3 AND ozon_warehouse_id=$4)) AND id<>$5)`, r.Pool.MoyskladAccount, r.Pool.WarehouseID, r.Target.OzonAccount, r.Target.WarehouseID, wm.ID).Scan(&sharedWarehouse)
	if err != nil {
		return fmt.Errorf("check warehouse exclusivity: %w", err)
	}
	e.WarehouseConflict = e.WarehouseConflict || sharedWarehouse
	return nil
}

func loadStockObservations(ctx context.Context, tx pgx.Tx, r domain.StockCalculationRequest, e *domain.StockEvidence) error {
	e.Source.Tenant, e.Source.Account, e.Source.WarehouseID, e.Source.ItemKind, e.Source.ItemID = r.Pool.Tenant, r.Pool.MoyskladAccount, r.Pool.WarehouseID, r.Pool.ItemKind, r.Pool.ItemID
	e.Marketplace.Tenant, e.Marketplace.Account, e.Marketplace.WarehouseID, e.Marketplace.ProductID = r.Pool.Tenant, r.Target.OzonAccount, r.Target.WarehouseID, r.Target.ProductID
	var sourceID int64
	var scope string
	var started, finished time.Time
	err := tx.QueryRow(ctx, `SELECT id,report_scope,started_at,finished_at FROM source_imports WHERE source='moysklad' AND account_id=$1 AND ($2 OR started_at >= $3) ORDER BY id DESC LIMIT 1`, r.Pool.MoyskladAccount, e.Connections[0].IncludeImportHistory, e.Connections[0].RegisteredAt).Scan(&sourceID, &scope, &started, &finished)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read source import: %w", err)
	}
	if err == nil {
		e.Source.ImportID = &sourceID
		e.Source.TraversalComplete = !finished.Before(started) && strings.Contains(scope, "stock/bystore;stockMode=all;groupBy=variant")
		var stock, reserve string
		var observed time.Time
		err = tx.QueryRow(ctx, `SELECT stock::text,reserve::text,observed_at FROM source_warehouse_stock_observations WHERE import_id=$1 AND kind=$2 AND external_id=$3 AND warehouse_id=$4 FOR SHARE`, sourceID, r.Pool.ItemKind, r.Pool.ItemID, r.Pool.WarehouseID).Scan(&stock, &reserve, &observed)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read warehouse stock observation: %w", err)
		}
		if err == nil {
			e.Source.ObservationFound = true
			e.Source.Physical, e.Source.Reserve, e.Source.ObservedAt = &stock, &reserve, &observed
			e.Source.TimeBasis = "local_batch_finished"
		}
	}
	var ozonID int64
	err = tx.QueryRow(ctx, `SELECT i.id FROM source_imports i JOIN ozon_import_batches b ON b.import_id=i.id AND b.traversal_status='completed' WHERE i.source='ozon' AND i.account_id=$1 AND ($2 OR i.started_at >= $3) ORDER BY i.id DESC LIMIT 1`, r.Target.OzonAccount, e.Connections[1].IncludeImportHistory, e.Connections[1].RegisteredAt).Scan(&ozonID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read Ozon import: %w", err)
	}
	if err == nil {
		e.Marketplace.ImportID = &ozonID
		var present, reserved, free int64
		var observed time.Time
		err = tx.QueryRow(ctx, `SELECT present,reserved,free_stock,observed_at FROM ozon_fbs_stock_observations WHERE import_id=$1 AND product_id=$2 AND warehouse_id=$3 FOR SHARE`, ozonID, r.Target.ProductID, r.Target.WarehouseID).Scan(&present, &reserved, &free, &observed)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read Ozon stock observation: %w", err)
		}
		if err == nil {
			e.Marketplace.StockRowFound = true
			e.Marketplace.Present, e.Marketplace.Reserved, e.Marketplace.FreeStock, e.Marketplace.ObservedAt = &present, &reserved, &free, &observed
		}
	}
	var batch int64
	var from, to time.Time
	err = tx.QueryRow(ctx, `SELECT id,window_from,window_to FROM ozon_posting_batches WHERE tenant_id=$1 AND account_id=$2 AND traversal_status='completed' ORDER BY id DESC LIMIT 1`, r.Pool.Tenant, r.Target.OzonAccount).Scan(&batch, &from, &to)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read posting batch: %w", err)
	}
	if err == nil {
		e.Marketplace.PostingBatchID, e.Marketplace.PostingWindowFrom, e.Marketplace.PostingWindowTo = &batch, &from, &to
	}
	return nil
}

func persistStockDecision(ctx context.Context, tx pgx.Tx, poolID int64, d domain.StockDecision) error {
	body, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("encode stock decision: %w", err)
	}
	snapshot, err := json.Marshal(d.Evidence)
	if err != nil {
		return fmt.Errorf("encode stock evidence: %w", err)
	}
	r := d.Request
	var pmID, pmVersion, offer, wmID, wmVersion any
	if m := d.Evidence.ProductMapping; m != nil {
		pmID, pmVersion, offer = m.ID, m.Version, m.OfferID
	}
	if m := d.Evidence.WarehouseMapping; m != nil {
		wmID, wmVersion = m.ID, m.Version
	}
	_, err = tx.Exec(ctx, `INSERT INTO stock_decisions(id,tenant_id,pool_id,moysklad_account,moysklad_warehouse_id,item_kind,item_id,ozon_account,ozon_warehouse_id,ozon_product_id,offer_id,product_mapping_id,product_mapping_version,warehouse_mapping_id,warehouse_mapping_version,policy_version,evaluated_at,expires_at,status,quantity,decision) OVERRIDING SYSTEM VALUE VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21::jsonb)`, d.ID, r.Pool.Tenant, poolID, r.Pool.MoyskladAccount, r.Pool.WarehouseID, r.Pool.ItemKind, r.Pool.ItemID, r.Target.OzonAccount, r.Target.WarehouseID, r.Target.ProductID, offer, pmID, pmVersion, wmID, wmVersion, r.Policy.Version, d.EvaluatedAt, d.ExpiresAt, d.Status, d.Quantity, body)
	if err != nil {
		return fmt.Errorf("insert stock decision: %w", err)
	}
	var sourceStockID, ozonStockID *int64
	if d.Evidence.Source.ObservationFound {
		sourceStockID = d.Evidence.Source.ImportID
	}
	if d.Evidence.Marketplace.StockRowFound {
		ozonStockID = d.Evidence.Marketplace.ImportID
	}
	_, err = tx.Exec(ctx, `INSERT INTO stock_decision_evidence(decision_id,tenant_id,pool_id,moysklad_account,moysklad_warehouse_id,item_kind,item_id,ozon_account,ozon_warehouse_id,ozon_product_id,moysklad_import_id,moysklad_stock_import_id,ozon_import_id,ozon_stock_import_id,posting_batch_id,snapshot) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16::jsonb)`, d.ID, r.Pool.Tenant, poolID, r.Pool.MoyskladAccount, r.Pool.WarehouseID, r.Pool.ItemKind, r.Pool.ItemID, r.Target.OzonAccount, r.Target.WarehouseID, r.Target.ProductID, d.Evidence.Source.ImportID, sourceStockID, d.Evidence.Marketplace.ImportID, ozonStockID, d.Evidence.Marketplace.PostingBatchID, snapshot)
	if err != nil {
		return fmt.Errorf("insert stock decision evidence: %w", err)
	}
	return nil
}

// StockDecision is a scoped administrative read, not tenant authentication.
func (s *Store) StockDecision(ctx context.Context, tenant, ozonAccount string, id int64) (domain.StockDecision, error) {
	if strings.TrimSpace(tenant) == "" || strings.TrimSpace(ozonAccount) == "" || id <= 0 {
		return domain.StockDecision{}, errors.New("tenant, Ozon account and decision ID are required")
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	var raw []byte
	if err := s.Pool.QueryRow(ctx, `SELECT decision FROM stock_decisions WHERE id=$1 AND tenant_id=$2 AND ozon_account=$3`, id, tenant, ozonAccount).Scan(&raw); err != nil {
		return domain.StockDecision{}, fmt.Errorf("read scoped stock decision: %w", err)
	}
	var d domain.StockDecision
	if err := json.Unmarshal(raw, &d); err != nil {
		return d, fmt.Errorf("decode stock decision: %w", err)
	}
	return d, nil
}
