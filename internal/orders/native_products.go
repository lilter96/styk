package orders

import (
	"context"
	"time"
)

const MaxNativeProducts = 25

type NativeProductRequirements struct {
	Scope           Scope         `json:"scope"`
	ItemKind        string        `json:"item_kind"`
	ItemID          string        `json:"item_id"`
	ParentProductID string        `json:"parent_product_id,omitempty"`
	Archived        Field[bool]   `json:"archived"`
	ParentArchived  Field[bool]   `json:"parent_archived"`
	SerialTrackable Field[bool]   `json:"serial_trackable"`
	TrackingType    Field[string] `json:"tracking_type"`
	Revision        string        `json:"revision"`
	ObservedAt      time.Time     `json:"observed_at"`
}

type NativeProductReader interface {
	ReadNativeProductRequirements(context.Context, Scope, []ProductMappingRef) ([]NativeProductRequirements, error)
}

func (proof NativeProductRequirements) Validate(scope Scope) error {
	if proof.Scope != scope || !validUUID(proof.ItemID) || proof.ItemKind != "product" && proof.ItemKind != "variant" || !validID(proof.Revision) || proof.ObservedAt.IsZero() {
		return ErrInvalid
	}
	if proof.ItemKind == "variant" && !validUUID(proof.ParentProductID) {
		return ErrInvalid
	}
	if proof.Archived.Validate() != nil || proof.ParentArchived.Validate() != nil || proof.SerialTrackable.Validate() != nil || proof.TrackingType.Validate() != nil {
		return ErrInvalid
	}
	return nil
}

func (proof NativeProductRequirements) Allows(action Action, posted bool) bool {
	if action == ActionDemandCreate {
		if !proof.Archived.IsKnown() || *proof.Archived.Value {
			return false
		}
		if proof.ItemKind == "variant" && (!proof.ParentArchived.IsKnown() || *proof.ParentArchived.Value) {
			return false
		}
	}
	return !posted || proof.SerialTrackable.IsKnown() && !*proof.SerialTrackable.Value && proof.TrackingType.IsKnown() && *proof.TrackingType.Value == "NOT_TRACKED"
}

func (movement NativeMovement) validateProducts(scope Scope, action Action) error {
	if len(movement.Products) == 0 || len(movement.Products) > MaxNativeProducts {
		return ErrBlocked
	}
	proofs := make(map[string]NativeProductRequirements, len(movement.Products))
	for _, proof := range movement.Products {
		key := proof.ItemKind + ":" + proof.ItemID
		if _, duplicate := proofs[key]; duplicate {
			return ErrInvalid
		}
		if proof.Validate(scope) != nil || !proof.Allows(action, *movement.Applicable.Value) {
			return ErrBlocked
		}
		proofs[key] = proof
	}
	for _, line := range movement.Lines {
		if _, found := proofs[line.Mapping.MoyskladKind+":"+line.Mapping.MoyskladID]; !found {
			return ErrBlocked
		}
	}
	return nil
}
