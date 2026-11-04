package orders

import "time"

const NativePointStream = "moysklad-native-points"

// NativeOrderPointRead is a bounded attempt to read one existing binding.
// Failed attempts invalidate earlier point evidence; they never assert zero
// positions, missing obligations or complete account/window coverage.
type NativeOrderPointRead struct {
	Correlation        Correlation       `json:"correlation"`
	CredentialRevision int64             `json:"credential_revision"`
	StartedAt          time.Time         `json:"started_at"`
	FinishedAt         time.Time         `json:"finished_at"`
	Point              *NativeOrderPoint `json:"point,omitempty"`
	Code               string            `json:"code,omitempty"`
}

type NativePointCandidates struct {
	Correlations []Correlation `json:"correlations"`
	NextCursor   string        `json:"next_cursor"`
}

func (read NativeOrderPointRead) Validate() error {
	if read.Correlation.Validate() != nil || read.Correlation.CustomerOrderID == "" || read.CredentialRevision <= 0 || read.StartedAt.IsZero() || read.FinishedAt.Before(read.StartedAt) {
		return ErrInvalid
	}
	if read.Point == nil {
		return validateNativePointFailureCode(read.Code)
	}
	point := read.Point
	if read.Code != "" || point.Validate() != nil || point.Correlation != read.Correlation || point.CredentialRevision != read.CredentialRevision || !point.StartedAt.Equal(read.StartedAt) || !point.FinishedAt.Equal(read.FinishedAt) {
		return ErrInvalid
	}
	return nil
}

func validateNativePointFailureCode(code string) error {
	switch code {
	case "NATIVE_READ_FAILED", "NATIVE_AUTHORIZATION_DENIED", "NATIVE_RATE_LIMITED", "NATIVE_PROVIDER_UNAVAILABLE", "NATIVE_ORDER_NOT_FOUND", "NATIVE_ORDER_SCHEMA_DRIFT", "NATIVE_BUDGET_EXHAUSTED", "NATIVE_READ_INTERRUPTED", "NATIVE_SCOPE_MISMATCH", "NATIVE_ORDER_INCOMPLETE", "NATIVE_BINDING_CHANGED", "NATIVE_ORDER_CHANGED_DURING_READ":
		return nil
	default:
		return ErrInvalid
	}
}
