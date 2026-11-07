package moysklad

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

var _ orders.CommissionReader = (*OrderClient)(nil)
var _ orders.CommissionWriter = (*OrderClient)(nil)

// WriteCommissionDraft admits exactly one POST; uncertain writes are reconciled
// through ReadCommissionDraft, never retried or corrected with PUT.
func (c *OrderClient) WriteCommissionDraft(ctx context.Context, plan orders.CommissionPlan, authorize func(context.Context) (bool, error)) (orders.Receipt, error) {
	if !c.consumeCommissionIntent(plan, authorize) {
		return commissionReceipt(plan, orders.StateRejected, 0, "intent_not_authorized"), ErrOrderIntent
	}
	body, err := c.commissionPayload(plan)
	if err != nil {
		return commissionReceipt(plan, orders.StateRejected, 0, "invalid_payload"), err
	}
	ctx, finish := c.boundedContext(ctx)
	defer finish()
	if err = c.commissionPreflight(ctx, plan); err != nil {
		return commissionReceipt(plan, orders.StateRejected, 0, "preflight_blocked"), err
	}
	return c.dispatchCommission(ctx, plan, body, authorize)
}

func (c *OrderClient) consumeCommissionIntent(plan orders.CommissionPlan, authorize func(context.Context) (bool, error)) bool {
	if !c.scopeAllowed(plan.Scope) || plan.Validate() != nil || authorize == nil || !time.Now().Before(plan.ExpiresAt) {
		return false
	}
	if transport, ok := c.client.http.Transport.(providercall.Transport); ok && transport.Scope.CredentialRevision != plan.Credentials.Moysklad {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.used || c.intent != nil {
		return false
	}
	c.used = true
	return true
}

func (c *OrderClient) commissionPreflight(ctx context.Context, plan orders.CommissionPlan) error {
	if err := c.verifyAccount(ctx); err != nil {
		return err
	}
	refs, err := c.readCommissionReferences(ctx, plan.Policy)
	if err != nil {
		return err
	}
	if refs.Revision != plan.References.Revision || refs.OrganizationAccountID != plan.References.OrganizationAccountID || refs.AgentAccountID != plan.References.AgentAccountID {
		return orders.ErrStale
	}
	lines := append(append([]orders.CommissionLine{}, plan.Lines...), plan.Returns...)
	seen := make(map[string]bool, len(lines))
	for _, line := range lines {
		key := line.Mapping.MoyskladKind + ":" + line.Mapping.MoyskladID
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, _, err = c.commissionEntity(ctx, line.Mapping.MoyskladKind, line.Mapping.MoyskladID); err != nil {
			return err
		}
	}
	_, err = c.lookupCommission(ctx, plan)
	if errors.Is(err, orders.ErrNotFound) {
		return nil
	}
	if err == nil {
		return orders.ErrConflict
	}
	return err
}

func (c *OrderClient) dispatchCommission(ctx context.Context, plan orders.CommissionPlan, body map[string]any, authorize func(context.Context) (bool, error)) (orders.Receipt, error) {
	if err := c.client.wait(ctx); err != nil {
		return commissionReceipt(plan, orders.StateRejected, 0, "dispatch_cancelled"), err
	}
	started := false
	guard := func(ctx context.Context) error {
		allowed, err := authorize(ctx)
		if err != nil {
			return err
		}
		if !allowed {
			return ErrOrderIntent
		}
		started = true
		return nil
	}
	ctx = providercall.WithGuard(providercall.WithTarget(ctx, plan.ExternalCode), guard)
	request, err := c.commissionWriteRequest(ctx, body)
	if err != nil {
		return commissionReceipt(plan, orders.StateRejected, 0, "invalid_request"), err
	}
	bound, ok := ctx.Value(budgetKey{}).(*budget)
	if !ok {
		return commissionReceipt(plan, orders.StateRejected, 0, "missing_budget"), ErrOrderIntent
	}
	if _, coordinated := bound.http.Transport.(providercall.Transport); !coordinated {
		if err = guard(ctx); err != nil {
			return commissionReceipt(plan, orders.StateRejected, 0, "intent_not_authorized"), err
		}
	}
	response, err := bound.http.Do(request)
	if err != nil {
		if !started || providercall.IsNotDispatched(err) {
			return commissionReceipt(plan, orders.StateRejected, 0, "dispatch_not_started"), ErrOrderIntent
		}
		return commissionReceipt(plan, orders.StateUnknown, 0, "transport_uncertain"), ErrOrderUnknown
	}
	return c.commissionResponse(plan, response)
}

func (c *OrderClient) commissionWriteRequest(ctx context.Context, body map[string]any) (*http.Request, error) {
	raw, err := json.Marshal(body)
	if err != nil || len(raw) > orderResponseLimit {
		return nil, orders.ErrInvalid
	}
	u := c.client.base.ResolveReference(&url.URL{Path: "entity/commissionreportin"})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(raw))
	if err != nil {
		return nil, orders.ErrInvalid
	}
	request.GetBody, request.Close = nil, true
	request.Header.Set("Authorization", "Bearer "+c.client.token)
	request.Header.Set("Accept", "application/json;charset=utf-8")
	request.Header.Set("Accept-Encoding", "gzip")
	request.Header.Set("Content-Type", "application/json")
	return request, nil
}

func (c *OrderClient) commissionResponse(plan orders.CommissionPlan, response *http.Response) (orders.Receipt, error) {
	raw, err := readOrderResponseBody(response)
	c.client.deferRequests(rateDelay(response.Header, response.StatusCode, time.Now()))
	if err != nil {
		return commissionReceipt(plan, orders.StateUnknown, response.StatusCode, "response_uncertain"), ErrOrderUnknown
	}
	if orderRejectedStatus(response.StatusCode) {
		return commissionReceipt(plan, orders.StateRejected, response.StatusCode, "provider_rejected"), nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return commissionReceipt(plan, orders.StateUnknown, response.StatusCode, "provider_uncertain"), ErrOrderUnknown
	}
	document, _, err := c.decodeCommissionIdentity(raw, plan, "")
	if err != nil {
		return commissionReceipt(plan, orders.StateUnknown, response.StatusCode, "acknowledgement_unverified"), ErrOrderUnknown
	}
	receipt := commissionReceipt(plan, orders.StateAcknowledged, response.StatusCode, "commission_draft_acknowledged")
	receipt.ExternalID = document.ID
	return receipt, nil
}

func commissionReceipt(plan orders.CommissionPlan, state orders.State, status int, code string) orders.Receipt {
	return orders.Receipt{CommandID: plan.ID, State: state, HTTPStatus: status, Code: strings.ToUpper(code), ObservedAt: time.Now().UTC()}
}
