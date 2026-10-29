package moysklad

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/lilter96/styk/internal/domain"
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

// Identity reads only the authenticated employee context. accountId is required
// in the official response, including accounts with an empty product catalog.
func (c *Client) Identity(ctx context.Context) (string, error) {
	ctx = context.WithValue(ctx, budgetKey{}, &budget{http: c.http, limits: domain.ImportLimits{Requests: 1, Attempts: 1, ResponseBytes: 64 << 10}})
	raw, err := c.get(ctx, "context/employee", nil)
	if err != nil {
		return "", err
	}
	var response struct {
		AccountID string `json:"accountId"`
	}
	if json.Unmarshal(raw, &response) != nil || !orderUUID.MatchString(response.AccountID) {
		return "", errors.New("invalid account identity")
	}
	return response.AccountID, nil
}
