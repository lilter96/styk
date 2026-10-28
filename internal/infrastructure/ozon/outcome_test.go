package ozon

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"example.com/styk/internal/application"
)

func TestExecutionOutcomes(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want Outcome
	}{
		{nil, CompleteTraversal}, {&LimitError{Resource: "pages"}, LimitReached}, {context.Canceled, Cancelled},
		{&APIError{Status: 401}, APIFailure}, {ErrTransport, APIFailure}, {errors.New("invalid response"), InvalidResponse},
		{fmt.Errorf("%w: %w", application.ErrOzonPersistence, context.DeadlineExceeded), PersistenceFailure},
	} {
		got := Result(time.Now(), Counts{Attempts: 2}, tc.err)
		if got.Outcome != tc.want || got.Counts.Attempts != 2 {
			t.Fatalf("outcome %s expected %s", got.Outcome, tc.want)
		}
		if tc.want == LimitReached && got.LimitingResource != "pages" {
			t.Fatal("limit resource lost")
		}
	}
}
