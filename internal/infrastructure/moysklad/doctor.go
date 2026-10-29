package moysklad

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
)

var ErrProbeAccountMismatch = errors.New("probe account mismatch")

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
		if json.Unmarshal(r, &e) != nil {
			return false, errors.New("invalid probe product")
		}
		if e.AccountID != account {
			return false, ErrProbeAccountMismatch
		}
		verified = true
	}
	return verified, nil
}
