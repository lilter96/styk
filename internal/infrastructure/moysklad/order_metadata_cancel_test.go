package moysklad

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOrderMetadataCancellationStopsRemainingReferenceTraversal(t *testing.T) {
	t.Parallel()
	simulator, client := newOrderSimulator(t, "ok")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var definitions, unwanted atomic.Int32
	simulator.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/customerorder/metadata") {
			definitions.Add(1)
			cancel()
			<-r.Context().Done()
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/context/employee") {
			unwanted.Add(1)
		}
		simulator.serveHTTP(w, r)
	})
	_, err := client.ReadOrderMetadata(ctx, simulator.command.Scope)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled metadata traversal returned %v", err)
	}
	if definitions.Load() != 1 || unwanted.Load() != 0 {
		t.Fatalf("metadata cancellation continued to unrelated provider reads: metadata=%d remaining=%d", definitions.Load(), unwanted.Load())
	}
}
