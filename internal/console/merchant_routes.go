package console

import "net/http"

func registerMerchantRoutes(mux *http.ServeMux, a *auth) {
	m := a.merchant
	mux.HandleFunc("POST /api/registration", m.register)
	mux.HandleFunc("POST /api/verification/request", m.resendVerification)
	mux.HandleFunc("POST /api/verification", m.verify)
	mux.HandleFunc("POST /api/password-reset/request", m.requestReset)
	mux.HandleFunc("POST /api/password-reset", m.reset)
	mux.HandleFunc("POST /api/session", m.login)
	mux.HandleFunc("GET /api/session", a.authorized(m.session))
	mux.HandleFunc("DELETE /api/session", a.authorized(m.logout))
}
