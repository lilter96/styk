package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
)

type ProductImportDraft struct {
	Revision  int64           `json:"revision"`
	State     json.RawMessage `json:"state"`
	UpdatedAt time.Time       `json:"updated_at"`
}
type ProductDraftSave struct {
	Scope                catalog.Scope
	Provider, Actor, Key string
	Revision             int64
	State                json.RawMessage
}

func (s *Store) ProductImportDraft(ctx context.Context, scope catalog.Scope, provider string) (ProductImportDraft, error) {
	var d ProductImportDraft
	if scope.Validate() != nil || (provider != "ozon" && provider != "moysklad") {
		return d, catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	if _, err := readCatalogConfig(ctx, s.Pool, scope); err != nil {
		return d, err
	}
	err := s.Pool.QueryRow(ctx, `SELECT revision,state,updated_at FROM product_import_drafts WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND provider=$4`, append(catalogScopeArgs(scope), provider)...).Scan(&d.Revision, &d.State, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		d.State = json.RawMessage(`{}`)
		return d, nil
	}
	return d, err
}
func (s *Store) SaveProductImportDraft(ctx context.Context, r ProductDraftSave) (ProductImportDraft, error) {
	var d ProductImportDraft
	var state map[string]json.RawMessage
	if r.Scope.Validate() != nil || (r.Provider != "ozon" && r.Provider != "moysklad") || r.Revision < 0 || !importKeyPattern.MatchString(r.Key) || len(r.State) > 8<<20 || json.Unmarshal(r.State, &state) != nil || state == nil || !validProductDraftState(r.State, r.Scope, r.Provider) {
		return d, catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return d, err
	}
	defer rollbackMerchantMutation(tx)
	if err = catalogActor(ctx, tx, r.Scope.Tenant, r.Actor); err != nil {
		return d, err
	}
	if err = catalogLock(ctx, tx, r.Scope); err != nil {
		return d, err
	}
	c, err := readCatalogConfig(ctx, tx, r.Scope)
	if err != nil {
		return d, err
	}
	if _, err = initializeImportConfig(ctx, tx, c, r.Actor); err != nil {
		return d, err
	}
	d, err = persistProductDraft(ctx, tx, r, state)
	if err != nil {
		return d, err
	}
	return d, tx.Commit(ctx)
}

func persistProductDraft(ctx context.Context, tx pgx.Tx, r ProductDraftSave, state map[string]json.RawMessage) (ProductImportDraft, error) {
	var d ProductImportDraft
	hash, err := catalogHash(state)
	if err != nil {
		return d, err
	}
	args := append(catalogScopeArgs(r.Scope), r.Provider, r.Revision+1, r.State, r.Key, hash, r.Revision)
	err = tx.QueryRow(ctx, `INSERT INTO product_import_drafts(tenant_id,moysklad_account,ozon_account,provider,revision,state,save_key,save_hash) SELECT $1,$2,$3,$4,$5,$6,$7,$8 WHERE $9::bigint=0 OR EXISTS(SELECT 1 FROM product_import_drafts WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND provider=$4)
 ON CONFLICT(tenant_id,moysklad_account,ozon_account,provider) DO UPDATE SET revision=CASE WHEN product_import_drafts.save_key=$7 THEN product_import_drafts.revision ELSE $5 END,state=EXCLUDED.state,save_key=$7,save_hash=$8,updated_at=clock_timestamp()
 WHERE (product_import_drafts.save_key=$7 AND product_import_drafts.save_hash=$8) OR (product_import_drafts.save_key<>$7 AND product_import_drafts.revision=$9)
 RETURNING revision,state,updated_at`, args...).Scan(&d.Revision, &d.State, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, catalogsync.ErrConflict
	}
	if err != nil {
		return d, fmt.Errorf("save product draft: %w", err)
	}
	return d, nil
}

// Draft contents are untrusted editable data, never an authorization to dispatch.
// Validate identities for safe restoration; approval still revalidates canonical previews.
func validProductDraftState(raw json.RawMessage, scope catalog.Scope, provider string) bool {
	var state struct {
		Selected map[string]productDraftSelection `json:"selected"`
		Plans    []catalogsync.Preview            `json:"plans"`
		Intents  []struct {
			PreviewID string                 `json:"preview_id"`
			IDs       []string               `json:"enrollment_ids"`
			Key       string                 `json:"idempotency_key"`
			Operation *catalogsync.Operation `json:"operation"`
		} `json:"intents"`
	}
	if json.Unmarshal(raw, &state) != nil {
		return false
	}
	if !validProductDraftSelections(state.Selected, scope, provider) {
		return false
	}
	for _, p := range state.Plans {
		if p.Scope != scope || p.ID == "" || len(p.Decisions) > 20 {
			return false
		}
	}
	for _, i := range state.Intents {
		if i.PreviewID == "" || len(i.IDs) == 0 || len(i.IDs) > 20 || !importKeyPattern.MatchString(i.Key) {
			return false
		}
		if i.Operation != nil && (i.Operation.Scope != scope || i.Operation.ID == "") {
			return false
		}
	}
	return true
}

type productDraftSelection struct {
	Product    catalog.Product        `json:"product"`
	Enrollment catalogsync.Enrollment `json:"enrollment"`
}

func validProductDraftSelections(selected map[string]productDraftSelection, scope catalog.Scope, provider string) bool {
	for key, item := range selected {
		p, e := item.Product, item.Enrollment
		if p.Identity.Validate() != nil || string(p.Identity.Account.Provider) != provider || !scope.Contains(p.Identity.Account) || key != p.Identity.EntityType+":"+p.Identity.ID || !p.Identity.SameEntity(e.Source) || !scope.Contains(e.Target.Account) || e.Create == nil || e.Automatic || e.Create.Values == nil {
			return false
		}
	}
	return true
}
