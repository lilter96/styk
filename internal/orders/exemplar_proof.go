package orders

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

func ExemplarRequirementsHash(posting Posting) string {
	raw, err := json.Marshal(posting.Requirements)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func ExemplarProofValid(posting Posting, proof ExemplarEvidence, now time.Time) bool {
	hash, err := PackingTargetFingerprint(posting)
	return err == nil && proof.Verified && proof.PackingReady && proof.Scope == posting.Scope && proof.Status == "ship_available" && proof.SnapshotID != "" && proof.TargetFingerprint == hash && proof.RequirementsHash == ExemplarRequirementsHash(posting) && !proof.CheckedAt.After(now) && now.Sub(proof.CheckedAt) <= 5*time.Minute
}
