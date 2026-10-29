package orderflow

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

type selectedFlowStore struct {
	*flowStore
	selected  orders.ReviewContext
	numbers   []string
	automatic bool
	calls     int
}

func (s *selectedFlowStore) OrderReviewContextFor(_ context.Context, _ orders.Scope, numbers []string, automatic bool) (orders.ReviewContext, error) {
	s.numbers, s.automatic = slices.Clone(numbers), automatic
	s.calls++
	return s.selected, nil
}

func TestServiceUsesBoundedSelectionPort(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		automatic bool
		numbers   []string
	}{{name: "manual exact selection", numbers: []string{"posting-101"}}, {name: "automatic next batch", automatic: true}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			review := flowReview(t, "posting-101")
			attachFlowGrant(&review)
			store := &selectedFlowStore{flowStore: &flowStore{review: flowReview(t, "posting-001")}, selected: review}
			service, err := NewService(store, flowResolver{}, ServiceOptions{AllowScope: func(orders.Scope) bool { return true }, Now: func() time.Time { return flowTestNow }})
			if err != nil {
				t.Fatal(err)
			}
			preview, err := service.preview(context.Background(), orders.PreviewRequest{Scope: review.Config.Scope, ExpectedRevision: 1, Actor: "owner", PostingNumbers: test.numbers}, test.automatic)
			if err != nil || store.calls != 1 || store.automatic != test.automatic || !slices.Equal(store.numbers, test.numbers) {
				t.Fatalf("bounded selection was not used: calls=%d automatic=%v numbers=%v error=%v", store.calls, store.automatic, store.numbers, err)
			}
			if len(preview.Decisions) != 1 || preview.Decisions[0].Posting.PostingNumber != "posting-101" || !preview.Decisions[0].Ready {
				t.Fatalf("preview reverted to the first source batch: %+v", preview.Decisions)
			}
		})
	}
}
