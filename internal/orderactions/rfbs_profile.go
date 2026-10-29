package orderactions

import (
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/lilter96/styk/internal/orders"
)

func rfbsRoute(posting orders.Posting, now time.Time) (RFBSRoute, []Diagnostic) {
	diagnostics := rfbsProfileDiagnostics(posting, now)
	profile := posting.DeliveryProfile
	if profile == nil || !profile.Carrier.IsKnown() || !profile.Destination.IsKnown() {
		return RFBSRouteUnknown, diagnostics
	}
	integration := posting.Delivery.IntegrationType
	if !integration.IsKnown() {
		return RFBSRouteUnknown, append(diagnostics, rfbsDiagnostic("rfbs_integration_missing", "delivery.integration_type"))
	}
	route, classification := rfbsIntegrationRoute(posting, *integration.Value)
	diagnostics = append(diagnostics, classification...)
	return route, diagnostics
}

func rfbsProfileDiagnostics(posting orders.Posting, now time.Time) []Diagnostic {
	diagnostics := make([]Diagnostic, 0, 8)
	checked := posting.Evidence.CheckedAt
	if checked.IsZero() {
		checked = posting.Evidence.ObservedAt
	}
	if now.Before(checked) || now.Sub(checked) > rfbsEvidenceMaxAge || posting.Evidence.Origin != orders.OriginOzon {
		diagnostics = append(diagnostics, rfbsDiagnostic("rfbs_source_not_fresh", "evidence.checked_at"))
	}
	profile := posting.DeliveryProfile
	if profile == nil || profile.Validate() != nil {
		return append(diagnostics, rfbsDiagnostic("rfbs_delivery_profile_required", "delivery_profile"))
	}
	if now.Before(profile.ObservedAt) || !now.Before(profile.ValidUntil) || now.Sub(profile.ObservedAt) > rfbsEvidenceMaxAge {
		diagnostics = append(diagnostics, rfbsDiagnostic("rfbs_delivery_capability_expired", "delivery_profile.valid_until"))
	}
	if profile.Scheme != "rFBS" || (posting.Scheme.IsKnown() && *posting.Scheme.Value != "rFBS") {
		diagnostics = append(diagnostics, rfbsDiagnostic("rfbs_scheme_conflict", "scheme"))
	}
	diagnostics = append(diagnostics, rfbsIdentityDiagnostics(posting, *profile)...)
	diagnostics = append(diagnostics, rfbsDimensionDiagnostics(posting, *profile)...)
	return diagnostics
}

func rfbsIdentityDiagnostics(posting orders.Posting, profile orders.CapabilityRef) []Diagnostic {
	diagnostics := make([]Diagnostic, 0, 2)
	if !posting.WarehouseID.IsKnown() || *posting.WarehouseID.Value != profile.WarehouseID {
		diagnostics = append(diagnostics, rfbsDiagnostic("rfbs_warehouse_profile_mismatch", "warehouse_id"))
	}
	if !posting.DeliveryMethodID.IsKnown() || *posting.DeliveryMethodID.Value != profile.DeliveryMethodID {
		diagnostics = append(diagnostics, rfbsDiagnostic("rfbs_delivery_method_profile_mismatch", "delivery_method_id"))
	}
	return diagnostics
}

func rfbsDimensionDiagnostics(posting orders.Posting, profile orders.CapabilityRef) []Diagnostic {
	diagnostics := make([]Diagnostic, 0, 7)
	for _, dimension := range []string{"scheme", "warehouse", "carrier", "express", "destination"} {
		if !slices.Contains(profile.VerifiedDimensions, dimension) {
			diagnostics = append(diagnostics, rfbsDiagnostic("rfbs_unverified_dimension", "delivery_profile."+dimension))
		}
	}
	if !profile.Express.IsKnown() || *profile.Express.Value || (posting.Delivery.Express.IsKnown() && *posting.Delivery.Express.Value) {
		diagnostics = append(diagnostics, rfbsDiagnostic("rfbs_standard_requires_non_express", "delivery.express"))
	}
	if !profile.Carrier.IsKnown() || !slices.Contains([]string{"non_integrated", "integrated", "aggregator"}, *profile.Carrier.Value) {
		diagnostics = append(diagnostics, rfbsDiagnostic("rfbs_carrier_unknown", "delivery_profile.carrier"))
	}
	if !profile.Destination.IsKnown() || !slices.Contains([]string{"customer", "ozon_pvz", "carrier_pvz"}, *profile.Destination.Value) {
		diagnostics = append(diagnostics, rfbsDiagnostic("rfbs_destination_unknown", "delivery_profile.destination"))
	}
	return append(diagnostics, rfbsDestinationDiagnostics(posting, profile)...)
}

func rfbsDestinationDiagnostics(posting orders.Posting, profile orders.CapabilityRef) []Diagnostic {
	if !profile.Destination.IsKnown() {
		return nil
	}
	destination := *profile.Destination.Value
	if posting.Delivery.DestinationType.IsKnown() && *posting.Delivery.DestinationType.Value != destination {
		return []Diagnostic{rfbsDiagnostic("rfbs_destination_profile_mismatch", "delivery.destination_type")}
	}
	if posting.Analytics.DeliveryType.IsKnown() && *posting.Analytics.DeliveryType.Value == "PVZ" && destination == "customer" {
		return []Diagnostic{rfbsDiagnostic("rfbs_destination_profile_mismatch", "analytics.delivery_type")}
	}
	return nil
}

func rfbsIntegrationRoute(posting orders.Posting, integration string) (RFBSRoute, []Diagnostic) {
	profile := posting.DeliveryProfile
	carrier, destination := *profile.Carrier.Value, *profile.Destination.Value
	switch integration {
	case "non_integrated":
		return rfbsSellerRoute(carrier, destination, RFBSRouteSeller)
	case "3pl_tracking":
		route, diagnostics := rfbsSellerRoute(carrier, destination, RFBSRouteSellerTracking)
		return route, append(diagnostics, rfbsDiagnostic("rfbs_tracking_workflow_unverified", "delivery.integration_type"))
	case "hybrid":
		return rfbsHybridRoute(posting, carrier, destination)
	default:
		if carrier == "integrated" && slices.Contains([]string{"customer", "carrier_pvz"}, destination) {
			return RFBSRouteIntegrated, []Diagnostic{rfbsDiagnostic("rfbs_integration_enum_unverified", "delivery.integration_type")}
		}
		return RFBSRouteUnknown, []Diagnostic{rfbsDiagnostic("rfbs_integration_unknown", "delivery.integration_type")}
	}
}

func rfbsSellerRoute(carrier, destination string, route RFBSRoute) (RFBSRoute, []Diagnostic) {
	if carrier != "non_integrated" || destination != "customer" {
		return RFBSRouteUnknown, []Diagnostic{rfbsDiagnostic("rfbs_integration_profile_conflict", "delivery.integration_type")}
	}
	return route, nil
}

func rfbsHybridRoute(posting orders.Posting, carrier, destination string) (RFBSRoute, []Diagnostic) {
	if destination != "ozon_pvz" {
		return RFBSRouteUnknown, []Diagnostic{rfbsDiagnostic("rfbs_hybrid_destination_required", "delivery_profile.destination")}
	}
	if rfbsPositiveID(posting.Delivery.TPLProviderID) && slices.Contains([]string{"integrated", "aggregator"}, carrier) {
		return RFBSRoutePartnerPVZ, nil
	}
	if posting.Delivery.TPLProviderID.IsKnown() {
		return RFBSRouteUnknown, []Diagnostic{rfbsDiagnostic("rfbs_hybrid_partner_profile_conflict", "delivery.tpl_provider_id")}
	}
	if posting.Delivery.ProviderID.Presence == orders.PresenceNull && carrier == "non_integrated" {
		return RFBSRouteSellerPVZ, nil
	}
	return RFBSRouteUnknown, []Diagnostic{rfbsDiagnostic("rfbs_hybrid_provider_evidence_required", "delivery.provider_id")}
}

func rfbsPositiveID(field orders.Field[string]) bool {
	if !field.IsKnown() || len(*field.Value) == 0 || (*field.Value)[0] == '0' {
		return false
	}
	return strings.Trim(*field.Value, "0123456789") == ""
}

func rfbsInputValid(input RFBSInput) bool {
	if input.ProviderAction != "" && !rfbsSafeText(input.ProviderAction) {
		return false
	}
	for _, field := range []orders.Field[string]{input.TrackingNumber, input.ReasonID} {
		if field.Validate() != nil || (field.IsKnown() && !rfbsSafeText(*field.Value)) {
			return false
		}
	}
	return true
}

func rfbsDeliveryValid(delivery orders.DeliveryFacts) bool {
	for _, field := range []orders.Field[string]{delivery.IntegrationType, delivery.ProviderID, delivery.TPLProviderID, delivery.TrackingNumber, delivery.DestinationType} {
		if field.Validate() != nil || (field.IsKnown() && !rfbsSafeText(*field.Value)) {
			return false
		}
	}
	return delivery.Express.Validate() == nil
}

func rfbsSafeText(value string) bool {
	return len(value) > 0 && len(value) <= 256 && strings.TrimSpace(value) == value && utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}
