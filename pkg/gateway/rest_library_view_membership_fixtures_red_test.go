// Omnipus — recorded-membership and Retry fixtures for FR-VA-031–039.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/stretchr/testify/require"
)

// stagePendingViewMove models the private record immediately after the atomic
// pending-receipt/revocation save. It does not use a .view marker as authority.
// The files and real recorded member must have been planted beforehand.
func stagePendingViewMove(t *testing.T, home, vault, id, from, to string, folder bool,
	member knowledge.PendingViewMoveMember, started time.Time,
) {
	t.Helper()
	require.NoError(t, knowledge.WithViewMembership(home, vault, func(m *knowledge.ViewMembership) error {
		require.Equal(t, member.OldPath, m.Bases[member.OldBase][member.Name],
			"the pending receipt must describe a real previously enrolled member")
		delete(m.Bases[member.OldBase], member.Name)
		if len(m.Bases[member.OldBase]) == 0 {
			delete(m.Bases, member.OldBase)
		}
		if m.PendingMoves == nil {
			m.PendingMoves = make(map[string]knowledge.PendingViewMove)
		}
		m.PendingMoves[id] = knowledge.PendingViewMove{
			ID: id, CollectionRoot: m.Root, From: from, To: to, Folder: folder,
			Members: []knowledge.PendingViewMoveMember{member}, StartedAt: started,
		}
		return knowledge.SaveViewMembership(m)
	}))
}

func pendingViewMember(base, newBase, from, to string) knowledge.PendingViewMoveMember {
	return knowledge.PendingViewMoveMember{
		OldBase: base, NewBase: newBase, Name: "open", OldPath: from, NewPath: to,
	}
}

func retryMoveHTTP(t *testing.T, api *restAPI, ws, id string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(gen.RetryMoveRequest{PendingMoveId: id})
	require.NoError(t, err)
	return libPostJSON(t, api, "/api/v1/library/"+ws+"/retry-move", string(body))
}

func requireRetryError(t *testing.T, w *httptest.ResponseRecorder, status int,
	id string, code gen.RetryMoveErrorCode,
) gen.RetryMoveError {
	t.Helper()
	require.Equal(t, status, w.Code, "Retry must return its typed error: %s", w.Body.String())
	var body gen.RetryMoveError
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), "Retry must return generated wire type")
	require.Equal(t, id, body.PendingMoveId)
	require.Equal(t, code, body.Code)
	return body
}

func requireRetryResult(t *testing.T, w *httptest.ResponseRecorder,
	outcome gen.RetryMoveResultOutcome,
) gen.RetryMoveResult {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, "Retry must succeed: %s", w.Body.String())
	var body gen.RetryMoveResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, outcome, body.Outcome)
	return body
}

func requireTrackedConflict(t *testing.T, w *httptest.ResponseRecorder, path string) gen.LibraryMoveConflictError {
	t.Helper()
	require.Equal(t, http.StatusConflict, w.Code, "tracked transfer must be refused: %s", w.Body.String())
	var body gen.LibraryMoveConflictError
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, gen.LibraryMoveConflictErrorCodeViewTrackedTransferRefused, body.Code)
	require.NotNil(t, body.TrackedPaths)
	require.Contains(t, *body.TrackedPaths, path)
	return body
}

func loadRecordedViews(t *testing.T, home, vault string) *knowledge.ViewMembership {
	t.Helper()
	m, err := knowledge.LoadViewMembership(home, vault)
	require.NoError(t, err)
	return m
}
