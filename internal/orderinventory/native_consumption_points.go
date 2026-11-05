package orderinventory

import (
	"github.com/lilter96/styk/internal/commitment"
	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

func nativeConsumptionStockOrdered(input Input, o Observation) bool {
	s := input.Baseline
	if !nativeConsumptionParentPoint(o) || !nativePointBaselineOrdered(input, o) || !nativeDemandProviderVersion(o, input.Now) {
		return false
	}
	if !s.ObservationFound || !s.TraversalComplete || s.ImportID == nil || *s.ImportID <= 0 || s.Physical == nil || s.Reserve == nil || s.Tenant != input.Pool.Tenant || s.Account != input.Pool.MoyskladAccount || s.WarehouseID != input.Pool.WarehouseID || s.ItemKind != input.Pool.ItemKind || s.ItemID != input.Pool.ItemID {
		return false
	}
	_, physical := whole(*s.Physical)
	_, reserve := whole(*s.Reserve)
	at := commitment.SourceFreshnessTime(s, input.Now)
	return physical && reserve && at != nil && !at.After(input.Now) && input.MaxAge > 0 && input.Now.Sub(*at) < input.MaxAge
}

func nativeConsumptionParentPoint(o Observation) bool {
	p := o.NativeParentPoint
	if p == nil || p.Validate() != nil || o.Target == nil || o.Target.ProviderUpdatedAt.Validate() != nil || !o.Target.ProviderUpdatedAt.IsKnown() || o.NativeReadCode != "" || o.NativeCredentialRevision < 1 || p.CredentialRevision != o.NativeCredentialRevision || p.Scope != o.Posting.Scope || p.Correlation != o.Binding {
		return false
	}
	if p.TargetRevision != o.NativePointTargetRevision || p.TargetRevision != o.Target.Revision || p.StoreID != o.Target.Fields.StoreID || !p.Applicable.IsKnown() || *p.Applicable.Value || !orders.NativePositionsEqual(p.Positions, o.Target.Positions) {
		return false
	}
	return !p.SourceDeleted.IsKnown() || *p.SourceDeleted.Value == ""
}

func nativeConsumptionPoints(input Input, o Observation, f orders.NativeConsumptionFacts) (bool, string) {
	returned := false
	for _, event := range f.Events {
		if event.Document == nil {
			continue // Snapshot validation proved these were no-application outcomes.
		}
		point, found := o.NativeDocumentPoints[event.Work.ID]
		if !found || point.CredentialRevision != o.NativeCredentialRevision || !nativeConsumptionPointOrdered(input, point, event, f.Correlation) {
			return false, "native_document_fresh_point_required"
		}
		actual := point.Observation
		if actual.Correlation != event.Document.Correlation || !orders.NativeDocumentMatches(event.Work.Command, *actual) || ordermoney.NativeDocumentTotal(event.Work.Command, *actual).Code != "" {
			return false, "native_document_current_facts_changed"
		}
		if actual.Correlation.Kind == "salesreturn" && actual.Applicable.IsKnown() && *actual.Applicable.Value {
			returned = true
		}
		if actual.Correlation.Kind == "demand" {
			seen, valid := nativeConsumptionReturns(*actual, o, f)
			if !valid {
				return false, "native_document_return_coverage_unproven"
			}
			returned = returned || seen
		}
	}
	return returned, ""
}

func nativeConsumptionPointOrdered(input Input, p orders.NativeDocumentPointEvidence, event orders.NativeConsumptionEvent, parent orders.Correlation) bool {
	if !p.Eligible || p.Code != "" || p.WorkID != event.Work.ID || p.Parent != parent || p.CredentialRevision < 1 || p.Observation == nil || p.Observation.Validate() != nil || !p.Observation.Complete || p.Observation.Scope != parent.Scope {
		return false
	}
	if input.MaxAge <= 0 || p.CheckedAt.IsZero() || p.CheckedAt.After(input.Now) || input.Now.Sub(p.CheckedAt) >= input.MaxAge || p.StartedAt.IsZero() || p.FinishedAt.Before(p.StartedAt) || p.FinishedAt.After(input.Now) || p.AdmittedAt.IsZero() || !p.AdmittedAt.After(event.ConfirmedAt) || !p.RecordedAt.After(p.AdmittedAt) || p.RecordedAt.After(p.CheckedAt) {
		return false
	}
	start := NativeEffectBaselineStart(input.Pool, input.Baseline, input.BaselineStartedAt, input.Now)
	return !start.IsZero() && start.After(p.RecordedAt)
}

func nativeConsumptionReturns(d orders.NativeDocumentObservation, o Observation, f orders.NativeConsumptionFacts) (bool, bool) {
	if !d.ReturnsComplete || !d.Sum.IsKnown() {
		return false, false
	}
	returned := false
	seen := make(map[string]bool, len(d.Returns))
	for _, evidence := range d.Returns {
		if seen[evidence.ID] || evidence.Validate(d.Correlation.DocumentID, d.Sum.Value.Currency) != nil || !nativeConsumptionKnownReturn(evidence, o, f) {
			return false, false
		}
		seen[evidence.ID] = true
		returned = returned || *evidence.Applicable.Value
	}
	return returned, true
}
