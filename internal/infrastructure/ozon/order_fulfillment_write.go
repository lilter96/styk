package ozon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

// fulfillmentPath returns the POST path for one reviewed action, or empty when
// the saved operation subset does not contain a verified endpoint.
func fulfillmentPath(action orders.Action) string {
	switch action {
	case orders.ActionPostingPack, orders.ActionPostingSplit:
		return "/v4/posting/fbs/ship"
	case orders.ActionPostingCancel:
		return "/v2/posting/fbs/cancel"
	case orders.ActionPostingPartialCancel:
		return "/v2/posting/fbs/product/cancel"
	case orders.ActionPostingTracking:
		return "/v2/fbs/posting/tracking-number/set"
	case orders.ActionPostingShip:
		return "/v2/fbs/posting/delivering"
	case orders.ActionPostingLastMile:
		return "/v2/fbs/posting/last-mile"
	case orders.ActionPostingDeliver:
		return "/v2/fbs/posting/delivered"
	case orders.ActionPostingLabelCreate:
		return "/v3/posting/fbs/package-label/create"
	default:
		return ""
	}
}

// WriteOrder sends exactly one fulfillment mutation; it never retries.
func (c *OrdersClient) WriteOrder(ctx context.Context, command orders.Command) (orders.Receipt, error) {
	if c.ReadonlyReaderRequest {
		return orderReceipt(command, orders.StateRejected, 0, "read_only_client"), orders.ErrBlocked
	}
	if command.CustomerOrder != nil || command.PostingAction == nil || command.Action.IsMirror() || c.read == nil {
		return orderReceipt(command, orders.StateRejected, 0, "invalid_fulfillment_intent"), orders.ErrInvalid
	}
	path := fulfillmentPath(command.Action)
	if path == "" {
		return orderReceipt(command, orders.StateRejected, 0, "fulfillment_endpoint_unverified"), orders.ErrBlocked
	}
	body, err := fulfillmentPayload(command, path)
	if err != nil {
		return orderReceipt(command, orders.StateRejected, 0, "invalid_fulfillment_payload"), err
	}
	if err := c.verifyAccount(ctx); err != nil {
		return orderReceipt(command, orders.StateRejected, 0, "account_not_verified"), err
	}
	return c.dispatchFulfillment(ctx, command, path, body)
}

func (c *OrdersClient) verifyAccount(ctx context.Context) error {
	if c.read == nil || c.read.id == "" || c.scope.OzonAccount != c.read.id {
		return orders.ErrInvalid
	}
	return nil
}

func (c *OrdersClient) dispatchFulfillment(ctx context.Context, command orders.Command, path string, body map[string]any) (orders.Receipt, error) {
	if err := c.read.wait(ctx); err != nil {
		return orderReceipt(command, orders.StateRejected, 0, "dispatch_cancelled"), err
	}
	payload, err := json.Marshal(body)
	if err != nil || len(payload) > maxBody {
		return orderReceipt(command, orders.StateRejected, 0, "invalid_fulfillment_payload"), err
	}
	u := c.read.base.ResolveReference(&url.URL{Path: path})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return orderReceipt(command, orders.StateRejected, 0, "invalid_request"), err
	}
	request.Header.Set("Client-Id", c.read.id)
	request.Header.Set("Api-Key", c.read.key)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Close = true

	response, err := c.read.http.Do(request)
	if err != nil {
		return orderReceipt(command, orders.StateUnknown, 0, "transport_uncertain"), orders.ErrStale
	}
	return c.fulfillmentResponse(command, path, response)
}

func (c *OrdersClient) fulfillmentResponse(command orders.Command, path string, response *http.Response) (orders.Receipt, error) {
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || len(raw) > maxBody {
		return orderReceipt(command, orders.StateUnknown, response.StatusCode, "response_uncertain"), orders.ErrStale
	}
	if response.StatusCode >= 400 {
		return orderReceipt(command, orders.StateRejected, response.StatusCode, "provider_rejected"), nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return orderReceipt(command, orders.StateUnknown, response.StatusCode, "provider_uncertain"), orders.ErrStale
	}
	return c.parseFulfillmentReceipt(command, path, raw, response.StatusCode)
}

func (c *OrdersClient) parseFulfillmentReceipt(command orders.Command, path string, data []byte, status int) (orders.Receipt, error) {
	switch command.Action {
	case orders.ActionPostingPack, orders.ActionPostingSplit:
		return c.shipReceipt(command, data, status)
	case orders.ActionPostingCancel:
		return c.cancelReceipt(command, data, status)
	case orders.ActionPostingPartialCancel:
		return c.partialCancelReceipt(command, data, status)
	case orders.ActionPostingTracking:
		return c.trackingReceipt(command, data, status)
	case orders.ActionPostingLabelCreate:
		return c.labelCreateReceipt(command, data, status)
	default:
		return c.statusTransitionReceipt(command, data, status)
	}
}

func (c *OrdersClient) shipReceipt(command orders.Command, data []byte, status int) (orders.Receipt, error) {
	var response struct {
		Result []string `json:"result"`
	}
	if json.Unmarshal(data, &response) != nil || !slices.Contains(response.Result, command.PostingNumber) {
		return orderReceipt(command, orders.StateUnknown, status, "acknowledgement_unverified"), orders.ErrStale
	}
	return orderReceipt(command, orders.StateAcknowledged, status, "provider_acknowledged"), nil
}

func (c *OrdersClient) cancelReceipt(command orders.Command, data []byte, status int) (orders.Receipt, error) {
	var response struct {
		Result bool `json:"result"`
	}
	if json.Unmarshal(data, &response) != nil || !response.Result {
		return orderReceipt(command, orders.StateUnknown, status, "cancellation_result_unverified"), orders.ErrStale
	}
	return orderReceipt(command, orders.StateReconciled, status, "provider_confirmed_cancellation"), nil
}

func (c *OrdersClient) partialCancelReceipt(command orders.Command, data []byte, status int) (orders.Receipt, error) {
	var response struct {
		Result string `json:"result"`
	}
	if json.Unmarshal(data, &response) != nil || response.Result != command.PostingNumber {
		return orderReceipt(command, orders.StateUnknown, status, "partial_cancellation_result_unverified"), orders.ErrStale
	}
	return orderReceipt(command, orders.StateAcknowledged, status, "provider_acknowledged"), nil
}

func (c *OrdersClient) trackingReceipt(command orders.Command, data []byte, status int) (orders.Receipt, error) {
	var response struct {
		Result []struct {
			PostingNumber string `json:"posting_number"`
			Result        bool   `json:"result"`
			Error         string `json:"error"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &response) != nil || len(response.Result) != 1 || response.Result[0].PostingNumber != command.PostingNumber {
		return orderReceipt(command, orders.StateUnknown, status, "tracking_confirmation_unverified"), orders.ErrStale
	}
	if !response.Result[0].Result {
		code := "tracking_rejected"
		if response.Result[0].Error != "" {
			code = "tracking_rejected:" + ordersSafeString(response.Result[0].Error)
		}
		return orderReceipt(command, orders.StateRejected, status, code), nil
	}
	return orderReceipt(command, orders.StateReconciled, status, "provider_confirmed_tracking"), nil
}

func (c *OrdersClient) labelCreateReceipt(command orders.Command, data []byte, status int) (orders.Receipt, error) {
	var response struct {
		Tasks []struct {
			TaskID   int64  `json:"task_id"`
			TaskType string `json:"task_type"`
		} `json:"tasks"`
	}
	if json.Unmarshal(data, &response) != nil || len(response.Tasks) == 0 {
		return orderReceipt(command, orders.StateUnknown, status, "label_task_unverified"), orders.ErrStale
	}
	receipt := orderReceipt(command, orders.StatePolling, status, "provider_label_pending")
	receipt.TaskID = formatInt64(response.Tasks[0].TaskID)
	return receipt, nil
}

func (c *OrdersClient) statusTransitionReceipt(command orders.Command, data []byte, status int) (orders.Receipt, error) {
	var response struct {
		Result []struct {
			PostingNumber string `json:"posting_number"`
			Result        bool   `json:"result"`
			Error         string `json:"error"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &response) != nil || len(response.Result) != 1 || response.Result[0].PostingNumber != command.PostingNumber {
		return orderReceipt(command, orders.StateUnknown, status, "status_transition_unverified"), orders.ErrStale
	}
	if !response.Result[0].Result {
		code := "status_transition_rejected"
		if response.Result[0].Error != "" {
			code = "status_transition_rejected:" + ordersSafeString(response.Result[0].Error)
		}
		return orderReceipt(command, orders.StateRejected, status, code), nil
	}
	return orderReceipt(command, orders.StateAcknowledged, status, "provider_acknowledged"), nil
}

// BindOrderIntent begins the durable dispatch intent for a fulfillment command.
// It invokes the runner's authorization guard (Begin: true) to record that the
// command is about to be sent to the provider. The guard must be entered before
// WriteOrder is called; otherwise the runner treats the dispatch as rejected.
func (c *OrdersClient) BindOrderIntent(command orders.Command, authorize func(context.Context) (bool, error)) error {
	if authorize == nil {
		return orders.ErrInvalid
	}
	allowed, err := authorize(context.Background())
	if err != nil || !allowed {
		return orders.ErrBlocked
	}
	return nil
}

func fulfillmentPayload(command orders.Command, path string) (map[string]any, error) {
	if command.PostingAction == nil || command.PostingAction.Parameters.HasPrivateValues() {
		return nil, orders.ErrInvalid
	}
	switch command.Action {
	case orders.ActionPostingPack, orders.ActionPostingSplit:
		return shipPayload(command)
	case orders.ActionPostingCancel:
		return cancelPayload(command)
	case orders.ActionPostingPartialCancel:
		return partialCancelPayload(command)
	case orders.ActionPostingTracking:
		return trackingPayload(command)
	case orders.ActionPostingLabelCreate:
		return labelCreatePayload(command)
	default:
		return statusTransitionPayload(command)
	}
}

func shipPayload(command orders.Command) (map[string]any, error) {
	packages := make([]map[string]any, 0, len(command.PostingAction.Parameters.Packages))
	for _, box := range command.PostingAction.Parameters.Packages {
		products := make([]map[string]any, 0, len(box.Lines))
		for _, line := range box.Lines {
			parsed, err := parseSKU(line.Identity.SKU)
			if err != nil {
				return nil, orders.ErrInvalid
			}
			qty, err := parseQuantity(line.Quantity)
			if err != nil || qty <= 0 {
				return nil, orders.ErrInvalid
			}
			products = append(products, map[string]any{"product_id": parsed, "quantity": qty})
		}
		packages = append(packages, map[string]any{"products": products})
	}
	return map[string]any{"posting_number": command.PostingNumber, "packages": packages}, nil
}

func cancelPayload(command orders.Command) (map[string]any, error) {
	reasonID, err := parseSKU(command.PostingAction.Parameters.ReasonID)
	if err != nil {
		return nil, orders.ErrInvalid
	}
	body := map[string]any{"posting_number": command.PostingNumber, "cancel_reason_id": reasonID}
	if command.PostingAction.Parameters.ReasonMessageProvided {
		body["cancel_reason_message"] = ""
	}
	return body, nil
}

func partialCancelPayload(command orders.Command) (map[string]any, error) {
	reasonID, err := parseSKU(command.PostingAction.Parameters.ReasonID)
	if err != nil {
		return nil, orders.ErrInvalid
	}
	items := make([]map[string]any, 0, len(command.PostingAction.Parameters.Lines))
	for _, line := range command.PostingAction.Parameters.Lines {
		parsed, err := parseSKU(line.Identity.SKU)
		if err != nil {
			return nil, orders.ErrInvalid
		}
		qty, err := parseQuantity(line.Quantity)
		if err != nil || qty <= 0 {
			return nil, orders.ErrInvalid
		}
		items = append(items, map[string]any{"sku": parsed, "quantity": qty})
	}
	body := map[string]any{"posting_number": command.PostingNumber, "cancel_reason_id": reasonID, "cancel_reason_message": "", "items": items}
	return body, nil
}

func trackingPayload(command orders.Command) (map[string]any, error) {
	if command.PostingAction == nil || command.PostingAction.Parameters.TrackingNumber == "" {
		return nil, orders.ErrInvalid
	}
	return map[string]any{"tracking_numbers": []map[string]any{
		{"posting_number": command.PostingNumber, "tracking_number": command.PostingAction.Parameters.TrackingNumber, "delivery_service": "styk"},
	}}, nil
}

func labelCreatePayload(command orders.Command) (map[string]any, error) {
	body := map[string]any{"posting_numbers": []string{command.PostingNumber}}
	if command.PostingAction != nil && command.PostingAction.Parameters.LabelType != "" {
		body["label_type"] = command.PostingAction.Parameters.LabelType
	}
	return body, nil
}

func statusTransitionPayload(command orders.Command) (map[string]any, error) {
	return map[string]any{"posting_number": []string{command.PostingNumber}}, nil
}

func parseSKU(value string) (int64, error) {
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, orders.ErrInvalid
		}
	}
	var result int64
	for _, char := range value {
		result = result*10 + int64(char-'0')
	}
	if result <= 0 || result > 1<<63-1 {
		return 0, orders.ErrInvalid
	}
	return result, nil
}

func parseQuantity(value orders.Decimal) (int64, error) {
	s := string(value)
	if strings.Contains(s, ".") || strings.HasPrefix(s, "-") || strings.Contains(s, "e") || strings.Contains(s, "E") {
		return 0, orders.ErrInvalid
	}
	for _, char := range s {
		if char < '0' || char > '9' {
			return 0, orders.ErrInvalid
		}
	}
	var result int64
	for _, char := range s {
		result = result*10 + int64(char-'0')
	}
	if result <= 0 || result > 2147483647 {
		return 0, orders.ErrInvalid
	}
	return result, nil
}

func formatInt64(value int64) string {
	var buf [20]byte
	i := len(buf)
	sign := value < 0
	if sign {
		value = -value
	}
	for {
		i--
		buf[i] = byte(value%10 + '0')
		value /= 10
		if value == 0 {
			break
		}
	}
	if sign {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func orderReceipt(command orders.Command, state orders.State, status int, code string) orders.Receipt {
	return orders.Receipt{CommandID: command.ID, State: state, HTTPStatus: status, Code: strings.ToUpper(code), ObservedAt: time.Now().UTC()}
}

func ordersSafeString(value string) string {
	if len(value) > 64 {
		return value[:64]
	}
	return value
}
