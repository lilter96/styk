package domain

import (
	"strings"
	"testing"
	"time"
)

func TestWarehouseReviewRequestValidation(t *testing.T) {
	good := WarehouseReviewRequest{Tenant: "tenant", MoyskladAccount: "moy", OzonAccount: "ozon", MoyskladWarehouseID: "m", OzonWarehouseID: "o", SessionID: "session", Lifetime: 5 * time.Minute}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*WarehouseReviewRequest){"missing identity": func(r *WarehouseReviewRequest) { r.MoyskladWarehouseID = "" }, "untrimmed account": func(r *WarehouseReviewRequest) { r.OzonAccount = " ozon" }, "oversized tenant": func(r *WarehouseReviewRequest) { r.Tenant = strings.Repeat("x", 257) }, "missing session": func(r *WarehouseReviewRequest) { r.SessionID = "" }, "zero lifetime": func(r *WarehouseReviewRequest) { r.Lifetime = 0 }, "excessive lifetime": func(r *WarehouseReviewRequest) { r.Lifetime = 10*time.Minute + time.Second }} {
		t.Run(name, func(t *testing.T) {
			r := good
			mutate(&r)
			if r.Validate() == nil {
				t.Fatal("invalid review accepted")
			}
		})
	}
}
