package orders

import (
	"context"
	"time"
)

// NativeDemandCoverage is derived account-wide read evidence. It never replaces
// the provider's demands field or changes the raw customerorder revision.
type NativeDemandCoverage struct {
	Scope          Scope       `json:"scope"`
	Correlation    Correlation `json:"correlation"`
	ParentRevision string      `json:"parent_revision"`
	DemandIDs      []string    `json:"demand_ids"`
	AccountRows    int         `json:"account_rows"`
	Visibility     string      `json:"visibility"`
	Complete       bool        `json:"complete"`
	StartedAt      time.Time   `json:"started_at"`
	FinishedAt     time.Time   `json:"finished_at"`
}

type NativeDemandCoverageReader interface {
	ReadNativeDemandCoverage(context.Context, TargetObservation) (NativeDemandCoverage, error)
}

func (coverage NativeDemandCoverage) Validation(scope Scope, correlation Correlation, revision string, now time.Time) Validation {
	if coverage.Scope != scope || coverage.Correlation != correlation || revision == "" || coverage.ParentRevision != revision || !coverage.Complete || coverage.Visibility != "ALL" || coverage.DemandIDs == nil || coverage.AccountRows < len(coverage.DemandIDs) {
		return Validation{Field: "customer_order.demands", Code: "native_linked_demand_coverage_unavailable"}
	}
	if coverage.StartedAt.IsZero() || coverage.FinishedAt.Before(coverage.StartedAt) || coverage.FinishedAt.After(now) || coverage.FinishedAt.Sub(coverage.StartedAt) > 2*time.Minute || now.Sub(coverage.StartedAt) > 5*time.Minute {
		return Validation{Field: "customer_order.demands", Code: "native_linked_demand_coverage_unavailable"}
	}
	if len(coverage.DemandIDs) != 0 {
		return Validation{Field: "customer_order.demands", Code: "native_existing_linked_demand_requires_review"}
	}
	return Validation{}
}

func (movement NativeMovement) DemandCoverageValidation(command Command, now time.Time) Validation {
	issue := AutomaticDemandCoverage(movement.LinkedDemandIDs)
	if issue.Code != "native_linked_demand_coverage_unavailable" || movement.LinkedDemandIDs.Validate() != nil || movement.DemandCoverage == nil {
		return issue
	}
	return movement.DemandCoverage.Validation(command.Scope, command.Correlation, command.ExpectedTargetRevision, now)
}
