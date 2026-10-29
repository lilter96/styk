package catalog

import (
	"testing"
	"time"
)

func TestCommandFingerprintBindsScopeSelectionAndApproval(t *testing.T) {
	t.Parallel()
	baseline := commandHash(t, commandFixture())
	tests := []struct {
		name   string
		change func(*Command)
	}{
		{"account", func(c *Command) { c.Target.Account.ID = "another-account" }},
		{"tenant", func(c *Command) { c.Scope.Tenant = "another-tenant" }},
		{"field selection", func(c *Command) { c.Values[FieldDescription] = Text("extra") }},
		{"value", func(c *Command) { c.Values[FieldName] = Text("different name") }},
		{"action", func(c *Command) { c.Action = ActionArchive }},
		{"revision", func(c *Command) { c.ExpectedSourceRevision = "changed" }},
		{"mapping version", func(c *Command) { c.MappingVersion++ }},
		{"policy revision", func(c *Command) { c.PolicyRevision = "changed" }},
		{"create marker", func(c *Command) { c.CreateExternalCode = "changed" }},
		{"attestation", func(c *Command) {
			value := c.Values[FieldName]
			value.Provenance.Kind = EvidenceOperatorAttested
			c.Values[FieldName] = value
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			command := commandFixture()
			tc.change(&command)
			if commandHash(t, command) == baseline {
				t.Fatal("changed approved command kept the same fingerprint")
			}
		})
	}
}

func TestProductFingerprintIgnoresReadTimeAndPreservesIdentity(t *testing.T) {
	t.Parallel()
	product := planFixture().Source
	initial := productHash(t, product)
	product.Revision, product.ObservedAt = "later-revision", time.Now()
	value := product.Fields[FieldName]
	value.Provenance = Provenance{Provider: ProviderMoySklad, Revision: "new-read", ObservedAt: time.Now()}
	product.Fields[FieldName] = value
	if productHash(t, product) != initial {
		t.Fatal("observation metadata introduced a synchronization loop")
	}
	product.Identity.ID = product.Identity.OfferID
	if productHash(t, product) == initial {
		t.Fatal("product ID and offer ID namespaces were conflated")
	}
}

func TestPlanFingerprintIsDeterministicAndBindsRevision(t *testing.T) {
	t.Parallel()
	request := descriptionFixture()
	request.Policies[FieldName] = MoySkladAuthoritative
	initial := mustPlan(t, request)
	request.Policies = Policies{FieldName: MoySkladAuthoritative, FieldDescription: MoySkladAuthoritative}
	value := request.Source.Fields[FieldDescription]
	value.Provenance.ObservedAt = time.Now()
	request.Source.Fields[FieldDescription] = value
	if next := mustPlan(t, request); next.Fingerprint != initial.Fingerprint {
		t.Fatal("map insertion order or read time changed deterministic plan")
	}
	request.Source.Revision = "new-source-revision"
	if next := mustPlan(t, request); next.Fingerprint == initial.Fingerprint {
		t.Fatal("stale source revision did not invalidate plan fingerprint")
	}
}

func TestNewBaselineAndReverseAuthorityDetectConflicts(t *testing.T) {
	t.Parallel()
	request := planFixture()
	request.Policies[FieldName] = OzonAuthoritative
	request.Source.Fields[FieldName], request.Target.Fields[FieldName] = Text("old MS"), Text("old Ozon")
	baseline, err := NewBaseline(request.Source, request.Target)
	if err != nil {
		t.Fatal(err)
	}
	request.Baseline = &baseline
	request.Source.Fields[FieldName], request.Target.Fields[FieldName] = Text("new MS"), Text("new Ozon")
	plan := mustPlan(t, request)
	if len(plan.Conflicts) != 1 || plan.Conflicts[0].BaselineSource != baseline.Target[FieldName] || plan.Conflicts[0].BaselineTarget != baseline.Source[FieldName] {
		t.Fatalf("reverse policy used baseline from the wrong provider: %+v", plan)
	}
}

func TestCommandFingerprintDoesNotMutateSelectedFacts(t *testing.T) {
	t.Parallel()
	command := commandFixture()
	command.Values[FieldPrice] = Value{State: StateKnown, Data: []byte(`{ "b":2, "a":1 }`)}
	before := string(command.Values[FieldPrice].Data)
	commandHash(t, command)
	if string(command.Values[FieldPrice].Data) != before {
		t.Fatal("fingerprinting changed the approved command")
	}
}

func commandHash(t *testing.T, command Command) string {
	t.Helper()
	result, err := CommandFingerprint(command)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func productHash(t *testing.T, product Product) string {
	t.Helper()
	result, err := ProductFingerprint(product)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
