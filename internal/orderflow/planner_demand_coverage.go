package orderflow

import (
	"context"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func planNativeDemandCoverage(ctx context.Context, client orders.Provider, parent orders.TargetObservation, automaticDemand bool, now func() time.Time) (*orders.NativeDemandCoverage, orders.Validation) {
	if !automaticDemand {
		return nil, orders.Validation{}
	}
	issue := orders.AutomaticDemandCoverage(parent.LinkedDemandIDs)
	if issue.Code != "native_linked_demand_coverage_unavailable" {
		return nil, issue
	}
	reader, supported := client.(orders.NativeDemandCoverageReader)
	if !supported {
		return nil, issue
	}
	coverage, err := reader.ReadNativeDemandCoverage(ctx, parent)
	if err != nil {
		return nil, issue
	}
	if issue = coverage.Validation(parent.Scope, parent.Correlation, parent.Revision, now()); issue.Code != "" {
		return nil, issue
	}
	return &coverage, orders.Validation{}
}
