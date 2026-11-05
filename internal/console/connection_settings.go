package console

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/domain"
)

func (a *credentialAPI) getSettings(w http.ResponseWriter, r *http.Request) {
	scope := credentials.Scope{Tenant: r.URL.Query().Get("tenant"), Provider: "moysklad", Account: r.URL.Query().Get("account")}
	if !scope.Valid() {
		writeError(w, http.StatusBadRequest, "INVALID_SCOPE")
		return
	}
	settings, err := a.db.ConnectionSettings(r.Context(), scope)
	if err != nil {
		settingsError(w, err)
		return
	}
	writeJSON(w, settings)
}

func (a *credentialAPI) saveSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Tenant  string `json:"tenant"`
		Account string `json:"account"`
		domain.ConnectionSettings
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(&struct{}{}) != io.EOF || !in.IsValid() {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	scope := credentials.Scope{Tenant: in.Tenant, Provider: "moysklad", Account: in.Account}
	if !scope.Valid() {
		writeError(w, http.StatusBadRequest, "INVALID_SCOPE")
		return
	}
	settings, err := a.db.SaveConnectionSettings(r.Context(), scope, in.ConnectionSettings, actorProvenance(r))
	if err != nil {
		settingsError(w, err)
		return
	}
	writeJSON(w, settings)
}

func settingsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, credentials.ErrNotFound):
		writeError(w, http.StatusNotFound, "NOT_FOUND")
	case errors.Is(err, credentials.ErrVersion):
		writeError(w, http.StatusConflict, "SETTINGS_VERSION_CONFLICT")
	default:
		writeError(w, http.StatusServiceUnavailable, "SETTINGS_SAVE_UNCONFIRMED")
	}
}
