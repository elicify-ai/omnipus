// Tests for POST /api/v1/library/{workspace_id}/files/search — the file
// search over a plain folder or mount
// (docs/internal/specs/unified-search-and-grep-spec.md workstream B/C).
//
// Expected values are derived from the SPEC and the CONTRACT, never from what
// the handler happens to do: field names come from
// contracts/components/schemas/FileSearch*.yaml, bound defaults from MV-3
// (max matches 1000), the error taxonomy from MV-1 and the Library's existing
// mapLibraryErr table, and the walk-cap behaviour from MV-11.

package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/filegrep"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// TestLibraryFilesSearch_HandlerHappy — US-2 AS-1/2: a name match in a plain
// folder, a content match with a bounded excerpt, an honestly-empty zero-hit
// answer (MV-5: hits is [] on the wire, never null), and a clamp-disclosed
// limits_applied echo (R2-MIN-007) when the caller over-asks.
func TestLibraryFilesSearch_HandlerHappy(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	dir := workDir(api, ws)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Q3 report.md"), []byte("nothing relevant in here"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("the meeting notes are here"), 0o600))

	// US-2 AS-1: a name match returns the workspace-relative path.
	w := libPostJSON(t, api, "/api/v1/library/"+ws+"/files/search", `{"query":"report"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.FileSearchResponse](t, w)
	require.Len(t, resp.Hits, 1)
	assert.Equal(t, "Q3 report.md", resp.Hits[0].Path)
	assert.Equal(t, gen.FileSearchResponseHitsMatchKindName, resp.Hits[0].MatchKind)
	assert.False(t, resp.Truncated)

	// US-2 AS-2: a content match carries a bounded, non-empty excerpt and the
	// 1-based matching line number.
	w = libPostJSON(t, api, "/api/v1/library/"+ws+"/files/search", `{"query":"meeting"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp = decodeJSON[gen.FileSearchResponse](t, w)
	require.Len(t, resp.Hits, 1)
	assert.Equal(t, "notes.txt", resp.Hits[0].Path)
	assert.Equal(t, gen.FileSearchResponseHitsMatchKindContent, resp.Hits[0].MatchKind)
	require.NotNil(t, resp.Hits[0].Excerpt)
	assert.Contains(t, *resp.Hits[0].Excerpt, "meeting")
	require.NotNil(t, resp.Hits[0].Line)
	assert.Equal(t, 1, *resp.Hits[0].Line)

	// MV-5: a zero-hit answer is [] on the wire, never null. Asserted on the
	// RAW body — decoding through encoding/json would silently turn either
	// shape into the same empty Go slice and hide a regression.
	w = libPostJSON(t, api, "/api/v1/library/"+ws+"/files/search", `{"query":"zzzznomatchzzzz"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"hits":[]`,
		"a zero-hit response must marshal hits as [] not null: %s", w.Body.String())
	resp = decodeJSON[gen.FileSearchResponse](t, w)
	assert.Empty(t, resp.Hits)
	assert.False(t, resp.Truncated)

	// R2-MIN-007: an over-cap request is clamped to the server default (MV-3:
	// max matches 1000) and the clamp is disclosed via limits_applied, not
	// honored silently.
	w = libPostJSON(t, api, "/api/v1/library/"+ws+"/files/search", `{"query":"report","limits":{"matches":999999}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp = decodeJSON[gen.FileSearchResponse](t, w)
	assert.Equal(t, 1000, resp.LimitsApplied.Matches,
		"a matches override above the server default must be clamped down and disclosed, per MV-3")
}

// TestLibraryFilesSearch_ErrTaxonomy — MV-1's status table: 400 invalid body,
// 400 invalid regex (with the engine's own compile error), 401
// unauthenticated, 403 a scope path outside the confined root, 404 an unknown
// workspace, 429 rate-limited (with Retry-After).
func TestLibraryFilesSearch_ErrTaxonomy(t *testing.T) {
	t.Run("400 invalid JSON body", func(t *testing.T) {
		api, ws := buildLibraryTestAPI(t)
		w := libPostJSON(t, api, "/api/v1/library/"+ws+"/files/search", `{"query":`)
		assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	})

	t.Run("400 invalid regex surfaces the compile error", func(t *testing.T) {
		api, ws := buildLibraryTestAPI(t)
		require.NoError(t, os.MkdirAll(workDir(api, ws), 0o700))
		w := libPostJSON(t, api, "/api/v1/library/"+ws+"/files/search", `{"query":"(","regex":true}`)
		assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), "missing closing",
			"the 400 must surface the engine's own RE2 compile error, not a generic message")
	})

	// Drives the REAL registered middleware chain (a.withUploadAuth wrapping
	// a.HandleLibraryTree, the same wrapper every /api/v1/library/{id}/...
	// endpoint is registered under — rest.go's
	// RegisterHTTPHandler("/api/v1/library/", ...) line), not a bare handler
	// shortcut — mirrors TestVaultSearch_RefusesUnauthenticatedCallsLikeItsNeighbours's
	// own reasoning: a shortcut here would hide an auth regression on this
	// one endpoint while its neighbours stayed protected.
	t.Run("401 unauthenticated", func(t *testing.T) {
		api, ws := buildLibraryTestAPI(t)
		guarded := api.withUploadAuth(api.HandleLibraryTree)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/library/"+ws+"/files/search",
			strings.NewReader(`{"query":"x"}`))
		r.Header.Set("Content-Type", "application/json")
		guarded(w, r)
		assert.Equal(t, http.StatusUnauthorized, w.Code,
			"an unauthenticated caller must be refused before any search runs: %s", w.Body.String())
	})

	t.Run("403 path outside the confined root", func(t *testing.T) {
		api, ws := buildLibraryTestAPI(t)
		require.NoError(t, os.MkdirAll(workDir(api, ws), 0o700))
		outsideDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("top secret"), 0o600))
		require.NoError(t, os.Symlink(outsideDir, filepath.Join(workDir(api, ws), "escape")))

		w := libPostJSON(t, api, "/api/v1/library/"+ws+"/files/search", `{"query":"secret","path":"escape"}`)
		assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	})

	t.Run("404 unknown workspace", func(t *testing.T) {
		api, _ := buildLibraryTestAPI(t)
		w := libPostJSON(t, api, "/api/v1/library/"+ulidLikeID(t)+"/files/search", `{"query":"x"}`)
		assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	})

	// MV-11/FR-017: files/search sits behind the SAME knowledge-retrieval-
	// class limiter as its sibling knowledge routes — exhaust the shared
	// singleton for this workspace's own bucket directly, the same call the
	// handler itself makes, rather than firing dozens of real requests.
	t.Run("429 rate limited", func(t *testing.T) {
		api, ws := buildLibraryTestAPI(t)
		require.NoError(t, os.MkdirAll(workDir(api, ws), 0o700))
		for i := 0; i < knowledgeRESTLimiter.Limit(); i++ {
			knowledgeRESTLimiter.Allow(knowledgeRateKey(ws))
		}
		w := libPostJSON(t, api, "/api/v1/library/"+ws+"/files/search", `{"query":"x"}`)
		assert.Equal(t, http.StatusTooManyRequests, w.Code, w.Body.String())
	})
}

// TestLibraryFilesSearch_ConcurrencyCapAndCancel — MV-11 / R2-MAJ-003: the
// ONE shared 2-slot walk semaphore answers a 3rd concurrent request with 429
// + Retry-After, and a disconnected client's r.Context() cancellation stops
// the walk promptly rather than running to an injected large deadline.
func TestLibraryFilesSearch_ConcurrencyCapAndCancel(t *testing.T) {
	t.Run("3rd walk while both slots are busy answers 429", func(t *testing.T) {
		api, ws := buildLibraryTestAPI(t)
		require.NoError(t, os.MkdirAll(workDir(api, ws), 0o700))

		// Occupy both shared slots directly, exactly as a slow REST walk or
		// the agent grep tool's own walk would (MV-11: "TOOL walk shares the
		// semaphore") — the semaphore does not know or care which surface
		// acquired it.
		release1, ok1 := AcquireFilegrepWalkSlot(context.Background(), 0)
		require.True(t, ok1)
		release2, ok2 := AcquireFilegrepWalkSlot(context.Background(), 0)
		require.True(t, ok2)
		defer release1()
		defer release2()

		w := libPostJSON(t, api, "/api/v1/library/"+ws+"/files/search", `{"query":"anything"}`)
		assert.Equal(t, http.StatusTooManyRequests, w.Code, w.Body.String())
		assert.Equal(t, "1", w.Header().Get("Retry-After"))
	})

	// filegrepSearchFn is this file's swappable seam over filegrep.Search
	// (see its doc comment). Substituting a controllable stand-in here lets
	// this test prove r.Context() cancellation propagates into the engine
	// call deterministically — a real, disk-backed walk fast enough to
	// finish in a unit test would never be slow enough to reliably still be
	// running when the cancel fires, and one slow enough to guarantee that
	// window would make the suite flaky/slow. The semaphore acquisition,
	// root building and request wiring this exercises are all the REAL
	// production code path; only the engine call itself is stood in for.
	t.Run("client disconnect cancels the walk", func(t *testing.T) {
		api, ws := buildLibraryTestAPI(t)
		require.NoError(t, os.MkdirAll(workDir(api, ws), 0o700))

		orig := filegrepSearchFn
		t.Cleanup(func() { filegrepSearchFn = orig })

		started := make(chan struct{})
		filegrepSearchFn = func(ctx context.Context, _ []filegrep.Root, opts filegrep.Options) (filegrep.Result, error) {
			close(started)
			select {
			case <-ctx.Done():
				return filegrep.Result{
					Hits:            []filegrep.Hit{},
					Truncated:       true,
					TruncatedReason: filegrep.ReasonDeadline,
					LimitsApplied:   opts.Limits.Normalize(),
				}, nil
			case <-time.After(30 * time.Second):
				return filegrep.Result{Hits: []filegrep.Hit{}, LimitsApplied: opts.Limits.Normalize()}, nil
			}
		}

		ctx, cancel := context.WithCancel(context.Background())
		req := httptest.NewRequest(http.MethodPost, "/api/v1/library/"+ws+"/files/search",
			strings.NewReader(`{"query":"anything"}`)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		done := make(chan struct{})
		go func() {
			api.HandleLibrary(rec, req)
			close(done)
		}()

		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("search never reached the engine call")
		}

		cancelledAt := time.Now()
		cancel()

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("handler did not return after the client's context was cancelled")
		}
		assert.Less(t, time.Since(cancelledAt), 1*time.Second,
			"a disconnected client must cancel the walk promptly via r.Context(), not run to the fake engine's 30s fallback")
	})
}

// TestLibraryFilesSearch_MountScope — US-2 AS-3: a mount is searched as its
// own root, so its hits carry the mount-prefixed workspace-relative path; an
// engineered small bound truncates honestly over the mount's own entries too,
// not just the plain work tree.
func TestLibraryFilesSearch_MountScope(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	require.NoError(t, os.MkdirAll(workDir(api, ws), 0o700))

	mountDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(mountDir, "shared-a.txt"), []byte("alpha"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(mountDir, "shared-b.txt"), []byte("beta"), 0o600))
	_, _, err := workspace.CreateMount(api.homePath, ws, "mymount", mountDir)
	require.NoError(t, err)

	w := libPostJSON(t, api, "/api/v1/library/"+ws+"/files/search", `{"query":"shared"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.FileSearchResponse](t, w)
	require.Len(t, resp.Hits, 2)
	paths := []string{resp.Hits[0].Path, resp.Hits[1].Path}
	assert.Contains(t, paths, "mymount/shared-a.txt",
		"a mount hit must carry the mount-prefixed workspace-relative path")
	assert.Contains(t, paths, "mymount/shared-b.txt")
	assert.False(t, resp.Truncated)

	// Edge-case table (US-2 AS-3; US-3 AS-6): an engineered small bound
	// truncates honestly. Deterministic path-lexicographic order (spec A3)
	// means the alphabetically-first match is the one a 1-match cap keeps.
	w = libPostJSON(t, api, "/api/v1/library/"+ws+"/files/search", `{"query":"shared","limits":{"matches":1}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp = decodeJSON[gen.FileSearchResponse](t, w)
	require.True(t, resp.Truncated)
	require.NotNil(t, resp.TruncatedReason)
	assert.Equal(t, gen.MaxMatches, *resp.TruncatedReason)
	require.Len(t, resp.Hits, 1)
	assert.Equal(t, "mymount/shared-a.txt", resp.Hits[0].Path)
}

// TestLibraryFilesSearch_SemaphoreIsSharedWithTheAgentTool — MV-11's actual
// guarantee, which no other test states: the REST surface and the agent grep
// tool draw from ONE 2-slot counter, not one each.
//
// This test exists because the defect it catches shipped in the parallel
// build and survived a green suite. The gateway owned a private 2-slot
// channel while pkg/tools/grep.go acquired pkg/filegrep's — two independent
// caps of 2, so four walks could run at once and the "cap" bounded nothing.
// TestLibraryFilesSearch_ConcurrencyCapAndCancel could not see it: it fills
// the slots through AcquireFilegrepWalkSlot, the same door the handler uses,
// so it stays green whichever counter that door happens to open.
//
// The discriminating move is to fill both slots through the TOOL's door —
// filegrep.TryAcquire, exactly what GrepTool.Execute calls — and then assert
// the REST surface is refused. Under one shared counter that is a 429; under
// two separate counters the handler finds its own slots free and answers 200.
func TestLibraryFilesSearch_SemaphoreIsSharedWithTheAgentTool(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	require.NoError(t, os.MkdirAll(workDir(api, ws), 0o700))

	// Both acquisitions go through pkg/filegrep directly — the agent tool's
	// path, never the gateway's wrapper.
	require.True(t, filegrep.TryAcquire(context.Background()),
		"tool-side slot 1 must be available on a quiet process")
	require.True(t, filegrep.TryAcquire(context.Background()),
		"tool-side slot 2 must be available on a quiet process")
	defer filegrep.Release()
	defer filegrep.Release()

	w := libPostJSON(t, api, "/api/v1/library/"+ws+"/files/search", `{"query":"anything"}`)
	assert.Equalf(t, http.StatusTooManyRequests, w.Code,
		"two agent-tool walks already hold both shared slots, so the human file "+
			"search must be refused with 429 — a 200 here means the REST surface counts "+
			"against its OWN private semaphore and MV-11's cross-surface cap is not real. "+
			"body=%s", w.Body.String())
	assert.Equal(t, "1", w.Header().Get("Retry-After"))
}
