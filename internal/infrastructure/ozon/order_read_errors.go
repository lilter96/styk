package ozon

// ProviderReadStatus exposes status without leaking provider bodies to refresh
// diagnostics, while keeping orchestration independent from this adapter.
func (e *APIError) ProviderReadStatus() int { return e.Status }

func (e *LimitError) ProviderBudgetExhausted() bool { return true }
