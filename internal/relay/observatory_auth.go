package relay

import "net/http"

// ObservatoryEasyAuthMiddleware gates the observatory read surface behind
// X-MS-CLIENT-PRINCIPAL (Azure App Service EasyAuth). Requests without a
// valid header are rejected with HTTP 401.
//
// Mirrors the chat EasyAuth gate (chatIdentity in chat_handlers.go).
// T6 applies this when mounting ServeObservatoryRead.
func (r *Relay) ObservatoryEasyAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		header := req.Header.Get("X-MS-CLIENT-PRINCIPAL")
		if parseClientPrincipal(header) == nil && !r.Config.DevMode {
			obsJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, req)
	})
}
