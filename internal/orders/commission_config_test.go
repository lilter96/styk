package orders

import (
	"errors"
	"testing"
	"time"
)

func TestFinanceCurrencyCoherenceOnlyForEnabledOrderMirrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		enabled, finance bool
		currency         string
		invalid          bool
	}{
		{"matching explicit currency", true, true, "00000000-0000-4000-8000-000000000004", false},
		{"different native currency", true, true, "00000000-0000-4000-8000-000000000099", true},
		{"implicit native currency", true, true, "", true},
		{"source only independent policy", false, true, "00000000-0000-4000-8000-000000000099", false},
		{"finance disabled preserves mirror", true, false, "00000000-0000-4000-8000-000000000099", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			scope := Scope{Tenant: "tenant", MoyskladAccount: "native-account", OzonAccount: "seller-account"}
			policy := &CommissionPolicy{ID: "policy", Scope: scope, Revision: 1, ConfigRevision: 1, OwnerID: "owner", Enabled: tc.finance, GrantedAt: time.Now().Add(-time.Minute), OrganizationID: "00000000-0000-4000-8000-000000000001", AgentID: "00000000-0000-4000-8000-000000000002", ContractID: "00000000-0000-4000-8000-000000000003", CurrencyID: "00000000-0000-4000-8000-000000000004", CurrencyISO: "RUB", RateValue: "1", PriceBasis: CommissionSellerPrice, VATEnabled: Present(false), VATIncluded: Present(false), VAT: "0", OtherComponents: CommissionZeroOtherComponents}
			config := Config{Scope: scope, Revision: 1, Enabled: tc.enabled, ReadConfigured: true, ReadEnabled: true, IntervalSeconds: 300, SourceFreshnessSeconds: 300, PreviewTTLSeconds: 300, OrganizationID: policy.OrganizationID, AgentID: policy.AgentID, StateID: "state", SalesChannelID: "sales-channel", CurrencyID: tc.currency, Finance: policy}
			err := config.Validate()
			if tc.invalid && !errors.Is(err, ErrInvalid) || !tc.invalid && err != nil {
				t.Fatalf("configuration validation=%v, want invalid=%t", err, tc.invalid)
			}
		})
	}
}
