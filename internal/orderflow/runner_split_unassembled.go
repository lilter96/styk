package orderflow

import (
	"context"
	"slices"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

// Allocation readback shares the existing work lease and provider guard. Each
// bounded page is committed with its immutable source observations, not copied
// into the work JSON. A replacement worker resumes those exact point reads.
func (r *Runner) readbackUnassembledSplit(ctx context.Context, work orders.Work, client orders.Provider) error {
	decision, err := r.reviewedDecision(ctx, work)
	store, ok := r.store.(orders.SplitReadStore)
	if err != nil || !ok {
		return r.deferReadback(ctx, work, "SPLIT_REVIEW_OR_READ_EVIDENCE_UNAVAILABLE")
	}
	candidates, err := orders.UnassembledSplitCandidates(work.Command, work.Receipt)
	if err != nil {
		return r.deferReadback(ctx, work, "SPLIT_ACKNOWLEDGEMENT_UNVERIFIED")
	}
	reads, err := store.OrderSplitReadbacks(ctx, work.Scope, work.ID)
	if err != nil {
		return r.deferReadback(ctx, work, "SPLIT_READ_EVIDENCE_UNAVAILABLE")
	}
	selected, cursor := unassembledReadWindow(candidates, reads, work.Receipt.SplitReadCursor)
	fresh := r.readUnassembledCandidates(ctx, work, client, selected)
	work.Receipt.SplitReadCursor = cursor
	reads = mergeUnassembledReads(reads, fresh)
	resolution, err := orders.ResolveUnassembledSplit(decision.Posting, work, reads)
	if err != nil {
		return r.deferReadback(ctx, work, "SPLIT_ALLOCATION_EVIDENCE_CONFLICT")
	}
	return r.completeUnassembledSplit(ctx, work, reads, fresh, resolution)
}

func (r *Runner) readUnassembledCandidates(ctx context.Context, work orders.Work, client orders.Provider, candidates []string) []orders.SplitReadback {
	reads := make([]orders.SplitReadback, 0, 10)
	for _, number := range candidates {
		if len(reads) == 10 {
			break
		}
		posting, err := client.ReadOrderPosting(ctx, work.Scope, number)
		read := orders.SplitReadback{PostingNumber: number, Code: "SOURCE_UNAVAILABLE", CheckedAt: r.now()}
		if err == nil && validUnassembledPoint(work, number, posting, read.CheckedAt) {
			posting.PII = nil
			read.Code, read.Posting = "OBSERVED", &posting
			read.CheckedAt = posting.Evidence.CheckedAt
			if read.CheckedAt.IsZero() {
				read.CheckedAt = posting.Evidence.ObservedAt
			}
		}
		reads = append(reads, read)
		_, stop := refreshReadFailure(err)
		if stop || ctx.Err() != nil {
			break
		}
	}
	return reads
}

// A decoded point must still belong to the immutable intent. Rejecting only
// during whole-allocation resolution discards other successful point reads and
// can starve every later page. Never retain the body of a rejected point.
func validUnassembledPoint(work orders.Work, number string, posting orders.Posting, now time.Time) bool {
	if posting.Validate() != nil || posting.Scope != work.Scope || posting.PostingNumber != number || posting.CommercialOrderID != work.Command.CommercialOrderID || posting.Evidence.Origin != orders.OriginOzon {
		return false
	}
	checked := posting.Evidence.CheckedAt
	if checked.IsZero() {
		checked = posting.Evidence.ObservedAt
	}
	return !checked.IsZero() && !checked.Before(work.Receipt.ObservedAt) && !checked.After(now) && !posting.Evidence.ObservedAt.After(now)
}

func mergeUnassembledReads(cached, fresh []orders.SplitReadback) []orders.SplitReadback {
	result := slices.Clone(cached)
	for _, read := range fresh {
		index := slices.IndexFunc(result, func(old orders.SplitReadback) bool { return old.PostingNumber == read.PostingNumber })
		if index < 0 {
			result = append(result, read)
			continue
		}
		result[index] = read
	}
	return result
}

func (r *Runner) completeUnassembledSplit(ctx context.Context, work orders.Work, reads, fresh []orders.SplitReadback, resolution orders.UnassembledSplitResolution) error {
	receipt := work.Receipt
	receipt.ObservedPostings = nil
	receipt.SplitReadCount = 0
	for _, read := range reads {
		if read.Code == "OBSERVED" && read.Posting != nil {
			receipt.SplitReadCount++
		}
	}
	state, code := orders.StatePolling, "UNASSEMBLED_SPLIT_READBACK_PENDING"
	if work.State == orders.StateUnknown {
		state = orders.StateUnknown
	}
	if resolution.Confirmed {
		state, code = orders.StateReconciled, "UNASSEMBLED_SPLIT_ALLOCATION_CONFIRMED"
		receipt.PostingNumbers = resolution.PostingNumbers
	}
	receipt.State = state
	return r.complete(ctx, work, orders.Completion{State: state, Code: code, Receipt: receipt, SplitReads: fresh, NextRunAt: r.now().Add(r.options.PollInterval)})
}

// Unattempted identities take priority. After that, every candidate (including
// successful-but-not-yet-conserving observations) gets a fair new point read.
// The cursor is persisted with the same work completion, never process memory.
func unassembledReadWindow(candidates []string, reads []orders.SplitReadback, cursor int) ([]string, int) {
	selected := make([]string, 0, 10)
	seen := make(map[string]bool, len(reads))
	for _, read := range reads {
		seen[read.PostingNumber] = true
	}
	next := 0
	for i, number := range candidates {
		if !seen[number] {
			selected = append(selected, number)
			next = i + 1
		}
		if len(selected) == 10 {
			return selected, next % len(candidates)
		}
	}
	if len(selected) > 0 || len(candidates) == 0 {
		return selected, next
	}
	if cursor < 0 {
		cursor = 0
	}
	cursor %= len(candidates)
	for i := 0; i < len(candidates) && len(selected) < 10; i++ {
		index := (cursor + i) % len(candidates)
		selected = append(selected, candidates[index])
	}
	return selected, (cursor + len(selected)) % len(candidates)
}
