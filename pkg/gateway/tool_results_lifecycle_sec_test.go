package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// #1452 security review F1/F2 (1452-sec-result.md) on the existing
// GET /api/v1/sessions/{id}/tool-results/{ref} route. Every request goes
// through the registered auth wrapper and HandleSessions dispatch, not the
// bare handler, so the session gate that F1 requires is actually exercised.

func secToolResultGet(env *u1Env, sid, ref string) *httptest.ResponseRecorder {
	path := "/api/v1/sessions/" + sid + "/tool-results/" + ref
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.URL.Path = path
	r.Header.Set("Authorization", "Bearer sec-tool-results-token")
	w := httptest.NewRecorder()
	env.api.withAuth(env.api.HandleSessions)(w, r)
	return w
}

func secSaveResult(t *testing.T, env *u1Env, sid string, body []byte) string {
	t.Helper()
	ref, ok := newToolResultStore(env.api.homePath).saveJSON(sid, body)
	require.True(t, ok, "fixture: result body saved through the production store")
	return ref
}

// F1: a main whose agent leaves the workspace team stops serving its stored
// results, exactly as its history does; re-adding membership restores both.
func TestToolResultsSecF1_IneligibleMainRefusesStoredResult(t *testing.T) {
	env := u1NewEnv(t, false)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "sec-tool-results-token")
	const workspaceID = "01JSECTR00000000000000000A"
	env.seedWorkspace(t, workspaceID, false, "mia")
	sid := u1MainID(workspaceID, "mia")
	code, _ := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+sid, "")
	require.Equal(t, http.StatusOK, code, "fixture main exists and is eligible")
	body := []byte(`{"text":"SEC_F1_RESULT_CANARY ` + strings.Repeat("x", 60*1024) + `"}`)
	ref := secSaveResult(t, env, sid, body)

	ok := secToolResultGet(env, sid, ref)
	require.Equal(t, http.StatusOK, ok.Code, "positive control: eligible main serves its result: %s", ok.Body.String())
	require.Equal(t, string(body), ok.Body.String())

	env.seedWorkspace(t, workspaceID, false)
	history := secHistoryGet(env, sid)
	require.Equal(t, http.StatusNotFound, history.Code, "history refuses the ineligible main")
	refused := secToolResultGet(env, sid, ref)
	require.Equal(t, http.StatusNotFound, refused.Code)
	require.JSONEq(t, `{"error":"session not found"}`, refused.Body.String(), "same refusal and copy as history")
	require.NotContains(t, refused.Body.String(), "SEC_F1_RESULT_CANARY")

	env.seedWorkspace(t, workspaceID, false, "mia")
	again := secToolResultGet(env, sid, ref)
	require.Equal(t, http.StatusOK, again.Code, "re-added membership serves the retained main's result again")
	require.Equal(t, string(body), again.Body.String())
}

func secHistoryGet(env *u1Env, sid string) *httptest.ResponseRecorder {
	path := "/api/v1/sessions/" + sid + "/messages"
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.URL.Path = path
	r.Header.Set("Authorization", "Bearer sec-tool-results-token")
	w := httptest.NewRecorder()
	env.api.withAuth(env.api.HandleSessions)(w, r)
	return w
}

// F1 deletion variant: deleting an ordinary session refuses its results and
// removes their files through the real deletion path.
func TestToolResultsSecF1_DeletedSessionRefusesAndRemovesResults(t *testing.T) {
	env := u1NewEnv(t, false)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "sec-tool-results-token")
	meta, err := env.store(t).NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	body := []byte(`{"text":"SEC_F1_DELETE_CANARY ` + strings.Repeat("y", 60*1024) + `"}`)
	ref := secSaveResult(t, env, meta.ID, body)
	ok := secToolResultGet(env, meta.ID, ref)
	require.Equal(t, http.StatusOK, ok.Code, "positive control before deletion: %s", ok.Body.String())

	code, out := sf1Do(env, http.MethodDelete, "/api/v1/sessions/"+meta.ID, "")
	require.Equal(t, http.StatusOK, code, "real deletion succeeds: %s", out)

	refused := secToolResultGet(env, meta.ID, ref)
	require.Equal(t, http.StatusNotFound, refused.Code)
	require.JSONEq(t, `{"error":"session not found"}`, refused.Body.String())
	require.NotContains(t, refused.Body.String(), "SEC_F1_DELETE_CANARY")
	_, statErr := os.Stat(filepath.Join(env.api.homePath, "tool_results", meta.ID))
	require.True(t, errors.Is(statErr, os.ErrNotExist), "offloaded results are removed with their session, got %v", statErr)
}

// F1 missing state: a result file whose session metadata is gone is never
// served, even though the file itself still exists.
func TestToolResultsSecF1_MissingSessionNeverServesResultFile(t *testing.T) {
	env := u1NewEnv(t, false)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "sec-tool-results-token")
	const sid = "sec-missing-session-0001"
	ref := secSaveResult(t, env, sid, []byte(`{"text":"SEC_F1_ORPHAN_CANARY"}`))
	refused := secToolResultGet(env, sid, ref)
	require.Equal(t, http.StatusNotFound, refused.Code)
	require.JSONEq(t, `{"error":"session not found"}`, refused.Body.String())
	require.NotContains(t, refused.Body.String(), "SEC_F1_ORPHAN_CANARY")
}

// F2: the route returns the whole stored JSON up to the 1 MiB cap and refuses
// an over-cap body whole — never a 200 byte prefix that is not valid JSON.
func TestToolResultsSecF2_OverCapStoredBodyRefusedWhole(t *testing.T) {
	env := u1NewEnv(t, false)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "sec-tool-results-token")
	meta, err := env.store(t).NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	jsonString := func(total int) []byte {
		b := []byte(`"` + strings.Repeat("a", total-2) + `"`)
		require.Len(t, b, total)
		require.True(t, json.Valid(b))
		return b
	}
	atCap := jsonString(replayMaxResultBytes)
	refAt := secSaveResult(t, env, meta.ID, atCap)
	ok := secToolResultGet(env, meta.ID, refAt)
	require.Equal(t, http.StatusOK, ok.Code)
	require.Equal(t, len(atCap), ok.Body.Len(), "a body exactly at the cap is returned whole")
	require.True(t, json.Valid(ok.Body.Bytes()))

	over := jsonString(2 * 1024 * 1024)
	refOver := secSaveResult(t, env, meta.ID, over)
	refused := secToolResultGet(env, meta.ID, refOver)
	require.Equal(t, http.StatusInternalServerError, refused.Code)
	require.JSONEq(t, `{"error":"could not read tool result"}`, refused.Body.String(), "over-cap body is refused, not cut into invalid JSON")
}

// F2: the offloader publishes a retrievable reference only up to the cap;
// above it the generated TruncatedResult sentinel is returned and no file is
// written, so no reference can promise a body the route cannot return.
func TestToolResultsSecF2_OffloadAboveCapPublishesNoReference(t *testing.T) {
	home := t.TempDir()
	store := newToolResultStore(home)
	const sid = "sec-offload-cap-0001"
	encode := func(n int) []byte { return []byte(`"` + strings.Repeat("b", n-2) + `"`) }

	atCap, offloaded := maybeOffloadResult(store, sid, encode(replayMaxResultBytes))
	require.True(t, offloaded)
	ref, isRef := atCap.(generated.ToolResultRef)
	require.True(t, isRef, "a body at the cap is offloaded as a retrievable ToolResultRef, got %T", atCap)
	data, err := store.readByRef(sid, ref.Ref)
	require.NoError(t, err)
	require.Len(t, data, replayMaxResultBytes)

	over, offloaded := maybeOffloadResult(store, sid, encode(replayMaxResultBytes+1))
	require.True(t, offloaded, "an over-cap body is never re-inlined")
	truncated, isTruncated := over.(generated.TruncatedResult)
	require.True(t, isTruncated, "an over-cap body becomes the TruncatedResult sentinel, got %T", over)
	require.True(t, truncated.Truncated)
	require.Equal(t, replayMaxResultBytes+1, truncated.OriginalSizeBytes)
	require.Len(t, truncated.Preview, toolResultPreviewBytes)
	files, err := os.ReadDir(filepath.Join(home, "tool_results", sid))
	require.NoError(t, err)
	require.Len(t, files, 1, "only the at-cap body was written; the over-cap body published no file")
}
