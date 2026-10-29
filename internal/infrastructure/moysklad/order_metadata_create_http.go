package moysklad

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

func (c *OrderClient) dispatchOrderAttribute(ctx context.Context, intent orders.MetadataCreationIntent, authorize func(context.Context) (bool, error)) (orders.MetadataCreationCompletion, error) {
	completion := orderAttributeCompletion(intent, orders.StateRejected)
	if err := c.client.wait(ctx); err != nil {
		return completion, err
	}
	started := false
	guard := func(ctx context.Context) error {
		if !c.validOrderAttributeIntent(intent.Scope, intent) {
			return ErrOrderIntent
		}
		allowed, err := authorize(ctx)
		if err != nil || !allowed {
			return ErrOrderIntent
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		started = true
		return nil
	}
	request, err := c.orderAttributeRequest(providercall.WithGuard(ctx, guard), intent)
	if err != nil {
		return completion, err
	}
	bound, _ := ctx.Value(budgetKey{}).(*budget)
	if _, coordinated := bound.http.Transport.(providercall.Transport); !coordinated {
		if !catalogLoopback(c.client.base) {
			return completion, ErrOrderIntent
		}
		if err = guard(ctx); err != nil {
			return completion, err
		}
	}
	response, err := bound.http.Do(request)
	if err != nil {
		if !started || providercall.IsNotDispatched(err) {
			return completion, ErrOrderIntent
		}
		completion.State = orders.StateUnknown
		return completion, ErrOrderUnknown
	}
	return c.readOrderAttributeResponse(ctx, intent, response)
}

func (c *OrderClient) orderAttributeRequest(ctx context.Context, intent orders.MetadataCreationIntent) (*http.Request, error) {
	body := []struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		Required bool   `json:"required"`
	}{{Name: intent.Marker, Type: intent.Type, Required: false}}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, ErrOrderIntent
	}
	u := c.client.base.ResolveReference(&url.URL{Path: "entity/customerorder/metadata/attributes"})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(raw))
	if err != nil {
		return nil, ErrOrderIntent
	}
	request.GetBody, request.Close = nil, true
	request.Header.Set("Authorization", "Bearer "+c.client.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json;charset=utf-8")
	return request, nil
}

func (c *OrderClient) readOrderAttributeResponse(ctx context.Context, intent orders.MetadataCreationIntent, response *http.Response) (orders.MetadataCreationCompletion, error) {
	completion := orderAttributeCompletion(intent, orders.StateUnknown)
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, orderResponseLimit+1))
	closeErr := response.Body.Close()
	c.client.deferRequests(rateDelay(response.Header, response.StatusCode, time.Now()))
	if readErr != nil || closeErr != nil || len(raw) > orderResponseLimit {
		return completion, ErrOrderUnknown
	}
	if response.StatusCode != http.StatusOK {
		if orderRejectedStatus(response.StatusCode) {
			completion.State = orders.StateRejected
		}
		return completion, orderAttributeResponseError(response.StatusCode)
	}
	if orderCheckJSON(raw) != nil {
		completion.Code = "POST_RESPONSE_UNVERIFIED"
		return completion, ErrOrderUnknown
	}
	reported, responseErr := c.reportedOrderAttributeID(raw, intent)
	observed, err := c.findOrderAttribute(ctx, intent)
	if err != nil || observed == "" {
		return completion, ErrOrderUnknown
	}
	confirmed, err := c.confirmOrderAttribute(ctx, intent, observed)
	if err != nil {
		return completion, ErrOrderUnknown
	}
	if errors.Is(responseErr, ErrOrderIdentity) || (reported != "" && observed != reported) {
		completion.Code = "POST_RESPONSE_IDENTITY_CONFLICT"
		return completion, ErrOrderUnknown
	}
	if responseErr != nil {
		confirmed.Code = "CONFIRMED_BY_MARKER_AFTER_RESPONSE_DRIFT"
	}
	return confirmed, nil
}

func (c *OrderClient) reportedOrderAttributeID(raw []byte, intent orders.MetadataCreationIntent) (string, error) {
	var attributes []orderCreatedAttribute
	if json.Unmarshal(raw, &attributes) != nil || len(attributes) == 0 {
		return "", ErrOrderSchema
	}
	if len(attributes) != 1 {
		return "", ErrOrderIdentity
	}
	attribute := attributes[0]
	if attribute.ID == "" {
		if (attribute.Name != "" && attribute.Name != intent.Marker) || (attribute.Type != "" && attribute.Type != intent.Type) || (attribute.AccountID != "" && attribute.AccountID != intent.Scope.MoyskladAccount) {
			return "", ErrOrderIdentity
		}
		return "", ErrOrderSchema
	}
	return c.orderAttributeIdentity(attribute, intent, true)
}
