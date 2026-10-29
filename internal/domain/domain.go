package domain

import (
	"fmt"
	"strings"
	"time"
)

type Observation struct {
	OperationKey, Source, SKU string
	Quantity                  int
}

func (o Observation) Validate() error {
	if strings.TrimSpace(o.OperationKey) == "" || strings.TrimSpace(o.Source) == "" || strings.TrimSpace(o.SKU) == "" {
		return fmt.Errorf("operation key, source and SKU are required")
	}
	if o.Quantity < 0 {
		return fmt.Errorf("quantity must be nonnegative")
	}
	return nil
}

type Task struct {
	ID                    int64
	OperationKey          string
	ObservationID         int64
	Attempts, MaxAttempts int
	ClaimToken            string
	Type                  string
	ImportOperationID     string
}
type State string

const (
	Pending   State = "pending"
	Running   State = "running"
	Succeeded State = "succeeded"
	Failed    State = "failed"
)

func FailureState(attempts, max int) State {
	if attempts >= max {
		return Failed
	}
	return Pending
}
func RetryAt(now time.Time, delay time.Duration) time.Time { return now.Add(delay) }
