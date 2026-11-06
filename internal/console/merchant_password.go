package console

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/lilter96/styk/internal/identity"
)

func (m *merchantAuth) changePassword(w http.ResponseWriter, r *http.Request) {
	session := sessionFrom(r)
	if !m.allowIdentity(w, r, "password-change:user:"+session.User.ID, 10) {
		return
	}
	var body struct {
		Tenant          string `json:"tenant"`
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	err := m.identity.ChangePassword(r.Context(), session, body.CurrentPassword, body.NewPassword)
	switch {
	case errors.Is(err, identity.ErrInvalid):
		writeError(w, http.StatusBadRequest, "INVALID_PASSWORD")
	case errors.Is(err, identity.ErrAuth):
		writeError(w, http.StatusBadRequest, "INVALID_CURRENT_PASSWORD")
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
