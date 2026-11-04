package orderactions

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestPackingRequirementContractCoversEveryTypedSKUList(t *testing.T) {
	t.Parallel()
	var contract struct {
		PackingRequirements struct {
			SKULists []string `json:"sku_lists"`
		} `json:"packing_requirements"`
	}
	if err := json.Unmarshal(requirementsContract, &contract); err != nil {
		t.Fatal(err)
	}
	if len(contract.PackingRequirements.SKULists) != len(orders.PackingRequirementKinds()) {
		t.Fatal("typed requirement and reviewed field coverage diverged")
	}
	for _, kind := range orders.PackingRequirementKinds() {
		if !slices.Contains(contract.PackingRequirements.SKULists, "products_requiring_"+string(kind)) {
			t.Fatal("typed field absent from reviewed contract")
		}
	}
}

func emptyPackingRequirementFixture() *orders.PackingRequirements {
	r := &orders.PackingRequirements{Presence: orders.PresenceValue, Products: make(map[orders.PackingRequirementKind]orders.Field[[]string])}
	for _, kind := range orders.PackingRequirementKinds() {
		r.Products[kind] = orders.Present([]string{})
	}
	return r
}

func TestPackingRequirementsAllowPlainGoodsAndIsolatePerSKUBlockers(t *testing.T) {
	t.Parallel()
	posting := rfbsTestPosting()
	posting.Requirements = emptyPackingRequirementFixture()
	if got := PackingRequirementValidation(posting, orders.ActionPostingPack); len(got) != 0 {
		t.Fatalf("plain explicit empty requirements blocked: %+v", got)
	}
	posting.Requirements.Products[orders.RequirementMandatoryMark] = orders.Present([]string{"123"})
	got := PackingRequirementValidation(posting, orders.ActionPostingPack)
	if len(got) != 1 || got[0].Field != "products[sku=123].requirements.mandatory_mark" || got[0].Code != "packing_mandatory_mark_required" {
		t.Fatalf("missing actionable SKU blocker: %+v", got)
	}
	if len(PackingRequirementValidation(posting, orders.ActionMirrorCreate)) != 0 {
		t.Fatal("marking requirement blocked independent order mirror")
	}
	posting.Requirements.Products[orders.RequirementMandatoryMark] = orders.Present([]string{"999"})
	if got = PackingRequirementValidation(posting, orders.ActionPostingSplit); len(got) != 1 || got[0].Code != "packing_requirement_sku_not_in_posting" {
		t.Fatal("foreign requirement SKU associated by coincidence")
	}
}

func TestPackingRequirementsNeverInventMissingOrNewProviderRules(t *testing.T) {
	t.Parallel()
	posting := rfbsTestPosting()
	if len(PackingRequirementValidation(posting, orders.ActionPostingPack)) == 0 {
		t.Fatal("missing requirements treated as ordinary goods")
	}
	posting.Requirements = emptyPackingRequirementFixture()
	posting.Requirements.Products[orders.RequirementGTD] = orders.Field[[]string]{Presence: orders.PresenceNull}
	got := PackingRequirementValidation(posting, orders.ActionPostingPack)
	if len(got) != 1 || got[0].Code != "packing_requirement_unknown" || got[0].Field != "requirements.gtd" {
		t.Fatal("null declaration requirement became zero")
	}
	posting.Requirements = emptyPackingRequirementFixture()
	posting.Requirements.UnknownFields = true
	if got = PackingRequirementValidation(posting, orders.ActionPostingPack); !slices.ContainsFunc(got, func(v orders.Validation) bool { return v.Code == "packing_requirement_contract_changed" }) {
		t.Fatal("unknown provider rule silently ignored")
	}
}
