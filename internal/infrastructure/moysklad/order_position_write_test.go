package moysklad

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrderPositionWriteUsesExactNativeUUIDAndNeverReplacesCollection(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		status int
		state  orders.State
	}{{"acknowledged", 200, orders.StateAcknowledged}, {"uncertain", 500, orders.StateUnknown}, {"rejected", 429, orders.StateRejected}} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			simulator, client := newOrderSimulator(t, "ok")
			var writes atomic.Int32
			captured := make(chan map[string]any, 1)
			simulator.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				positionTestHTTP(simulator, &writes, captured, test.status, w, r)
			})
			command := positionTestCommand(t, simulator, client)
			var grants atomic.Int32
			if err := client.BindOrderIntent(command, func(context.Context) (bool, error) { grants.Add(1); return true, nil }); err != nil {
				t.Fatal(err)
			}
			receipt, err := client.WriteOrder(t.Context(), command)
			if receipt.State != test.state || receipt.HTTPStatus != test.status || writes.Load() != 1 || grants.Load() != 1 {
				t.Fatalf("receipt=%+v err=%v writes=%d", receipt, err, writes.Load())
			}
			body := <-captured
			if len(body) != 1 || body["quantity"] != json.Number("3") {
				t.Fatalf("position patch overwrote unrelated fields: %+v", body)
			}
			if _, err = client.WriteOrder(t.Context(), command); !errors.Is(err, ErrOrderIntent) || writes.Load() != 1 {
				t.Fatal("one-use position intent replayed")
			}
		})
	}
}

func positionTestHTTP(s *orderSimulator, writes *atomic.Int32, captured chan map[string]any, status int, w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/remap/1.2/")
	if r.Method == http.MethodGet && strings.HasSuffix(path, "/positions") {
		row := s.position(0)
		row["quantity"] = json.Number("2")
		s.replyPage(w, []any{row}, 1, 1, 0)
		return
	}
	if r.Method == http.MethodGet && path == "entity/customerorder/"+orderTestID(9) {
		row := s.document()
		row["positions"].(map[string]any)["meta"].(map[string]any)["size"] = 1
		s.reply(w, row)
		return
	}
	if r.Method != http.MethodPut {
		s.serveHTTP(w, r)
		return
	}
	if path != "entity/customerorder/"+orderTestID(9)+"/positions/"+orderTestID(10) {
		w.WriteHeader(400)
		return
	}
	var body map[string]any
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&body); err != nil {
		w.WriteHeader(400)
		return
	}
	writes.Add(1)
	captured <- body
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	row := s.position(0)
	row["meta"] = s.reference("entity/customerorder/"+orderTestID(9)+"/positions", "customerorderposition", 10)["meta"]
	row["quantity"] = body["quantity"]
	s.reply(w, row)
}

func positionTestCommand(t *testing.T, s *orderSimulator, client *OrderClient) orders.Command {
	t.Helper()
	command := s.command
	target, err := client.ReadCustomerOrder(t.Context(), orders.Correlation{Scope: command.Scope, PostingNumber: command.PostingNumber, SyncID: command.Correlation.SyncID, Marker: command.Correlation.Marker, CustomerOrderID: orderTestID(9)})
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := orders.ProjectionPositionBaseline(orders.Work{ID: "reconciled-prior", DispatchCount: 1, Scope: command.Scope, State: orders.StateReconciled, Command: command}, target)
	if err != nil {
		t.Fatal(err)
	}
	command.Action = orders.ActionMirrorUpdate
	command.Correlation = target.Correlation
	command.ExpectedTargetRevision = target.Revision
	command.TargetPositions = target.Positions
	command.TargetPositionsComplete = true
	command.CustomerOrder.Positions[0].Quantity = "3"
	patch, err := orders.PlanPositionPatch(command, baseline)
	if err != nil || patch == nil {
		t.Fatalf("plan patch: %v", err)
	}
	command.Action, command.PositionPatch = orders.ActionMirrorPositionUpdate, patch
	command.CustomerOrder.OwnedFields = patch.Fields()
	return command
}
