package moysklad

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
)

// Probe performs one bounded product GET; an empty result cannot verify account identity.
func (c *Client) Probe(ctx context.Context, account string) (bool, error) {
	raw, err := c.get(ctx, "entity/product", url.Values{"limit": {"1"}, "offset": {"0"}})
	if err != nil {
		return false, err
	}
	var p page
	if json.Unmarshal(raw, &p) != nil || p.Meta == nil || p.Rows == nil || len(*p.Rows) > 1 {
		return false, errors.New("invalid probe envelope")
	}
	verified := false
	for _, r := range *p.Rows {
		var e entity
		if json.Unmarshal(r, &e) != nil || e.AccountID != account {
			return false, errors.New("probe account mismatch")
		}
		verified = true
	}
	return verified, nil
}
