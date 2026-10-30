package ozon

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/lilter96/styk/internal/orders"
)

// ReadOrderActionReasons uses Ozon's posting-specific FBS/rFBS reasons, not
// the similarly named Ozon Delivery cancellation endpoint.
func (c *OrdersClient) ReadOrderActionReasons(ctx context.Context, scope orders.Scope, number string) ([]orders.ActionReason, error) {
	if scope != c.scope || strings.TrimSpace(number) == "" || len(number) > 200 || strings.ContainsAny(number, "\r\n") {
		return nil, orders.ErrInvalid
	}
	data, err := c.orderPost(ctx, "/v1/posting/fbs/cancel-reason", map[string]any{"related_posting_numbers": []string{number}})
	if err != nil {
		return nil, err
	}
	return decodeActionReasons(data, number)
}

func decodeActionReasons(data []byte, number string) ([]orders.ActionReason, error) {
	var response struct {
		Result []struct {
			PostingNumber string `json:"posting_number"`
			Reasons       []struct {
				ID    json.Number `json:"id"`
				Title string      `json:"title"`
				Type  string      `json:"type_id"`
			} `json:"reasons"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &response) != nil || len(response.Result) != 1 || response.Result[0].PostingNumber != number || response.Result[0].Reasons == nil || len(response.Result[0].Reasons) > 100 {
		return nil, errOrderPayload
	}
	result := make([]orders.ActionReason, 0, len(response.Result[0].Reasons))
	seen := make(map[string]bool, len(response.Result[0].Reasons))
	for _, reason := range response.Result[0].Reasons {
		id, err := strconv.ParseInt(string(reason.ID), 10, 64)
		if err != nil || id <= 0 || seen[string(reason.ID)] || len(reason.Title) > 512 || strings.TrimSpace(reason.Title) == "" || strings.ContainsAny(reason.Title, "\x00\r\n") {
			return nil, errOrderPayload
		}
		seen[string(reason.ID)] = true
		result = append(result, orders.ActionReason{ID: string(reason.ID), Title: reason.Title})
	}
	return result, nil
}
