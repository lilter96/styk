package delivery

import (
	"slices"
	"strconv"
	"strings"

	"github.com/lilter96/styk/internal/ordercontract"
)

// Coverage is supplied by actual implementation, deterministic-test and live
// evidence owners. None of these states implies one of the other states.
type Coverage struct {
	Implemented   bool `json:"implemented"`
	Deterministic bool `json:"deterministic"`
	ObservedLive  bool `json:"observed_live"`
}

type CapabilityStatus struct {
	Scheme           string   `json:"scheme"`
	Carrier          string   `json:"carrier"`
	Express          bool     `json:"express"`
	Destination      string   `json:"destination"`
	Action           string   `json:"action"`
	Endpoint         string   `json:"endpoint"`
	Implemented      bool     `json:"implemented"`
	ContractVerified bool     `json:"contract_verified"`
	Deterministic    bool     `json:"deterministic"`
	ObservedLive     bool     `json:"observed_live"`
	WorkflowStatus   string   `json:"workflow_status"`
	Prerequisites    []string `json:"prerequisites"`
}

// CapabilityMatrix is pure and does not upgrade unavailable official schemas
// merely because a deterministic implementation or live read exists.
func CapabilityMatrix(manifest ordercontract.Manifest, coverage map[string]Coverage) []CapabilityStatus {
	result := make([]CapabilityStatus, 0, len(manifest.Capabilities))
	for _, capability := range manifest.Capabilities {
		observed := coverage[CapabilityKey(capability)]
		result = append(result, CapabilityStatus{Scheme: capability.Scheme, Carrier: capability.Carrier, Express: capability.Express,
			Destination: capability.Destination, Action: capability.Action, Endpoint: capability.Endpoint,
			Implemented: observed.Implemented, Deterministic: observed.Deterministic, ObservedLive: observed.ObservedLive,
			ContractVerified: requestVerified(manifest, capability.Endpoint), WorkflowStatus: capability.Status, Prerequisites: slices.Clone(capability.Prerequisites)})
	}
	return result
}

// CapabilityKey keeps verification of one Express/carrier/destination workflow
// from turning other workflows for the same endpoint green.
func CapabilityKey(capability ordercontract.Capability) string {
	return strings.Join([]string{capability.Scheme, capability.Carrier, strconv.FormatBool(capability.Express), capability.Destination, capability.Action, capability.Endpoint}, ":")
}

func requestVerified(manifest ordercontract.Manifest, endpoint string) bool {
	for _, contract := range manifest.Contracts {
		if contract.Endpoint == endpoint && contract.Direction == "request" && contract.SchemaStatus == "official_verified" {
			return true
		}
	}
	return false
}
