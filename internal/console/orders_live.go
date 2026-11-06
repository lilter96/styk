package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/orders"
)

// Node runs only the official SDK's local sealing code; Go retains HTTP,
// identity and tenant authorization. Bound both concurrency and runtime.
var historySealSlots = make(chan struct{}, 4)

func (a *ordersAPI) liveHistory(w http.ResponseWriter, r *http.Request) {
	session, scope := sessionFrom(r), orderScope(r)
	if scope.Validate() != nil || session.User.ID == "" {
		writeError(w, 400, "INVALID_ORDER_REQUEST")
		return
	}
	if os.Getenv("NEON_REALTIME_SECRET") == "" || os.Getenv("NEON_REALTIME_URL") == "" {
		writeError(w, 503, "REALTIME_UNAVAILABLE")
		return
	}
	if time.Until(session.Expires) < time.Minute {
		writeError(w, 401, "UNAUTHORIZED")
		return
	}
	query, err := sealHistoryQuery(r.Context(), session, scope)
	if err != nil {
		writeError(w, 503, "REALTIME_UNAVAILABLE")
		return
	}
	writeJSON(w, struct {
		Query json.RawMessage `json:"query"`
		User  string          `json:"user_id"`
	}{query, session.User.ID})
}

func sealHistoryQuery(ctx context.Context, session identity.Session, scope orders.Scope) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	select {
	case historySealSlots <- struct{}{}:
		defer func() { <-historySealSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	input, err := json.Marshal(map[string]string{"tenant": scope.Tenant, "moy": scope.MoyskladAccount, "ozon": scope.OzonAccount, "user": session.User.ID, "audit": session.AuditID})
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "node", "scripts/seal-history.mjs")
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.Output()
	if err != nil || len(output) > 16384 || !json.Valid(output) {
		return nil, errors.New("history capability unavailable")
	}
	return output, nil
}
