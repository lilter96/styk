package orders

// NativeRetryAuthorization identifies a separately approved attempt for the
// same physical event after a proved no-application rejection. Its shape alone
// grants nothing: complete scoped history must be rechecked before dispatch.
// Actor and authorization time remain in the existing operation approval audit.
type NativeRetryAuthorization struct {
	PreviousWorkID             string `json:"previous_work_id"`
	PreviousCommandFingerprint string `json:"previous_command_fingerprint"`
	EventDigest                string `json:"event_digest"`
}

func (retry NativeRetryAuthorization) Validate(command Command) error {
	movement := command.NativeMovement
	if movement == nil || !command.Action.IsNativeMovement() || !validID(retry.PreviousWorkID) || retry.PreviousWorkID == command.ID || !nativeConsumptionHash(retry.PreviousCommandFingerprint) || !nativeConsumptionHash(retry.EventDigest) || retry.EventDigest != movement.Event.Digest {
		return ErrInvalid
	}
	return nil
}
