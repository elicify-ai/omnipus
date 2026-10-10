// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"log/slog"
	"net/http"
)

// jsonServerFailure answers a server-side storage, filesystem or configuration
// failure with one fixed, actionable message. what is a short fixed phrase
// ("could not save config"); the underlying error - which can carry absolute
// paths, file names, ids and OS error text - goes to the error log only and
// never to the client.
func jsonServerFailure(w http.ResponseWriter, status int, what string, err error) {
	jsonErr(w, status, serverFailureText(what, err))
}

// serverFailureText logs err and returns the fixed client text for what. It is
// the one place a server-side cause is turned into client-safe prose, for the
// callers that carry the text in a field rather than an HTTP error body (a
// diagnostic inside an HTTP 200, a WebSocket error frame).
func serverFailureText(what string, err error) string {
	slog.Error("rest: server-side failure", "what", what, "error", err)
	return what + ": server storage or configuration failed. " +
		"Check disk space and permissions, then retry. Details are in the server log."
}
