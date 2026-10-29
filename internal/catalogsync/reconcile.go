package catalogsync

import (
	"context"

	"github.com/lilter96/styk/internal/catalog"
)

func (runner Runner) poll(ctx context.Context, work Work) error {
	client, err := resolveProvider(ctx, runner.Resolver, work.Scope, work.Command.Target.Account, work.Credentials)
	if err != nil {
		return runner.stop(ctx, work, "TASK_CREDENTIALS_UNAVAILABLE")
	}
	poller, ok := client.(Poller)
	if !ok || work.Receipt.TaskID == "" {
		return runner.stop(ctx, work, "TASK_POLL_UNAVAILABLE")
	}
	read, cancel := context.WithTimeout(ctx, providerTimeout)
	receipt, err := poller.PollCatalog(read, work.Command, work.Receipt)
	cancel()
	if err != nil {
		return runner.stop(ctx, work, "TASK_POLL_UNAVAILABLE")
	}
	if receipt.TaskID == "" {
		receipt.TaskID = work.Receipt.TaskID
	}
	if !validReceiptFor(work.Command, receipt) || receipt.TaskID != work.Receipt.TaskID {
		return runner.stop(ctx, work, "TASK_RESULT_INVALID")
	}
	if receipt.State != catalog.ReceiptApplied && receipt.State != catalog.ReceiptRejected && receipt.State != catalog.ReceiptAccepted {
		return runner.stop(ctx, work, "TASK_OUTCOME_NOT_FINAL")
	}
	return runner.recordReceipt(ctx, work, receipt)
}

func (runner Runner) readback(ctx context.Context, work Work) error {
	clients, err := runner.resolvePair(ctx, work)
	if err != nil {
		return runner.stop(ctx, work, "READBACK_CREDENTIALS_UNAVAILABLE")
	}
	source, err := readProduct(ctx, clients.source, work.Scope, work.Decision.Source.Identity)
	if err != nil {
		return runner.stop(ctx, work, "READBACK_SOURCE_UNAVAILABLE")
	}
	target, err := observeReadbackTarget(ctx, clients.target, work)
	if err != nil {
		return runner.stop(ctx, work, "READBACK_TARGET_UNAVAILABLE")
	}
	observed := target
	if work.Command.Target.Account == source.Identity.Account {
		observed = source
	}
	if !selectedFieldsMatch(work.Command, observed) {
		return runner.stop(ctx, work, "READBACK_SELECTED_FIELDS_DIFFER")
	}
	baseline, err := catalog.NewBaseline(source, target)
	if err != nil {
		return runner.stop(ctx, work, "READBACK_VALUES_INVALID")
	}
	var created *catalog.Identity
	if work.Command.Action == catalog.ActionCreate {
		created = &target.Identity
	}
	return runner.complete(ctx, work, Completion{State: StateReconciled, Code: "READBACK_MATCHED_EFFECT", Receipt: work.Receipt,
		CreatedTarget: created, Baseline: &baseline, ObservedSource: &source, ObservedTarget: &target})
}

func observeReadbackTarget(ctx context.Context, reader catalog.Reader, work Work) (catalog.Product, error) {
	if work.Command.Action != catalog.ActionCreate {
		return readProduct(ctx, reader, work.Scope, work.Decision.Target.Identity)
	}
	identity := work.CreatedTarget
	if identity == nil {
		identity = createdIdentity(work.Command, work.Receipt)
	}
	if identity != nil {
		return readProduct(ctx, reader, work.Scope, *identity)
	}
	snapshot, err := readCreationTarget(ctx, reader, work.Scope, creationEnrollment(work.Command))
	if err != nil {
		return catalog.Product{}, err
	}
	if len(snapshot.Products) != 1 || snapshot.Products[0].Revision == "" || !creationIdentityMatches(work.Command, snapshot.Products[0].Identity) {
		return catalog.Product{}, ErrStale
	}
	return snapshot.Products[0], nil
}

func creationIdentityMatches(command catalog.Command, identity catalog.Identity) bool {
	if identity.Account != command.Target.Account {
		return false
	}
	return command.Target.OfferID == "" || identity.OfferID == command.Target.OfferID
}

func selectedFieldsMatch(command catalog.Command, observed catalog.Product) bool {
	for field, value := range command.Values {
		actual, ok := observed.Fields[field]
		if !ok || !catalog.EqualValue(value, actual) {
			return false
		}
	}
	for extension, value := range command.Extensions {
		actual, ok := observed.Extensions[extension]
		if !ok || !catalog.EqualValue(value, actual) {
			return false
		}
	}
	if command.Parent != nil && (observed.Parent == nil || !command.Parent.SameEntity(*observed.Parent)) {
		return false
	}
	return true
}

// RebaseDecision lets the store advance pending commands of one enrollment after
// a sibling command is RECONCILED. The immutable preview remains unchanged. The
// store must serialize sibling claims and rebase only on these verified facts.
// Unrelated concurrent edits reject rebasing and require a fresh review.
func RebaseDecision(decision Decision, applied catalog.Command, source, target catalog.Product) (Decision, error) {
	if applied.Action != catalog.ActionUpdate || !selectedFieldsMatch(applied, productForAccount(source, target, applied.Target.Account)) {
		return Decision{}, ErrStale
	}
	expectedSource, expectedTarget := decision.Source, decision.Target
	if applied.Target.Account == expectedSource.Identity.Account {
		expectedSource = withObservedFields(expectedSource, source, applied)
	} else if applied.Target.Account == expectedTarget.Identity.Account {
		expectedTarget = withObservedFields(expectedTarget, target, applied)
	} else {
		return Decision{}, ErrStale
	}
	if !sameFacts(expectedSource, source, false) || !sameFacts(expectedTarget, target, false) {
		return Decision{}, ErrStale
	}
	decision.Source, decision.Target = source, target
	decision.Commands = rebaseCommandRevisions(decision.Commands, source, target)
	return decision, nil
}

func rebaseCommandRevisions(commands []catalog.Command, source, target catalog.Product) []catalog.Command {
	result := make([]catalog.Command, 0, len(commands))
	for _, command := range commands {
		command.ExpectedSourceRevision = productForAccount(source, target, command.Source.Account).Revision
		command.ExpectedTargetRevision = productForAccount(source, target, command.Target.Account).Revision
		result = append(result, command)
	}
	return result
}

// RebaseWork is restricted to unsent siblings. The store persists the revised
// revision guards and fingerprint atomically before that sibling is dispatched.
// Values, identities, command ID and idempotency key remain exactly as approved.
func RebaseWork(work Work, applied catalog.Command, source, target catalog.Product) (Work, error) {
	if work.State != StateQueued && work.State != StateClaimed {
		return Work{}, ErrStale
	}
	decision, err := RebaseDecision(work.Decision, applied, source, target)
	if err != nil {
		return Work{}, err
	}
	for _, command := range decision.Commands {
		if command.ID != work.Command.ID {
			continue
		}
		fingerprint, err := catalog.CommandFingerprint(command)
		if err != nil {
			return Work{}, err
		}
		work.Decision, work.Command, work.CommandFingerprint = decision, command, fingerprint
		return work, nil
	}
	return Work{}, ErrStale
}

func productForAccount(source, target catalog.Product, account catalog.Account) catalog.Product {
	if source.Identity.Account == account {
		return source
	}
	return target
}

func withObservedFields(expected, observed catalog.Product, command catalog.Command) catalog.Product {
	fields := make(map[catalog.Field]catalog.Value, len(expected.Fields)+len(command.Values))
	for field, value := range expected.Fields {
		fields[field] = value
	}
	for field := range command.Values {
		fields[field] = observed.Fields[field]
	}
	extensions := make(map[string]catalog.Value, len(expected.Extensions)+len(command.Extensions))
	for key, value := range expected.Extensions {
		extensions[key] = value
	}
	for key := range command.Extensions {
		extensions[key] = observed.Extensions[key]
	}
	expected.Fields, expected.Extensions = fields, extensions
	return expected
}
