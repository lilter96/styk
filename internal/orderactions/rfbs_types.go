package orderactions

import (
	"time"

	"github.com/lilter96/styk/internal/orders"
)

// RFBSOperation names an explicit operation, never an arbitrary target status.
// Operations without a durable orders.Action remain inspection-only.
type RFBSOperation string

const (
	RFBSPack           RFBSOperation = "pack"
	RFBSSetTracking    RFBSOperation = "set_tracking"
	RFBSMarkDelivering RFBSOperation = "mark_delivering"
	RFBSMarkLastMile   RFBSOperation = "mark_last_mile"
	RFBSMarkDelivered  RFBSOperation = "mark_delivered"
	RFBSSentBySeller   RFBSOperation = "sent_by_seller"
	RFBSLabelCreate    RFBSOperation = "label_create"
	RFBSLabelGet       RFBSOperation = "label_get"
	RFBSCancel         RFBSOperation = "cancel"
)

type RFBSRoute string

const (
	RFBSRouteUnknown        RFBSRoute = "unknown"
	RFBSRouteSeller         RFBSRoute = "seller_non_integrated"
	RFBSRouteSellerTracking RFBSRoute = "seller_registered_carrier"
	RFBSRouteIntegrated     RFBSRoute = "integrated_carrier"
	RFBSRouteSellerPVZ      RFBSRoute = "hybrid_seller_ozon_pvz"
	RFBSRoutePartnerPVZ     RFBSRoute = "hybrid_partner_ozon_pvz"
)

// RFBSInput is an inspection proposal. A provider action token is checked
// against the exact observed list; matching a token cannot verify its semantics.
// Tracking and cancellation reasons are never synthesized from other facts.
type RFBSInput struct {
	Operation      RFBSOperation        `json:"operation"`
	ProviderAction string               `json:"provider_action,omitempty"`
	TrackingNumber orders.Field[string] `json:"-"`
	ReasonID       orders.Field[string] `json:"reason_id"`
}

// RFBSActionDecision exposes local restrictions and documentation gaps. The
// graph is deliberately blocked while official complete contracts are absent.
type RFBSActionDecision struct {
	Operation      RFBSOperation        `json:"operation"`
	Route          RFBSRoute            `json:"route"`
	Endpoint       string               `json:"endpoint"`
	CurrentStatus  orders.Field[string] `json:"current_status"`
	ExpectedStatus string               `json:"expected_status,omitempty"`
	Authority      string               `json:"authority"`
	ContractHash   string               `json:"contract_hash,omitempty"`
	Diagnostics    []Diagnostic         `json:"diagnostics"`
	Allowed        bool                 `json:"allowed"`
}

const rfbsEvidenceMaxAge = 5 * time.Minute

func (operation RFBSOperation) valid() bool {
	switch operation {
	case RFBSPack, RFBSSetTracking, RFBSMarkDelivering, RFBSMarkLastMile, RFBSMarkDelivered, RFBSSentBySeller, RFBSLabelCreate, RFBSLabelGet, RFBSCancel:
		return true
	default:
		return false
	}
}

func rfbsDiagnostic(code, field string) Diagnostic {
	return Diagnostic{Severity: "blocked", Code: code, Field: field}
}
