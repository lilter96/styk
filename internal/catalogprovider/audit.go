package catalogprovider

import (
	"context"
	"log/slog"
	"os"

	"github.com/lilter96/styk/internal/catalog"
)

// AuditRequests enables secret-free attempt accounting for controlled diagnostic
// runs. It grants no provider permissions and does not change retry behavior.
func AuditRequests(ctx context.Context) context.Context {
	if os.Getenv("STYK_CATALOG_REQUEST_AUDIT") != "true" {
		return ctx
	}
	return catalog.WithRequestObserver(ctx, func(event catalog.RequestEvent) {
		slog.Info("catalog provider request", "provider", event.Provider, "method", event.Method,
			"operation", event.Operation, "http_status", event.Status, "interrupted", event.Interrupted)
	})
}
