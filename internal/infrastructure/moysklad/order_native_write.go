package moysklad

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

func (c *OrderClient) nativeMovementPayload(command orders.Command) (map[string]any, error) {
	movement := command.NativeMovement
	if movement == nil || !nativeDispatchEventBound(command) || ordermoney.NativeDocumentAmounts(movement).Code != "" {
		return nil, ErrOrderIntent
	}
	body := map[string]any{"syncId": movement.Correlation.SyncID, "externalCode": movement.Correlation.Marker, "name": movement.Correlation.Marker, "organization": c.reference("entity/organization", "organization", movement.OrganizationID), "agent": c.reference("entity/counterparty", "counterparty", movement.AgentID), "store": c.reference("entity/store", "store", movement.StoreID), "applicable": *movement.Applicable.Value, "vatEnabled": *movement.VATEnabled.Value, "vatIncluded": *movement.VATIncluded.Value, "moment": movement.Moment.In(time.FixedZone("Moscow", 3*60*60)).Format("2006-01-02 15:04:05.000")}
	rate := map[string]any{"currency": c.reference("entity/currency", "currency", movement.CurrencyID)}
	if movement.RateValue.IsKnown() {
		rate["value"] = json.Number(*movement.RateValue.Value)
	}
	body["rate"] = rate
	if command.Action == orders.ActionDemandCreate {
		body["customerOrder"] = c.reference("entity/customerorder", "customerorder", command.Correlation.CustomerOrderID)
	}
	if command.Action == orders.ActionSalesReturnCreate {
		body["demand"] = c.reference("entity/demand", "demand", movement.DemandBasis.Correlation.DocumentID)
	}
	positions, err := c.nativeMovementPositions(movement.Lines)
	if err != nil {
		return nil, err
	}
	body["positions"] = positions
	return body, nil
}

func nativeDispatchEventBound(command orders.Command) bool {
	if command.NativeMovement == nil || command.ActionInput == nil {
		return false
	}
	input, event := command.ActionInput, command.NativeMovement.Event
	return input.EventReference != "" && input.NativeEventKind == event.Kind && input.EventConfirmed == event.Confirmed && input.NativeEventOccurredAt.Equal(event.OccurredAt)
}

func (c *OrderClient) nativeMovementPositions(lines []orders.NativeMovementLine) ([]map[string]any, error) {
	positions := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if !orderUUID.MatchString(line.Mapping.MoyskladID) {
			return nil, ErrOrderIdentity
		}
		price, err := ordermoney.MoneyToMinor(line.Price)
		if err != nil {
			return nil, err
		}
		positions = append(positions, map[string]any{"assortment": c.reference("entity/"+line.Mapping.MoyskladKind, line.Mapping.MoyskladKind, line.Mapping.MoyskladID), "quantity": json.Number(line.Quantity), "price": price, "discount": json.Number(*line.Discount.Value), "vat": json.Number(*line.VAT.Value), "vatEnabled": *line.VATEnabled.Value})
	}
	return positions, nil
}

func (c *OrderClient) nativeMovementPreflight(ctx context.Context, command orders.Command) error {
	movement := command.NativeMovement
	if movement == nil {
		return orders.ErrInvalid
	}
	parent, err := c.readCustomerOrder(ctx, command.Correlation)
	if err != nil {
		return err
	}
	if !parent.Complete || parent.Revision != command.ExpectedTargetRevision || !orders.NativePositionsEqual(command.TargetPositions, parent.Positions) {
		return orders.ErrStale
	}
	if err = c.nativeMovementProductPreflight(ctx, command); err != nil {
		return err
	}
	if command.Action == orders.ActionSalesReturnCreate {
		basis, readErr := c.ReadNativeDocument(ctx, orders.NativeDocumentRequest{Scope: command.Scope, Correlation: movement.DemandBasis.Correlation, IncludeReturns: true})
		if readErr != nil {
			return readErr
		}
		if !sameNativeBasis(*movement.DemandBasis, basis) {
			return orders.ErrStale
		}
	}
	_, err = c.lookupNativeDocument(ctx, movement.Correlation)
	if errors.Is(err, orders.ErrNotFound) {
		return nil
	}
	if err == nil {
		return orders.ErrConflict
	}
	return err
}

func (c *OrderClient) nativeMovementProductPreflight(ctx context.Context, command orders.Command) error {
	cache := make(map[string]nativeProductFacts, len(command.NativeMovement.Products)*2)
	mappings := make([]orders.ProductMappingRef, 0, len(command.NativeMovement.Lines))
	for _, line := range command.NativeMovement.Lines {
		mappings = append(mappings, line.Mapping)
	}
	unique, err := nativeUniqueProducts(command.Scope, mappings)
	if err != nil {
		return err
	}
	for _, mapping := range unique {
		current, readErr := c.readNativeProductRequirement(ctx, mapping, cache)
		if readErr != nil {
			return readErr
		}
		matched := false
		for _, prior := range command.NativeMovement.Products {
			matched = matched || prior.ItemKind == current.ItemKind && prior.ItemID == current.ItemID && prior.Revision == current.Revision
		}
		if !matched {
			return orders.ErrStale
		}
	}
	return nil
}

func sameNativeBasis(before, after orders.NativeDocumentObservation) bool {
	// Observation times are traversal facts and cannot manufacture a revision.
	before.Returns = slices.Clone(before.Returns)
	after.Returns = slices.Clone(after.Returns)
	before.ObservedAt, after.ObservedAt = time.Time{}, time.Time{}
	before.ReturnsStartedAt, after.ReturnsStartedAt = time.Time{}, time.Time{}
	before.ReturnsFinishedAt, after.ReturnsFinishedAt = time.Time{}, time.Time{}
	for i := range before.Returns {
		before.Returns[i].ObservedAt = time.Time{}
	}
	for i := range after.Returns {
		after.Returns[i].ObservedAt = time.Time{}
	}
	left, err := json.Marshal(before)
	if err != nil {
		return false
	}
	right, err := json.Marshal(after)
	return err == nil && string(left) == string(right)
}
