package postgres

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/stockfixture"
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
	t.Setenv("STYK_STOCK_PUBLICATION_TEST_ENDPOINT", "")
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
			if facts.policySupported(stockfixture.Scope(), tc.enrolled, true) != tc.want {
				t.Fatal("warehouse contract or enrollment was inferred from incomplete facts")
			}
		})
	}
}

func stockCapabilityBool(value bool) *bool { return &value }

func TestStockWarehouseCapabilitiesRequireValidScopeAndEnrollment(t *testing.T) {
	t.Setenv("STYK_STOCK_PUBLICATION_TEST_ENDPOINT", "")
	facts := stockWarehouseFacts{kind: "rfbs_express", status: "created", rfbs: stockCapabilityBool(true)}
	for _, scope := range []domain.StockSyncScope{stockfixture.Scope(), {Tenant: "another-merchant", MoyskladAccount: "source", OzonAccount: "market"}} {
		if !facts.policySupported(scope, true, true) {
			t.Fatal("valid connected scope blocked by a fixed-account assumption")
		}
		if facts.policySupported(scope, false, true) {
			t.Fatal("warehouse published without enrollment")
		}
	}
	for _, scope := range []domain.StockSyncScope{{}, {Tenant: "merchant", MoyskladAccount: "source"}, {Tenant: "merchant", OzonAccount: "market"}} {
		if facts.policySupported(scope, true, true) {
			t.Fatal("invalid scope accepted")
		}
	}
	t.Setenv("STYK_STOCK_PUBLICATION_TEST_ENDPOINT", "http://127.0.0.1:1234")
	if facts.policySupported(stockfixture.Scope(), true, true) {
		t.Fatal("simulator authorized an ordinary tenant")
	}

}

func TestMerchantWarehousePreviewSharesActualScopedReviewPolicy(t *testing.T) {
	t.Setenv("STYK_STOCK_PUBLICATION_TEST_ENDPOINT", "")
	warehouse := domain.OzonWarehouse{WarehouseType: "rfbs_express", Status: "created", IsRFBS: stockCapabilityBool(true)}
	if !WarehouseMappingStockSupported(stockfixture.Scope(), warehouse) {
		t.Fatal("merchant preview incorrectly blocks supported controlled Express mapping")
	}
	other := stockfixture.Scope()
	other.Tenant = "another-merchant"
	if !WarehouseMappingStockSupported(other, warehouse) {
		t.Fatal("merchant preview imposed obsolete fixed-account restriction")
	}
	warehouse.Status = "disabled"
	if WarehouseMappingStockSupported(stockfixture.Scope(), warehouse) {
		t.Fatal("merchant preview ignored disabled warehouse")
	}
}
