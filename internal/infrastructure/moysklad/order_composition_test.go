package moysklad

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/lilter96/styk/internal/orders"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCompositionAdapterPreservesNativeValuesAndOneShot(t *testing.T) {
	t.Parallel()
	simulator, client := newOrderSimulator(t, "ok")
	var writes atomic.Int32
	captured := make(chan map[string]any, 1)
	simulator.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/positions") {
			row := simulator.position(0)
			row["quantity"] = json.Number("2")
			foreign := compositionForeignFixture(simulator)
			offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
			if err != nil || offset < 0 || offset > 1 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			simulator.replyPage(w, []any{[]any{row, foreign}[offset]}, 2, 1, offset)
			return
		}
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/customerorder/"+orderTestID(9)) {
			row := simulator.document()
			row["positions"].(map[string]any)["meta"].(map[string]any)["size"] = 2
			simulator.reply(w, row)
			return
		}
		if r.Method != http.MethodPut {
			simulator.serveHTTP(w, r)
			return
		}
		if r.URL.Path != "/api/remap/1.2/entity/customerorder/"+orderTestID(9) {
			w.WriteHeader(400)
			return
		}
		var body map[string]any
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		if decoder.Decode(&body) != nil {
			w.WriteHeader(400)
			return
		}
		writes.Add(1)
		captured <- body
		simulator.reply(w, simulator.document())
	})
	command := compositionAdapterCommand(t, simulator, client)
	if err := client.BindOrderIntent(command, func(context.Context) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	receipt, err := client.WriteOrder(t.Context(), command)
	if err != nil || receipt.State != orders.StateAcknowledged || writes.Load() != 1 {
		t.Fatalf("receipt=%+v err=%v writes=%d", receipt, err, writes.Load())
	}
	body := <-captured
	if len(body) != 1 {
		t.Fatalf("composition modified scalar metadata: %v", body)
	}
	rows := body["positions"].([]any)
	old := rows[0].(map[string]any)
	added := rows[1].(map[string]any)
	if old["id"] != orderTestID(10) || old["discount"] != json.Number("0") || old["vatEnabled"] != false || added["id"] != nil || added["vatEnabled"] != false {
		t.Fatalf("position identity/tax preservation failed: %v", rows)
	}
	assertCompositionForeignPayload(t, rows)
	if _, err := client.WriteOrder(t.Context(), command); !errors.Is(err, ErrOrderIntent) || writes.Load() != 1 {
		t.Fatal("composition intent replayed")
	}
}

func compositionForeignFixture(s *orderSimulator) map[string]any {
	row := s.position(1)
	row["assortment"] = s.reference("entity/product", "product", 77)
	row["quantity"], row["reserve"], row["price"] = json.Number("3"), json.Number("1"), json.Number("1234")
	row["discount"], row["vat"], row["vatEnabled"] = json.Number("5"), json.Number("20"), true
	return row
}

func assertCompositionForeignPayload(t *testing.T, rows []any) {
	t.Helper()
	if len(rows) != 3 {
		t.Fatal("foreign position was omitted from full replacement")
	}
	foreign := rows[2].(map[string]any)
	if foreign["id"] != orderTestID(11) || foreign["quantity"] != json.Number("3") || foreign["reserve"] != json.Number("1") || foreign["price"] != json.Number("1234") || foreign["discount"] != json.Number("5") || foreign["vat"] != json.Number("20") || foreign["vatEnabled"] != true || foreign["shipped"] != nil {
		t.Fatal("foreign writable values changed or computed fields were sent")
	}
}

func compositionAdapterCommand(t *testing.T, s *orderSimulator, client *OrderClient) orders.Command {
	t.Helper()
	command := s.command
	target, err := client.ReadCustomerOrder(t.Context(), orders.Correlation{Scope: command.Scope, PostingNumber: command.PostingNumber, SyncID: command.Correlation.SyncID, Marker: command.Correlation.Marker, CustomerOrderID: orderTestID(9)})
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := orders.ProjectionPositionBaseline(orders.Work{ID: "prior-create", Scope: command.Scope, State: orders.StateReconciled, DispatchCount: 1, Command: command}, target)
	if err != nil {
		t.Fatal(err)
	}
	command.Action = orders.ActionMirrorCompositionUpdate
	command.Correlation = target.Correlation
	command.ExpectedTargetRevision = target.Revision
	command.TargetPositions = target.Positions
	command.TargetPositionsComplete = true
	// Keep the existing native UUID and add an independently mapped product.
	command.CustomerOrder.Positions = command.CustomerOrder.Positions[:1]
	added := command.CustomerOrder.Positions[0]
	added.Identity = orders.LineIdentity{OfferID: "added-offer", SKU: "added-sku"}
	added.Mapping.ID = "new-map"
	added.Mapping.MoyskladID = orderTestID(42)
	added.Mapping.OzonProductID = "added-product"
	added.Mapping.OfferID = added.Identity.OfferID
	added.Mapping.SKU = added.Identity.SKU
	command.CustomerOrder.Positions = append(command.CustomerOrder.Positions, added)
	replacement, err := orders.PlanPositionReplacement(command, baseline, target.Fields.VATEnabled, target.Fields.Applicable)
	if err != nil {
		t.Fatal(err)
	}
	command.PositionReplacement = replacement
	command.CustomerOrder.OwnedFields = []string{"positions.quantity", "positions.reserve", "positions.price", "positions.vat"}
	return command
}

func TestCompositionUnsupportedNativeExtensionsAreNotRoundtripped(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		raw  string
		safe bool
	}{
		{"plain", `{"id":"x","quantity":1,"vatEnabled":false}`, true},
		{"pack", `{"id":"x","pack":{"quantity":2}}`, false},
		{"nullpack", `{"id":"x","pack":null}`, true},
		{"newfield", `{"id":"x","newProviderMeaning":0}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc.raw), &fields); err != nil {
				t.Fatal(err)
			}
			if got := orderCompositionPositionSafe(fields); got != tc.safe {
				t.Fatalf("safe=%v", got)
			}
		})
	}
}
