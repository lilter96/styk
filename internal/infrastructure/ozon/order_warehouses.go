package ozon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/lilter96/styk/internal/delivery"
	"github.com/lilter96/styk/internal/orders"
)

const (
	orderWarehousePath = "/v2/warehouse/list"
	orderMethodsPath   = "/v2/delivery-method/list"
	maxDeliveryRows    = 1000
)

// ListOrderDelivery reads bounded, value-minimal metadata. The cache belongs to
// this exact tenant/account client and expires after delivery.Validity.
func (c *OrdersClient) ListOrderDelivery(ctx context.Context, scope orders.Scope) (delivery.Discovery, error) {
	if scope != c.scope {
		return delivery.Discovery{}, orders.ErrInvalid
	}
	if !c.ReadonlyReaderRequest {
		return delivery.Discovery{}, orders.ErrBlocked
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for {
		c.deliveryMu.Lock()
		if c.deliveryCache != nil && !c.now().Before(c.deliveryCache.CheckedAt) && c.now().Before(c.deliveryCache.ValidUntil) {
			cached := c.deliveryCache
			c.deliveryMu.Unlock()
			return cloneDeliveryDiscovery(*cached)
		}
		flight := c.deliveryFlight
		if flight == nil {
			c.deliveryFlight = make(chan struct{})
			c.deliveryMu.Unlock()
			return c.loadOrderDelivery(ctx)
		}
		c.deliveryMu.Unlock()
		select {
		case <-ctx.Done():
			return delivery.Discovery{}, ctx.Err()
		case <-flight:
		}
	}
}

func (c *OrdersClient) loadOrderDelivery(ctx context.Context) (delivery.Discovery, error) {
	result, err := c.discoverOrderDelivery(ctx)
	if err == nil && c.options.DeliveryObserver != nil {
		err = c.options.DeliveryObserver.SaveDeliveryObservations(ctx, result.Scope, result.Profiles)
	}
	c.deliveryMu.Lock()
	if err == nil {
		c.deliveryCache = &result
	}
	close(c.deliveryFlight)
	c.deliveryFlight = nil
	c.deliveryMu.Unlock()
	if err != nil {
		return delivery.Discovery{}, err
	}
	return cloneDeliveryDiscovery(result)
}

func cloneDeliveryDiscovery(result delivery.Discovery) (delivery.Discovery, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return delivery.Discovery{}, fmt.Errorf("encode delivery metadata: %w", err)
	}
	var clone delivery.Discovery
	if err = json.Unmarshal(raw, &clone); err != nil {
		return delivery.Discovery{}, fmt.Errorf("copy delivery metadata: %w", err)
	}
	return clone, nil
}

func (c *OrdersClient) discoverOrderDelivery(ctx context.Context) (delivery.Discovery, error) {
	evidence, err := delivery.Evidence(c.manifest)
	if err != nil {
		return delivery.Discovery{}, err
	}
	checked := c.now()
	result := delivery.Discovery{Scope: delivery.OrderScope(c.scope), CheckedAt: checked, ValidUntil: checked.Add(delivery.Validity)}
	result.Warehouses, err = c.readOrderWarehouses(ctx)
	if err != nil {
		return result, err
	}
	result.Methods, err = c.readOrderMethods(ctx)
	if err != nil {
		return result, err
	}
	result.Profiles = make([]delivery.Profile, 0, len(result.Warehouses)+len(result.Methods))
	for _, warehouse := range result.Warehouses {
		profile, classifyErr := delivery.Classify(result.Scope, warehouse, nil, evidence, checked)
		if classifyErr != nil {
			return result, classifyErr
		}
		result.Profiles = append(result.Profiles, profile)
		if err = appendDeliveryMethods(&result, warehouse, evidence); err != nil {
			return result, err
		}
	}
	result.Complete = true
	return result, nil
}

func appendDeliveryMethods(result *delivery.Discovery, warehouse delivery.Warehouse, evidence delivery.ContractEvidence) error {
	for _, method := range result.Methods {
		if method.WarehouseID != warehouse.ID {
			continue
		}
		profile, err := delivery.Classify(result.Scope, warehouse, &method, evidence, result.CheckedAt)
		if err != nil {
			return err
		}
		result.Profiles = append(result.Profiles, profile)
	}
	return nil
}

func (c *OrdersClient) ReadOrderDelivery(ctx context.Context, scope orders.Scope, posting orders.Posting) (delivery.Profile, error) {
	if scope != c.scope || posting.Scope != scope || !posting.WarehouseID.IsKnown() {
		return delivery.Profile{}, orders.ErrInvalid
	}
	result, err := c.ListOrderDelivery(ctx, scope)
	if err != nil {
		return delivery.Profile{}, err
	}
	var warehouse *delivery.Profile
	for _, profile := range result.Profiles {
		if profile.WarehouseID != *posting.WarehouseID.Value {
			continue
		}
		if posting.DeliveryMethodID.IsKnown() && profile.MethodID.IsKnown() && *profile.MethodID.Value == *posting.DeliveryMethodID.Value {
			return profile, nil
		}
		if !profile.MethodID.IsKnown() {
			copyProfile := profile
			warehouse = &copyProfile
		}
	}
	if warehouse == nil {
		return delivery.Profile{}, orders.ErrNotFound
	}
	return *warehouse, nil
}

func (c *OrdersClient) EnrichDelivery(ctx context.Context, scope orders.Scope, posting orders.Posting) (orders.Posting, error) {
	if scope != c.scope || posting.Scope != scope {
		return posting, orders.ErrInvalid
	}
	if !posting.WarehouseID.IsKnown() || orderKnown(posting.Scheme) == "FBO" {
		return posting, nil
	}
	profile, err := c.ReadOrderDelivery(ctx, scope, posting)
	if errors.Is(err, orders.ErrNotFound) {
		posting.Drift = append(posting.Drift, orders.Drift{Path: "$.result.delivery_method.warehouse_id", Code: "warehouse_unverified"})
		return posting, nil
	}
	if err != nil {
		return posting, err
	}
	if profile.Scheme.IsKnown() {
		if posting.Scheme.IsKnown() && *posting.Scheme.Value != *profile.Scheme.Value {
			posting.Scheme, posting.DeliveryProfile = orders.Field[string]{}, nil
			posting.Drift = append(posting.Drift, orders.Drift{Path: "$.result.scheme", Code: "conflicting_scheme"})
			return posting, nil
		}
		posting.Scheme = profile.Scheme
		posting.Drift = slices.DeleteFunc(posting.Drift, func(d orders.Drift) bool { return d.Code == "scheme_unverified" })
	}
	posting.DeliveryProfile = profile.CapabilityRef()
	posting.Drift = append(posting.Drift, profile.Drift...)
	if orderKnown(posting.Scheme) == "FBO" {
		posting.Drift = append(posting.Drift, orders.Drift{Path: "$.result.scheme", Code: "fbo_out_of_scope"})
	}
	return posting, nil
}
