package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type namedCheck struct {
	name string
	run  func(context.Context) error
}

// Full regression has exactly three independent lanes. Database packages remain
// serial within their one race-enabled Go invocation; no mutable fixture is
// shared with another Go invocation. Keep failures from every started lane.
func concurrentChecks(ctx context.Context, checks []namedCheck) error {
	var workers sync.WaitGroup
	results := make([]error, len(checks))
	for i, check := range checks {
		workers.Go(func() {
			started := time.Now()
			fmt.Printf("Starting lane: %s\n", check.name)
			if err := check.run(ctx); err != nil {
				results[i] = fmt.Errorf("%s: %w", check.name, err)
				return
			}
			fmt.Printf("Passed lane: %s in %s\n", check.name, time.Since(started).Round(time.Millisecond))
		})
	}
	workers.Wait()
	if ctx.Err() != nil {
		results = append(results, fmt.Errorf("verification incomplete; unfinished checks did not pass: %w", ctx.Err()))
	}
	return errors.Join(results...)
}
