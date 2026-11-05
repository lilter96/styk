package orders

func deferredNativeEvent(f SplitAncestorSettlementFacts, previous, next SplitDeferredNative) bool {
	if next.Work.Command.Action != ActionMirrorUpdate {
		return deferredReduction(f, previous, next)
	}
	prior, err := ProjectionPositionBaseline(previous.Work, previous.Target)
	if err != nil || !deferredNativeBound(f, next) || !splitScalarMetadataObserved(next.Work, next.Target, prior) {
		return false
	}
	// The existing forest event verifier uses these exact immutable facts; it
	// performs no freshness or absence inference from this historical prefix.
	previousAt, before := deferredOriginAttempt(f.NativeAttempts, previous.Work.ID)
	nextAt, after := deferredOriginAttempt(f.NativeAttempts, next.Work.ID)
	if !before || !after {
		return false
	}
	forest, complete := deferredMetadataHistory(f)
	if !complete {
		return false
	}
	return forestScalarMetadata(forest,
		SplitForestNative{Work: previous.Work, Source: previous.Source, Target: previous.Target, Attempt: previousAt},
		SplitForestNative{Work: next.Work, Source: next.Source, Target: next.Target, Attempt: nextAt})
}

func deferredMetadataHistory(f SplitAncestorSettlementFacts) (SplitForestFacts, bool) {
	forest := SplitForestFacts{Lineage: f.Lineage, Attempts: f.Attempts}
	events := append([]SplitDeferredNative{f.Deferred.Origin}, f.Deferred.Reductions...)
	forest.Native = make([]SplitForestNative, 0, len(events))
	for _, event := range events {
		at, found := deferredOriginAttempt(f.NativeAttempts, event.Work.ID)
		if !found {
			return forest, false
		}
		forest.Native = append(forest.Native, SplitForestNative{Work: event.Work, Source: event.Source, Target: event.Target, Attempt: at})
	}
	return forest, true
}

func deferredMetadataClocks(f SplitAncestorSettlementFacts, timing NestedSplitAttempt, splits map[string]NestedSplitAttempt) bool {
	for _, step := range f.Deferred.Spine {
		split, found := splits[step.Work.ID]
		if !found || split.CreatedAt.Before(timing.ReconciledAt) && timing.CreatedAt.Before(split.ReconciledAt) {
			return false
		}
	}
	return true
}
