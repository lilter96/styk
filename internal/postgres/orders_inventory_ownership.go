package postgres

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orderinventory"
	"github.com/lilter96/styk/internal/orders"
)

// Legacy scalar records remain unchanged. At most one bounded ownership query
// per affected account pair supplies a separate immutable earlier identity proof.
func enrichInventoryPositionOwnership(ctx context.Context, tx pgx.Tx, observations []orderinventory.Observation) error {
	groups := make(map[orders.Scope][]int)
	for index, observation := range observations {
		if observation.Work == nil || observation.Work.Command.Action != orders.ActionMirrorUpdate || observation.Work.Command.PositionBaseline != nil {
			continue
		}
		groups[observation.Posting.Scope] = append(groups[observation.Posting.Scope], index)
	}
	for scope, indexes := range groups {
		review := orders.ReviewContext{Config: orders.Config{Scope: scope}, Postings: make([]orders.Posting, 0, len(indexes))}
		for _, index := range indexes {
			review.Postings = append(review.Postings, observations[index].Posting)
		}
		if err := enrichOrderPositionBaselines(ctx, tx, &review); err != nil {
			return err
		}
		for _, index := range indexes {
			if proof, found := review.PositionBaselines[observations[index].Posting.PostingNumber]; found {
				observations[index].PositionOwnership = &proof
			}
		}
	}
	return nil
}
