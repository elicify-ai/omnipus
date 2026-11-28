// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"net/http"
)

// writeRequestRefusal answers a *requestRefusalError with 400 and its text and
// reports whether it did (office preview spec §3.4 D9). The shared config
// writer returns one when a save would give agents write access to a document
// preview program folder (§3.2), so a settings route calls this directly
// before its generic failure; any other error is left to that failure.
func writeRequestRefusal(w http.ResponseWriter, err error) bool {
	var refusal *requestRefusalError
	if !errors.As(err, &refusal) {
		return false
	}
	jsonErr(w, http.StatusBadRequest, refusal.Error())
	return true
}
