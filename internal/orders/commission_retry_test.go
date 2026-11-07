package orders

import "testing"

func TestCommissionKnownUnsentRetryPreservesNativeIdentityFence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		state   State
		count   int
		code    string
		allowed bool
	}{
		{"authority blocked before dispatch", StateBlocked, 0, "FINANCE_AUTHORITY_CHANGED", true},
		{"configuration rejected before dispatch", StateRejected, 0, "FINANCE_FINAL_ADMISSION_BLOCKED", true},
		{"known native document", StateBlocked, 0, "FINANCE_EXISTING_DOCUMENT_REQUIRES_REVIEW", false},
		{"native document unverified", StateBlocked, 0, "FINANCE_EXISTING_DOCUMENT_UNVERIFIED", false},
		{"any sent attempt", StateBlocked, 1, "FINANCE_AUTHORITY_CHANGED", false},
		{"unknown outcome", StateUnknown, 0, "TRANSPORT_UNCERTAIN", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			work := CommissionWork{State: tc.state, DispatchCount: tc.count, Code: tc.code}
			if work.CanRetryKnownUnsent() != tc.allowed {
				t.Fatalf("retry=%t want=%t", work.CanRetryKnownUnsent(), tc.allowed)
			}
		})
	}
}
