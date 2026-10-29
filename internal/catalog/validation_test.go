package catalog

import (
	"encoding/json"
	"testing"
)

func TestValidateCommandRejectsScopeAndImplicitWrites(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*Command)
	}{
		{"missing tenant", func(c *Command) { c.Scope.Tenant = "" }},
		{"wrong source account", func(c *Command) { c.Source.Account.ID = "unrelated-account" }},
		{"wrong target account", func(c *Command) { c.Target.Account.ID = "unrelated-account" }},
		{"wrong source provider", func(c *Command) { c.Source.Account.Provider = ProviderOzon }},
		{"missing exact target", func(c *Command) { c.Target.ID = "" }},
		{"missing exact source", func(c *Command) { c.Source.ID = "" }},
		{"missing durable intent", func(c *Command) { c.ID = "" }},
		{"unsupported action", func(c *Command) { c.Action = "delete" }},
		{"empty selection", func(c *Command) { c.Values = nil }},
		{"implicit clear", func(c *Command) { c.Values[FieldName] = Empty() }},
		{"unknown selected value", func(c *Command) { c.Values[FieldName] = Value{State: StateUnknown} }},
		{"unsupported selected field", func(c *Command) { c.Values["http_body"] = Text("payload") }},
		{"name is not text", func(c *Command) { c.Values[FieldName] = Value{State: StateKnown, Data: []byte(`42`)} }},
		{"name cannot clear", func(c *Command) { c.Values[FieldName] = Clear() }},
		{"cross-account variant parent", func(c *Command) { parent := c.Source; c.Parent = &parent }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			command := commandFixture()
			tc.change(&command)
			if err := ValidateCommand(command); err == nil {
				t.Fatal("invalid command accepted")
			}
		})
	}
}

func TestValidateCommandAllowsKnownCreationFacts(t *testing.T) {
	t.Parallel()
	command := commandFixture()
	command.Action, command.Target.ID, command.CreateID = ActionCreate, "", "durably-reserved-create-id"
	for _, field := range []Field{FieldPrice, FieldCategory, FieldAttributes, FieldImages, FieldDimensions, FieldBarcodes, FieldUnit} {
		command.Values[field] = Value{State: StateKnown, Data: json.RawMessage(`{"provider_fact":123}`)}
	}
	command.Values[FieldCurrency], command.Values[FieldArticle] = Text("RUB"), Text("supplier-code")
	command.Extensions = map[string]Value{"vat": {State: StateKnown, Data: []byte(`"20"`),
		Provenance: Provenance{Kind: EvidenceOperatorAttested, Evidence: "approved operator input"}}}
	if err := ValidateCommand(command); err != nil {
		t.Fatalf("known creation facts rejected before provider validation: %v", err)
	}
	command.CreateID = ""
	if err := ValidateCommand(command); err == nil {
		t.Fatal("creation without a durable correlation key accepted")
	}
}

func TestValidateCommandAllowsExplicitDescriptionClear(t *testing.T) {
	t.Parallel()
	command := commandFixture()
	command.Values = map[Field]Value{FieldDescription: Clear()}
	if err := ValidateCommand(command); err != nil {
		t.Fatalf("explicit description clear rejected: %v", err)
	}
}

func commandFixture() Command {
	request := planFixture()
	return Command{ID: "intent-id", Scope: Scope{Tenant: "tenant-id", MoyskladAccount: "ms-account", OzonAccount: "ozon-account"},
		MappingID: request.Mapping.ID, MappingVersion: request.Mapping.Version, PolicyRevision: "policy-version",
		Source: request.Source.Identity, Target: request.Target.Identity, Action: ActionUpdate,
		Values: map[Field]Value{FieldName: Text("reviewed name")}, ExpectedSourceRevision: "source-rev", ExpectedTargetRevision: "target-rev"}
}
