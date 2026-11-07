package ordermoney

import "github.com/lilter96/styk/internal/orders"

// CommissionPlanComponentsSupported preserves the stricter legacy gate while
// reconstructing new partial/return plans from their exact frozen components.
func CommissionPlanComponentsSupported(plan orders.CommissionPlan) bool {
	if plan.Validate() != nil {
		return false
	}
	if plan.Accounting == nil {
		return CommissionSourceComponentsSupported(plan.Source)
	}
	mappings := make([]orders.ProductMappingRef, 0, len(plan.Lines)+len(plan.Returns))
	seen := make(map[string]bool)
	for _, line := range append(append([]orders.CommissionLine{}, plan.Lines...), plan.Returns...) {
		if seen[line.Mapping.ID] {
			continue
		}
		seen[line.Mapping.ID] = true
		mappings = append(mappings, line.Mapping)
	}
	config := orders.Config{Scope: plan.Scope, Revision: plan.ConfigRevision, Finance: &plan.Policy}
	rebuilt, issues := PlanCommissionDraft(config, plan.Credentials, plan.Source, mappings, plan.References, plan.ID, plan.CreatedAt)
	return len(issues) == 0 && rebuilt.Fingerprint == plan.Fingerprint
}
