package orderflow

import "github.com/lilter96/styk/internal/orders"

// Status projection owns only an unchanged, exact revision last written by
// STYK. Legacy/foreign history and merchant edits preserve the native state;
// independent authorized metadata updates can still proceed.
func preserveNativeState(command *orders.Command, target orders.TargetObservation, baseline orders.NativeStateBaseline) {
	if command.CustomerOrder.StateID == target.Fields.StateID {
		return
	}
	if !baseline.OwnsTarget(target) {
		command.CustomerOrder.StateID = target.Fields.StateID
		return
	}
	command.NativeStateBaseline = &baseline
}
