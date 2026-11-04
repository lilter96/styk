package ordercontract

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
)

//go:embed testdata/reviewed/ozon_split_unassembled_2026_11_05.json
var unassembledSplitContractBytes []byte

// UnassembledSplitContractHash seals STYK's reviewed local operation subset.
// It is not a hash of a complete remotely downloaded provider specification.
func UnassembledSplitContractHash() string {
	digest := sha256.Sum256(unassembledSplitContractBytes)
	return hex.EncodeToString(digest[:])
}
