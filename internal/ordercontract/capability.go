package ordercontract

import "slices"

// CapabilityFacts must come from reviewed warehouse and delivery evidence.
// Endpoint URLs, warehouse names and posting statuses are not scheme evidence.
type CapabilityFacts struct {
	Scheme             string   `json:"scheme"`
	WarehouseID        string   `json:"warehouse_id"`
	Carrier            string   `json:"carrier"`
	Express            bool     `json:"express"`
	Destination        string   `json:"destination"`
	VerifiedDimensions []string `json:"verified_dimensions"`
}

// Capability describes a workflow without promising an unverified action.
type Capability struct {
	Scheme        string   `json:"scheme"`
	Carrier       string   `json:"carrier"`
	Express       bool     `json:"express"`
	Destination   string   `json:"destination"`
	Action        string   `json:"action"`
	Endpoint      string   `json:"endpoint"`
	Status        string   `json:"status"`
	Prerequisites []string `json:"prerequisites"`
	Sources       []string `json:"sources"`
}

// CapabilityDecision fails closed for missing evidence or unverified schemas.
// Available means the request contract is documented; all prerequisites still apply.
type CapabilityDecision struct {
	Status        string   `json:"status"`
	Endpoint      string   `json:"endpoint"`
	Reason        string   `json:"reason"`
	Prerequisites []string `json:"prerequisites"`
}

// AssessCapability checks explicit facts against the embedded registry.
func AssessCapability(facts CapabilityFacts, action string) CapabilityDecision {
	manifest, err := ContractManifest()
	if err != nil {
		return CapabilityDecision{Status: "blocked", Reason: "invalid_manifest"}
	}
	return manifest.AssessCapability(facts, action)
}

func (m Manifest) AssessCapability(f CapabilityFacts, action string) CapabilityDecision {
	if f.Scheme == "FBO" {
		return CapabilityDecision{Status: "excluded", Reason: "fbo_out_of_scope"}
	}
	if !f.complete() {
		return CapabilityDecision{Status: "blocked", Reason: "unverified_dimensions"}
	}
	for _, capability := range m.Capabilities {
		if !capability.matches(f, action) {
			continue
		}
		return m.capabilityDecision(capability)
	}
	return CapabilityDecision{Status: "unsupported", Reason: "no_verified_workflow"}
}

func (f CapabilityFacts) complete() bool {
	if f.WarehouseID == "" || f.Scheme == "" || f.Carrier == "" || f.Destination == "" {
		return false
	}
	for _, dimension := range []string{"scheme", "warehouse", "carrier", "express", "destination"} {
		if !slices.Contains(f.VerifiedDimensions, dimension) {
			return false
		}
	}
	return true
}

func (c Capability) matches(f CapabilityFacts, action string) bool {
	return c.Scheme == f.Scheme && c.Carrier == f.Carrier && c.Express == f.Express &&
		c.Destination == f.Destination && c.Action == action
}

func (m Manifest) capabilityDecision(c Capability) CapabilityDecision {
	decision := CapabilityDecision{Status: "blocked", Endpoint: c.Endpoint,
		Reason: c.Status, Prerequisites: slices.Clone(c.Prerequisites)}
	if c.Status != "verified" {
		return decision
	}
	for _, contract := range m.Contracts {
		if contract.Endpoint == c.Endpoint && contract.Direction == "request" && contract.SchemaStatus == "official_verified" {
			decision.Status, decision.Reason = "available", "verified_request_contract"
			return decision
		}
	}
	decision.Reason = "request_contract_unverified"
	return decision
}

func (m Manifest) capabilitiesValid(sources map[string]bool) bool {
	type key struct {
		scheme, carrier, destination, action string
		express                              bool
	}
	seen := make(map[key]bool, len(m.Capabilities))
	for _, c := range m.Capabilities {
		identity := key{c.Scheme, c.Carrier, c.Destination, c.Action, c.Express}
		if seen[identity] || !c.valid(sources) {
			return false
		}
		seen[identity] = true
	}
	return true
}

func (c Capability) valid(sources map[string]bool) bool {
	if c.Action == "" || c.Endpoint == "" || len(c.Prerequisites) == 0 || !references(c.Sources, sources) {
		return false
	}
	if !slices.Contains([]string{"FBS", "rFBS"}, c.Scheme) {
		return false
	}
	if !slices.Contains([]string{"ozon", "integrated", "non_integrated", "aggregator"}, c.Carrier) {
		return false
	}
	if !slices.Contains([]string{"customer", "ozon_pvz", "carrier_pvz"}, c.Destination) {
		return false
	}
	return slices.Contains([]string{"verified", "schema_unverified", "workflow_unverified", "carrier_managed_unverified", "pvz_workflow_unverified"}, c.Status)
}
