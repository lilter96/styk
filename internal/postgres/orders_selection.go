package postgres

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

type orderReviewSelection struct {
	numbers   []string
	automatic bool
	exact     bool
}

// OrderReviewContextFor loads at most one bounded source batch. Automatic
// selection continues after the latest durable preview, including blocked rows.
func (s *Store) OrderReviewContextFor(ctx context.Context, scope orders.Scope, numbers []string, automatic bool) (orders.ReviewContext, error) {
	if scope.Validate() != nil || validateOrderSelection(numbers) != nil {
		return orders.ReviewContext{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return orders.ReviewContext{}, ordersError("begin bounded order review", err)
	}
	defer rollbackMerchantMutation(tx)
	selection := orderReviewSelection{numbers: numbers, automatic: automatic, exact: len(numbers) > 0}
	result, err := readSelectedOrderReview(ctx, tx, scope, selection)
	if err != nil {
		return result, err
	}
	return result, ordersRowsError("commit bounded order review", tx.Commit(ctx))
}

func readOrderPreviewContext(ctx context.Context, q ordersQuerier, preview orders.Preview) (orders.ReviewContext, error) {
	numbers := make([]string, 0, len(preview.Decisions))
	for _, decision := range preview.Decisions {
		if decision.Posting.Scope != preview.Scope {
			return orders.ReviewContext{}, orders.ErrStale
		}
		numbers = append(numbers, decision.Posting.PostingNumber)
	}
	if validateOrderSelection(numbers) != nil {
		return orders.ReviewContext{}, orders.ErrInvalid
	}
	review, err := readSelectedOrderReview(ctx, q, preview.Scope, orderReviewSelection{numbers: numbers, exact: true})
	if errors.Is(err, orders.ErrNotFound) {
		return review, orders.ErrStale
	}
	return review, err
}

func readSelectedOrderReview(ctx context.Context, q ordersQuerier, scope orders.Scope, selection orderReviewSelection) (orders.ReviewContext, error) {
	var result orders.ReviewContext
	var err error
	result.Config, err = readOrderConfig(ctx, q, scope)
	if err != nil {
		return result, err
	}
	result.Credentials, err = ordersCredentials(ctx, q, scope)
	if err != nil {
		return result, err
	}
	result.Postings, err = readOrderSelectionPostings(ctx, q, result.Config, selection)
	if err != nil {
		return result, err
	}
	if err = readOrderSelectionReferences(ctx, q, &result); err != nil {
		return result, err
	}
	return result, nil
}

func readOrderSelectionReferences(ctx context.Context, q ordersQuerier, result *orders.ReviewContext) error {
	scope := result.Config.Scope
	var err error
	result.ProductMappings, err = readOrderProductMappings(ctx, q, scope)
	if err != nil {
		return err
	}
	result.WarehouseMappings, err = readOrderWarehouseMappings(ctx, q, scope)
	if err != nil {
		return err
	}
	result.Metadata, err = readOrderMetadata(ctx, q, scope)
	if err != nil {
		return err
	}
	numbers := make([]string, 0, len(result.Postings))
	for _, posting := range result.Postings {
		numbers = append(numbers, posting.PostingNumber)
	}
	result.Correlations, err = readSelectedOrderCorrelations(ctx, q, scope, numbers)
	if err != nil {
		return err
	}
	result.ActivePostings, err = readSelectedOrderFences(ctx, q, scope, numbers)
	return err
}

func validateOrderSelection(numbers []string) error {
	if len(numbers) > orders.MaxPagePostings {
		return orders.ErrInvalid
	}
	seen := make(map[string]bool, len(numbers))
	for _, number := range numbers {
		if number == "" || len(number) > 256 || strings.TrimSpace(number) != number || strings.ContainsAny(number, "\r\n\x00") || seen[number] {
			return orders.ErrInvalid
		}
		seen[number] = true
	}
	return nil
}
