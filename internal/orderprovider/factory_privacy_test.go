package orderprovider

import (
	"context"
	"errors"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

type privacyConfigReader struct {
	config    orders.Config
	err       error
	requested orders.Scope
}

func (r *privacyConfigReader) OrderConfig(_ context.Context, scope orders.Scope) (orders.Config, error) {
	r.requested = scope
	return r.config, r.err
}

func privacyTestConfig() orders.Config {
	return orders.Config{Scope: orders.Scope{Tenant: "merchant", MoyskladAccount: "moy", OzonAccount: "ozon"}, Revision: 1, Enabled: true, TestEnabled: true, IntervalSeconds: 30, SourceFreshnessSeconds: 300, PreviewTTLSeconds: 300, PIIEnabled: true, PIIRetentionSeconds: 3600, OrganizationID: "organization", AgentID: "agent", StoreID: "store", StateID: "state", SalesChannelID: "channel"}
}

func TestOrderFactoryCustomerDataRequiresScopedConsent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*privacyConfigReader)
		pii    bool
		want   error
	}{
		{name: "consented", pii: true},
		{name: "disabled", change: func(r *privacyConfigReader) { r.config.Enabled = false }},
		{name: "no consent", change: func(r *privacyConfigReader) { r.config.PIIEnabled = false; r.config.PIIRetentionSeconds = 0 }},
		{name: "missing config", change: func(r *privacyConfigReader) { r.err = orders.ErrNotFound }},
		{name: "database unavailable", change: func(r *privacyConfigReader) { r.err = context.Canceled }, want: context.Canceled},
		{name: "foreign tenant", change: func(r *privacyConfigReader) { r.config.Scope.Tenant = "foreign" }, want: orders.ErrStale},
		{name: "foreign Ozon", change: func(r *privacyConfigReader) { r.config.Scope.OzonAccount = "foreign" }, want: orders.ErrStale},
		{name: "foreign MoySklad", change: func(r *privacyConfigReader) { r.config.Scope.MoyskladAccount = "foreign" }, want: orders.ErrStale},
		{name: "invalid retention", change: func(r *privacyConfigReader) { r.config.PIIRetentionSeconds = 0 }, want: orders.ErrStale},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			config := privacyTestConfig()
			reader := &privacyConfigReader{config: config}
			if tt.change != nil {
				tt.change(reader)
			}
			factory := Factory{Configs: reader}
			options, err := factory.ozonReadOptions(t.Context(), config.Scope)
			if !errors.Is(err, tt.want) || options.CustomerPII != tt.pii || reader.requested != config.Scope {
				t.Fatalf("PII consent result enabled=%v error=%v", options.CustomerPII, err)
			}
			if !options.AnalyticsData || !options.FinancialData || !options.Barcodes || !options.ResolveDelivery {
				t.Fatal("existing read behavior changed")
			}
		})
	}
}

func TestOrderFactoryWithoutConfigNeverRequestsCustomerData(t *testing.T) {
	t.Parallel()
	options, err := (Factory{}).ozonReadOptions(t.Context(), privacyTestConfig().Scope)
	if err != nil || options.CustomerPII || options.LegalInfo {
		t.Fatal("missing consent enabled personal data")
	}
}
