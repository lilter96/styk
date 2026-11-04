package postgres

import (
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestCountryUnknownCannotBecomeAcknowledgedByReadback(t *testing.T) {
	t.Parallel()
	scope := orders.Scope{Tenant: "tenant", OzonAccount: "ozon", MoyskladAccount: "moy"}
	work := orders.Work{Scope: scope, State: orders.StateUnknown, Command: orders.Command{Action: orders.ActionCountrySet, PostingAction: &orders.PostingAction{Parameters: orders.ActionParameters{CountrySKU: "2001"}, PackingTargetFingerprint: "fingerprint"}}}
	evidence := &orders.CountryEvidence{Scope: scope, SKU: "2001", Meaning: "country_requirement_resolution", TargetFingerprint: "fingerprint", RequirementResolved: true}
	completion := orders.Completion{State: orders.StateReconciled, Receipt: orders.Receipt{HTTPStatus: 200, State: orders.StateReconciled, CountryEvidence: evidence}}
	if validateCountryReceiptUpdate(work, completion) == nil {
		t.Fatal("readback fabricated a lost write acknowledgment")
	}
	completion.State = orders.StateUnknown
	if validateCountryReceiptUpdate(work, completion) == nil {
		t.Fatal("unknown write gained a fabricated HTTP status")
	}
	completion.Receipt.HTTPStatus = 0
	if err := validateCountryReceiptUpdate(work, completion); err != nil {
		t.Fatal("honest requirement observation blocked", err)
	}
	evidence.CountryValueObserved = true
	if validateCountryReceiptUpdate(work, completion) == nil {
		t.Fatal("unavailable ISO readback asserted")
	}
}
