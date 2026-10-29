package orderactions

import "slices"

type FBSOperation string

const (
	FBSPack          FBSOperation = "pack"
	FBSShip          FBSOperation = "ship"
	FBSSplit         FBSOperation = "split"
	FBSCancel        FBSOperation = "cancel"
	FBSPartialCancel FBSOperation = "partial_cancel"
	FBSLabelCreate   FBSOperation = "label_create"
	FBSLabelGet      FBSOperation = "label_get"
	FBSInstances     FBSOperation = "instances"
	FBSTrustAccept   FBSOperation = "trust_acceptance"
)

const (
	FBSShipEndpoint        = "/v4/posting/fbs/ship"
	FBSLabelCreateEndpoint = "/v3/posting/fbs/package-label/create"
	FBSLabelGetEndpoint    = "/v2/posting/fbs/package-label/get"
	fbsDocs                = "https://docs.ozon.ru/api/seller/"
	fbsFAQ                 = "https://dev.ozon.ru/start/257-FAQ-spravochnik-oshibok-i-sposoby-ikh-resheniia"
	fbsLabelNotice         = "https://dev.ozon.ru/news/800-Podderzhka-novoi-etiketki-scanit-i-migratsiia-metodov-v-Seller-API/"
)

// FBSNode describes planning prerequisites, never permission to dispatch. Empty
// endpoints and target statuses deliberately preserve missing official evidence.
// ShipFbsPostingV4 means assembling a posting, not handing it to a carrier.
type FBSNode struct {
	Operation       FBSOperation `json:"operation"`
	Endpoint        string       `json:"endpoint,omitempty"`
	SourceStatuses  []string     `json:"source_statuses,omitempty"`
	TargetStatuses  []string     `json:"target_statuses,omitempty"`
	Prerequisites   []string     `json:"prerequisites"`
	ProviderManaged bool         `json:"provider_managed"`
	SchemaVerified  bool         `json:"schema_verified"`
	Sources         []string     `json:"sources"`
}

// FBSGraph retains the distinctions between assembly, physical handover, split
// lineage, labels and trust acceptance. Current official JSON schemas could not
// be retrieved on 2026-10-30; every node therefore remains schema-unverified.
func FBSGraph() []FBSNode {
	return []FBSNode{
		{Operation: FBSPack, Endpoint: FBSShipEndpoint, SourceStatuses: []string{"awaiting_packaging"}, TargetStatuses: []string{"awaiting_deliver"}, Prerequisites: []string{"awaiting_packaging", "complete_packages", "required_instance_data", "fresh_available_actions", "split_lineage_readback"}, Sources: []string{fbsDocs, fbsFAQ}},
		{Operation: FBSShip, SourceStatuses: []string{"awaiting_deliver"}, TargetStatuses: []string{"delivering"}, ProviderManaged: true, Prerequisites: []string{"assembled_posting", "physical_handover", "provider_observation"}, Sources: []string{fbsDocs, fbsFAQ}},
		{Operation: FBSSplit, Prerequisites: []string{"complete_packages", "quantity_conservation", "parent_child_readback", "single_reservation_frontier"}, Sources: []string{fbsDocs, fbsFAQ}},
		{Operation: FBSCancel, TargetStatuses: []string{"cancelled"}, Prerequisites: []string{"current_cancellation_reasons", "reason_id", "fresh_available_actions", "cancellation_readback"}, Sources: []string{fbsDocs}},
		{Operation: FBSPartialCancel, Prerequisites: []string{"current_cancellation_reasons", "reason_id", "actual_line_quantities", "fresh_available_actions", "split_lineage_readback"}, Sources: []string{fbsDocs}},
		{Operation: FBSLabelCreate, Endpoint: FBSLabelCreateEndpoint, Prerequisites: []string{"new_label_contract", "posting_task_binding", "bounded_polling"}, Sources: []string{fbsLabelNotice}},
		{Operation: FBSLabelGet, Endpoint: FBSLabelGetEndpoint, Prerequisites: []string{"new_label_contract", "posting_task_binding", "provider_origin_pdf_download"}, Sources: []string{fbsLabelNotice}},
		{Operation: FBSInstances, Endpoint: "/v4/fbs/posting/product/exemplar/set", Prerequisites: []string{"source_requirements", "per_unit_instance_data", "required_marking_customs_fields", "instance_readback"}, Sources: []string{fbsFAQ}},
		{Operation: FBSTrustAccept, TargetStatuses: []string{"acceptance_in_progress"}, ProviderManaged: true, Prerequisites: []string{"reviewed_trust_acceptance_capability", "provider_acceptance_observation"}, Sources: []string{fbsFAQ}},
	}
}

func fbsNode(operation FBSOperation) (FBSNode, bool) {
	for _, node := range FBSGraph() {
		if node.Operation == operation {
			return node, true
		}
	}
	return FBSNode{}, false
}

// AssessFBSOperation does not equate a provider status with an instruction.
func AssessFBSOperation(operation FBSOperation) []Diagnostic {
	node, known := fbsNode(operation)
	if !known {
		return []Diagnostic{{Severity: "error", Code: "unknown_fbs_operation", Field: "action"}}
	}
	result := []Diagnostic{{Severity: "error", Code: "official_request_schema_unverified", Field: "contract"}}
	if node.ProviderManaged {
		result = append(result, Diagnostic{Severity: "error", Code: "provider_managed_transition", Field: "action"})
	}
	return slices.Clone(result)
}
