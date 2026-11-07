package ozon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sync/atomic"

	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

const carriageCreatePath = "/v1/carriage/create"
const carriageApprovePath = "/v1/carriage/approve"

func (c *OrdersClient) WriteCarriage(ctx context.Context, plan orders.CarriagePlan, authorize func(context.Context) (bool, error)) (orders.Receipt, error) {
	if c.read == nil || !c.read.coordinated || plan.Scope != c.scope || plan.Validate() != nil || authorize == nil || !plan.ExpiresAt.After(c.now()) {
		return c.carriageReceipt(plan, orders.StateRejected, 0, "CARRIAGE_INTENT_INVALID"), orders.ErrBlocked
	}
	actual, err := c.ReadCarriageBatch(ctx, plan.Scope, plan.CarriageInput)
	if err != nil || !plan.MatchesMembers(actual) || actual.Fingerprint != plan.Snapshot.Fingerprint {
		return c.carriageReceipt(plan, orders.StateRejected, 0, "CARRIAGE_PREFLIGHT_CHANGED"), orders.ErrStale
	}
	path, body := carriageWritePayload(plan)
	payload, err := json.Marshal(body)
	if err != nil {
		return c.carriageReceipt(plan, orders.StateRejected, 0, "CARRIAGE_PAYLOAD_INVALID"), err
	}
	if err = c.read.wait(ctx); err != nil {
		return c.carriageReceipt(plan, orders.StateRejected, 0, "CARRIAGE_NOT_DISPATCHED"), err
	}
	u := c.read.base.ResolveReference(&url.URL{Path: path})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return c.carriageReceipt(plan, orders.StateRejected, 0, "CARRIAGE_REQUEST_INVALID"), err
	}
	request.Header.Set("Client-Id", c.read.id)
	request.Header.Set("Api-Key", c.read.key)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Close = true
	var entered atomic.Bool
	guard := func(check context.Context) error {
		if !entered.CompareAndSwap(false, true) {
			return orders.ErrConflict
		}
		allowed, e := authorize(check)
		if e != nil {
			return e
		}
		if !allowed {
			return orders.ErrBlocked
		}
		return nil
	}
	response, err := c.read.http.Do(request.WithContext(providercall.WithGuard(ctx, guard)))
	if err != nil {
		return c.carriageReceipt(plan, orders.StateUnknown, 0, "CARRIAGE_TRANSPORT_UNCERTAIN"), err
	}
	return c.carriageWriteResponse(plan, response)
}

func carriageWritePayload(plan orders.CarriagePlan) (string, map[string]any) {
	if plan.Action == orders.CarriageApprove {
		body := map[string]any{"carriage_id": jsonCarriageID(plan.CarriageID)}
		if plan.ContainersCount != nil {
			body["containers_count"] = *plan.ContainersCount
		}
		return carriageApprovePath, body
	}
	body := map[string]any{"delivery_method_id": jsonCarriageID(plan.DeliveryMethodID), "all_blr_traceable": *plan.AllBLRTraceable}
	if plan.DepartureDate != "" {
		body["departure_date"] = plan.DepartureDate
	}
	return carriageCreatePath, body
}

func (c *OrdersClient) carriageWriteResponse(plan orders.CarriagePlan, response *http.Response) (orders.Receipt, error) {
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	closeErr := response.Body.Close()
	if err != nil || closeErr != nil || len(data) > maxBody || response.StatusCode != http.StatusOK {
		return c.carriageReceipt(plan, orders.StateUnknown, response.StatusCode, "CARRIAGE_RESPONSE_UNCERTAIN"), orders.ErrStale
	}
	receipt := c.carriageReceipt(plan, orders.StateAcknowledged, response.StatusCode, "CARRIAGE_ACKNOWLEDGED_REQUIRES_READBACK")
	if plan.Action == orders.CarriageApprove {
		if len(bytes.TrimSpace(data)) != 0 && string(bytes.TrimSpace(data)) != "{}" {
			return c.carriageReceipt(plan, orders.StateUnknown, response.StatusCode, "CARRIAGE_APPROVAL_ACK_UNVERIFIED"), orders.ErrSchemaDrift
		}
		receipt.ExternalID = plan.CarriageID
		return receipt, nil
	}
	f := newOrderFields(data)
	id := orderKnown(f.id("carriage_id"))
	if *f.err != nil || !orders.CarriageIDValid(id) || len(f.raw) != 1 {
		return c.carriageReceipt(plan, orders.StateUnknown, response.StatusCode, "CARRIAGE_CREATE_ACK_UNVERIFIED"), orders.ErrSchemaDrift
	}
	receipt.ExternalID = id
	return receipt, nil
}

func (c *OrdersClient) carriageReceipt(plan orders.CarriagePlan, state orders.State, status int, code string) orders.Receipt {
	return orders.Receipt{CommandID: plan.ID, State: state, HTTPStatus: status, Code: code, ObservedAt: c.now()}
}
