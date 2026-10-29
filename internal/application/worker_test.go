package application

import (
	"context"
	"github.com/lilter96/styk/internal/domain"
	"testing"
)

func TestMockIdempotent(t *testing.T) {
	h := MockHandler{}
	task := domain.Task{OperationKey: "x", ObservationID: 1}
	a, e := h.Process(context.Background(), task)
	if e != nil {
		t.Fatal(e)
	}
	b, e := h.Process(context.Background(), task)
	if e != nil || a != b {
		t.Fatalf("different result: %q %q %v", a, b, e)
	}
}
