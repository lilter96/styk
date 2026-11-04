package orders

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"time"
)

const ActionMirrorPositionUpdate Action = "mirror_position_update"

// PositionBaseline comes from reconciled work and native readback, or an
// explicitly identified expiring operator recovery attestation. A matching SKU
// or a provider-supplied Managed flag is not ownership.
type PositionBaseline struct {
	RecoveryID    string            `json:"recovery_id,omitempty"`
	EvidenceClass string            `json:"evidence_class,omitempty"`
	ValidUntil    time.Time         `json:"valid_until,omitzero"`
	Scope         Scope             `json:"scope"`
	WorkID        string            `json:"work_id"`
	CommandID     string            `json:"command_id"`
	Correlation   Correlation       `json:"correlation"`
	Bindings      []PositionBinding `json:"bindings"`
	ObservedAt    time.Time         `json:"observed_at"`
}

type PositionBinding struct {
	Source PositionWrite  `json:"source"`
	Native TargetPosition `json:"native"`
}

// PositionPatch changes at most one existing native line. Collection creation,
// deletion, assortment changes and VAT changes require separate reviewed paths.
type PositionPatch struct {
	Baseline   PositionBaseline `json:"baseline"`
	PositionID string           `json:"position_id"`
	Identity   LineIdentity     `json:"identity"`
	Quantity   Field[Decimal]   `json:"quantity"`
	Reserve    Field[Decimal]   `json:"reserve"`
	Price      Field[Money]     `json:"price"`
}

func (baseline PositionBaseline) Validate() error {
	if baseline.RecoveryID != "" && (!validID(baseline.RecoveryID) || baseline.EvidenceClass != "OPERATOR_ATTESTED" || !baseline.ValidUntil.After(baseline.ObservedAt)) {
		return ErrInvalid
	}
	if baseline.Scope.Validate() != nil || !validID(baseline.WorkID) || !validID(baseline.CommandID) || baseline.Correlation.Validate() != nil || baseline.Correlation.Scope != baseline.Scope || baseline.Correlation.CustomerOrderID == "" || baseline.ObservedAt.IsZero() || len(baseline.Bindings) == 0 || len(baseline.Bindings) > 1000 {
		return ErrInvalid
	}
	seenID, seenLine := make(map[string]bool, len(baseline.Bindings)), make(map[string]bool, len(baseline.Bindings))
	for _, binding := range baseline.Bindings {
		if binding.Source.Validate(baseline.Scope) != nil || binding.Native.ID == "" || seenID[binding.Native.ID] || seenLine[binding.Source.Identity.Key()] || !PositionRepresented(binding.Source, binding.Native) {
			return ErrInvalid
		}
		seenID[binding.Native.ID], seenLine[binding.Source.Identity.Key()] = true, true
	}
	return nil
}

// ProjectionPositionBaseline reconstructs ownership from the exact reconciled
// source/native pair. Partial patches retain earlier bindings for other lines.
func ProjectionPositionBaseline(work Work, target TargetObservation) (PositionBaseline, error) {
	result := PositionBaseline{Scope: work.Scope, WorkID: work.ID, CommandID: work.Command.ID, Correlation: target.Correlation, ObservedAt: target.ObservedAt}
	if work.State != StateReconciled || !work.Command.Action.IsMirror() || work.Command.CustomerOrder == nil || !target.Complete || target.Scope != work.Scope || target.Correlation.PostingNumber != work.Command.PostingNumber || !baselineCorrelationMatches(work.Command.Correlation, target.Correlation) {
		return result, ErrStale
	}
	sources := work.Command.CustomerOrder.Positions
	if work.Command.PositionPatch != nil {
		sources = work.Command.PositionPatch.appliedSources()
	}
	result.Bindings = make([]PositionBinding, 0, len(sources))
	for _, source := range sources {
		index := uniqueNativePosition(source, target.Positions)
		if index < 0 || !PositionRepresented(source, target.Positions[index]) {
			return result, ErrConflict
		}
		result.Bindings = append(result.Bindings, PositionBinding{Source: source, Native: target.Positions[index]})
	}
	return result, result.Validate()
}

func uniqueNativePosition(source PositionWrite, positions []TargetPosition) int {
	index := -1
	for i, native := range positions {
		if native.MoyskladKind != source.Mapping.MoyskladKind || native.MoyskladID != source.Mapping.MoyskladID {
			continue
		}
		if index >= 0 {
			return -1
		}
		index = i
	}
	return index
}

func PositionRepresented(source PositionWrite, native TargetPosition) bool {
	return validNativePosition(native) && source.Mapping.MoyskladKind == native.MoyskladKind && source.Mapping.MoyskladID == native.MoyskladID && ExactDecimalEqual(source.Quantity, native.Quantity) && desiredDecimal(source.Reserve, native.Reserve) && desiredMoney(source.Price, native.Price) && desiredDecimal(source.VAT, native.VAT)
}

func ExactDecimalEqual(a, b Decimal) bool {
	if a.Validate() != nil || b.Validate() != nil {
		return false
	}
	return normalizedPositionDecimal(a) == normalizedPositionDecimal(b)
}

func normalizedPositionDecimal(v Decimal) string {
	s := string(v)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		return "0"
	}
	return s
}

func desiredDecimal(wanted, actual Field[Decimal]) bool {
	return !wanted.IsKnown() || actual.IsKnown() && ExactDecimalEqual(*wanted.Value, *actual.Value)
}
func desiredMoney(wanted, actual Field[Money]) bool {
	return !wanted.IsKnown() || actual.IsKnown() && wanted.Value.Currency == actual.Value.Currency && ExactDecimalEqual(wanted.Value.Amount, actual.Value.Amount)
}

func (patch PositionPatch) appliedSources() []PositionWrite {
	result := make([]PositionWrite, 0, len(patch.Baseline.Bindings))
	for _, binding := range patch.Baseline.Bindings {
		source := binding.Source
		if binding.Native.ID == patch.PositionID {
			if patch.Quantity.IsKnown() {
				source.Quantity = *patch.Quantity.Value
			}
			if patch.Reserve.IsKnown() {
				source.Reserve = patch.Reserve
			}
			if patch.Price.IsKnown() {
				source.Price = patch.Price
			}
		}
		result = append(result, source)
	}
	return result
}

func (patch PositionPatch) Fields() []string {
	result := make([]string, 0, 3)
	if patch.Quantity.IsKnown() {
		result = append(result, "positions.quantity")
	}
	if patch.Reserve.IsKnown() {
		result = append(result, "positions.reserve")
	}
	if patch.Price.IsKnown() {
		result = append(result, "positions.price")
	}
	return result
}

func (patch PositionPatch) Validate(command Command) error {
	if patch.Baseline.Validate() != nil || patch.Baseline.Scope != command.Scope || patch.Baseline.Correlation != command.Correlation || !command.TargetPositionsComplete || len(command.TargetPositions) == 0 || command.CustomerOrder == nil || len(patch.Fields()) == 0 {
		return ErrInvalid
	}
	if patch.Quantity.Validate() != nil || patch.Reserve.Validate() != nil || patch.Price.Validate() != nil {
		return ErrInvalid
	}
	if patch.Quantity.IsSpecified() && !patch.Quantity.IsKnown() || patch.Reserve.IsSpecified() && !patch.Reserve.IsKnown() || patch.Price.IsSpecified() && !patch.Price.IsKnown() {
		return ErrInvalid
	}
	expected, err := PlanPositionPatch(command, patch.Baseline)
	if err != nil || expected == nil || !positionJSONEqual(*expected, patch) || !slices.Equal(command.CustomerOrder.OwnedFields, patch.Fields()) {
		return ErrStale
	}
	return nil
}

// PlanPositionPatch requires every prior source identity to remain mapped to
// its native UUID and refuses manually changed owned fields before selecting
// one next change. Other pending source changes wait for a later decision.
func PlanPositionPatch(command Command, baseline PositionBaseline) (*PositionPatch, error) {
	if baseline.Validate() != nil || command.CustomerOrder == nil || baseline.Scope != command.Scope || baseline.Correlation != command.Correlation || len(command.CustomerOrder.Positions) != len(baseline.Bindings) {
		return nil, ErrBlocked
	}
	var result *PositionPatch
	for _, wanted := range command.CustomerOrder.Positions {
		binding, index, err := currentPositionBinding(command, baseline, wanted)
		if err != nil {
			return nil, err
		}
		patch, err := planBoundPosition(baseline, binding, command.TargetPositions[index], wanted)
		if err != nil {
			return nil, err
		}
		if result == nil && len(patch.Fields()) > 0 {
			result = &patch
		}
	}
	return result, nil
}

func currentPositionBinding(command Command, baseline PositionBaseline, wanted PositionWrite) (PositionBinding, int, error) {
	var binding PositionBinding
	found := false
	for _, candidate := range baseline.Bindings {
		if candidate.Source.Identity.Key() != wanted.Identity.Key() {
			continue
		}
		if found || !positionMappingsEqual(candidate.Source.Mapping, wanted.Mapping) {
			return binding, -1, ErrConflict
		}
		binding, found = candidate, true
	}
	if !found {
		return binding, -1, ErrBlocked
	}
	index := -1
	for i, native := range command.TargetPositions {
		if native.ID != binding.Native.ID {
			continue
		}
		if index >= 0 || !PositionRepresented(binding.Source, native) || !nativePriorReserveUnchanged(binding.Native, native) {
			return binding, -1, ErrConflict
		}
		index = i
	}
	if index < 0 {
		return binding, -1, ErrConflict
	}
	return binding, index, nil
}

func nativePriorReserveUnchanged(before, after TargetPosition) bool {
	return SameObservedDecimal(before.Reserve, after.Reserve)
}

func planBoundPosition(baseline PositionBaseline, binding PositionBinding, current TargetPosition, wanted PositionWrite) (PositionPatch, error) {
	patch := PositionPatch{Baseline: baseline, PositionID: binding.Native.ID, Identity: wanted.Identity}
	if !desiredDecimal(wanted.VAT, current.VAT) {
		return patch, ErrBlocked
	}
	if !ExactDecimalEqual(wanted.Quantity, current.Quantity) {
		patch.Quantity = Present(wanted.Quantity)
	}
	if wanted.Reserve.IsKnown() && !desiredDecimal(wanted.Reserve, current.Reserve) {
		if !binding.Source.Reserve.IsKnown() || !binding.Native.Reserve.IsKnown() || !desiredDecimal(binding.Source.Reserve, binding.Native.Reserve) {
			return patch, ErrBlocked
		}
		patch.Reserve = wanted.Reserve
	}
	if wanted.Price.IsKnown() && !desiredMoney(wanted.Price, current.Price) {
		if !binding.Source.Price.IsKnown() || !desiredMoney(binding.Source.Price, binding.Native.Price) {
			return patch, ErrBlocked
		}
		patch.Price = wanted.Price
	}
	if err := validatePositionPatchValues(patch, current); err != nil {
		return patch, err
	}
	return patch, nil
}

func validatePositionPatchValues(patch PositionPatch, current TargetPosition) error {
	if patch.Quantity.IsKnown() && !positiveDecimal(*patch.Quantity.Value) || patch.Reserve.IsKnown() && !nonnegativeDecimal(*patch.Reserve.Value) || patch.Price.IsKnown() && (patch.Price.Value.Validate() != nil || !nonnegativeDecimal(patch.Price.Value.Amount)) {
		return ErrInvalid
	}
	if patch.Quantity.IsKnown() || patch.Reserve.IsKnown() {
		if !current.Shipped.IsKnown() || !decimalZero(*current.Shipped.Value) || !current.Reserve.IsKnown() {
			return ErrBlocked
		}
		// A non-zero reserve may only change with known STYK ownership. Quantity
		// changes with a live reserve need the reserve change in the same intent.
		if patch.Quantity.IsKnown() && !decimalZero(*current.Reserve.Value) && !patch.Reserve.IsKnown() {
			return ErrBlocked
		}
	}
	quantity, reserve := current.Quantity, current.Reserve
	if patch.Quantity.IsKnown() {
		quantity = *patch.Quantity.Value
	}
	if patch.Reserve.IsKnown() {
		reserve = patch.Reserve
	}
	if reserve.IsKnown() && !nonnegativeNotGreater(*reserve.Value, quantity) {
		return ErrBlocked
	}
	if patch.Price.IsKnown() && (!current.Price.IsKnown() || current.Price.Value.Currency != patch.Price.Value.Currency) {
		return ErrBlocked
	}
	if patch.Price.IsKnown() && (!current.Discount.IsKnown() || !decimalZero(*current.Discount.Value)) {
		return ErrBlocked
	}
	return nil
}

func SameObservedDecimal(a, b Field[Decimal]) bool {
	if a.Validate() != nil || b.Validate() != nil {
		return false
	}
	if a.IsKnown() && b.IsKnown() {
		return ExactDecimalEqual(*a.Value, *b.Value)
	}
	return a.Presence == b.Presence || a.IsMissing() && b.IsMissing()
}

// PositionPatchObserved verifies one patch and the unchanged complete native
// frontier. It does not claim other pending source lines are synchronized.
func PositionPatchObserved(command Command, target TargetObservation) bool {
	patch := command.PositionPatch
	if patch == nil || !command.TargetPositionsComplete || len(command.TargetPositions) != len(target.Positions) {
		return false
	}
	before := slices.Clone(command.TargetPositions)
	for i := range before {
		if before[i].ID != patch.PositionID {
			continue
		}
		if patch.Quantity.IsKnown() {
			before[i].Quantity = *patch.Quantity.Value
		}
		if patch.Reserve.IsKnown() {
			before[i].Reserve = patch.Reserve
		}
		if patch.Price.IsKnown() {
			before[i].Price = patch.Price
		}
	}
	return NativePositionsEqual(before, target.Positions)
}

func NativePositionsEqual(before, after []TargetPosition) bool {
	if len(before) != len(after) {
		return false
	}
	byID := make(map[string]TargetPosition, len(after))
	for _, p := range after {
		if p.ID == "" {
			return false
		}
		if _, duplicate := byID[p.ID]; duplicate {
			return false
		}
		byID[p.ID] = p
	}
	for _, p := range before {
		a, found := byID[p.ID]
		if !found || !NativePositionEqual(p, a) {
			return false
		}
		delete(byID, p.ID)
	}
	return len(byID) == 0
}

func NativePositionEqual(before, after TargetPosition) bool {
	return validNativePosition(before) && validNativePosition(after) && before.ID == after.ID && sameNativeVATEnabled(before.VATEnabled, after.VATEnabled) && before.MoyskladKind == after.MoyskladKind && before.MoyskladID == after.MoyskladID && ExactDecimalEqual(before.Quantity, after.Quantity) && SameObservedDecimal(before.Reserve, after.Reserve) && SameObservedDecimal(before.Shipped, after.Shipped) && SameObservedDecimal(before.Discount, after.Discount) && SameObservedDecimal(before.VAT, after.VAT) && sameObservedPrice(before.Price, after.Price)
}

func sameObservedPrice(a, b Field[Money]) bool {
	if a.Validate() != nil || b.Validate() != nil {
		return false
	}
	if a.IsKnown() && b.IsKnown() {
		return a.Value.Currency == b.Value.Currency && ExactDecimalEqual(a.Value.Amount, b.Value.Amount)
	}
	return a.Presence == b.Presence || a.IsMissing() && b.IsMissing()
}

func nonnegativeNotGreater(a, b Decimal) bool {
	if !nonnegativeDecimal(a) || !nonnegativeDecimal(b) {
		return false
	}
	left, right := strings.Split(normalizedPositionDecimal(a), "."), strings.Split(normalizedPositionDecimal(b), ".")
	if len(left[0]) != len(right[0]) {
		return len(left[0]) < len(right[0])
	}
	if left[0] != right[0] {
		return left[0] < right[0]
	}
	lf, rf := "", ""
	if len(left) == 2 {
		lf = left[1]
	}
	if len(right) == 2 {
		rf = right[1]
	}
	width := max(len(lf), len(rf))
	return lf+strings.Repeat("0", width-len(lf)) <= rf+strings.Repeat("0", width-len(rf))
}

func positionMappingsEqual(a, b ProductMappingRef) bool {
	a.ConfirmedAt, b.ConfirmedAt = a.ConfirmedAt.UTC(), b.ConfirmedAt.UTC()
	return positionJSONEqual(a, b)
}

func positionJSONEqual(a, b any) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	return err == nil && bytes.Equal(left, right)
}

func (baseline PositionBaseline) Equal(other PositionBaseline) bool {
	return positionJSONEqual(baseline, other)
}

func baselineCorrelationMatches(command, actual Correlation) bool {
	return command.Scope == actual.Scope && command.PostingNumber == actual.PostingNumber && command.SyncID == actual.SyncID && command.Marker == actual.Marker && (command.CustomerOrderID == "" || command.CustomerOrderID == actual.CustomerOrderID)
}

func validNativePosition(native TargetPosition) bool {
	if !validID(native.ID) || !validID(native.MoyskladID) || (native.MoyskladKind != "product" && native.MoyskladKind != "variant") || !positiveDecimal(native.Quantity) {
		return false
	}
	for _, field := range []Field[Decimal]{native.Reserve, native.Shipped, native.VAT} {
		if field.Validate() != nil || field.IsKnown() && !nonnegativeDecimal(*field.Value) {
			return false
		}
	}
	if native.Discount.Validate() != nil || native.Discount.IsKnown() && native.Discount.Value.Validate() != nil {
		return false
	}
	return native.Price.Validate() == nil && (!native.Price.IsKnown() || native.Price.Value.Validate() == nil && nonnegativeDecimal(native.Price.Value.Amount))
}

func sameNativeVATEnabled(a, b Field[bool]) bool {
	if a.Validate() != nil || b.Validate() != nil {
		return false
	}
	if a.IsKnown() && b.IsKnown() {
		return *a.Value == *b.Value
	}
	return a.Presence == b.Presence || a.IsMissing() && b.IsMissing()
}
