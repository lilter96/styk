//go:build integration

package integration

import (
	"encoding/json"
	"github.com/lilter96/styk/internal/orders"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOrdersNativeHTTPLegacyMomentMinuteReadbackAfterUnknownAndPause(t *testing.T) {
	f := newNativeMovementHTTPFixture(t)
	preview := f.proposal(t, orders.ActionDemandCreate, false, "legacy-native-moment", "")
	// Separate newly inserted legacy-shaped preview models an older installation;
	// no historical preview, work, receipt or dispatched intent is overwritten.
	preview.ID += "-legacy-minute"
	preview.Actor = f.session.AuditID
	command := &preview.Decisions[0].Commands[0]
	command.ID += "-legacy-minute"
	command.IdempotencyKey = command.ID
	legacy := command.NativeMovement.Event.OccurredAt.UTC().Truncate(time.Minute).Add(17*time.Second + 584*time.Millisecond)
	command.NativeMovement.Moment = legacy
	var err error
	preview, err = f.db.SaveOrderPreview(f.ctx, preview)
	if err != nil {
		t.Fatal(err)
	}
	f.approve(t, preview)
	f.provider.base.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/entity/demand" {
			capture := httptest.NewRecorder()
			f.native.serve(capture, r)
			f.native.mu.Lock()
			for _, document := range f.native.documents {
				document.body["moment"] = legacy.Truncate(time.Minute).In(time.FixedZone("MSK", 3*60*60)).Format("2006-01-02 15:04:05.000")
			}
			f.native.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte(`{}`)); err != nil {
				t.Error(err)
			}
			return
		}
		f.native.serve(w, r)
	})
	f.run(t, orders.StateUnknown)
	before := f.latestWork(t)
	pauseNativeReadbackFixture(t, &f)
	f.run(t, orders.StateReconciled)
	after := f.latestWork(t)
	if after.Code != "NATIVE_DOCUMENT_MOMENT_MINUTE_CONFIRMED" || after.CommandFingerprint != before.CommandFingerprint || !after.Command.NativeMovement.Moment.Equal(legacy) || !after.Command.NativeMovement.Event.OccurredAt.Equal(before.Command.NativeMovement.Event.OccurredAt) || after.Receipt.State != orders.StateUnknown || after.Receipt.HTTPStatus != 200 || f.native.writes.Load() != 1 {
		t.Fatal("minute compatibility changed immutable evidence or repeated mutation")
	}
	var actual []byte
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT observed_native_document FROM orders_reconciliations WHERE work_id=$1`, after.ID).Scan(&actual); err != nil {
		t.Fatal(err)
	}
	var observed orders.NativeDocumentObservation
	if json.Unmarshal(actual, &observed) != nil || !observed.Moment.IsKnown() || !observed.Moment.Value.Equal(legacy.Truncate(time.Minute)) {
		t.Fatal("real provider accounting precision not persisted")
	}
}
