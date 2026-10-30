package moysklad

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

// WriteOrder never retries a mutation. An accepted, rejected or uncertain
// receipt is transport evidence; durable state changes belong to the runner.
func (c *OrderClient) WriteOrder(ctx context.Context, command orders.Command) (orders.Receipt, error) {
	authorize, err := c.consumeIntent(command)
	if err != nil {
		return orderReceipt(command, orders.StateRejected, 0, "intent_not_authorized"), err
	}
	body, err := c.orderPayload(command)
	if err != nil {
		return orderReceipt(command, orders.StateRejected, 0, "invalid_payload"), err
	}
	ctx, finish := c.boundedContext(ctx)
	defer finish()
	if err = c.verifyAccount(ctx); err != nil {
		return orderReceipt(command, orders.StateRejected, 0, "account_not_verified"), err
	}
	if err = c.orderPreflight(ctx, command, body); err != nil {
		return orderReceipt(command, orders.StateRejected, 0, "preflight_blocked"), err
	}
	return c.dispatchOrder(ctx, command, body, authorize)
}

func (c *OrderClient) orderPreflight(ctx context.Context, command orders.Command, body map[string]any) error {
	metadata, err := c.readOrderMetadata(ctx)
	if err != nil {
		return err
	}
	if err = validateOrderMetadata(command, metadata); err != nil {
		return err
	}
	if command.Action == orders.ActionMirrorCreate {
		_, err = c.readOrderCorrelation(ctx, command.Correlation)
		if errors.Is(err, ErrOrderCorrelation) {
			return nil
		}
		if err == nil {
			return orders.ErrConflict
		}
		return err
	}
	if body["attributes"] != nil {
		return c.mergeOrderAttributeUpdate(ctx, command, body, metadata)
	}
	target, err := c.readCustomerOrder(ctx, command.Correlation)
	if err != nil {
		return err
	}
	if !target.Complete || target.Revision != command.ExpectedTargetRevision {
		return orders.ErrStale
	}
	return nil
}

func validateOrderMetadata(command orders.Command, metadata OrderMetadata) error {
	write := command.CustomerOrder
	if !orderHasReference(metadata.Organizations, write.OrganizationID) || !orderHasReference(metadata.Agents, write.AgentID) || !orderHasReference(metadata.Stores, write.StoreID) || !orderHasReference(metadata.SalesChannels, write.SalesChannelID) {
		return ErrOrderIdentity
	}
	stateFound := false
	for _, state := range metadata.States {
		stateFound = stateFound || state.ID == write.StateID
	}
	if !stateFound {
		return ErrOrderIdentity
	}
	if err := orderValidateCurrency(*write, metadata.Currencies); err != nil {
		return err
	}
	for _, attribute := range write.Attributes {
		if !orderHasTextAttribute(metadata.Attributes, attribute.AttributeID) {
			return orders.ErrBlocked
		}
	}
	return nil
}

func orderHasReference(references []OrderReference, id string) bool {
	for _, reference := range references {
		if reference.ID == id {
			return true
		}
	}
	return false
}

func orderHasTextAttribute(attributes []OrderAttribute, id string) bool {
	for _, attribute := range attributes {
		if attribute.ID == id && (attribute.Type == "string" || attribute.Type == "text") {
			return true
		}
	}
	return false
}

func orderValidateCurrency(write orders.CustomerOrderWrite, currencies []OrderCurrency) error {
	iso := ""
	for _, currency := range currencies {
		if currency.ID == write.CurrencyID {
			iso = currency.ISOCode
		}
	}
	if iso == "" {
		return ErrOrderIdentity
	}
	for _, position := range write.Positions {
		if position.Price.IsKnown() && position.Price.Value.Currency != iso {
			return orders.ErrBlocked
		}
	}
	return nil
}

func (c *OrderClient) dispatchOrder(ctx context.Context, command orders.Command, body map[string]any, authorize func(context.Context) (bool, error)) (orders.Receipt, error) {
	if err := c.client.wait(ctx); err != nil {
		return orderReceipt(command, orders.StateRejected, 0, "dispatch_cancelled"), err
	}
	started := false
	guard := func(ctx context.Context) error {
		if err := c.finalOrderAuthorization(ctx, command, authorize); err != nil {
			return err
		}
		started = true
		return nil
	}
	ctx = providercall.WithGuard(ctx, guard)
	request, err := c.orderWriteRequest(ctx, command, body)
	if err != nil {
		return orderReceipt(command, orders.StateRejected, 0, "invalid_request"), err
	}
	bound, _ := ctx.Value(budgetKey{}).(*budget)
	if _, coordinated := bound.http.Transport.(providercall.Transport); !coordinated {
		if err = guard(ctx); err != nil {
			return orderReceipt(command, orders.StateRejected, 0, "intent_not_authorized"), err
		}
	}
	response, err := bound.http.Do(request)
	if err != nil {
		if !started || providercall.IsNotDispatched(err) {
			return orderReceipt(command, orders.StateRejected, 0, "dispatch_not_started"), ErrOrderIntent
		}
		return orderReceipt(command, orders.StateUnknown, 0, "transport_uncertain"), ErrOrderUnknown
	}
	return c.orderResponse(command, response)
}

func (c *OrderClient) orderWriteRequest(ctx context.Context, command orders.Command, body map[string]any) (*http.Request, error) {
	raw, err := json.Marshal(body)
	if err != nil || len(raw) > orderResponseLimit {
		return nil, orders.ErrInvalid
	}
	method, path := http.MethodPost, "entity/customerorder"
	if command.Action == orders.ActionMirrorUpdate {
		method, path = http.MethodPut, path+"/"+command.Correlation.CustomerOrderID
	}
	u := c.client.base.ResolveReference(&url.URL{Path: path})
	request, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(raw))
	if err != nil {
		return nil, orders.ErrInvalid
	}
	request.GetBody, request.Close = nil, true
	request.Header.Set("Authorization", "Bearer "+c.client.token)
	request.Header.Set("Accept", "application/json;charset=utf-8")
	request.Header.Set("Content-Type", "application/json")
	return request, nil
}

func (c *OrderClient) orderResponse(command orders.Command, response *http.Response) (orders.Receipt, error) {
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, orderResponseLimit+1))
	closeErr := response.Body.Close()
	c.client.deferRequests(rateDelay(response.Header, response.StatusCode, time.Now()))
	if readErr != nil || closeErr != nil || len(raw) > orderResponseLimit {
		return orderReceipt(command, orders.StateUnknown, response.StatusCode, "response_uncertain"), ErrOrderUnknown
	}
	if orderRejectedStatus(response.StatusCode) {
		return orderReceipt(command, orders.StateRejected, response.StatusCode, "provider_rejected"), nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return orderReceipt(command, orders.StateUnknown, response.StatusCode, "provider_uncertain"), ErrOrderUnknown
	}
	document, _, err := c.decodeOrderIdentity(raw, command.Correlation)
	if err != nil {
		return orderReceipt(command, orders.StateUnknown, response.StatusCode, "acknowledgement_unverified"), ErrOrderUnknown
	}
	receipt := orderReceipt(command, orders.StateAcknowledged, response.StatusCode, "provider_acknowledged")
	receipt.ExternalID = document.ID
	return receipt, nil
}

func orderRejectedStatus(status int) bool {
	switch status {
	case 400, 401, 403, 404, 405, 409, 412, 413, 415, 422, 429:
		return true
	}
	return false
}

func orderReceipt(command orders.Command, state orders.State, status int, code string) orders.Receipt {
	return orders.Receipt{CommandID: command.ID, State: state, HTTPStatus: status, Code: strings.ToUpper(code), ObservedAt: time.Now().UTC()}
}

// WriteOrderStatus updates the status of an existing MoySklad customer order.
// It is used after Ozon fulfillment is confirmed to write the confirmed status
// back to MoySklad. PUT is idempotent; callers must reconcile on unknown
// outcomes by re-reading the order before retrying.
func (c *OrderClient) WriteOrderStatus(ctx context.Context, scope orders.Scope, orderID string, stateID string) error {
	if scope.MoyskladAccount == "" || orderID == "" || stateID == "" {
		return orders.ErrInvalid
	}
	ctx, finish := c.boundedContext(ctx)
	defer finish()
	if err := c.verifyAccount(ctx); err != nil {
		return err
	}
	body := map[string]any{
		"state": map[string]any{
			"meta": map[string]any{
				"href":      "https://api.moysklad.ru/api/remap/1.2/entity/customerorder/metadata/states/" + stateID,
				"type":      "state",
				"mediaType": "application/json",
			},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal order status: %w", err)
	}
	u := c.client.base.ResolveReference(&url.URL{Path: "entity/customerorder/" + orderID})
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("build order status request: %w", err)
	}
	request.GetBody, request.Close = nil, true
	request.Header.Set("Authorization", "Bearer "+c.client.token)
	request.Header.Set("Accept", "application/json;charset=utf-8")
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client.http.Do(request)
	if err != nil {
		return fmt.Errorf("dispatch order status to MoySklad: %w", err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, orderResponseLimit+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || len(raw) > orderResponseLimit {
		return fmt.Errorf("read order status response: %w", ErrOrderUnknown)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("MoySklad rejected order status: HTTP %d", response.StatusCode)
	}
	var document orderNative
	if json.Unmarshal(raw, &document) != nil || document.ID != orderID {
		return ErrOrderUnknown
	}
	return nil
}
