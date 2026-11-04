package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orderinventory"
	"github.com/lilter96/styk/internal/orders"
)

// Only scope-bounded acknowledged retirement can settle a replaced posting.
// Every child is loaded from its exact current source head, including products
// outside the requested pool. No provider I/O occurs in this read transaction.
func enrichInventorySupersededSplits(ctx context.Context, tx pgx.Tx, observations []orderinventory.Observation) error {
	groups := make(map[orders.Scope][]int)
	for index, observation := range observations {
		if observation.Work != nil && observation.Work.Command.SupersededSplitWorkID != "" && observation.Posting.Status.IsKnown() && *observation.Posting.Status.Value == orders.SupersededSplitParentStatus {
			groups[observation.Posting.Scope] = append(groups[observation.Posting.Scope], index)
		}
	}
	for scope, indexes := range groups {
		review := orders.ReviewContext{Config: orders.Config{Scope: scope}}
		numbers := make([]string, 0, len(indexes))
		for _, index := range indexes {
			review.Postings = append(review.Postings, observations[index].Posting)
			numbers = append(numbers, observations[index].Posting.PostingNumber)
		}
		if err := enrichOrderSupersededSplitReserves(ctx, tx, &review, numbers); err != nil {
			return err
		}
		for _, index := range indexes {
			applyInventorySupersededSplit(&observations[index], review.SplitSupersededReserves[observations[index].Posting.PostingNumber])
		}
	}
	return nil
}

func applyInventorySupersededSplit(observation *orderinventory.Observation, retirement orders.SplitSupersededReserveFacts) {
	if observation.Target == nil || observation.Work == nil || observation.PositionOwnership == nil || len(retirement.Children) == 0 {
		return
	}
	retirement.Target, retirement.Baseline = *observation.Target, *observation.PositionOwnership
	facts := orders.SplitReserveTransferFacts{Split: retirement.Split, Before: retirement.Before,
		OriginalCorrelation: retirement.OriginalCorrelation, ParentRelease: *observation.Work,
		Parent: observation.Posting, ParentTarget: *observation.Target, Child: retirement.Children[0]}
	if proof, err := orders.NewSupersededSplitTransfer(facts, retirement); err == nil {
		observation.SplitSuperseded = &proof
	}
}
