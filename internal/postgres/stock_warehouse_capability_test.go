package postgres

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/testpublication"
)

func TestStockCapabilityBindsReviewedPrimarySubset(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../docs/ozon-stock-contract-2026-10.json")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != stockContractSHA256 {
		t.Fatal("reviewed stock capability evidence changed")
	}
}

func TestStockContractChangeInvalidatesFrozenEvidence(t *testing.T) {
	t.Parallel()
	evidence := domain.StockEvidence{Proofs: make(map[string]domain.StockProof)}
	recordStockContractEvidence(&evidence)
	review := domain.StockSyncReview{Targets: []domain.StockSyncTarget{{Decision: domain.StockDecision{Evidence: evidence}}}}
	frozen := stableSyncEvidence(review)
	proof := evidence.Proofs["OZON_STOCK_REVIEWED_SUBSET"]
	proof.Reference = "changed reviewed subset"
	evidence.Proofs["OZON_STOCK_REVIEWED_SUBSET"] = proof
	if frozen == stableSyncEvidence(review) {
		t.Fatal("pending intent remained valid after its stock contract changed")
	}
}

func TestStockWarehouseCapabilitiesRequireExplicitScopedFacts(t *testing.T) {
	t.Setenv("STYK_TEST_ACCOUNT_PUBLICATION", "true")
	tests := []struct {
		name, kind, status string
		rfbs               *bool
		enrolled, want     bool
	}{
		{"legacy_fbs", "", "created", stockCapabilityBool(false), false, true},
		{"fbs", "fbs", "created", stockCapabilityBool(false), false, true},
		{"rfbs_enrolled", "rfbs", "created", stockCapabilityBool(true), true, true},
		{"express_enrolled", "rfbs_express", "created", stockCapabilityBool(true), true, true},
		{"rfbs_not_enrolled", "rfbs", "created", stockCapabilityBool(true), false, false},
		{"express_disabled", "rfbs_express", "disabled", stockCapabilityBool(true), true, false},
		{"rfbs_unknown_status", "rfbs", "future", stockCapabilityBool(true), true, false},
		{"fbo_false_flag", "fbo", "created", stockCapabilityBool(false), true, false},
		{"unknown_type", "future", "created", stockCapabilityBool(true), true, false},
		{"missing_rfbs_flag", "rfbs", "created", nil, true, false},
		{"conflicting_rfbs_flag", "rfbs", "created", stockCapabilityBool(false), true, false},
		{"missing_type", "", "created", stockCapabilityBool(true), true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			facts := stockWarehouseFacts{kind: tc.kind, status: tc.status, rfbs: tc.rfbs}
			if facts.policySupported(testpublication.Scope(), tc.enrolled, true) != tc.want {
				t.Fatal("warehouse contract or enrollment was inferred from incomplete facts")
			}
		})
	}
}

func stockCapabilityBool(value bool) *bool { return &value }

func TestStockWarehouseCapabilitiesNeverWidenAccounts(t *testing.T) {
	t.Setenv("STYK_TEST_ACCOUNT_PUBLICATION", "true")
	facts := stockWarehouseFacts{kind: "rfbs_express", status: "created", rfbs: stockCapabilityBool(true)}
	for _, scope := range []domain.StockSyncScope{
		{Tenant: "other", MoyskladAccount: testpublication.MoyskladAccount, OzonAccount: testpublication.OzonAccount},
		{Tenant: testpublication.Tenant, MoyskladAccount: "other", OzonAccount: testpublication.OzonAccount},
		{Tenant: testpublication.Tenant, MoyskladAccount: testpublication.MoyskladAccount, OzonAccount: "other"},
	} {
		if facts.policySupported(scope, true, true) {
			t.Fatal("rFBS capability widened the authorized account pair")
		}
	}
	t.Setenv("STYK_TEST_ACCOUNT_PUBLICATION", "")
	if facts.policySupported(testpublication.Scope(), true, true) {
		t.Fatal("rFBS capability bypassed the deployment hold")
	}
}
