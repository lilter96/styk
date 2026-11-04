package orderflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func privateDispatchedFlow(t *testing.T) (*flowStore, *flowProvider, *flowProvider, *Runner) {
	t.Helper()
	review := flowReview(t, "posting")
	review.Config.PIIEnabled, review.Config.PIIRetentionSeconds = true, 3600
	review.Config.FieldMappings = append(review.Config.FieldMappings, orders.FieldMapping{Source: "customer.address", Target: "shipment_address"})
	review.Postings[0].Customer = orders.ProtectedCustomer{Reference: "protected-reference", Schema: "customer-v1", Revision: 1, Presence: orders.PresenceValue}
	store := &flowStore{review: review}
	customer := &orders.Customer{Name: orders.Present("PRIVATE_BUYER"), Address: orders.Present(orders.Address{City: orders.Present("PRIVATE_CITY")})}
	source := &flowProvider{posting: review.Postings[0]}
	source.posting.Customer = orders.ProtectedCustomer{Presence: orders.PresenceValue}
	source.posting.PII = customer
	target := &flowProvider{correlateErr: orders.ErrNotFound, receipt: orders.Receipt{State: orders.StateAcknowledged, HTTPStatus: 200, ExternalID: "native-order", ObservedAt: flowTestNow}}
	resolver := flowResolver{source: source, target: target}
	reader := flowCustomerReader{customer: customer}
	service, err := NewService(store, resolver, ServiceOptions{AllowScope: func(orders.Scope) bool { return true }, Now: func() time.Time { return flowTestNow }, CustomerReader: reader})
	if err != nil {
		t.Fatal(err)
	}
	preview := flowPreview(t, service, review)
	work := flowWork(t, preview)
	store.work = &work
	runner := flowRunner(t, store, resolver, service)
	runner.options.CustomerReader = reader
	return store, source, target, runner
}

func TestRunnerFreshPrivateValuesComparedBeforeDispatch(t *testing.T) {
	t.Parallel()
	tests := privateFreshCases()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store, source, target, runner := privateDispatchedFlow(t)
			if tt.change != nil {
				tt.change(source, runner)
			}
			runFlow(t, runner)
			if (target.writes == 1) != tt.allowed || (store.intents == 1) != tt.allowed {
				t.Fatalf("private freshness bypassed: state=%s writes=%d intents=%d", store.work.State, target.writes, store.intents)
			}
			if !tt.allowed && store.work.State != orders.StateBlocked {
				t.Fatal("changed unsubmitted personal data was not blocked")
			}
			raw, err := json.Marshal(store.work)
			if err != nil || strings.Contains(string(raw), "PRIVATE_") {
				t.Fatal("private values escaped durable work")
			}
		})
	}
}

type privateFreshCase struct {
	name    string
	change  func(*flowProvider, *Runner)
	allowed bool
}

func privateFreshCases() []privateFreshCase {
	return []privateFreshCase{
		{name: "same data without provider STYK reference", allowed: true},
		{name: "changed buyer", change: func(p *flowProvider, _ *Runner) {
			copy := *p.posting.PII
			copy.Name = orders.Present("NEW_BUYER")
			p.posting.PII = &copy
		}},
		{name: "changed address", change: func(p *flowProvider, _ *Runner) {
			copy := *p.posting.PII
			copy.Address = orders.Present(orders.Address{City: orders.Present("OTHER_CITY")})
			p.posting.PII = &copy
		}},
		{name: "missing private data", change: func(p *flowProvider, _ *Runner) { p.posting.PII = nil }},
		{name: "null customer", change: func(p *flowProvider, _ *Runner) { p.posting.Customer.Presence = orders.PresenceNull }},
		{name: "no protected reader", change: func(_ *flowProvider, r *Runner) { r.options.CustomerReader = nil }},
		{name: "protected data expired", change: func(_ *flowProvider, r *Runner) { r.options.CustomerReader = expiredCustomerReader{} }},
		{name: "same forged marker different values", change: func(p *flowProvider, _ *Runner) {
			copy := *p.posting.PII
			copy.Name = orders.Present("FORGED")
			p.posting.PII = &copy
			p.posting.Customer = orders.ProtectedCustomer{Reference: "protected-reference", Schema: "customer-v1", Revision: 1, Presence: orders.PresenceValue}
		}},
		{name: "unknown versus empty", change: func(p *flowProvider, _ *Runner) {
			copy := *p.posting.PII
			copy.Phone = orders.Present("")
			p.posting.PII = &copy
		}},
	}
}

type expiredCustomerReader struct{}

func (expiredCustomerReader) Customer(context.Context, orders.Scope, string, orders.ProtectedCustomer) (*orders.Customer, error) {
	return nil, orders.ErrNotFound
}

func TestRunnerPrivateReadbackPreservesUnknownWithoutAnotherWrite(t *testing.T) {
	t.Parallel()
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "same customer", true: "customer changed after dispatch"}[changed], func(t *testing.T) {
			t.Parallel()
			store, source, target, runner := privateDispatchedFlow(t)
			runFlow(t, runner)
			if store.work.State != orders.StateAcknowledged {
				t.Fatal("private projection never dispatched")
			}
			exposeWrittenTarget(store, target)
			target.target.Fields.ShipmentAddress = orders.Present("PRIVATE_CITY")
			expected := orders.StateReconciled
			if changed {
				customer := *source.posting.PII
				customer.Name = orders.Present("OTHER_BUYER")
				source.posting.PII = &customer
				expected = orders.StateUnknown
			}
			runFlow(t, runner)
			if store.work.State != expected || target.writes != 1 || store.intents != 1 {
				t.Fatalf("private readback state=%s writes=%d intents=%d", store.work.State, target.writes, store.intents)
			}
		})
	}
}
