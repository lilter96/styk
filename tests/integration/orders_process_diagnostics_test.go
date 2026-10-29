//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

// Failure diagnostics compare field names only. Provider bodies, buyer data,
// credentials and database connection strings are never printed.
func (s *ordersProcessStack) reportSource(t *testing.T, number string) {
	t.Helper()
	for _, worker := range s.workers {
		_, raw := worker.request(t, "GET", "/__test/state", nil)
		var state struct {
			SourceError string `json:"source_error"`
		}
		if json.Unmarshal(raw, &state) == nil && state.SourceError != "" {
			t.Logf("source diagnostics from %s: %s", worker.settings.ID, state.SourceError)
		}
	}
	var raw []byte
	var work orders.Work
	err := s.db.Pool.QueryRow(context.Background(), `SELECT work FROM orders_work WHERE work->'command'->>'posting_number'=$1 ORDER BY created_at DESC LIMIT 1`, number).Scan(&raw)
	if err != nil || json.Unmarshal(raw, &work) != nil {
		t.Log("source diagnostics: durable work unavailable")
		return
	}
	preview, err := s.db.OrderPreview(context.Background(), work.Scope, work.PreviewID)
	if err != nil {
		t.Log("source diagnostics: saved preview unavailable")
		return
	}
	factory := ordersProcessFactory(t, s.db, s.provider.server.URL)
	binding, err := factory.ResolveOrder(context.Background(), work.Scope, "ozon", work.Scope.OzonAccount, work.Credentials.Ozon)
	if err != nil {
		t.Log("source diagnostics: binding unavailable")
		return
	}
	fresh, err := binding.Client.ReadOrderPosting(context.Background(), work.Scope, number)
	if err != nil {
		t.Logf("source diagnostics: fresh synthetic read failed: %v", err)
		return
	}
	for _, decision := range preview.Decisions {
		if decision.Posting.PostingNumber == number {
			t.Logf("source diagnostics: differing top-level fact fields: %v", ordersProcessSourceDifference(decision.Posting, fresh))
		}
	}
}

func ordersProcessSourceDifference(left, right orders.Posting) []string {
	before := ordersProcessSourceFields(left)
	after := ordersProcessSourceFields(right)
	changed := make([]string, 0)
	for key, value := range before {
		if string(value) != string(after[key]) {
			changed = append(changed, key)
		}
	}
	slices.Sort(changed)
	return changed
}

func ordersProcessSourceFields(posting orders.Posting) map[string]json.RawMessage {
	posting.Evidence.Revision, posting.Evidence.CollectionID, posting.Evidence.CollectionKind = "", "", ""
	posting.Evidence.ObservedAt, posting.Evidence.CheckedAt, posting.Evidence.LocalSequence = time.Time{}, time.Time{}, 0
	posting.PII, posting.Customer = nil, orders.ProtectedCustomer{}
	if posting.DeliveryProfile != nil {
		profile := *posting.DeliveryProfile
		profile.ObservedAt, profile.ValidUntil = time.Time{}, time.Time{}
		posting.DeliveryProfile = &profile
	}
	raw, err := json.Marshal(posting)
	if err != nil {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	return fields
}
