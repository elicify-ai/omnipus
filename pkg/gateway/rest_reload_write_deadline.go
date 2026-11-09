package gateway

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// reloadWaitResponseSlack is the time a handler may spend on its own work
// around the reload wait: persisting before it, auditing and answering after.
const reloadWaitResponseSlack = 30 * time.Second

// reloadWaitWriteDeadlineMiddleware gives every state-changing /api/v1 request
// a write deadline long enough to outlast the longest reload wait.
//
// The gateway server's WriteTimeout (pkg/channels/manager.go::SetupHTTPServer)
// is 30 seconds, but a handler that saves config and waits for the reload may
// legitimately wait up to reloadWaitTimeout (90 seconds). When the wait passed
// 30 seconds, net/http dropped the connection at the handler's first write, so
// the client saw a bare "socket hang up" even though the change was saved.
// Worse, the handler's own "saved but not yet live" answer, the state
// reloadWaitTimeout exists to report, could never be delivered.
//
// Only this per-request deadline moves. The server default stays in force for
// reads and for GET/HEAD/OPTIONS, which never wait for a reload.
func reloadWaitWriteDeadlineMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if waitsForReload(r) {
			deadline := time.Now().Add(reloadWaitTimeout + reloadWaitResponseSlack)
			if err := http.NewResponseController(w).SetWriteDeadline(deadline); err != nil {
				// The server default stays in force, so a save whose reload
				// runs past 30 seconds would be dropped again: say so.
				slog.Warn("rest: could not extend write deadline for reload wait",
					"path", r.URL.Path, "error", err)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// waitsForReload reports whether r is a request that can block on a config
// reload: a state-changing call to the REST API.
func waitsForReload(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return strings.HasPrefix(r.URL.Path, "/api/v1/")
}
