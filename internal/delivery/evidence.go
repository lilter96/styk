package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"time"

	"github.com/lilter96/styk/internal/ordercontract"
	"github.com/lilter96/styk/internal/orders"
)

// Evidence binds observations to exact reviewed contracts, including their
// unverified schema status. A verified endpoint version is a separate fact.
func Evidence(manifest ordercontract.Manifest) (ContractEvidence, error) {
	contracts := make([]ordercontract.Contract, 0, 4)
	verified := true
	for _, contract := range manifest.Contracts {
		if contract.Endpoint != "/v2/warehouse/list" && contract.Endpoint != "/v2/delivery-method/list" {
			continue
		}
		contracts = append(contracts, contract)
		verified = verified && contract.SchemaStatus == "official_verified"
	}
	if len(contracts) != 4 {
		return ContractEvidence{}, orders.ErrInvalid
	}
	checked, err := time.Parse("2006-01-02", manifest.CheckedAt)
	if err != nil {
		return ContractEvidence{}, orders.ErrInvalid
	}
	raw, err := json.Marshal(contracts)
	if err != nil {
		return ContractEvidence{}, orders.ErrInvalid
	}
	sum := sha256.Sum256(raw)
	return ContractEvidence{Hash: hex.EncodeToString(sum[:]), CheckedAt: checked, SchemaVerified: verified, VersionsVerified: versionsVerified(manifest)}, nil
}

func versionsVerified(manifest ordercontract.Manifest) bool {
	for _, source := range manifest.Sources {
		if source.ID == "ozon_news648" && source.Status == "official_notice_verified" {
			return true
		}
	}
	return false
}

func (p Profile) Fresh(now time.Time, evidence ContractEvidence) bool {
	return !now.Before(p.CheckedAt) && now.Before(p.ValidUntil) && p.Contract == evidence
}

// CapabilityRef exposes only proven dimensions. Unknown carrier/destination
// cannot satisfy the independent workflow registry's prerequisites.
func (p Profile) CapabilityRef() *orders.CapabilityRef {
	if !p.MethodVerified || !p.MethodID.IsKnown() || !p.Scheme.IsKnown() || *p.Scheme.Value == "FBO" {
		return nil
	}
	dimensions := make([]string, 0, 5)
	for _, dimension := range []string{"scheme", "warehouse", "express", "carrier", "destination"} {
		if slices.Contains(p.VerifiedDimensions, dimension) {
			dimensions = append(dimensions, dimension)
		}
	}
	ref := &orders.CapabilityRef{ID: p.ID, Revision: p.Revision, WarehouseID: p.WarehouseID, DeliveryMethodID: *p.MethodID.Value,
		Scheme: *p.Scheme.Value, Carrier: p.CarrierIntegration, Destination: p.DestinationType, Express: p.Express,
		VerifiedDimensions: dimensions, ObservedAt: p.CheckedAt, ValidUntil: p.ValidUntil}
	if ref.Validate() != nil {
		return nil
	}
	return ref
}
