package orders

import "context"

// NativeRetrySecretRequest permits restoration of one exact rejected native
// event, not general historical access to protected action input.
type NativeRetrySecretRequest struct {
	Scope              Scope
	WorkID             string
	CommandFingerprint string
	PostingNumber      string
	Action             Action
	ProtectedInput     ProtectedActionInput
}

func (request NativeRetrySecretRequest) Validate() error {
	if request.Scope.Validate() != nil || !validID(request.WorkID) || !nativeConsumptionHash(request.CommandFingerprint) || !validID(request.PostingNumber) || !request.Action.IsNativeMovement() || request.ProtectedInput.Revision < 1 || !nativeConsumptionHash(request.ProtectedInput.Reference) {
		return ErrInvalid
	}
	return nil
}

type NativeRetryInputReader interface {
	ReadNativeRetryInput(context.Context, NativeRetrySecretRequest) (ActionParameters, error)
}

type NativeRetrySecretStore interface {
	OrderNativeRetrySecret(context.Context, NativeRetrySecretRequest) (ActionSecret, error)
}
