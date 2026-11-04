package orders

import (
	"encoding/hex"
	"strings"
)

// NativeCorrelationFor includes the operation kind and protected event digest;
// it must never reuse the parent customerorder's correlation namespace.
func NativeCorrelationFor(scope Scope, action Action, eventDigest string) (NativeDocumentCorrelation, error) {
	digest, err := hex.DecodeString(eventDigest)
	if !action.IsNativeMovement() || err != nil || len(digest) != 32 {
		return NativeDocumentCorrelation{}, ErrInvalid
	}
	kind := action.NativeDocumentKind()
	parent, err := CorrelationFor(scope, "native-"+kind+"-"+eventDigest)
	if err != nil {
		return NativeDocumentCorrelation{}, err
	}
	return NativeDocumentCorrelation{Kind: kind, SyncID: parent.SyncID, Marker: "styk-native-v1-" + kind + "-" + strings.TrimPrefix(parent.Marker, "styk-order-v1-")}, nil
}
