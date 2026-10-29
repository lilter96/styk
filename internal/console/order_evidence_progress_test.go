package console

import (
	"context"
	"errors"
	"testing"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
)

func TestOrderEvidenceFailureClassification(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name             string
		err              error
		rates            int
		status, resource string
	}{
		{"success", nil, 0, "IN_PROGRESS", ""},
		{"source budget", &moysklad.LimitError{Resource: "pages"}, 0, "LIMIT_REACHED", "pages"},
		{"market budget", &ozon.LimitError{Resource: "requests"}, 0, "LIMIT_REACHED", "requests"},
		{"unknown resource is sanitized", &ozon.LimitError{Resource: "unsafe\x00value"}, 0, "LIMIT_REACHED", "other"},
		{"retry budget after rate limit", &ozon.LimitError{Resource: "attempts"}, 1, "RATE_LIMITED", ""},
		{"rate limit", &moysklad.APIError{Status: 429}, 0, "RATE_LIMITED", ""},
		{"source authorization", &moysklad.APIError{Status: 401}, 0, "AUTHORIZATION_REQUIRED", ""},
		{"market forbidden", &ozon.APIError{Status: 403}, 0, "AUTHORIZATION_REQUIRED", ""},
		{"server error", &ozon.APIError{Status: 503}, 0, "UNAVAILABLE", ""},
		{"transport error", moysklad.ErrTransport, 0, "UNAVAILABLE", ""},
		{"duration", context.DeadlineExceeded, 0, "LIMIT_REACHED", "duration"},
		{"invalid pagination", errors.New("invalid pagination"), 0, "INVALID_RESPONSE", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			status, resource := orderAttemptStatus(tc.err, tc.rates)
			if status != tc.status || resource != tc.resource {
				t.Fatalf("got %s/%s, want %s/%s", status, resource, tc.status, tc.resource)
			}
		})
	}
}

func TestOrderEvidenceLockKeyDisambiguatesScopes(t *testing.T) {
	t.Parallel()
	left := domain.StockSyncScope{Tenant: "t", MoyskladAccount: "m", OzonAccount: "o:x"}
	right := domain.StockSyncScope{Tenant: "t", MoyskladAccount: "m:o", OzonAccount: "x"}
	if orderEvidenceLockKey(left) == orderEvidenceLockKey(right) {
		t.Fatal("different account pairs share a lock key")
	}
}
