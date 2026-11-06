package console

import (
	"net/http"

	"github.com/lilter96/styk/internal/catalogsync"
)

func (a *catalogAPI) importPreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		catalogRequestScope
		Targets []catalogsync.Enrollment `json:"targets"`
	}
	if !decodeStockSync(w, r, &body) {
		return
	}
	preview, err := a.service.PreviewImport(r.Context(), catalogsync.ImportRequest{Scope: body.catalogScope(), Targets: body.Targets, Actor: catalogActor(r)})
	if err != nil {
		catalogHTTPError(w, err)
		return
	}
	writeJSON(w, preview)
}
