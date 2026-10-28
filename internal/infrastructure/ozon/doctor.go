package ozon

import (
	"context"
	"encoding/json"
	"errors"
)

// Probe invokes a single allowlisted catalog read; it does not traverse or import.
func (c *Client) Probe(ctx context.Context) error {
	var out struct {
		Result *struct {
			Items *[]json.RawMessage `json:"items"`
		} `json:"result"`
	}
	if err := c.post(ctx, productPath, map[string]any{"filter": map[string]string{"visibility": "ALL"}, "last_id": "", "limit": 1}, &out); err != nil {
		return err
	}
	if out.Result == nil || out.Result.Items == nil || len(*out.Result.Items) > 1 {
		return errors.New("invalid probe envelope")
	}
	return nil
}
