package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lilter96/styk/internal/commitment"
	"github.com/lilter96/styk/internal/config"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/testpublication"
)

var syncProofCodes = map[string]bool{
	commitment.ProofUnit: true, commitment.ProofBaseline: true, commitment.ProofSourceOrdering: true,
	commitment.ProofUnassigned: true, commitment.ProofAttribution: true, commitment.ProofPostingCoverage: true,
	commitment.ProofCompeting: true, commitment.ProofBound: true, commitment.ProofCustomerOrders: true,
}

func syncKey(s domain.StockSyncScope) string {
	return "stock-publication:" + s.Tenant + ":" + s.MoyskladAccount + ":" + s.OzonAccount
}
func syncLock(ctx context.Context, tx pgx.Tx, scope domain.StockSyncScope) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, syncKey(scope))
	return err
}
func stockSyncID() (string, error) {
	b := make([]byte, 16)
	if _, err := randRead(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// randRead is shared only to make secure ID allocation easy to audit.
var randRead = func(b []byte) (int, error) { return rand.Read(b) }

func syncHash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func syncChecks(d domain.StockDecision) []domain.StockSyncCheck {
	out := make([]domain.StockSyncCheck, 0, len(d.Checks))
	for _, c := range d.Checks {
		out = append(out, domain.StockSyncCheck{Code: c.Code, Passed: c.Passed, Message: c.Message})
	}
	return out
}
func positiveAPIID(v string) bool {
	n, e := strconv.ParseInt(v, 10, 64)
	return e == nil && n > 0 && strconv.FormatInt(n, 10) == v
}
func syncQuantity(v *string) bool {
	if v == nil {
		return false
	}
	n, e := strconv.ParseInt(*v, 10, 64)
	return e == nil && n >= 0 && n <= 2147483647
}
func validateAttestations(items []domain.StockSyncAttestation, now time.Time) error {
	if len(items) > 100 {
		return errors.New("at most 100 stock targets may be attested")
	}
	seen := map[string]bool{}
	for _, a := range items {
		key := a.ProductID + ":" + a.WarehouseID
		if a.ProductID == "" || a.WarehouseID == "" || seen[key] || len(a.Proofs) > 9 || a.AutomaticPolicyRevision != 0 {
			return errors.New("invalid or duplicate target attestation")
		}
		seen[key] = true
		codes := map[string]bool{}
		for _, p := range a.Proofs {
			if !syncProofCodes[p.Code] || codes[p.Code] || len(strings.TrimSpace(p.Reference)) < 16 || len(p.Reference) > 1000 || p.AsOf.IsZero() || p.AsOf.After(now) || !p.ValidUntil.After(now) || p.ValidUntil.After(now.Add(15*time.Minute)) || p.ValidUntil.Sub(p.AsOf) > 15*time.Minute {
				return errors.New("each proof requires scoped provenance and a validity of at most fifteen minutes")
			}
			codes[p.Code] = true
		}
	}
	return nil
}
func (s *Store) stockSyncReview(ctx context.Context, tx pgx.Tx, scope domain.StockSyncScope, attestations []domain.StockSyncAttestation, now time.Time) (domain.StockSyncReview, error) {
	review := domain.StockSyncReview{StockSyncScope: scope, Ready: true, CreatedAt: now, ExpiresAt: now.Add(2 * time.Minute), Checks: []domain.StockSyncCheck{}, Targets: []domain.StockSyncTarget{}, Attestations: attestations}
	// Use the established import/mapping lock order. Every target in a frozen
	// review sees one consistent latest batch and matching version; no external
	// request is made while these transaction locks are held.
	for _, key := range []string{"ozon:" + scope.OzonAccount, "moysklad:" + scope.MoyskladAccount, "matching:" + scope.Tenant, "warehouse-mapping:" + scope.Tenant} {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
			return review, err
		}
	}

	connections := []domain.StockConnectionReference{{Source: "moysklad", Account: scope.MoyskladAccount}, {Source: "ozon", Account: scope.OzonAccount}}
	for i := range connections {
		c := &connections[i]
		if err := tx.QueryRow(ctx, `SELECT provenance,registered_at,include_import_history FROM stock_connections WHERE tenant_id=$1 AND source=$2 AND account_id=$3 FOR SHARE`, scope.Tenant, c.Source, c.Account).Scan(&c.Provenance, &c.RegisteredAt, &c.IncludeImportHistory); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return review, domain.ErrStockSyncNotFound
			}
			return review, err
		}
	}
	// A disabled stored connection is authoritative, even if environment fallback
	// still holds old credentials. Credential revisions are frozen with review.
	for _, p := range []struct {
		provider, account string
		revision          *int64
	}{{"moysklad", scope.MoyskladAccount, &review.MoyskladCredentialRevision}, {"ozon", scope.OzonAccount, &review.OzonCredentialRevision}} {
		var enabled bool
		err := tx.QueryRow(ctx, `SELECT revision,enabled FROM connection_credentials WHERE tenant_id=$1 AND provider=$2 AND account_id=$3 FOR SHARE`, scope.Tenant, p.provider, p.account).Scan(p.revision, &enabled)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return review, err
		}
		if err == nil && !enabled {
			review.Ready = false
			review.Checks = append(review.Checks, domain.StockSyncCheck{Code: "CONNECTION_DISABLED", Message: "Подключение отключено."})
		}
	}
	// An older traversal can finish after a newer one (e.g. CLI and worker).
	// Its larger ID and recent finish must not make old reads publishable.
	orderingValid := true
	for _, source := range []struct {
		provider, account string
		registered        time.Time
		includeHistory    bool
	}{
		{"moysklad", scope.MoyskladAccount, connections[0].RegisteredAt, connections[0].IncludeImportHistory},
		{"ozon", scope.OzonAccount, connections[1].RegisteredAt, connections[1].IncludeImportHistory},
	} {
		var outOfOrder bool
		err := tx.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM source_imports latest JOIN source_imports prior ON prior.source=latest.source AND prior.account_id=latest.account_id
			WHERE latest.id=(SELECT id FROM source_imports WHERE source=$1 AND account_id=$2 AND ($3 OR started_at >= $4) ORDER BY id DESC LIMIT 1)
			AND prior.id<latest.id AND prior.started_at>=latest.started_at AND ($3 OR prior.started_at >= $4))`, source.provider, source.account, source.includeHistory, source.registered).Scan(&outOfOrder)
		if err != nil {
			return review, err
		}
		if outOfOrder {
			orderingValid = false
			review.Ready = false
			review.Checks = append(review.Checks, domain.StockSyncCheck{Code: "OUT_OF_ORDER_IMPORT", Message: "Обнаружены пересекающиеся загрузки в неверном порядке. Повторите загрузку перед отправкой."})
		}
	}
	var automaticProducts, automaticWarehouses []string
	for _, a := range attestations {
		if a.AutomaticPolicyRevision != 0 {
			automaticProducts = append(automaticProducts, a.ProductID)
			automaticWarehouses = append(automaticWarehouses, a.WarehouseID)
		}
	}
	rows, err := tx.Query(ctx, `SELECT pm.ozon_product_id,pm.target_kind,pm.target_id,wm.ozon_warehouse_id,wm.moysklad_warehouse_id FROM product_mappings pm CROSS JOIN warehouse_mappings wm WHERE pm.tenant_id=$1 AND pm.moysklad_account=$2 AND pm.ozon_account=$3 AND wm.tenant_id=pm.tenant_id AND wm.moysklad_account=pm.moysklad_account AND wm.ozon_account=pm.ozon_account AND wm.status='active' AND (NOT $4::boolean OR EXISTS(SELECT 1 FROM unnest($5::text[],$6::text[]) AS selected(product_id,warehouse_id) WHERE selected.product_id=pm.ozon_product_id AND selected.warehouse_id=wm.ozon_warehouse_id)) ORDER BY pm.ozon_product_id,wm.ozon_warehouse_id LIMIT 101`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, len(automaticProducts) > 0, automaticProducts, automaticWarehouses)
	if err != nil {
		return review, err
	}
	type target struct{ pid, kind, item, wid, sourceWarehouse string }
	var targets []target
	for rows.Next() {
		var t target
		if err = rows.Scan(&t.pid, &t.kind, &t.item, &t.wid, &t.sourceWarehouse); err != nil {
			rows.Close()
			return review, err
		}
		targets = append(targets, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return review, err
	}
	if testpublication.Enabled(scope) {
		// The real test mode never publishes the other confirmed mappings in
		// this workspace. Shared-pool checks still inspect all mappings below.
		selected := targets[:0]
		for _, t := range targets {
			if t.pid == testpublication.OzonProduct && t.wid == testpublication.OzonWarehouse && t.kind == "product" && t.item == testpublication.SourceProduct && t.sourceWarehouse == testpublication.SourceWarehouse {
				selected = append(selected, t)
			}
		}
		targets = selected
	}
	if len(targets) == 0 || len(targets) > 100 {
		review.Ready = false
		code, msg := "MAPPINGS_REQUIRED", "Подтвердите соответствия товаров и складов."
		if len(targets) > 100 {
			code, msg = "TARGET_LIMIT_REACHED", "За один запуск поддерживается не более 100 пар товар–склад."
		}
		review.Checks = append(review.Checks, domain.StockSyncCheck{Code: code, Message: msg})
		return review, nil
	}
	inputs := map[string]domain.StockSyncAttestation{}
	for _, a := range attestations {
		inputs[a.ProductID+":"+a.WarehouseID] = a
	}
	for _, t := range targets {
		a, attested := inputs[t.pid+":"+t.wid]
		_, simulator := config.StockPublicationTestEndpoint()
		r := domain.StockCalculationRequest{Pool: domain.StockPool{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, WarehouseID: t.sourceWarehouse, ItemKind: t.kind, ItemID: t.item}, Target: domain.StockTarget{OzonAccount: scope.OzonAccount, WarehouseID: t.wid, ProductID: t.pid}, Policy: domain.StockCalculationPolicy{Version: 1, Unit: "piece", SafetyStock: a.SafetyStock, MaxObservationAge: 15 * time.Minute, DecisionLifetime: 2 * time.Minute, AcceptOperatorAttested: testpublication.Enabled(scope) || simulator}}
		var automaticTarget *domain.AutomaticStockTarget
		automaticValid := a.AutomaticPolicyRevision == 0
		if a.AutomaticPolicyRevision != 0 {
			automaticTarget, err = authorizedAutomaticAttestation(ctx, tx, scope, a, now)
			if err != nil {
				return review, err
			}
			automaticValid = automaticTarget != nil
			if automaticValid {
				r.Policy.TestStandingAttestationMaxAge = 24 * time.Hour
			}
		}
		e := domain.StockEvidence{Connections: connections, Proofs: map[string]domain.StockProof{}}
		registered := connections[0].RegisteredAt
		if connections[1].RegisteredAt.After(registered) {
			registered = connections[1].RegisteredAt
		}
		e.Proofs[commitment.ProofOwnership] = domain.StockProof{State: domain.ProofVerified, Reference: "trusted administrator account registrations", AsOf: registered, ValidUntil: now.Add(2 * time.Minute)}
		if err = loadStockMappings(ctx, tx, r, now, &e); err != nil {
			return review, err
		}
		if err = loadStockObservations(ctx, tx, r, &e); err != nil {
			return review, err
		}
		if e.Source.ImportID != nil {
			// A mapped product and one of its mapped variants can expose the
			// same physical stock twice even though their IDs/kinds differ.
			var productVariantOverlap bool
			err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM source_entities v JOIN product_mappings other ON other.moysklad_account=$2 AND ((other.target_kind='variant' AND other.target_id=v.external_id) OR (other.target_kind='product' AND other.target_id=v.parent_id)) WHERE v.import_id=$1 AND v.kind='variant' AND v.parent_id<>'' AND (($3='product' AND v.parent_id=$4 AND other.target_kind='variant') OR ($3='variant' AND v.external_id=$4 AND other.target_kind='product')))`, *e.Source.ImportID, scope.MoyskladAccount, t.kind, t.item).Scan(&productVariantOverlap)
			if err != nil {
				return review, err
			}
			e.SharedPool = e.SharedPool || productVariantOverlap
		}
		if err = loadStockCustomerOrders(ctx, tx, r, &e); err != nil {
			return review, err
		}
		if attested {
			e.Unreflected = &a.Unreflected
			e.UnseenBound = &a.UnseenBound
			for _, p := range a.Proofs {
				e.Proofs[p.Code] = domain.StockProof{State: domain.ProofAttested, EvidenceClass: "OPERATOR_ATTESTED", Reference: "operator-attested: " + p.Reference, AsOf: p.AsOf, ValidUntil: p.ValidUntil}
			}
		}
		d := commitment.Evaluate(r, e, now)
		checks := syncChecks(d)
		if a.AutomaticPolicyRevision != 0 {
			if automaticTarget != nil {
				automaticValid = e.ProductMapping != nil && e.WarehouseMapping != nil && e.ProductMapping.ID == automaticTarget.ProductMappingID && e.ProductMapping.Version == automaticTarget.ProductMappingVersion && e.WarehouseMapping.ID == automaticTarget.WarehouseMappingID && e.WarehouseMapping.Version == automaticTarget.WarehouseMappingVersion && t.item == automaticTarget.SourceItemID && t.sourceWarehouse == automaticTarget.SourceWarehouseID
			}
			checks = append(checks, domain.StockSyncCheck{Code: "AUTOMATIC_POLICY_CHANGED", Passed: automaticValid, Message: "Выбранные соответствия и тестовые допущения автоматической синхронизации действуют."})
		}
		// A historical mapping does not prove the latest catalog still refers
		// to the same offer, SKU, source entity, or FBS warehouse.
		var sourceIdentity, productIdentity, warehouseIdentity bool
		if e.Source.ImportID != nil {
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM source_entities WHERE import_id=$1 AND kind=$2 AND external_id=$3) AND EXISTS(SELECT 1 FROM source_entities WHERE import_id=$1 AND kind='store' AND external_id=$4)`, *e.Source.ImportID, t.kind, t.item, t.sourceWarehouse).Scan(&sourceIdentity)
			if err != nil {
				return review, err
			}
		}
		if e.Marketplace.ImportID != nil && e.ProductMapping != nil {
			err = tx.QueryRow(ctx, `SELECT EXISTS(
				SELECT 1 FROM ozon_product_observations p
				JOIN ozon_fbs_stock_observations s ON s.import_id=p.import_id AND s.product_id=p.product_id AND s.offer_id=p.offer_id AND s.sku=p.sku
				JOIN product_mapping_history h ON h.mapping_id=$5 AND h.version=$6
				JOIN ozon_product_observations original ON original.import_id=h.ozon_import_id AND original.product_id=h.ozon_product_id AND original.offer_id=h.ozon_offer_id AND original.sku=p.sku
				WHERE p.import_id=$1 AND p.product_id=$2 AND p.offer_id=$3 AND p.archived=false AND p.sku IS NOT NULL AND s.warehouse_id=$4
				AND (NOT $7::boolean OR (p.offer_id=$8 AND p.sku=$9))
			), EXISTS(SELECT 1 FROM ozon_warehouse_observations WHERE import_id=$1 AND warehouse_id=$4 AND is_rfbs=false AND (NOT $7::boolean OR status='created'))`, *e.Marketplace.ImportID, t.pid, e.ProductMapping.OfferID, t.wid, e.ProductMapping.ID, e.ProductMapping.Version, testpublication.Enabled(scope), testpublication.OzonOffer, testpublication.OzonSKU).Scan(&productIdentity, &warehouseIdentity)
			if err != nil {
				return review, err
			}
		}
		checks = append(checks,
			domain.StockSyncCheck{Code: "SOURCE_IDENTITY_UNCONFIRMED", Passed: sourceIdentity, Message: "Товар и склад присутствуют в актуальной загрузке МойСклад."},
			domain.StockSyncCheck{Code: "CATALOG_IDENTITY_CHANGED", Passed: productIdentity, Message: "Товар, предложение и SKU совпадают с подтверждённым соответствием."},
			domain.StockSyncCheck{Code: "WAREHOUSE_SCHEMA_UNCONFIRMED", Passed: warehouseIdentity, Message: "Актуальная загрузка подтверждает склад FBS."},
		)
		// Batch finish timestamps are not per-row observation times. An
		// operator assertion after a traversal started cannot prove what was
		// already read during that traversal, especially after an unknown write.
		var sourceStarted, marketStarted time.Time
		if e.Source.ImportID != nil {
			if err = tx.QueryRow(ctx, `SELECT started_at FROM source_imports WHERE id=$1`, *e.Source.ImportID).Scan(&sourceStarted); err != nil {
				return review, err
			}
		}
		if e.Marketplace.ImportID != nil {
			if err = tx.QueryRow(ctx, `SELECT started_at FROM source_imports WHERE id=$1`, *e.Marketplace.ImportID).Scan(&marketStarted); err != nil {
				return review, err
			}
		}
		if t := e.Source.FreshTraversal; t != nil && !t.FinishedAt.After(now) {
			sourceStarted = t.StartedAt
		}
		if t := e.Marketplace.FreshTraversal; t != nil && !t.FinishedAt.After(now) {
			marketStarted = t.StartedAt
		}
		recoveryFresh := true
		if testpublication.Enabled(scope) {
			var recoveredAt time.Time
			recoveryErr := tx.QueryRow(ctx, `SELECT created_at FROM stock_publication_recoveries WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND product_id=$4 AND warehouse_id=$5 ORDER BY created_at DESC LIMIT 1`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, t.pid, t.wid).Scan(&recoveredAt)
			if recoveryErr != nil && !errors.Is(recoveryErr, pgx.ErrNoRows) {
				return review, recoveryErr
			}
			if recoveryErr == nil {
				recoveryFresh = sourceStarted.After(recoveredAt) && marketStarted.After(recoveredAt)
				for _, proof := range a.Proofs {
					if proof.AsOf.Before(recoveredAt) {
						recoveryFresh = false
					}
				}
				checks = append(checks, domain.StockSyncCheck{Code: "TEST_RECOVERY_FRESH_TRAVERSALS", Passed: recoveryFresh, Message: "После принятия неопределённости условия и оба источника проверены заново."})
			}
		}
		sourceTraversalFresh := !sourceStarted.IsZero() && !sourceStarted.After(now) && now.Sub(sourceStarted) < 15*time.Minute
		marketTraversalFresh := !marketStarted.IsZero() && !marketStarted.After(now) && now.Sub(marketStarted) < 15*time.Minute
		beforeTraversal := !sourceStarted.IsZero()
		for _, proof := range a.Proofs {
			if proof.Code != commitment.ProofUnit && proof.AsOf.After(sourceStarted) {
				beforeTraversal = false
			}
		}
		checks = append(checks,
			domain.StockSyncCheck{Code: "SOURCE_TRAVERSAL_STALE", Passed: sourceTraversalFresh, Message: "Загрузка МойСклад началась не более пятнадцати минут назад."},
			domain.StockSyncCheck{Code: "OZON_TRAVERSAL_STALE", Passed: marketTraversalFresh, Message: "Загрузка Ozon началась не более пятнадцати минут назад."},
			domain.StockSyncCheck{Code: "PROOF_AFTER_SOURCE_TRAVERSAL_STARTED", Passed: beforeTraversal, Message: "Условия были подтверждены до начала загрузки МойСклад."},
		)
		validIDs := positiveAPIID(t.pid) && positiveAPIID(t.wid)
		checks = append(checks, domain.StockSyncCheck{Code: "OZON_TARGET_IDENTITIES", Passed: validIDs, Message: "Товар и склад имеют допустимые идентификаторы Ozon."})
		stockComplete := e.Marketplace.ImportID != nil && e.Marketplace.StockRowFound
		checks = append(checks, domain.StockSyncCheck{Code: "OZON_STOCK_BASELINE_REQUIRED", Passed: stockComplete, Message: "В полной загрузке Ozon есть остаток выбранного товара на складе."})
		validStockValues := e.Marketplace.Present != nil && e.Marketplace.Reserved != nil && e.Marketplace.FreeStock != nil && *e.Marketplace.Present >= 0 && *e.Marketplace.Reserved >= 0 && *e.Marketplace.FreeStock >= 0
		checks = append(checks, domain.StockSyncCheck{Code: "OZON_STOCK_VALUES_INVALID", Passed: validStockValues, Message: "Числа остатков и резервов Ozon присутствуют и неотрицательны."})
		marketFreshness := e.Marketplace.ObservedAt
		if t := e.Marketplace.FreshTraversal; t != nil && !t.FinishedAt.After(now) {
			marketFreshness = &t.StartedAt
		}
		if marketFreshness != nil {
			stockComplete = stockComplete && !marketFreshness.After(now) && now.Sub(*marketFreshness) < 15*time.Minute
		} else {
			stockComplete = false
		}
		checks = append(checks, domain.StockSyncCheck{Code: "OZON_BASELINE_FRESH", Passed: stockComplete, Message: "Данные Ozon получены не более пятнадцати минут назад."})
		qtyValid := d.Status == domain.StockReady && syncQuantity(d.Quantity)
		checks = append(checks, domain.StockSyncCheck{Code: "PUBLICATION_QUANTITY_VALID", Passed: qtyValid, Message: "Доступный остаток рассчитан в целых штуках без округления."})
		var settlementID string
		var settlementAt *time.Time
		settlementErr := tx.QueryRow(ctx, `SELECT operation_id,finished_at FROM stock_publication_targets WHERE ozon_account=$1 AND product_id=$2 AND warehouse_id=$3 AND state='CONFIRMED_EFFECTIVE' ORDER BY finished_at DESC,id DESC LIMIT 1`, scope.OzonAccount, t.pid, t.wid).Scan(&settlementID, &settlementAt)
		if settlementErr != nil && !errors.Is(settlementErr, pgx.ErrNoRows) {
			return review, settlementErr
		}
		settled := true
		if settlementErr == nil {
			proof := e.Proofs[commitment.ProofCompeting]
			settled = settlementAt != nil && (proof.State == domain.ProofVerified || (r.Policy.AcceptOperatorAttested && proof.State == domain.ProofAttested && proof.EvidenceClass == "OPERATOR_ATTESTED")) && proof.AsOf.After(*settlementAt) && strings.Contains(proof.Reference, settlementID)
			checks = append(checks, domain.StockSyncCheck{Code: "UNCERTAIN_WRITER_SETTLEMENT_REQUIRED", Passed: settled, Message: "После неизвестного результата подтверждено отсутствие незавершённых отправок; проверка ссылается на исходную операцию."})
		}
		testAllowed := true
		if testpublication.Enabled(scope) {
			approved := false
			if e.ProductMapping != nil && e.WarehouseMapping != nil {
				err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_mapping_confirmation_audits WHERE mapping_id=$1 AND mapping_version=$2 AND tenant_id=$5 AND ozon_account=$6 AND moysklad_account=$7) AND EXISTS(SELECT 1 FROM warehouse_mapping_confirmation_audits WHERE mapping_id=$3 AND mapping_version=$4 AND tenant_id=$5 AND ozon_account=$6 AND moysklad_account=$7)`, e.ProductMapping.ID, e.ProductMapping.Version, e.WarehouseMapping.ID, e.WarehouseMapping.Version, scope.Tenant, scope.OzonAccount, scope.MoyskladAccount).Scan(&approved)
				if err != nil {
					return review, err
				}
			}
			quantity := ""
			if d.Quantity != nil {
				quantity = *d.Quantity
			}
			testAllowed = approved && testpublication.TargetAllowed(scope, t.pid, t.wid, t.item, t.sourceWarehouse, quantity)
			checks = append(checks, domain.StockSyncCheck{Code: "TEST_TARGET_ALLOWED", Passed: testAllowed, Message: "Тестовая пара, подтверждённые соответствия и ограничение количества проверены."})
		}
		if automaticTarget != nil && d.Quantity != nil {
			quantity, parseErr := strconv.ParseInt(*d.Quantity, 10, 64)
			automaticValid = automaticValid && parseErr == nil && quantity <= automaticTarget.Maximum
			checks = append(checks, domain.StockSyncCheck{Code: "AUTOMATIC_MAXIMUM_EXCEEDED", Passed: parseErr == nil && quantity <= automaticTarget.Maximum, Message: "Расчёт не превышает настроенный предел тестовой публикации."})
		}
		ready := d.Status == domain.StockReady && validIDs && stockComplete && qtyValid && settled && sourceIdentity && productIdentity && warehouseIdentity && orderingValid && validStockValues && sourceTraversalFresh && marketTraversalFresh && beforeTraversal && testAllowed && recoveryFresh && automaticValid
		item := domain.StockSyncTarget{SettlementOperationID: settlementID, SettlementObservedAt: settlementAt, ProductID: t.pid, WarehouseID: t.wid, SourceItemID: t.item, SourceWarehouseID: t.sourceWarehouse, SourceImportID: e.Source.ImportID, SourcePhysical: e.Source.Physical, SourceReserve: e.Source.Reserve, MarketplaceFree: e.Marketplace.FreeStock, MarketplacePresent: e.Marketplace.Present, MarketplaceReserved: e.Marketplace.Reserved, Status: string(d.Status), Checks: checks, Decision: d}
		if e.ProductMapping != nil {
			item.ProductMappingVersion = e.ProductMapping.Version
		}
		if e.WarehouseMapping != nil {
			item.WarehouseMappingVersion = e.WarehouseMapping.Version
		}
		if ready {
			item.Quantity = d.Quantity
			if d.ExpiresAt != nil && d.ExpiresAt.Before(review.ExpiresAt) {
				review.ExpiresAt = *d.ExpiresAt
			}
			// The preview must expire no later than the oldest permitted
			// traversal, even when that traversal finished much later.
			for _, started := range []time.Time{sourceStarted, marketStarted} {
				if deadline := started.Add(15 * time.Minute); deadline.Before(review.ExpiresAt) {
					review.ExpiresAt = deadline
				}
			}
		} else {
			review.Ready = false
			item.Status = "BLOCKED"
		}
		review.Targets = append(review.Targets, item)
	}
	for _, a := range attestations {
		found := false
		for _, t := range targets {
			if a.ProductID == t.pid && a.WarehouseID == t.wid {
				found = true
			}
		}
		if !found {
			review.Ready = false
			review.Checks = append(review.Checks, domain.StockSyncCheck{Code: "ATTESTATION_TARGETS_CHANGED", Message: "Изменился набор сопоставленных товаров или складов."})
		}
	}
	review.Checks = append(review.Checks, domain.StockSyncCheck{Code: "ALL_TARGETS_READY", Passed: review.Ready, Message: "Для всех выбранных товаров подтверждены условия безопасной отправки."})
	return review, nil
}
func (s *Store) PreviewStockSync(ctx context.Context, req domain.StockSyncPreviewRequest, now time.Time) (domain.StockSyncReview, error) {
	if !req.StockSyncScope.Valid() {
		return domain.StockSyncReview{}, domain.ErrStockSyncNotFound
	}
	now = now.UTC().Truncate(time.Microsecond)
	if err := validateAttestations(req.Attestations, now); err != nil {
		return domain.StockSyncReview{}, domain.ErrStockSyncInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return domain.StockSyncReview{}, err
	}
	defer tx.Rollback(context.Background())
	review, err := s.stockSyncReview(ctx, tx, req.StockSyncScope, req.Attestations, now)
	if err != nil {
		return review, err
	}
	review.ID, err = stockSyncID()
	if err != nil {
		return review, err
	}
	raw, _ := json.Marshal(review)
	if _, err = tx.Exec(ctx, `INSERT INTO stock_sync_reviews(id,tenant_id,moysklad_account,ozon_account,ready,snapshot,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6::jsonb,$7,$8)`, review.ID, req.Tenant, req.MoyskladAccount, req.OzonAccount, review.Ready, raw, now, review.ExpiresAt); err != nil {
		return review, err
	}
	if err = tx.Commit(ctx); err != nil {
		return review, err
	}
	return review, nil
}
func readSyncReview(ctx context.Context, tx pgx.Tx, scope domain.StockSyncScope, id string) (domain.StockSyncReview, error) {
	var r domain.StockSyncReview
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT snapshot FROM stock_sync_reviews WHERE id=$1 AND tenant_id=$2 AND moysklad_account=$3 AND ozon_account=$4`, id, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, domain.ErrStockSyncNotFound
	}
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(raw, &r)
	return r, err
}
func stableSyncEvidence(r domain.StockSyncReview) string {
	type evidence struct {
		P, W   string
		D      domain.StockEvidence
		Qty    *string
		PM, WM int
	}
	out := []evidence{}
	for _, t := range r.Targets {
		e := t.Decision.Evidence
		copiedProofs := make(map[string]domain.StockProof, len(e.Proofs))
		for code, proof := range e.Proofs {
			copiedProofs[code] = proof
		}
		e.Proofs = copiedProofs
		for _, code := range []string{commitment.ProofOwnership, commitment.ProofProductMapping, commitment.ProofWarehouseMapping} {
			p := e.Proofs[code]
			p.ValidUntil = time.Time{}
			e.Proofs[code] = p
		}
		out = append(out, evidence{t.ProductID, t.WarehouseID, e, t.Quantity, t.ProductMappingVersion, t.WarehouseMappingVersion})
	}
	return syncHash(struct {
		Targets []evidence
		M, O    int64
	}{out, r.MoyskladCredentialRevision, r.OzonCredentialRevision})
}
func createPublication(ctx context.Context, tx pgx.Tx, review domain.StockSyncReview, now time.Time) (domain.StockPublication, error) {
	id, err := stockSyncID()
	if err != nil {
		return domain.StockPublication{}, err
	}
	op := domain.StockPublication{ID: id, StockSyncScope: review.StockSyncScope, ReviewID: review.ID, State: "QUEUED", CreatedAt: now, Targets: []domain.StockPublicationTarget{}}
	if _, err = tx.Exec(ctx, `INSERT INTO stock_publications(id,tenant_id,moysklad_account,ozon_account,review_id,created_at) VALUES($1,$2,$3,$4,$5,$6)`, id, review.Tenant, review.MoyskladAccount, review.OzonAccount, review.ID, now); err != nil {
		return op, err
	}
	for _, t := range review.Targets {
		if t.Quantity == nil {
			return op, domain.ErrStockSyncBlocked
		}
		raw, _ := json.Marshal(t.Decision)
		v := domain.StockPublicationTarget{OperationID: id, ProductID: t.ProductID, WarehouseID: t.WarehouseID, Quantity: *t.Quantity, State: "QUEUED", Decision: t.Decision}
		err = tx.QueryRow(ctx, `INSERT INTO stock_publication_targets(operation_id,ozon_account,product_id,warehouse_id,quantity,state,decision) VALUES($1,$2,$3,$4,$5,'QUEUED',$6::jsonb) RETURNING id`, id, review.OzonAccount, t.ProductID, t.WarehouseID, *t.Quantity, raw).Scan(&v.ID)
		if err != nil {
			var conflict *pgconn.PgError
			if errors.As(err, &conflict) && conflict.Code == "23505" && (conflict.ConstraintName == "stock_publication_target_fence" || conflict.ConstraintName == "stock_publication_target_fences_pkey") {
				return op, domain.ErrStockSyncBlocked
			}
			return op, err
		}
		op.Targets = append(op.Targets, v)
	}
	if testpublication.Enabled(review.StockSyncScope) {
		if len(op.Targets) != 1 || !testpublication.TargetAllowed(review.StockSyncScope, op.Targets[0].ProductID, op.Targets[0].WarehouseID, review.Targets[0].SourceItemID, review.Targets[0].SourceWarehouseID, op.Targets[0].Quantity) {
			return op, domain.ErrStockSyncBlocked
		}
		if _, err = tx.Exec(ctx, `INSERT INTO outbox_tasks(operation_key,observation_id,max_attempts,task_type,publication_operation_id) VALUES($1,NULL,1,'stock_publication',$2)`, "stock-publication:"+id, id); err != nil {
			return op, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO stock_publication_recovery_links(recovery_id,new_operation_id,created_at) SELECT r.id,$1,$2 FROM stock_publication_recoveries r LEFT JOIN stock_publication_recovery_links l ON l.recovery_id=r.id WHERE r.tenant_id=$3 AND r.moysklad_account=$4 AND r.ozon_account=$5 AND r.product_id=$6 AND r.warehouse_id=$7 AND l.recovery_id IS NULL AND r.created_at<$8 ORDER BY r.created_at DESC LIMIT 1`, id, now, review.Tenant, review.MoyskladAccount, review.OzonAccount, op.Targets[0].ProductID, op.Targets[0].WarehouseID, review.CreatedAt)
		if err != nil {
			return op, err
		}
	}
	return op, nil
}

// MutateStockSync deduplicates enable/disable by the caller's immutable intent.
func (s *Store) MutateStockSync(ctx context.Context, scope domain.StockSyncScope, action, reviewID, key string, now time.Time) (domain.StockSyncView, error) {
	if !scope.Valid() || (action != "enable" && action != "disable") || len(key) < 16 || len(key) > 128 {
		return domain.StockSyncView{}, domain.ErrStockSyncNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return domain.StockSyncView{}, err
	}
	defer tx.Rollback(context.Background())
	if err = syncLock(ctx, tx, scope); err != nil {
		return domain.StockSyncView{}, err
	}
	var automaticEnabled bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_auto_configs WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND enabled)`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&automaticEnabled); err != nil {
		return domain.StockSyncView{}, err
	}
	if automaticEnabled {
		return domain.StockSyncView{}, domain.ErrStockSyncBlocked
	}
	hash := syncHash(struct {
		Scope          domain.StockSyncScope
		Action, Review string
	}{scope, action, reviewID})
	var old string
	err = tx.QueryRow(ctx, `SELECT request_hash FROM stock_sync_mutations WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND idempotency_key=$4`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, key).Scan(&old)
	if err == nil {
		if old != hash {
			return domain.StockSyncView{}, domain.ErrStockSyncConflict
		}
		if err = tx.Commit(ctx); err != nil {
			return domain.StockSyncView{}, err
		}
		return s.StockSyncStatus(ctx, scope, now)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.StockSyncView{}, err
	}
	var operation any
	if action == "enable" {
		var alreadyEnabled bool
		var previousReview *string
		err = tx.QueryRow(ctx, `SELECT enabled,review_id FROM stock_sync_settings WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&alreadyEnabled, &previousReview)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return domain.StockSyncView{}, err
		}
		if alreadyEnabled && previousReview != nil && *previousReview == reviewID {
			return domain.StockSyncView{}, domain.ErrStockSyncBlocked
		}
		var pending bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_publication_targets t JOIN stock_publication_target_fences f ON f.target_id=t.id JOIN stock_publications p ON p.id=t.operation_id WHERE p.tenant_id=$1 AND p.moysklad_account=$2 AND p.ozon_account=$3 AND t.state IN ('QUEUED','DISPATCHING','ACKNOWLEDGED','UNKNOWN'))`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&pending); err != nil {
			return domain.StockSyncView{}, err
		}
		if pending {
			return domain.StockSyncView{}, domain.ErrStockSyncBlocked
		}

		review, err := readSyncReview(ctx, tx, scope, reviewID)
		if err != nil {
			return domain.StockSyncView{}, err
		}
		if !review.Ready {
			return domain.StockSyncView{}, domain.ErrStockSyncBlocked
		}
		if !review.ExpiresAt.After(now) {
			return domain.StockSyncView{}, domain.ErrStockSyncStale
		}
		current, err := s.stockSyncReview(ctx, tx, scope, review.Attestations, now)
		if err != nil {
			return domain.StockSyncView{}, err
		}
		if !current.Ready || stableSyncEvidence(current) != stableSyncEvidence(review) {
			return domain.StockSyncView{}, domain.ErrStockSyncStale
		}
		_, err = tx.Exec(ctx, `INSERT INTO stock_sync_settings(tenant_id,moysklad_account,ozon_account,enabled,review_id,updated_at,last_evaluation_key) VALUES($1,$2,$3,true,$4,$5,$6) ON CONFLICT(tenant_id,moysklad_account,ozon_account) DO UPDATE SET enabled=true,review_id=EXCLUDED.review_id,updated_at=EXCLUDED.updated_at,last_evaluation_key=EXCLUDED.last_evaluation_key,last_checks='[]'::jsonb`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, review.ID, now, stableSyncEvidence(current))
		if err != nil {
			return domain.StockSyncView{}, err
		}
		op, err := createPublication(ctx, tx, review, now)
		if err != nil {
			return domain.StockSyncView{}, err
		}
		operation = op.ID
	} else {
		// Disabling does not undo an in-flight marketplace request. DISPATCHING and
		// UNKNOWN remain visible until their outcomes are known.
		var owned bool
		if err = tx.QueryRow(ctx, `SELECT count(*)=2 FROM stock_connections WHERE tenant_id=$1 AND ((source='moysklad' AND account_id=$2) OR (source='ozon' AND account_id=$3))`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&owned); err != nil {
			return domain.StockSyncView{}, err
		}
		if !owned {
			return domain.StockSyncView{}, domain.ErrStockSyncNotFound
		}
		_, err = tx.Exec(ctx, `INSERT INTO stock_sync_settings(tenant_id,moysklad_account,ozon_account,enabled,updated_at) VALUES($1,$2,$3,false,$4) ON CONFLICT(tenant_id,moysklad_account,ozon_account) DO UPDATE SET enabled=false,updated_at=EXCLUDED.updated_at`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, now)
		if err != nil {
			return domain.StockSyncView{}, err
		}
		_, err = tx.Exec(ctx, `UPDATE stock_publication_targets t SET state='CANCELLED',safe_error='SYNC_DISABLED',finished_at=$4 FROM stock_publications p WHERE t.operation_id=p.id AND p.tenant_id=$1 AND p.moysklad_account=$2 AND p.ozon_account=$3 AND t.state='QUEUED'`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, now)
		if err != nil {
			return domain.StockSyncView{}, err
		}
		_, err = tx.Exec(ctx, `UPDATE outbox_tasks q SET state='cancelled',last_error='SYNC_DISABLED',updated_at=$4 FROM stock_publications p WHERE q.publication_operation_id=p.id AND p.tenant_id=$1 AND p.moysklad_account=$2 AND p.ozon_account=$3 AND q.state='pending'`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, now)
		if err != nil {
			return domain.StockSyncView{}, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO stock_sync_mutations(tenant_id,moysklad_account,ozon_account,idempotency_key,request_hash,action,operation_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, key, hash, action, operation)
	if err != nil {
		return domain.StockSyncView{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.StockSyncView{}, err
	}
	return s.StockSyncStatus(ctx, scope, now)
}
func (s *Store) StockSyncStatus(ctx context.Context, scope domain.StockSyncScope, now time.Time) (domain.StockSyncView, error) {
	if !scope.Valid() {
		return domain.StockSyncView{}, domain.ErrStockSyncNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return domain.StockSyncView{}, err
	}
	defer tx.Rollback(context.Background())
	view := domain.StockSyncView{StockSyncScope: scope, State: "DISABLED", Checks: []domain.StockSyncCheck{}, Targets: []domain.StockSyncTarget{}}
	var reviewID *string
	var lastChecks []byte
	err = tx.QueryRow(ctx, `SELECT enabled,review_id,last_checks FROM stock_sync_settings WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&view.Enabled, &reviewID, &lastChecks)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return view, err
	}
	var attestations []domain.StockSyncAttestation
	if reviewID != nil {
		r, e := readSyncReview(ctx, tx, scope, *reviewID)
		if e != nil {
			return view, e
		}
		attestations = r.Attestations
	}
	ready, e := s.stockSyncReview(ctx, tx, scope, attestations, now)
	if e != nil {
		return view, e
	}
	view.Ready, view.Checks, view.Targets = ready.Ready, ready.Checks, ready.Targets
	if reviewID != nil {
		original, err := readSyncReview(ctx, tx, scope, *reviewID)
		if err != nil {
			return view, err
		}
		if !sameSyncConditions(original, ready) {
			view.Ready = false
			view.Checks = append(view.Checks, domain.StockSyncCheck{Code: "SYNC_CONDITIONS_CHANGED", Message: "Повторно проверьте условия отправки после изменения соответствий или подключения."})
		}
	}
	if view.Enabled {
		view.State = "BLOCKED"
		if view.Ready {
			view.State = "ENABLED"
		}
	}
	var recovery domain.StockPublicationRecovery
	err = tx.QueryRow(ctx, `SELECT r.id,p.id,COALESCE(l.new_operation_id,''),r.product_id,r.warehouse_id,r.observed_quantity,r.observed_at,r.created_at,r.principal_type,r.reason FROM stock_publication_recoveries r JOIN stock_publication_targets t ON t.id=r.target_id JOIN stock_publications p ON p.id=t.operation_id LEFT JOIN stock_publication_recovery_links l ON l.recovery_id=r.id WHERE r.tenant_id=$1 AND r.moysklad_account=$2 AND r.ozon_account=$3 ORDER BY r.created_at DESC LIMIT 1`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&recovery.ID, &recovery.OriginalOperationID, &recovery.NewOperationID, &recovery.ProductID, &recovery.WarehouseID, &recovery.ObservedQuantity, &recovery.ObservedAt, &recovery.CreatedAt, &recovery.PrincipalType, &recovery.Reason)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return view, err
	}
	if err == nil {
		view.Recovery = &recovery
	}
	var id string
	err = tx.QueryRow(ctx, `SELECT id FROM stock_publications WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 ORDER BY created_at DESC,id DESC LIMIT 1`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return view, err
	}
	if err == nil {
		op, e := readPublication(ctx, tx, scope, id)
		if e != nil {
			return view, e
		}
		view.LastOperation = &op
		switch op.State {
		case "BLOCKED", "FAILED", "CANCELLED":
			if view.Enabled {
				view.State = "BLOCKED"
			}
		case "DISPATCHING":
			if view.Enabled {
				view.State = "UPDATING"
			} else {
				view.State = "STOPPING"
			}
		case "UNKNOWN", "PARTIAL":
			if view.Recovery != nil && view.Recovery.OriginalOperationID == op.ID {
				view.State = "TEST_SUPERSEDED_REVIEW_REQUIRED"
			} else if view.Enabled {
				view.State = op.State
			} else {
				view.State = "DISABLED_OUTCOME_UNKNOWN"
			}
		case "QUEUED":
			if view.Enabled {
				view.State = "QUEUED"
			}
		case "ACKNOWLEDGED":
			view.State = "RECONCILING"
			for _, target := range op.Targets {
				if target.SafeError != "" {
					view.State = "READBACK_REQUIRED"
				}
			}
		case "RECONCILED":
			view.State = "RECONCILED"
			for _, target := range op.Targets {
				if target.ReadbackAt != nil && (view.LastConfirmedAt == nil || target.ReadbackAt.After(*view.LastConfirmedAt)) {
					v := *target.ReadbackAt
					view.LastConfirmedAt = &v
				}
			}
		case "SUCCEEDED":
			var confirmed *time.Time
			for _, t := range op.Targets {
				if t.FinishedAt != nil && (confirmed == nil || t.FinishedAt.After(*confirmed)) {
					v := *t.FinishedAt
					confirmed = &v
				}
			}
			view.LastConfirmedAt = confirmed
			if view.Enabled && view.Ready {
				view.State = "ACTIVE"
			}
		}
	}
	if !view.Enabled && view.LastOperation != nil {
		unresolved := false
		for _, target := range view.LastOperation.Targets {
			if target.State == "UNKNOWN" || target.State == "DISPATCHING" || target.State == "QUEUED" || target.State == "ACKNOWLEDGED" {
				unresolved = true
			}
		}
		for _, target := range view.LastOperation.Targets {
			if !unresolved && target.State == "CONFIRMED_EFFECTIVE" {
				view.State = "EFFECT_CONFIRMED_REVIEW_REQUIRED"
				break
			}
		}
	}
	return view, nil
}
func readPublication(ctx context.Context, tx pgx.Tx, scope domain.StockSyncScope, id string) (domain.StockPublication, error) {
	op := domain.StockPublication{ID: id, StockSyncScope: scope, Targets: []domain.StockPublicationTarget{}}
	err := tx.QueryRow(ctx, `SELECT review_id,created_at FROM stock_publications WHERE id=$1 AND tenant_id=$2 AND moysklad_account=$3 AND ozon_account=$4`, id, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&op.ReviewID, &op.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return op, domain.ErrStockSyncNotFound
	}
	if err != nil {
		return op, err
	}
	rows, err := tx.Query(ctx, `SELECT id,product_id,warehouse_id,quantity,state,safe_error,dispatched_at,finished_at,decision,acknowledged_at,readback_at,readback_quantity,http_status,provider_updated,provider_error_codes FROM stock_publication_targets WHERE operation_id=$1 ORDER BY id LIMIT 100`, id)
	if err != nil {
		return op, err
	}
	defer rows.Close()
	states := map[string]int{}
	for rows.Next() {
		v := domain.StockPublicationTarget{OperationID: id}
		var raw []byte
		if err = rows.Scan(&v.ID, &v.ProductID, &v.WarehouseID, &v.Quantity, &v.State, &v.SafeError, &v.DispatchedAt, &v.FinishedAt, &raw, &v.AcknowledgedAt, &v.ReadbackAt, &v.ReadbackQuantity, &v.HTTPStatus, &v.ProviderUpdated, &v.ProviderErrorCodes); err != nil {
			return op, err
		}
		if err = json.Unmarshal(raw, &v.Decision); err != nil {
			return op, err
		}
		op.Targets = append(op.Targets, v)
		states[v.State]++
	}
	if err = rows.Err(); err != nil {
		return op, err
	}
	rows.Close()
	for i := range op.Targets {
		observations, e := tx.Query(ctx, `SELECT observed_at,quantity,outcome FROM stock_publication_readbacks WHERE target_id=$1 ORDER BY observed_at DESC,id DESC LIMIT 20`, op.Targets[i].ID)
		if e != nil {
			return op, e
		}
		for observations.Next() {
			var item domain.StockPublicationReadback
			if e = observations.Scan(&item.ObservedAt, &item.Quantity, &item.Outcome); e != nil {
				observations.Close()
				return op, e
			}
			op.Targets[i].Readbacks = append(op.Targets[i].Readbacks, item)
		}
		e = observations.Err()
		observations.Close()
		if e != nil {
			return op, e
		}
	}
	switch {
	case states["DISPATCHING"] > 0:
		op.State = "DISPATCHING"
	case states["ACKNOWLEDGED"] > 0:
		op.State = "ACKNOWLEDGED"
	case states["UNKNOWN"] > 0:
		op.State = "UNKNOWN"
	case states["QUEUED"] > 0:
		op.State = "QUEUED"
	case len(states) > 1:
		op.State = "PARTIAL"
	default:
		for k := range states {
			op.State = k
		}
	}
	return op, nil
}
