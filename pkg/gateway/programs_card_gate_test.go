// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: S-112 (same-owner API token on a program card — dataset D-8
// rows 10–11) with its non-program regression clause. Cards are raised
// through the REAL request_secret tool against the real registry; submits go
// through the real production route (the wu5 pack's pattern).
//
// Oracle: D-8 rows 10–11. A program card is decided from the STORED request
// kind before the body is read: the person's own API bearer is refused 403
// with the person-session text, the card stays pending, nothing is stored
// and no check runs; the browser session submits (positive control). A
// pending non-program card submitted with the same bearer behaves exactly as
// today (regression). At RED the tool has no program recipient at all — the
// missing seam is reported BLOCKED with the spec reference, never skipped.

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/tests/testutil/programstore"
	"github.com/stretchr/testify/require"
)

// pgRaiseProgramCard raises one card through the real request_secret tool
// for a fresh web-chat session owned by the person, and returns the request
// id, the session id and an error when the tool refuses (at RED the program
// recipient does not exist — that error IS the finding).
func pgRaiseProgramCard(t *testing.T, f *pgGateFixture, agentID string, recipient map[string]any) (string, string, error) {
	t.Helper()
	s, err := f.api.agentLoop.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", agentID)
	if err != nil {
		return "", "", fmt.Errorf("new chat session: %w", err)
	}
	owner := "pg-person"
	workspaceID := testHarnessWorkspaceMembershipID
	if err := f.api.agentLoop.GetSessionStore().SetMeta(s.ID, session.MetaPatch{Owner: &owner, WorkspaceID: &workspaceID}); err != nil {
		return "", "", fmt.Errorf("stamp session owner and workspace: %w", err)
	}
	ctx := tools.WithTurnOrigin(
		tools.WithAgentID(
			tools.WithTranscriptSessionID(
				tools.WithToolContext(context.Background(), "webchat", s.ID),
				s.ID),
			agentID),
		tools.NewTurnOrigin(),
	)
	args := map[string]any{"purpose": "connect the program"}
	for k, v := range recipient {
		args[k] = v
	}
	tool := tools.NewRequestSecretTool(func() *secretrequest.Registry { return f.api.agentLoop.SecretRequests() }, nil)
	result := tool.Execute(ctx, args)
	if result.IsError {
		return "", "", fmt.Errorf("%s", result.ForLLM)
	}
	for _, card := range f.api.agentLoop.SecretRequests().Snapshot(owner, s.ID) {
		if card.State == "pending" {
			return card.RequestId, s.ID, nil
		}
	}
	return "", "", fmt.Errorf("tool reported success but no pending card is in session state for %s", s.ID)
}

// pgCardState reads one card's state from the real session state.
func pgCardState(t *testing.T, f *pgGateFixture, owner, sid, requestID string) string {
	t.Helper()
	for _, card := range f.api.agentLoop.SecretRequests().Snapshot(owner, sid) {
		if card.RequestId == requestID {
			return card.State
		}
	}
	t.Fatalf("card %s is not in session state for %s/%s", requestID, owner, sid)
	return ""
}

// pgCardFieldKey reads the pending card's first field key (K1).
func pgCardFieldKey(t *testing.T, f *pgGateFixture, owner, sid, requestID string) string {
	t.Helper()
	for _, card := range f.api.agentLoop.SecretRequests().Snapshot(owner, sid) {
		if card.RequestId == requestID && card.State == "pending" {
			require.NotEmpty(t, card.Fields, "the card must declare its field (K1)")
			return card.Fields[0].Key
		}
	}
	t.Fatalf("pending card %s not found for %s/%s", requestID, owner, sid)
	return ""
}

// pgSubmitCard drives the real K5 submit route with the given identity.
func pgSubmitCard(t *testing.T, f *pgGateFixture, requestID, fieldKey, value, bearer string, withSession, csrf bool) (*httptest.ResponseRecorder, int64) {
	t.Helper()
	body, err := json.Marshal(gen.SecretSubmitRequest{Values: &map[string]string{fieldKey: value}})
	require.NoError(t, err)
	var read int64
	req := httptest.NewRequest(http.MethodPost, "/api/v1/secret-requests/"+requestID+"/submit",
		&pgCountingBody{r: strings.NewReader(string(body)), n: &read})
	req.Header.Set("Content-Type", "application/json")
	pgAttach(f, req, bearer, withSession, csrf)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	return w, read
}

// All fixture-created receipt IDs, including the pending card's absent receipt,
// travel with the same attack's value/kind/selection/generation snapshot.
func pgProgramStoreSnapshot(t *testing.T, f *pgGateFixture, receiptIDs ...string) programstore.State {
	t.Helper()
	return programstore.Snapshot(t, f.store, receiptIDs...)
}

func TestProgramCard_PersonGate(t *testing.T) {
	const owner = "pg-person"

	t.Run("S112_program_card_is_raisable", func(t *testing.T) {
		f := pgPersonAPI(t)
		_, _, err := pgRaiseProgramCard(t, f, "dev", map[string]any{"program": "github"})
		if err != nil {
			t.Fatalf("BLOCKED: the request_secret tool has no program recipient — required by FR-051/S-112 (tool said: %v)", err)
		}
	})

	t.Run("D8_row10_bearer_submit_refused_before_body", func(t *testing.T) {
		f := pgPersonAPI(t)
		cardID, sid, err := pgRaiseProgramCard(t, f, "dev", map[string]any{"program": "github"})
		if err != nil {
			t.Fatalf("BLOCKED: the request_secret tool has no program recipient — required by FR-051/S-112 (tool said: %v)", err)
		}
		canary := programstore.SeedCanary(t, f.store)
		snapBefore := pgProgramStoreSnapshot(t, f, canary, cardID)
		starts := f.ghStarted()

		// The person's OWN API bearer: decided from the stored request kind
		// before the body is read.
		w, read := pgSubmitCard(t, f, cardID, pgCardFieldKey(t, f, owner, sid, cardID), "tok_valid_12345", f.token, false, true)
		require.Equal(t, http.StatusForbidden, w.Code,
			"a program card refuses the person's own API bearer: got %d %s", w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), pgPersonRefusal, "the person-session refusal text")
		require.Equal(t, int64(0), read, "decided before the body is read")
		require.Equal(t, starts, f.ghStarted(), "no check runs (per-row delta, CHECK F18)")
		require.Equal(t, "pending", pgCardState(t, f, owner, sid, cardID), "the card stays pending")
		require.Equal(t, snapBefore, pgProgramStoreSnapshot(t, f, canary, cardID),
			"same attack preserves values, kind, selections, metadata, generation and known receipts")
	})

	t.Run("D8_row11_browser_session_submit_processed", func(t *testing.T) {
		f := pgPersonAPI(t)
		cardID, sid, err := pgRaiseProgramCard(t, f, "dev", map[string]any{"program": "github"})
		if err != nil {
			t.Fatalf("BLOCKED: the request_secret tool has no program recipient — required by FR-051/S-112 (tool said: %v)", err)
		}
		key := pgCardFieldKey(t, f, owner, sid, cardID)
		w, read := pgSubmitCard(t, f, cardID, key, "tok_valid_12345", "", true, true)
		require.Equal(t, http.StatusOK, w.Code, "the positive control is processed: %s", w.Body.String())
		require.Greater(t, read, int64(0), "a processed row reads the body")
		require.Equal(t, "submitted", pgCardState(t, f, owner, sid, cardID), "the card is submitted")
	})

	t.Run("S112_regression_non_program_card_bearer_is_today", func(t *testing.T) {
		f := pgPersonAPI(t)
		// A pending NON-program card submitted with the same bearer behaves
		// exactly as today (policy unchanged).
		cardID, sid, err := pgRaiseProgramCard(t, f, "dev",
			map[string]any{"variable": "PG_GATE_VAR", "purpose": "gate regression"})
		require.NoError(t, err, "instrument: a private-variable card raises today")
		key := pgCardFieldKey(t, f, owner, sid, cardID)
		w, _ := pgSubmitCard(t, f, cardID, key, "PG_GATE_VALUE_1", f.token, false, true)
		require.Equal(t, http.StatusOK, w.Code,
			"non-program cards keep today's policy (bearer may submit): got %d %s",
			w.Code, w.Body.String())
	})
}
