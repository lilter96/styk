package orders

import "time"

// Document points refresh known native documents. They never enumerate every
// accounting document or prove that an unobserved external obligation is zero.
type NativeDocumentPointCandidate struct {
	WorkID      string
	Parent      Correlation
	Correlation NativeDocumentCorrelation
}

type NativeDocumentPointCandidates struct {
	Documents  []NativeDocumentPointCandidate
	NextCursor string
}

type NativeDocumentReadFailure struct {
	PostingNumber string `json:"posting_number"`
	WorkID        string `json:"work_id"`
	Code          string `json:"code"`
}

type NativeDocumentPointRead struct {
	WorkID             string                     `json:"work_id"`
	Parent             Correlation                `json:"parent"`
	Correlation        NativeDocumentCorrelation  `json:"correlation"`
	CredentialRevision int64                      `json:"credential_revision"`
	StartedAt          time.Time                  `json:"started_at"`
	FinishedAt         time.Time                  `json:"finished_at"`
	Observation        *NativeDocumentObservation `json:"observation,omitempty"`
	Code               string                     `json:"code,omitempty"`
}

// Observation retains its original snapshot timestamps. The other clocks are
// successful traversal evidence, not new source versions or atomic snapshots.
type NativeDocumentPointEvidence struct {
	WorkID             string
	Parent             Correlation
	CredentialRevision int64
	Observation        *NativeDocumentObservation
	Code               string
	StartedAt          time.Time
	FinishedAt         time.Time
	AdmittedAt         time.Time
	RecordedAt         time.Time
	CheckedAt          time.Time
	Eligible           bool
}

func (read NativeDocumentPointRead) Validate() error {
	if !validID(read.WorkID) || read.Parent.Validate() != nil || read.Parent.CustomerOrderID == "" || read.Correlation.Validate() != nil || read.Correlation.DocumentID == "" || read.CredentialRevision < 1 || read.StartedAt.IsZero() || read.FinishedAt.Before(read.StartedAt) {
		return ErrInvalid
	}
	if read.Observation == nil {
		return validateNativePointFailureCode(read.Code)
	}
	actual := read.Observation
	if read.Code != "" || actual.Validate() != nil || !actual.Complete || actual.Scope != read.Parent.Scope || actual.Correlation != read.Correlation || !nativePointInterval(read.StartedAt, read.FinishedAt, actual.ObservedAt) {
		return ErrInvalid
	}
	if actual.Correlation.Kind == "demand" && !actual.ReturnsComplete {
		return ErrBlocked
	}
	return nil
}
