package orderprivacy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func identityDigest(parts ...string) string {
	// A string slice has no unsupported JSON values.
	raw, err := json.Marshal(parts)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
