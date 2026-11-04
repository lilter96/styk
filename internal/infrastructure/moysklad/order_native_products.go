package moysklad

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

// Reads are bounded before any HTTP call and cached only within this exact
// account traversal. Variants reuse a parent-product read rather than fetching
// the same regulatory facts once per order line.
func (c *OrderClient) ReadNativeProductRequirements(ctx context.Context, scope orders.Scope, mappings []orders.ProductMappingRef) ([]orders.NativeProductRequirements, error) {
	unique, err := nativeUniqueProducts(scope, mappings)
	if err != nil {
		return nil, err
	}
	if !c.scopeAllowed(scope) {
		return nil, orders.ErrInvalid
	}
	ctx, finish := c.boundedContext(ctx)
	defer finish()
	if err = c.verifyAccount(ctx); err != nil {
		return nil, err
	}
	cache := make(map[string]nativeProductFacts, len(unique)*2)
	result := make([]orders.NativeProductRequirements, 0, len(unique))
	for _, mapping := range unique {
		proof, readErr := c.readNativeProductRequirement(ctx, mapping, cache)
		if readErr != nil {
			return nil, readErr
		}
		result = append(result, proof)
	}
	return result, nil
}

func nativeUniqueProducts(scope orders.Scope, mappings []orders.ProductMappingRef) ([]orders.ProductMappingRef, error) {
	if len(mappings) == 0 || len(mappings) > 100 {
		return nil, orders.ErrInvalid
	}
	seen := make(map[string]bool, min(len(mappings), orders.MaxNativeProducts))
	result := make([]orders.ProductMappingRef, 0, min(len(mappings), orders.MaxNativeProducts))
	for _, mapping := range mappings {
		if mapping.Validate() != nil || mapping.Scope != scope || !orderUUID.MatchString(mapping.MoyskladID) {
			return nil, orders.ErrInvalid
		}
		key := mapping.MoyskladKind + ":" + mapping.MoyskladID
		if seen[key] {
			continue
		}
		if len(result) == orders.MaxNativeProducts {
			return nil, orders.ErrBlocked
		}
		seen[key] = true
		result = append(result, mapping)
	}
	return result, nil
}

type nativeProductFacts struct {
	ID       string
	ParentID string
	Archived orders.Field[bool]
	Serial   orders.Field[bool]
	Tracking orders.Field[string]
}

func (c *OrderClient) readNativeProductRequirement(ctx context.Context, mapping orders.ProductMappingRef, cache map[string]nativeProductFacts) (orders.NativeProductRequirements, error) {
	item, err := c.nativeProductFacts(ctx, mapping.MoyskladKind, mapping.MoyskladID, cache)
	if err != nil {
		return orders.NativeProductRequirements{}, err
	}
	proof := orders.NativeProductRequirements{Scope: c.scope, ItemKind: mapping.MoyskladKind, ItemID: item.ID, Archived: item.Archived, SerialTrackable: item.Serial, TrackingType: item.Tracking, ObservedAt: time.Now().UTC()}
	if mapping.MoyskladKind == "variant" {
		parent, parentErr := c.nativeProductFacts(ctx, "product", item.ParentID, cache)
		if parentErr != nil {
			return proof, parentErr
		}
		proof.ParentProductID, proof.ParentArchived, proof.SerialTrackable, proof.TrackingType = parent.ID, parent.Archived, parent.Serial, parent.Tracking
	}
	canonical := proof
	canonical.ObservedAt = time.Time{}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return proof, ErrOrderSchema
	}
	sum := sha256.Sum256(raw)
	proof.Revision = hex.EncodeToString(sum[:])
	return proof, proof.Validate(c.scope)
}

func (c *OrderClient) nativeProductFacts(ctx context.Context, kind, id string, cache map[string]nativeProductFacts) (nativeProductFacts, error) {
	key := kind + ":" + id
	if fact, ok := cache[key]; ok {
		return fact, nil
	}
	if !orderUUID.MatchString(id) || kind != "product" && kind != "variant" {
		return nativeProductFacts{}, ErrOrderIdentity
	}
	raw, err := c.client.get(ctx, "entity/"+kind+"/"+id, nil)
	if err != nil {
		return nativeProductFacts{}, err
	}
	fact, err := c.decodeNativeProductFacts(raw, kind, id)
	if err == nil {
		cache[key] = fact
	}
	return fact, err
}

func (c *OrderClient) decodeNativeProductFacts(raw []byte, kind, id string) (nativeProductFacts, error) {
	var row struct {
		catalogReference
		ID        string           `json:"id"`
		AccountID string           `json:"accountId"`
		Product   catalogReference `json:"product"`
	}
	var fields map[string]json.RawMessage
	if orderCheckJSON(raw) != nil || json.Unmarshal(raw, &row) != nil || json.Unmarshal(raw, &fields) != nil || row.ID != id || row.AccountID != c.scope.MoyskladAccount {
		return nativeProductFacts{}, ErrOrderIdentity
	}
	checked, err := c.referenceID(row.catalogReference, "entity/"+kind, kind)
	if err != nil || checked != id {
		return nativeProductFacts{}, ErrOrderIdentity
	}
	fact := nativeProductFacts{ID: id}
	if fact.Archived, err = orderReadField[bool](fields, "archived"); err != nil {
		return fact, err
	}
	if fact.Serial, err = orderReadField[bool](fields, "isSerialTrackable"); err != nil {
		return fact, err
	}
	if fact.Tracking, err = orderReadField[string](fields, "trackingType"); err != nil {
		return fact, err
	}
	if kind == "variant" {
		fact.ParentID, err = c.referenceID(row.Product, "entity/product", "product")
	}
	return fact, err
}
