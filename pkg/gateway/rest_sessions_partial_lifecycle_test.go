package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SF-2 / architect ruling 2026-10-09: absence of execution is NOT an
// incomplete-state signal. Missing journals are normal; unreadable ones are not.
func TestSessionList_LifecycleReadFailuresArePartial(t *testing.T) {
	for _, fault := range []string{"corrupt", "empty", "I/O"} {
		t.Run(fault, func(t *testing.T) {
			f := newI1R1BootFixture(t)
			bad := f.root(t, session.LifecycleRunning, f.boot.Current())
			healthy := f.root(t, session.LifecycleRunning, f.boot.Current())
			ordinary := createTestSession(t, f.api)
			original := f.journal(t, bad)
			healthyBefore := f.journal(t, healthy)
			path := filepath.Join(f.ls.Dir(), bad+".jsonl")
			breakLifecycleJournal(t, path, fault)
			_, err := f.ls.Load(bad)
			require.Error(t, err, "instrument: actual durable journal read must fail")
			require.NotErrorIs(t, err, session.ErrLifecycleNotFound, "instrument: distinguish missing from unreadable")
			_, err = f.ls.Load(ordinary)
			require.ErrorIs(t, err, session.ErrLifecycleNotFound, "instrument: this row genuinely has no journal")

			rec, page := lifecycleFailurePage(t, f.api, "flat=true")
			assert.Equal(t, http.StatusOK, rec.Code)
			require.Len(t, page.Sessions, 3, "unreadable journal must NOT drop the row")
			require.NotNil(t, page.PartialErrors, "unexpected Load error must reach partial_errors")
			assert.Equal(t, []string{fmt.Sprintf("session=%s: lifecycle_read_unavailable", bad)}, *page.PartialErrors)
			assert.NotContains(t, rec.Body.String(), f.ls.Dir(), "no local path may leak")
			assert.NotContains(t, rec.Body.String(), "scan ", "no syscall details may leak")
			assert.NotContains(t, rec.Body.String(), "contains no readable", "no journal parser details may leak")
			rows, ok := decodeObject(t, rec.Body.Bytes())["sessions"].([]any)
			require.True(t, ok, "sessions must remain a JSON array")
			for _, rawRow := range rows {
				row, ok := rawRow.(map[string]any)
				require.True(t, ok, "each session row must remain a JSON object")
				if row["id"] == healthy {
					assert.Equal(t, "working", row["lifecycle_state"])
					assert.Equal(t, "running", row["execution"])
					continue
				}
				for _, key := range []string{"lifecycle_state", "stop_note", "execution"} {
					_, present := row[key]
					assert.False(t, present, "%s must be omitted, never null or fabricated; row=%v", key, row)
				}
			}
			assert.Equal(t, healthyBefore, f.journal(t, healthy), "projection must not rewrite the healthy journal")
			if fault != "I/O" {
				after, err := os.ReadFile(path)
				require.NoError(t, err)
				if fault == "corrupt" {
					assert.Equal(t, []byte("not-json\n"), after, "projection must not repair a corrupt journal")
				} else {
					assert.Empty(t, after, "projection must not invent a record")
				}
			}

			// Walk real pages: lifecycle failure must not invalidate the cursor.
			offset := "0"
			seen := make(map[string]bool)
			var degraded []string
			for i := 0; i < 3; i++ {
				_, single := lifecycleFailurePage(t, f.api, "flat=true&limit=1&offset="+offset)
				require.Len(t, single.Sessions, 1)
				id := single.Sessions[0].Id
				assert.False(t, seen[id], "a degraded page must not duplicate rows")
				seen[id] = true
				if single.PartialErrors != nil {
					degraded = append(degraded, (*single.PartialErrors)...)
				}
				if i < 2 {
					require.NotNil(t, single.NextCursor, "degradation must not halt pagination")
					offset = *single.NextCursor
				} else {
					assert.Nil(t, single.NextCursor)
				}
			}
			assert.Equal(t, map[string]bool{bad: true, healthy: true, ordinary: true}, seen)
			assert.Equal(t, []string{fmt.Sprintf("session=%s: lifecycle_read_unavailable", bad)}, degraded)

			// Retry after external repair must remove the warning and recover
			// the actual current-boot running projection, without a gateway restart.
			if fault == "I/O" {
				require.NoError(t, os.Remove(path))
			}
			require.NoError(t, os.WriteFile(path, original, 0o600))
			retryRec, retry := lifecycleFailurePage(t, f.api, "flat=true")
			assert.Nil(t, retry.PartialErrors, "repaired journal must stop reporting degradation")
			_, present := decodeObject(t, retryRec.Body.Bytes())["partial_errors"]
			assert.False(t, present, "partial_errors is omitted when empty")
			require.Len(t, retry.Sessions, 3)
			found := false
			for _, row := range retry.Sessions {
				if row.Id == bad {
					found = true
					require.NotNil(t, row.Execution)
					assert.Equal(t, gen.SessionExecutionRunning, *row.Execution)
					require.NotNil(t, row.LifecycleState)
					assert.Equal(t, gen.SessionLifecycleStateWorking, *row.LifecycleState)
				}
			}
			assert.True(t, found, "retry must retain the repaired row")
			assert.Equal(t, original, f.journal(t, bad), "retry reads do not rewrite repaired journal")
		})
	}
}

func breakLifecycleJournal(t *testing.T, path, fault string) {
	t.Helper()
	switch fault {
	case "corrupt":
		require.NoError(t, os.WriteFile(path, []byte("not-json\n"), 0o600))
	case "empty":
		require.NoError(t, os.WriteFile(path, nil, 0o600))
	case "I/O":
		// A directory in place of the journal forces an actual scanner I/O
		// failure on every supported OS, including privileged CI runners.
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.Mkdir(path, 0o700))
	default:
		t.Fatalf("unknown fixture fault %q", fault)
	}
}

func lifecycleFailurePage(t *testing.T, api *restAPI, query string) (*httptest.ResponseRecorder, gen.SessionPage) {
	t.Helper()
	rec := httptest.NewRecorder()
	api.HandleSessions(rec, httptest.NewRequest(http.MethodGet, "/api/v1/sessions?"+query, nil))
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var page gen.SessionPage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	return rec, page
}

func TestSessionList_MissingLifecycleIsNotPartial(t *testing.T) {
	for _, wired := range []bool{false, true} {
		t.Run(fmt.Sprintf("store=%t", wired), func(t *testing.T) {
			api, cleanup := newTestRestAPI(t)
			t.Cleanup(cleanup)
			if wired {
				api.agentLoop.SetSessionMessagingStores(nil, session.NewLifecycleStore(t.TempDir()))
			}
			id := createTestSession(t, api)
			rec, page := lifecycleFailurePage(t, api, "flat=true")
			require.Len(t, page.Sessions, 1)
			assert.Equal(t, id, page.Sessions[0].Id)
			assert.Nil(t, page.PartialErrors)
			row := decodeObject(t, rec.Body.Bytes())
			_, present := row["partial_errors"]
			assert.False(t, present, "normal absence must not flood the degradation channel")
			assert.Nil(t, page.Sessions[0].LifecycleState)
			assert.Nil(t, page.Sessions[0].StopNote)
			assert.Nil(t, page.Sessions[0].Execution)
		})
	}
}

// The dispatcher explicitly extends propagation to non-page builders: without
// a partial_errors channel they must fail visibly using the existing envelope.
func TestSessionRuntime_LifecycleReadFailuresPropagate(t *testing.T) {
	for _, endpoint := range []string{"detail", "agent list", "rename", "detail builder"} {
		t.Run(endpoint, func(t *testing.T) {
			f := newI1R1BootFixture(t)
			id := f.root(t, session.LifecycleRunning, f.boot.Current())
			path := filepath.Join(f.ls.Dir(), id+".jsonl")
			breakLifecycleJournal(t, path, "I/O")
			rec := httptest.NewRecorder()
			switch endpoint {
			case "detail":
				f.api.HandleSessions(rec, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+id, nil))
			case "agent list":
				f.api.HandleAgents(rec, httptest.NewRequest(http.MethodGet, "/api/v1/agents/mia/sessions", nil))
			case "rename":
				f.api.renameSession(rec, httptest.NewRequest(http.MethodPut, "/api/v1/sessions/"+id, strings.NewReader(`{"title":"Renamed"}`)), id)
			case "detail builder":
				meta, err := f.api.agentLoop.GetSessionStore().GetMeta(id)
				require.NoError(t, err)
				jsonSessionDetail(rec, meta, nil, false, f.ls, f.boot.Current())
			}
			assert.Equal(t, http.StatusInternalServerError, rec.Code, "body: %s", rec.Body.String())
			assert.Equal(t, map[string]any{"error": "session lifecycle unavailable"}, decodeObject(t, rec.Body.Bytes()))
			assert.NotContains(t, rec.Body.String(), f.ls.Dir())
		})
	}
}
