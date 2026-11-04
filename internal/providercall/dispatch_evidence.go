package providercall

import (
	"context"
	"sync/atomic"
)

type dispatchEvidenceKey struct{}

const (
	dispatchUnseen int32 = iota
	dispatchAdmitting
	dispatchStarted
	dispatchAborted
	dispatchUnseenAborted
)

// DispatchEvidence belongs to one HTTP attempt. It is not a durable outcome or
// proof that an already-started remote request can be cancelled or reordered.
// Only Transport can enter/start it; an arbitrary transport cannot attest that
// an admission timeout happened before its own network request.
type DispatchEvidence struct{ state atomic.Int32 }

func WithDispatchEvidence(ctx context.Context) (context.Context, *DispatchEvidence) {
	evidence := &DispatchEvidence{}
	return context.WithValue(ctx, dispatchEvidenceKey{}, evidence), evidence
}

// AbortBeforeDispatch seals a waiting Transport against any later Base call.
// An unseen transport is also fenced if it subsequently enters, but supplies no
// positive certainty: an uncoordinated transport may already have performed I/O.
func (e *DispatchEvidence) AbortBeforeDispatch() bool {
	for {
		state := e.state.Load()
		switch state {
		case dispatchStarted, dispatchUnseenAborted:
			return false
		case dispatchAborted:
			return true
		}
		closed := dispatchAborted
		if state == dispatchUnseen {
			closed = dispatchUnseenAborted
		}
		if e.state.CompareAndSwap(state, closed) {
			return state == dispatchAdmitting
		}
	}
}

func (e *DispatchEvidence) Started() bool { return e.state.Load() == dispatchStarted }

func enterDispatchEvidence(ctx context.Context) error {
	evidence, _ := ctx.Value(dispatchEvidenceKey{}).(*DispatchEvidence)
	if evidence != nil && !evidence.state.CompareAndSwap(dispatchUnseen, dispatchAdmitting) {
		return ErrAlreadyStarted
	}
	return nil
}

func startDispatchEvidence(ctx context.Context) error {
	evidence, _ := ctx.Value(dispatchEvidenceKey{}).(*DispatchEvidence)
	if err := ctx.Err(); err != nil {
		if evidence != nil {
			evidence.AbortBeforeDispatch()
		}
		return err
	}
	if evidence != nil && !evidence.state.CompareAndSwap(dispatchAdmitting, dispatchStarted) {
		return ErrAlreadyStarted
	}
	return nil
}
