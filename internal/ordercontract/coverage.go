package ordercontract

import (
	"slices"
	"strings"
)

// FieldFate is the implemented handling, independently of schema classification.
type FieldFate string

const (
	FateNativeField             FieldFate = "NATIVE_FIELD"
	FateCustomAttribute         FieldFate = "CUSTOM_ATTRIBUTE"
	FateLinkedEntity            FieldFate = "LINKED_ENTITY"
	FateStructuredExtension     FieldFate = "STRUCTURED_EXTENSION"
	FateReadOnlyDerived         FieldFate = "READ_ONLY_DERIVED"
	FateNotProvided             FieldFate = "NOT_PROVIDED"
	FateUnsupported             FieldFate = "UNSUPPORTED"
	FateExcludedByPrivacyPolicy FieldFate = "EXCLUDED_BY_PRIVACY_POLICY"
)

// CoverageProof keeps fixture validation separate from integration and live use.
type CoverageProof struct {
	Implemented              bool `json:"implemented"`
	ContractVerified         bool `json:"contract_verified"`
	ValidatorFixtureCovered  bool `json:"validator_fixture_covered"`
	DeterministicIntegration bool `json:"deterministic_integration"`
	LiveRead                 bool `json:"live_read"`
	LiveWrite                bool `json:"live_write"`
}

// FieldCoverageRow never contains provider values, identifiers, or credentials.
type FieldCoverageRow struct {
	Endpoint          string        `json:"endpoint"`
	Version           string        `json:"version"`
	Direction         string        `json:"direction"`
	Path              string        `json:"path"`
	Types             []string      `json:"types"`
	Presence          string        `json:"presence"`
	Optional          bool          `json:"optional"`
	Nullable          bool          `json:"nullable"`
	Sources           []string      `json:"sources"`
	Fixture           string        `json:"fixture"`
	Fate              FieldFate     `json:"fate"`
	Owner             string        `json:"owner"`
	Transformation    string        `json:"transformation"`
	Prerequisites     []string      `json:"prerequisites"`
	Proof             CoverageProof `json:"proof"`
	Reason            string        `json:"reason"`
	LiveScope         string        `json:"live_scope"`
	IntegrationReason string        `json:"integration_reason"`
}

// CoverageSnapshot is a conservative field-level implementation audit.
type CoverageSnapshot struct {
	CheckedAt   string             `json:"checked_at"`
	Rows        []FieldCoverageRow `json:"rows"`
	Limitations []string           `json:"limitations"`
}

// CoverageReport derives rows from the manifest without changing its hashes.
func CoverageReport() (CoverageSnapshot, error) {
	manifest, err := ContractManifest()
	if err != nil {
		return CoverageSnapshot{}, err
	}
	return CoverageSnapshot{CheckedAt: manifest.CheckedAt, Rows: manifest.FieldCoverage(), Limitations: []string{
		"Validator fixture coverage is not implementation, deterministic integration, or live-provider coverage.",
		"Integration remains false where a field-specific integration assertion has not been audited; adapter unit tests are not integration evidence.",
		"Live flags cover only the owner-reported two genuinely cancelled FBS mirrors on 2026-10-30, not rFBS or fulfillment transitions.",
		"Three native attribute definitions were created/read; attribute-value updates were still in progress and are not claimed as live writes.",
		"LiveWrite means this endpoint field was sent to its provider, not that an Ozon source value was mirrored elsewhere.",
		"Only existing manifest paths are reported; missing price/currency or metadata-create paths are not invented.",
	}}, nil
}

// FieldCoverage defaults every field to unsupported until an audited code path matches.
func (m Manifest) FieldCoverage() []FieldCoverageRow {
	count := 0
	for _, contract := range m.Contracts {
		count += len(contract.Fields)
	}
	rows := make([]FieldCoverageRow, 0, count)
	for _, contract := range m.Contracts {
		for _, field := range contract.Fields {
			row := coverageBase(contract, field)
			applyCoverageRule(&row)
			rows = append(rows, row)
		}
	}
	return rows
}

func coverageBase(c Contract, f Field) FieldCoverageRow {
	return FieldCoverageRow{Endpoint: c.Endpoint, Version: c.Version, Direction: c.Direction,
		Path: f.Path, Types: slices.Clone(f.Types), Presence: f.Presence, Optional: f.Presence != "required",
		Nullable: f.Nullable, Sources: slices.Clone(f.Sources), Fixture: f.Fixture,
		Fate: FateUnsupported, Owner: "unassigned", Transformation: "none; no audited implemented field mapping",
		Prerequisites: []string{"explicit_destination_and_owner", "typed_handling_and_transform_review"},
		Proof: CoverageProof{ContractVerified: c.SchemaStatus == "official_verified" && f.Verification == "official_field_verified",
			ValidatorFixtureCovered: f.Fixture != ""}, Reason: "no_implemented_field_mapping_audited",
		IntegrationReason: "field_specific_deterministic_integration_assertion_not_audited"}
}

func applyCoverageRule(row *FieldCoverageRow) {
	if row.Endpoint == "/v3/posting/fbs/get" && row.Direction == "response" {
		coverOzonRead(row)
		return
	}
	if row.Endpoint == "/entity/customerorder/metadata" && row.Direction == "response" {
		coverMetadataRead(row)
		return
	}
	if row.Endpoint == "/entity/customerorder" || row.Endpoint == "/entity/customerorder/{id}" {
		coverNativeOrder(row)
	}
}

func supportedCoverage(row *FieldCoverageRow, fate FieldFate, owner, transform string) {
	row.Fate, row.Owner, row.Transformation = fate, owner, transform
	row.Proof.Implemented = true
	row.Reason = "audited_typed_adapter_or_projection_path"
	row.Prerequisites = []string{"matching_tenant_and_provider_account", "reviewed_configuration_and_native_references",
		"complete_product_mapping_and_exact_money", "field_ownership_and_fresh_readback", "presence_and_privacy_policy"}
}

func coverOzonRead(row *FieldCoverageRow) {
	path := strings.TrimPrefix(row.Path, "$.result.")
	if path == "customer" || strings.HasPrefix(path, "customer.") {
		row.Fate, row.Owner = FateExcludedByPrivacyPolicy, "internal/orderflow/planner_fields.go:applyMirrorMapping"
		row.Reason = "customer_destination_projection_blocked_by_privacy_policy"
		row.Transformation = "Customer destination mappings are rejected; separately permissioned source PII reads do not establish implemented or live customer projection."
		row.Prerequisites = []string{"explicit_customer_PII_permission", "protected_transient_read", "reviewed_destination_projection_not_implemented"}
		return
	}
	text := []string{"posting_number", "order_id", "order_number", "status", "substatus", "delivery_method.warehouse_id", "delivery_method.id"}
	line := []string{"products[].offer_id", "products[].sku", "products[].quantity", "products[].price", "products[].currency_code", "products[].vat", "products[].name"}
	if slices.Contains(text, path) {
		supportedCoverage(row, FateCustomAttribute, "internal/orderflow/planner_fields.go:postingText",
			"Decode typed presence; project known text only through reviewed native name/description or native attribute UUID mappings.")
	} else if slices.Contains(line, path) {
		supportedCoverage(row, FateNativeField, "internal/ordermoney/projection.go:ProjectLines",
			"Match exact scoped product identity; preserve quantity; convert explicit-currency price to exact minor units; VAT needs an explicit mapping.")
	} else {
		return
	}
	if path == "products[].name" {
		row.Fate = FateStructuredExtension
		row.Owner = "internal/infrastructure/ozon/order_read_canonical.go:orderLines"
		row.Transformation = "Retain typed line-name presence in source observation; no native position-name write mapping is implemented."
	}
	if path == "products[].sku" || path == "products[].offer_id" {
		row.Fate = FateLinkedEntity
	}
	row.Proof.LiveRead = slices.Contains([]string{"posting_number", "order_id", "status", "substatus", "delivery_method.warehouse_id", "products[].sku", "products[].quantity", "products[].price", "products[].currency_code"}, path)
	if row.Proof.LiveRead {
		row.LiveScope = "owner_reported_2026_10_30_two_cancelled_FBS_source_reads_only"
	}
	row.Prerequisites = append(row.Prerequisites, "current_Ozon_full_schema_remains_unverified", "no_fulfillment_write_authority")
}

func coverNativeOrder(row *FieldCoverageRow) {
	path := strings.ReplaceAll(row.Path, "$.positions.rows[]", "$.positions[]")
	if nativeCoverageBlocked(row, path) {
		return
	}
	refs := []string{"organization", "agent", "store", "state", "salesChannel", "rate.currency", "positions[].assortment"}
	for _, reference := range refs {
		if !coverageReference(path, "$."+reference) {
			continue
		}
		supportedCoverage(row, FateLinkedEntity, "internal/infrastructure/moysklad/order_payload.go:addOrderReferences",
			"Use reviewed native entity UUID and exact reference kind/origin/path; no automatic counterparty creation or invented reference.")
		markNativeLive(row, true)
		return
	}
	coverNativeScalars(row, path)
}

func coverageReference(path, root string) bool {
	return slices.Contains([]string{root, root + ".meta", root + ".meta.href", root + ".meta.type", root + ".meta.mediaType"}, path)
}

func coverNativeScalars(row *FieldCoverageRow, path string) {
	if path == "$.sum" && row.Direction == "response" {
		supportedCoverage(row, FateReadOnlyDerived, "internal/infrastructure/moysklad/order_read_fields.go:decodeOrderFields", "Read provider-computed sum in explicit currency; never write or invent an aggregate.")
		markNativeLive(row, false)
		return
	}
	if slices.Contains([]string{"$.applicable", "$.name", "$.description", "$.moment", "$.deliveryPlannedMoment", "$.vatEnabled", "$.vatIncluded", "$.rate.value", "$.syncId", "$.externalCode", "$.positions[].quantity", "$.positions[].price", "$.positions[].reserve", "$.positions[].vat"}, path) {
		supportedCoverage(row, FateNativeField, "internal/infrastructure/moysklad/order_payload.go:addOrderFields/orderPositions",
			"Write only owned known fields; timestamps use Moscow time; decimal money uses exact minor units. Cancelled mirrors require applicable=false and reserve=0; position replacement on update is blocked.")
		if slices.Contains([]string{"$.applicable", "$.positions[].quantity", "$.positions[].price", "$.positions[].reserve"}, path) {
			markNativeLive(row, true)
		}
		return
	}
	if coverageReference(path, "$.attributes[]") || path == "$.attributes[].value" {
		supportedCoverage(row, FateCustomAttribute, "internal/infrastructure/moysklad/order_payload.go:orderAttributes",
			"Write/read only reviewed string/text native attribute UUIDs with owned fields and STYK markers; definition creation is separate from value update.")
	}
}

func markNativeLive(row *FieldCoverageRow, write bool) {
	row.Proof.LiveRead = row.Direction == "response"
	row.Proof.LiveWrite = row.Direction == "request" && row.Endpoint == "/entity/customerorder" && write
	if row.Proof.LiveRead || row.Proof.LiveWrite {
		row.LiveScope = "owner_reported_2026_10_30_two_cancelled_FBS_native_creates_and_readback_only; no_attribute_value_or_fulfillment_writes"
	}
}

func coverMetadataRead(row *FieldCoverageRow) {
	if !slices.Contains([]string{"$.attributes.meta.href", "$.attributes.meta.size", "$.attributes.meta.offset"}, row.Path) {
		return
	}
	supportedCoverage(row, FateReadOnlyDerived, "internal/infrastructure/moysklad/order_metadata_attributes.go:orderAttributeCollectionEmpty",
		"Validate exact configured attributes reference; explicit size zero is empty, unknown/nonzero size requires bounded separate discovery; offset must be zero.")
	row.Proof.LiveRead = true
	row.LiveScope = "authorized_2026_10_30_observed_metadata_MetaArray_shape_only"
}

func nativeCoverageBlocked(row *FieldCoverageRow, path string) bool {
	if row.Direction == "response" && strings.HasSuffix(path, ".meta.mediaType") {
		return true
	}
	if row.Direction != "request" || row.Endpoint != "/entity/customerorder/{id}" {
		return false
	}
	if strings.HasPrefix(path, "$.positions") {
		row.Reason = "native_position_collection_replacement_blocked"
		row.Owner = "internal/infrastructure/moysklad/order_payload.go:addOrderCollections"
		return true
	}
	if strings.HasPrefix(path, "$.organization") || strings.HasPrefix(path, "$.agent") {
		row.Reason = "native_reference_account_pair_update_blocked"
		row.Owner = "internal/infrastructure/moysklad/order_payload.go:orderOwnedFields"
		return true
	}
	return false
}
