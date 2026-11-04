package orderflow

import (
	"context"
	"slices"

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
	fresh := r.readUnassembledCandidates(ctx, work, client, candidates, reads)
	reads = mergeUnassembledReads(reads, fresh)
	resolution, err := orders.ResolveUnassembledSplit(decision.Posting, work, reads)
	if err != nil {
		return r.deferReadback(ctx, work, "SPLIT_ALLOCATION_EVIDENCE_CONFLICT")
	}
	return r.completeUnassembledSplit(ctx, work, reads, fresh, resolution)
}

func (r *Runner) readUnassembledCandidates(ctx context.Context, work orders.Work, client orders.Provider, candidates []string, cached []orders.SplitReadback) []orders.SplitReadback {
	reads := make([]orders.SplitReadback, 0, 10)
	for _, number := range candidates {
		if len(reads) == 10 {
			break
		}
		if slices.ContainsFunc(cached, func(read orders.SplitReadback) bool {
			return read.PostingNumber == number && read.Code == "OBSERVED" && read.Posting != nil
		}) {
			continue
		}
		posting, err := client.ReadOrderPosting(ctx, work.Scope, number)
		read := orders.SplitReadback{PostingNumber: number, Code: "SOURCE_UNAVAILABLE", CheckedAt: r.now()}
		if err == nil {
			posting.PII = nil
			read.Code, read.Posting = "OBSERVED", &posting
			read.CheckedAt = posting.Evidence.CheckedAt
			if read.CheckedAt.IsZero() {
				read.CheckedAt = posting.Evidence.ObservedAt
			}
		}
		reads = append(reads, read)
		if ctx.Err() != nil {
			break
		}
	}
	return reads
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
