package delivery

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func Classify(scope Scope, warehouse Warehouse, method *Method, evidence ContractEvidence, checked time.Time) (Profile, error) {
	p := Profile{Scope: scope, WarehouseID: warehouse.ID, WarehouseType: warehouse.Type, WarehouseStatus: warehouse.Status,
		Express: warehouse.Express, Contract: evidence, ObservedAt: checked, CheckedAt: checked, ValidUntil: checked.Add(Validity),
		VerifiedDimensions: []string{"warehouse"}}
	p.classifyWarehouse(warehouse)
	p.classifyMethod(method)
	if !evidence.SchemaVerified {
		p.Drift = append(p.Drift, orders.Drift{Path: "$", Code: "schema_unverified"})
	}
	if err := p.identify(); err != nil {
		return Profile{}, err
	}
	return p, p.Validate()
}

func (p *Profile) classifyWarehouse(w Warehouse) {
	if w.Type.IsKnown() {
		p.VerifiedDimensions = append(p.VerifiedDimensions, "warehouse_type")
		p.Scheme = warehouseScheme(*w.Type.Value)
		if !p.Scheme.IsKnown() {
			p.Drift = append(p.Drift, orders.Drift{Path: "$.warehouses[].warehouse_type", Code: "unknown_enum"})
		}
	}
	if p.Scheme.IsKnown() && schemeConflict(p.Scheme, w.IsRFBS) {
		p.Scheme = orders.Field[string]{}
		p.Drift = append(p.Drift, orders.Drift{Path: "$.warehouses[].is_rfbs", Code: "conflicting_scheme"})
	}
	if w.Type.IsMissing() && w.IsRFBS.IsKnown() && *w.IsRFBS.Value {
		p.Scheme = orders.Present("rFBS")
	}
	if p.Scheme.IsKnown() {
		p.VerifiedDimensions = append(p.VerifiedDimensions, "scheme")
	}
	p.classifyWarehouseStatus(w)
}

func (p *Profile) classifyWarehouseStatus(w Warehouse) {
	if w.Status.IsKnown() {
		p.VerifiedDimensions = append(p.VerifiedDimensions, "warehouse_status")
		if !slices.Contains([]string{"created", "disabled"}, *w.Status.Value) {
			p.Drift = append(p.Drift, orders.Drift{Path: "$.warehouses[].status", Code: "unknown_enum"})
		}
	}
	if w.Type.IsKnown() && *w.Type.Value == "rfbs_express" && w.Express.IsKnown() && !*w.Express.Value {
		p.Express = orders.Field[bool]{}
		p.Drift = append(p.Drift, orders.Drift{Path: "$.warehouses[].is_express", Code: "conflicting_express"})
	}
	if p.Express.IsKnown() {
		p.VerifiedDimensions = append(p.VerifiedDimensions, "express")
	}
}

func warehouseScheme(kind string) orders.Field[string] {
	switch kind {
	case "fbs":
		return orders.Present("FBS")
	case "rfbs", "rfbs_express":
		return orders.Present("rFBS")
	case "fbo":
		return orders.Present("FBO")
	default:
		return orders.Field[string]{}
	}
}

func schemeConflict(scheme orders.Field[string], rfbs orders.Field[bool]) bool {
	return scheme.IsKnown() && rfbs.IsKnown() && (*scheme.Value == "rFBS") != *rfbs.Value
}

func (p *Profile) classifyMethod(method *Method) {
	if method == nil || method.WarehouseID != p.WarehouseID {
		return
	}
	p.MethodID, p.MethodVerified = orders.Present(method.ID), true
	p.VerifiedDimensions = append(p.VerifiedDimensions, "delivery_method")
	if !method.Express.IsKnown() {
		return
	}
	if p.Express.IsKnown() && *p.Express.Value != *method.Express.Value {
		p.Express = orders.Field[bool]{}
		p.VerifiedDimensions = slices.DeleteFunc(p.VerifiedDimensions, func(d string) bool { return d == "express" })
		p.Drift = append(p.Drift, orders.Drift{Path: "$.delivery_methods[].is_express", Code: "conflicting_express"})
		return
	}
	p.Express = method.Express
	if !slices.Contains(p.VerifiedDimensions, "express") {
		p.VerifiedDimensions = append(p.VerifiedDimensions, "express")
	}
}

// Identity and revision are stable for unchanged evidence across fresh checks.
func (p *Profile) identify() error {
	identity := *p
	identity.ID, identity.Revision = "", 0
	identity.ObservedAt, identity.CheckedAt, identity.ValidUntil = time.Time{}, time.Time{}, time.Time{}
	identity.VerifiedDimensions = slices.Clone(p.VerifiedDimensions)
	slices.Sort(identity.VerifiedDimensions)
	raw, err := json.Marshal(identity)
	if err != nil {
		return orders.ErrInvalid
	}
	sum := sha256.Sum256(raw)
	p.ID = hex.EncodeToString(sum[:])
	p.Revision = int64(binary.BigEndian.Uint64(sum[:8]) & 0x7fffffffffffffff)
	if p.Revision == 0 {
		p.Revision = 1
	}
	return nil
}

func safeID(value string) bool {
	return len(value) > 0 && len(value) <= 256 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}
