package ozon

import (
	"context"
	"errors"
	"time"

	"github.com/lilter96/styk/internal/application"
)

type Outcome string

const (
	CompleteTraversal  Outcome = "COMPLETE_TRAVERSAL"
	LimitReached       Outcome = "LIMIT_REACHED"
	Cancelled          Outcome = "CANCELLED"
	APIFailure         Outcome = "API_FAILURE"
	InvalidResponse    Outcome = "INVALID_RESPONSE"
	PersistenceFailure Outcome = "PERSISTENCE_FAILURE"
)

// Execution contains only counters and classifications, never source data.
type Execution struct {
	Outcome          Outcome       `json:"outcome"`
	Counts           Counts        `json:"counts"`
	Duration         time.Duration `json:"duration_ns"`
	LimitingResource string        `json:"limiting_resource,omitempty"`
}

func Result(start time.Time, counts Counts, err error) Execution {
	r := Execution{Outcome: CompleteTraversal, Counts: counts, Duration: time.Since(start)}
	if err == nil {
		return r
	}
	var limit *LimitError
	var api *APIError
	switch {
	case errors.As(err, &limit):
		r.Outcome, r.LimitingResource = LimitReached, limit.Resource
	case errors.Is(err, application.ErrOzonPersistence):
		r.Outcome = PersistenceFailure
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		r.Outcome = Cancelled
	case errors.As(err, &api), errors.Is(err, ErrTransport):
		r.Outcome = APIFailure
	default:
		r.Outcome = InvalidResponse
	}
	return r
}
