package orderinventory

// DB admission precedes the actual native reads; DB recording follows their
// completion. A new durable source traversal must start after that recording.
// Actual provider/client read clocks remain separate and are never relabelled.
func nativePointBaselineOrdered(input Input, observation Observation) bool {
	if !nativePointFresh(input, observation) {
		return false
	}
	required := observation.ReconciledAt
	if observation.NativeConfirmedAt.After(required) {
		required = observation.NativeConfirmedAt
	}
	if required.IsZero() || !observation.NativePointAdmittedAt.After(required) || !observation.NativePointRecordedAt.After(observation.NativePointAdmittedAt) || observation.NativeReadCode != "" {
		return false
	}
	start := NativeEffectBaselineStart(input.Pool, input.Baseline, input.BaselineStartedAt, input.Now)
	return !start.IsZero() && start.After(observation.NativePointRecordedAt)
}

// Current aggregate reserve cannot refresh an old order-attribution proof:
// unrelated reserves may compensate an independently changed native order.
func nativePointFresh(input Input, observation Observation) bool {
	admitted, recorded := observation.NativePointAdmittedAt, observation.NativePointRecordedAt
	return input.MaxAge > 0 && !admitted.IsZero() && !admitted.After(input.Now) && !recorded.After(input.Now) && input.Now.Sub(admitted) < input.MaxAge
}
