package moysklad

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

// The official Remap general contract documents POST metadata/attributes as an
// administrator-only operation returning HTTP 200 and native attribute objects:
// https://raw.githubusercontent.com/moysklad/api-remap-1.2-doc/master/md/_general.md
// customerOrder explicitly uses that shared contract for its additional fields.
// Reviewed public source bytes (2026-10-30): general SHA-256
// 5dc61a23b376b7b2ea46ece7ac6f33f56476d2c34fdf152d457cbeb8e0da0c16;
// customerOrder SHA-256
// 990e50b153bd64ad0b423710f88fc435564878aa95485b792759415f0563c972.
// No syncId or idempotency guarantee is inferred for attribute creation.
type orderCreatedAttribute struct {
	catalogReference
	ID        string `json:"id"`
	AccountID string `json:"accountId"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Required  bool   `json:"required"`
}

type orderAttributeCreateError string

func (err orderAttributeCreateError) Error() string        { return string(err) }
func (err orderAttributeCreateError) MetadataCode() string { return string(err) }

var (
	ErrOrderAttributeAdmin    = orderAttributeCreateError("PROVIDER_ADMIN_REQUIRED")
	ErrOrderAttributeRejected = orderAttributeCreateError("PROVIDER_ATTRIBUTE_REJECTED")
)

// CreateOrderAttribute consumes only a newly journaled dispatch permission.
// Recovery always reads the exact generated marker and never resends a POST.
func (c *OrderClient) CreateOrderAttribute(ctx context.Context, scope orders.Scope, intent orders.MetadataCreationIntent, authorize func(context.Context) (bool, error)) (orders.MetadataCreationCompletion, error) {
	completion := orderAttributeCompletion(intent, orders.StateUnknown)
	if !c.validOrderAttributeIntent(scope, intent) {
		completion.State = orders.StateRejected
		return completion, ErrOrderIntent
	}
	ctx, finish := c.boundedContext(ctx)
	defer finish()
	if err := c.verifyAccount(ctx); err != nil {
		return completion, err
	}
	id, err := c.findOrderAttribute(ctx, intent)
	if err != nil {
		return completion, err
	}
	if id != "" {
		return c.confirmOrderAttribute(ctx, intent, id)
	}
	if !intent.DispatchPermitted || intent.State != orders.StateDispatching {
		return completion, ErrOrderUnknown
	}
	if err = c.consumeOrderAttributeIntent(intent, authorize); err != nil {
		completion.State = orders.StateRejected
		return completion, err
	}
	return c.dispatchOrderAttribute(ctx, intent, authorize)
}

func (c *OrderClient) validOrderAttributeIntent(scope orders.Scope, intent orders.MetadataCreationIntent) bool {
	if !c.scopeAllowed(scope) || !orderTransportScope(c.client.http.Transport, scope) || intent.Scope != scope || (intent.State != orders.StateDispatching && intent.State != orders.StateUnknown) {
		return false
	}
	if (intent.Type != "string" && intent.Type != "text") || intent.UniqueCode == "" || intent.IdempotencyKey == "" || intent.CreatedAt.IsZero() {
		return false
	}
	return validOrderAttributeMarker(intent.Marker) && intent.Marker == "styk-order-attribute-"+intent.ID
}

func validOrderAttributeMarker(marker string) bool {
	return strings.HasPrefix(marker, "styk-order-attribute-") && orderMetadataIntentID(strings.TrimPrefix(marker, "styk-order-attribute-"))
}

func orderMetadataIntentID(id string) bool {
	if orderUUID.MatchString(id) {
		return true
	}
	decoded, err := hex.DecodeString(id)
	return len(id) == 32 && err == nil && len(decoded) == 16
}

func (c *OrderClient) consumeOrderAttributeIntent(intent orders.MetadataCreationIntent, authorize func(context.Context) (bool, error)) error {
	if authorize == nil {
		return ErrOrderIntent
	}
	raw, err := json.Marshal(intent)
	if err != nil {
		return ErrOrderIntent
	}
	digest := sha256.Sum256(append([]byte("order-attribute-create:"), raw...))
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.used || c.intent != nil {
		return ErrOrderIntent
	}
	c.intent, c.used = digest[:], true
	return nil
}

func (c *OrderClient) findOrderAttribute(ctx context.Context, intent orders.MetadataCreationIntent) (string, error) {
	raw, err := c.client.get(ctx, "entity/customerorder/metadata", nil)
	if err != nil {
		return "", err
	}
	if orderCheckJSON(raw) != nil {
		return "", ErrOrderSchema
	}
	var metadata struct {
		Attributes json.RawMessage `json:"attributes"`
	}
	if json.Unmarshal(raw, &metadata) != nil {
		return "", ErrOrderSchema
	}
	attributes, err := c.readOrderAttributeDefinitions(ctx, metadata.Attributes)
	if err != nil {
		return "", err
	}
	if err = orderAttributeCollectionSize(metadata.Attributes, len(attributes)); err != nil {
		return "", err
	}
	matched := ""
	for _, attribute := range attributes {
		if attribute.Name != intent.Marker {
			continue
		}
		if attribute.Type != intent.Type {
			return "", ErrOrderIdentity
		}
		if matched != "" {
			return "", orders.ErrConflict
		}
		matched = attribute.ID
	}
	return matched, nil
}

func orderAttributeCollectionSize(raw json.RawMessage, count int) error {
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
		return nil
	}
	var collection struct {
		Meta struct {
			Size *int64 `json:"size"`
			Type string `json:"type"`
		} `json:"meta"`
	}
	if json.Unmarshal(raw, &collection) != nil {
		return ErrOrderSchema
	}
	if collection.Meta.Type != "" && collection.Meta.Type != "attributemetadata" {
		return ErrOrderSchema
	}
	if collection.Meta.Size != nil && *collection.Meta.Size != int64(count) {
		return orders.ErrStale
	}
	return nil
}

func (c *OrderClient) orderAttributeIdentity(attribute orderCreatedAttribute, intent orders.MetadataCreationIntent, requireID bool) (string, error) {
	id, err := c.referenceID(attribute.catalogReference, "entity/customerorder/metadata/attributes", "attributemetadata")
	if err != nil || attribute.Name != intent.Marker || attribute.Type != intent.Type || attribute.Required {
		return "", ErrOrderIdentity
	}
	if (requireID && attribute.ID == "") || (attribute.ID != "" && attribute.ID != id) || (attribute.AccountID != "" && attribute.AccountID != c.scope.MoyskladAccount) {
		return "", ErrOrderIdentity
	}
	return id, nil
}

func (c *OrderClient) confirmOrderAttribute(ctx context.Context, intent orders.MetadataCreationIntent, id string) (orders.MetadataCreationCompletion, error) {
	completion := orderAttributeCompletion(intent, orders.StateUnknown)
	if !orderUUID.MatchString(id) {
		return completion, ErrOrderIdentity
	}
	if err := c.verifyAccount(ctx); err != nil {
		return completion, err
	}
	completion.State, completion.AttributeID = orders.StateReconciled, id
	return completion, nil
}

func orderAttributeCompletion(intent orders.MetadataCreationIntent, state orders.State) orders.MetadataCreationCompletion {
	return orders.MetadataCreationCompletion{IntentID: intent.ID, Scope: intent.Scope, ExpectedState: intent.State, State: state, ObservedAt: time.Now().UTC()}
}

func orderAttributeResponseError(status int) error {
	if status == 403 {
		return ErrOrderAttributeAdmin
	}
	if orderRejectedStatus(status) {
		return ErrOrderAttributeRejected
	}
	return ErrOrderUnknown
}
