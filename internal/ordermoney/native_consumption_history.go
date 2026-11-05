package ordermoney

import "github.com/lilter96/styk/internal/orders"

func (h *nativeConsumptionHistory) read(c orders.Command, f orders.NativeConsumptionFacts) orders.Validation {
	for _, event := range f.Events {
		w := event.Work
		if _, duplicate := h.works[w.ID]; duplicate || !nativeConsumptionScope(c, w) {
			return consumptionIssue("native_consumption_identity_unverified")
		}
		h.works[w.ID] = event
	}
	for _, event := range f.Events {
		if issue := h.event(c, f, event); issue.Code != "" {
			return issue
		}
	}
	return orders.Validation{}
}

func nativeConsumptionScope(c orders.Command, w orders.Work) bool {
	if w.ID == "" || w.Scope != c.Scope || w.Command.Scope != c.Scope || w.Command.Correlation != c.Correlation || w.Command.PostingNumber != c.PostingNumber || !w.Command.Action.IsNativeMovement() || w.Command.NativeMovement == nil || w.Command.Validate() != nil {
		return false
	}
	fingerprint, err := orders.CommandFingerprint(w.Command)
	return err == nil && fingerprint == w.CommandFingerprint
}

func (h *nativeConsumptionHistory) event(c orders.Command, f orders.NativeConsumptionFacts, event orders.NativeConsumptionEvent) orders.Validation {
	w := event.Work
	key := nativeConsumptionIdentity{WorkID: w.ID, Fingerprint: w.CommandFingerprint, State: w.State, DispatchCount: w.DispatchCount, ReceiptState: w.Receipt.State, HTTPStatus: w.Receipt.HTTPStatus, ReceiptCode: w.Receipt.Code}
	if nativeConsumptionNoEffect(event) {
		h.keys = append(h.keys, key)
		return orders.Validation{}
	}
	if !nativeConsumptionReconciled(event, f) {
		return consumptionIssue("native_consumption_outcome_unresolved")
	}
	document := *event.Document
	key.DocumentID, key.DocumentRevision = document.Correlation.DocumentID, document.Revision
	docKey := document.Correlation.Kind + "/" + key.DocumentID
	if h.docs[docKey] {
		return consumptionIssue("native_consumption_document_not_unique")
	}
	h.docs[docKey] = true
	h.keys = append(h.keys, key)
	if issue := h.checkReturnBasis(event); issue.Code != "" {
		return issue
	}
	return h.appendEvent(c, event)
}

func nativeConsumptionNoEffect(event orders.NativeConsumptionEvent) bool {
	w, r := event.Work, event.Work.Receipt
	if event.Document != nil || event.Parent != nil || !event.ConfirmedAt.IsZero() || event.WriteIntent.Validate() != nil || !event.WriteIntent.IsKnown() {
		return false
	}
	if w.DispatchCount == 0 && (w.State == orders.StateBlocked || w.State == orders.StateRejected) {
		return !*event.WriteIntent.Value
	}
	if w.DispatchCount != 1 || w.State != orders.StateRejected || !*event.WriteIntent.Value || r.State != orders.StateRejected || r.CommandID != w.Command.ID || r.ExternalID != "" || r.Code != "PROVIDER_REJECTED" {
		return false
	}
	switch r.HTTPStatus {
	case 400, 401, 403, 404, 405, 412, 413, 415, 422, 429:
		return true // The existing adapter's explicit no-application responses.
	default:
		return false
	}
}

func nativeConsumptionReconciled(event orders.NativeConsumptionEvent, f orders.NativeConsumptionFacts) bool {
	w, d, p := event.Work, event.Document, event.Parent
	if w.State != orders.StateReconciled || w.DispatchCount != 1 || d == nil || p == nil || event.ConfirmedAt.IsZero() || event.ConfirmedAt.After(f.CheckedAt) || d.ObservedAt.After(event.ConfirmedAt) || p.ObservedAt.After(event.ConfirmedAt) || event.WriteIntent.Validate() != nil || !event.WriteIntent.IsKnown() || !*event.WriteIntent.Value {
		return false
	}
	r := w.Receipt
	if r.State != orders.StateAcknowledged || r.HTTPStatus < 200 || r.HTTPStatus >= 300 || r.CommandID != w.Command.ID || r.ExternalID != d.Correlation.DocumentID || r.ExternalID == "" {
		return false
	}
	return orders.NativeDocumentMatches(w.Command, *d) && orders.NativeParentReadback(w.Command, *p) && NativeParentEffectObserved(w.Command, *p) && NativeDocumentTotal(w.Command, *d).Code == ""
}

func (h *nativeConsumptionHistory) appendEvent(c orders.Command, event orders.NativeConsumptionEvent) orders.Validation {
	w, m := event.Work, event.Work.Command.NativeMovement
	sealed := orders.NativeConsumedEvent{WorkID: w.ID, CommandFingerprint: w.CommandFingerprint, DocumentID: event.Document.Correlation.DocumentID, Kind: m.Correlation.Kind, DemandWorkID: m.DemandWorkID, Lines: make([]orders.NativeConsumedLine, 0, len(m.Lines))}
	for _, line := range m.Lines {
		sealed.Lines = append(sealed.Lines, orders.NativeConsumedLine{OrderPositionID: line.OrderPositionID, BasisPositionID: line.BasisPositionID, Quantity: line.Quantity})
		if w.Command.Action == orders.ActionDemandCreate && *m.Applicable.Value {
			h.demands[line.OrderPositionID] = append(h.demands[line.OrderPositionID], line.Quantity)
		}
	}
	if *m.Applicable.Value && w.Command.Action == orders.ActionDemandCreate && m.ParentEffect != c.NativeMovement.ParentEffect && (m.ParentEffect == orders.NativeParentUnpostedDocument || c.NativeMovement.ParentEffect == orders.NativeParentUnpostedDocument) {
		return consumptionIssue("native_consumption_parent_mode_overlap_unproven")
	}
	h.seal.Events = append(h.seal.Events, sealed)
	return orders.Validation{}
}

func (h *nativeConsumptionHistory) checkReturnBasis(event orders.NativeConsumptionEvent) orders.Validation {
	w, m := event.Work, event.Work.Command.NativeMovement
	if w.Command.Action != orders.ActionSalesReturnCreate {
		return orders.Validation{}
	}
	basis, found := h.works[m.DemandWorkID]
	if !found || basis.Work.Command.Action != orders.ActionDemandCreate || basis.Document == nil || m.DemandBasis == nil || m.DemandBasis.Correlation != basis.Document.Correlation || basis.Work.Command.NativeMovement == nil || !basis.Work.Command.NativeMovement.Applicable.IsKnown() || !*basis.Work.Command.NativeMovement.Applicable.Value || !orders.NativeDocumentMatches(basis.Work.Command, *m.DemandBasis) || NativeDocumentTotal(basis.Work.Command, *m.DemandBasis).Code != "" {
		return consumptionIssue("native_consumption_return_basis_unproven")
	}
	for _, line := range m.Lines {
		position, present := nativeConsumptionBasisPosition(basis.Document.Positions, line.BasisPositionID)
		if !present || position.ItemKind != line.Mapping.MoyskladKind || position.ItemID != line.Mapping.MoyskladID {
			return consumptionIssue("native_consumption_return_position_unproven")
		}
	}
	return orders.Validation{}
}
