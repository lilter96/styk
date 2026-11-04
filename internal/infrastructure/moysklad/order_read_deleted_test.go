package moysklad

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestNativeCustomerOrderReadPreservesDeletedReadOnlyEvidence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		present  bool
		value    any
		presence orders.Presence
	}{
		{"missing", false, nil, orders.PresenceMissing},
		{"null", true, nil, orders.PresenceNull},
		{"empty", true, "", orders.PresenceEmpty},
		{"documented millis", true, "2026-11-04 15:16:17.123", orders.PresenceValue},
		{"documented seconds", true, "2026-11-04 15:16:17", orders.PresenceValue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			simulator, client := newOrderSimulator(t, "ok")
			setNativeDeletedHandler(simulator, tc.present, tc.value, false)
			target, err := client.ReadCustomerOrder(context.Background(), nativeDeletedCorrelation(simulator))
			if err != nil || !target.Complete || target.SourceDeleted.Presence != tc.presence {
				t.Fatal("native deletion evidence lost or collapsed", err)
			}
			if target.SourceDeleted.IsKnown() && *target.SourceDeleted.Value != tc.value {
				t.Fatal("timezone-less deletion fact was rewritten")
			}
			assertNativeDeletedReadOnly(t, target, simulator)
		})
	}
}

func assertNativeDeletedReadOnly(t *testing.T, target orders.TargetObservation, simulator *orderSimulator) {
	t.Helper()
	raw, err := json.Marshal(target.Fields)
	if err != nil || strings.Contains(string(raw), "deleted") || simulator.writes.Load() != 0 {
		t.Fatal("read-only deletion evidence became a writable field or mutation", err)
	}
	if simulator.positionPages.Load() != 2 || len(target.Positions) != 2 {
		t.Fatal("deletion field caused fabricated empty native positions")
	}
}

func TestNativeCustomerOrderReadRejectsMalformedDeletionFacts(t *testing.T) {
	t.Parallel()
	for _, bad := range []any{true, 0, []any{}, map[string]any{}, "not-a-date", "2026-02-30 12:00:00.000", "2026-11-04T15:16:17Z"} {
		t.Run("malformed", func(t *testing.T) {
			t.Parallel()
			simulator, client := newOrderSimulator(t, "ok")
			setNativeDeletedHandler(simulator, true, bad, false)
			target, err := client.ReadCustomerOrder(context.Background(), nativeDeletedCorrelation(simulator))
			if !errors.Is(err, ErrOrderSchema) || target.Complete {
				t.Fatal("malformed deletion evidence accepted as a complete native read", err)
			}
		})
	}
}

func TestNativeCustomerOrderDeletionDuringPositionTraversalIsStale(t *testing.T) {
	t.Parallel()
	simulator, client := newOrderSimulator(t, "ok")
	setNativeDeletedHandler(simulator, true, "2026-11-04 15:16:17.123", true)
	target, err := client.ReadCustomerOrder(context.Background(), nativeDeletedCorrelation(simulator))
	if !errors.Is(err, orders.ErrStale) || target.Complete {
		t.Fatal("concurrent native deletion was ignored during readback", err)
	}
}

func nativeDeletedCorrelation(simulator *orderSimulator) orders.Correlation {
	correlation := simulator.command.Correlation
	correlation.CustomerOrderID = orderTestID(9)
	return correlation
}

func setNativeDeletedHandler(simulator *orderSimulator, present bool, value any, changed bool) {
	simulator.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/remap/1.2/entity/customerorder/"+orderTestID(9) {
			simulator.serveHTTP(w, r)
			return
		}
		simulator.details.Add(1)
		document := simulator.document()
		if present && (!changed || simulator.details.Load() > 1) {
			document["deleted"] = value
		}
		simulator.reply(w, document)
	})
}
