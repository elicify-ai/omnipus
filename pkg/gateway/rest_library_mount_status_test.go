// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMapLibraryErr_MountSentinelsGetActionableStatuses is a regression for a
// defect that unit tests could not see and a live request found immediately.
//
// The engine correctly REFUSED a delete aimed at a mounted folder's own entry —
// the guard held, the operator's files survived — but the REST layer had no case
// for the new sentinel, so it fell through to the default and answered
// "internal server error" with a 500. The protection worked and reported itself
// as a bug in Omnipus, telling the caller nothing about what to do instead.
//
// A boundary that presents as a crash is a boundary people work around or file
// tickets about. The status code and the message are part of the control.
func TestMapLibraryErr_MountSentinelsGetActionableStatuses(t *testing.T) {
	t.Run("a mount's own entry is a conflict, not a crash", func(t *testing.T) {
		rec := httptest.NewRecorder()
		mapLibraryErr(rec, "delete entry", "ws-1", library.ErrIsMountRoot)

		assert.Equal(t, http.StatusConflict, rec.Code,
			"500 tells the caller Omnipus broke; 409 tells them this is a boundary")
		body := rec.Body.String()
		assert.Contains(t, body, "mounted folder")
		assert.Contains(t, body, "/mounts/",
			"the message must name the operation that DOES work, not just refuse")
		assert.Contains(t, body, "without deleting your files",
			"the whole risk is that unmount reads like delete — say what survives")
	})

	t.Run("a cross-root rename explains itself", func(t *testing.T) {
		rec := httptest.NewRecorder()
		mapLibraryErr(rec, "rename", "ws-1", library.ErrCrossRootTransfer)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, strings.ToLower(rec.Body.String()), "move or copy",
			"naming the verb that works is the difference between a refusal and a dead end")
	})

	t.Run("an unknown error is still a 500", func(t *testing.T) {
		// The default must keep its meaning: adding cases must not turn genuine
		// internal failures into confident, wrong 4xx answers.
		rec := httptest.NewRecorder()
		mapLibraryErr(rec, "delete entry", "ws-1", errors.New("disk on fire"))
		require.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}
