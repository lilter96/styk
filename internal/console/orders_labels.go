package console

import (
	"log/slog"
	"net/http"
	"strconv"
)

func (a *ordersAPI) labelArtifact(w http.ResponseWriter, r *http.Request) {
	artifact, err := a.db.OrderLabelArtifact(r.Context(), r.PathValue("workid"))
	if err != nil {
		ordersHTTPError(w, err)
		return
	}
	data, err := a.labels.OpenOrderLabel(artifact)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "LABEL_UNAVAILABLE")
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="ozon-label.pdf"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if _, err = w.Write(data); err != nil {
		slog.Debug("label download interrupted")
	}
}
