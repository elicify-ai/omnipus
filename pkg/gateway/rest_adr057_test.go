// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_adr057_test.go — tests for ADR-057 unit U18 (pkg/gateway/rest.go,
// pkg/gateway/replay.go): W11b (the four filter-site deletion), W16c (the
// REST pagination/nesting layer + gen.SessionPage envelope), W19a (the
// drill-down surface), W18b (uploads cascade wiring).
//
// Per the ADR-057 session-unification spec's binding rule 5, every unit's
// new tests go in a NEW file named `<subject>_adr057_test.go`; this is
// U18's. Rule 6: this file's own new package-level helpers are prefixed
// `u18` so a same-wave collision with another unit's new package-level test
// helper is a compile error, not a silent shadow.
//
// Binding rule 1 (real state, never a spy): every test below drives a REAL
// *agent.AgentLoop backed by a REAL session.UnifiedStore rooted at a
// t.TempDir() (via newTestRestAPI/mustAgentLoop), a REAL
// session.LifecycleStore for the uploads-cascade descendant walk, and real
// files on a real filesystem for the uploads cascade. Nothing here asserts
// "a function was called" — every assertion lands on the REST response body
// or a real on-disk artefact.
//
// Binding rule 4 (positive lower bound before any exclusion/zero-count
// assertion): TestU18IsDelegateChildEntry_ZeroNonTestReferences asserts it
// located >= 60 non-test Go ParentSpawnCallID references (measured 73)
// before asserting zero for IsDelegateChildEntry; TestU18DeleteSession_
// CascadesUploadsAcrossDescendants asserts each descendant's upload marker
// file exists BEFORE the delete, never just that the directory is gone
// after.
//
// Corollary "distinct ids everywhere": every parent/child/grandchild triple
// below is constructed as three distinct, non-equal literal or minted ids.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// ---------------------------------------------------------------------
// Static gate — test #58's U18 half (FR-034/FR-035/FR-038)
// ---------------------------------------------------------------------

// u18RepoRoot resolves the repository root relative to THIS test file's own
// path via runtime.Caller, so the gate works regardless of the cwd a test
// runner uses (mirrors the loadMessageSchema precedent, sessions_wire_test.go).
func u18RepoRoot(t *testing.T) string {
	t.Helper()
	//nolint:dogsled // runtime.Caller returns 4 values; only the file path is needed.
	_, thisFile, _, _ := runtime.Caller(0)
	// this file is pkg/gateway/rest_adr057_test.go — repo root is two levels up.
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

// u18CountGoSourceOccurrences walks pkg/ and cmd/ under root, and for every
// *.go file — optionally excluding *_test.go — counts occurrences of needle.
// Pure Go source scan (no shelling out to rg/grep, which is not guaranteed
// present on every CI runner). Returns the total count and the number of
// distinct files it appeared in, matching how the spec's bounds table states
// its measured counts ("73 across 9 non-test Go files").
func u18CountGoSourceOccurrences(t *testing.T, root, needle string, excludeTests bool) (total, files int) {
	t.Helper()
	for _, sub := range []string{"pkg", "cmd"} {
		dir := filepath.Join(root, sub)
		err := filepath.Walk(dir, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			if excludeTests && strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatalf("read %s: %v", path, readErr)
			}
			c := strings.Count(string(src), needle)
			if c > 0 {
				total += c
				files++
			}
			return nil
		})
		require.NoError(t, err, "walk %s", dir)
	}
	return total, files
}

// TestU18IsDelegateChildEntry_ZeroNonTestReferences is this unit's half of
// test #58 (TestIsDelegateChildEntry_ZeroNonTestReferences, jointly owned by
// U5/U18): U5 landed IsDelegateChildEntry as a compile-time shim (always
// false) so the tree kept compiling across three intervening waves; hard
// ordering 6 requires U18's commit to remove BOTH the shim and its four
// non-test call sites (pkg/gateway/replay.go, pkg/gateway/rest.go,
// pkg/agent/verifier_adjudication.go, pkg/tools/inspect_session.go)
// together. This test proves both halves are gone from THIS commit's
// perspective.
//
// Per binding rule 4, the positive lower bound (>= 60 non-test Go
// ParentSpawnCallID references, measured 73 across 9 files on
// feature/plan-swimlane-board 2026-08-03) is asserted FIRST — proving the
// walk itself is live — before the zero-count assertion for
// IsDelegateChildEntry is trusted. FR-034/FR-038; SC-003 requires the search
// be scoped to *.go so an unscoped grep (which would match this very test
// file's own doc comments, the spec, and the ADR) can never return zero.
func TestU18IsDelegateChildEntry_ZeroNonTestReferences(t *testing.T) {
	root := u18RepoRoot(t)

	spawnTotal, spawnFiles := u18CountGoSourceOccurrences(t, root, "ParentSpawnCallID", true)
	require.GreaterOrEqualf(t, spawnTotal, 60,
		"positive lower bound (binding rule 4): must locate >= 60 non-test Go ParentSpawnCallID "+
			"references before trusting the zero-count IsDelegateChildEntry assertion below — "+
			"got %d across %d files; a search this low means the walk itself is broken", spawnTotal, spawnFiles)

	shimTotal, shimFiles := u18CountGoSourceOccurrences(t, root, "IsDelegateChildEntry", true)
	assert.Equalf(t, 0, shimTotal,
		"IsDelegateChildEntry MUST have zero non-test Go references (FR-034) — "+
			"found %d across %d files; either the compile shim or a call site survived", shimTotal, shimFiles)
}

// TestU18FourFilterSites_NoLongerCallTheFilter is a narrower, source-level
// pin of the exact four call sites hard ordering 6 names, independent of the
// broader zero-reference sweep above: even if some future file reintroduced
// a differently-named predicate, none of these four sites may call
// IsDelegateChildEntry (which no longer exists) or filterDelegateChildEntries
// (also deleted, FR-035).
func TestU18FourFilterSites_NoLongerCallTheFilter(t *testing.T) {
	root := u18RepoRoot(t)
	sites := map[string]string{
		"pkg/gateway/replay.go":              filepath.Join(root, "pkg", "gateway", "replay.go"),
		"pkg/gateway/rest.go":                filepath.Join(root, "pkg", "gateway", "rest.go"),
		"pkg/agent/verifier_adjudication.go": filepath.Join(root, "pkg", "agent", "verifier_adjudication.go"),
		"pkg/tools/inspect_session.go":       filepath.Join(root, "pkg", "tools", "inspect_session.go"),
	}
	for name, path := range sites {
		src, err := os.ReadFile(path)
		require.NoErrorf(t, err, "read %s", name)
		content := string(src)
		assert.NotContainsf(t, content, "IsDelegateChildEntry", "%s must no longer call the retired predicate", name)
		assert.NotContainsf(t, content, "filterDelegateChildEntries", "%s must no longer call the retired REST filter helper", name)
	}
}

// ---------------------------------------------------------------------
// Shared fixtures — nested listing (W16c)
// ---------------------------------------------------------------------

// u18SetParent stamps childID's ParentSessionID via SetMeta, mirroring
// pkg/agent/loop_adr057_test.go's TestListAllSessions_HierarchyRootsOrphansAndParentFilter
// fixture (documented finding: CreateSessionWithID does not itself persist
// ParentSessionID — pkg/agent/subturn.go's spawnSubTurn closes that gap in
// production, U7's line item; a real caller in a test must make this
// follow-up call explicitly until then).
func u18SetParent(t *testing.T, store *session.UnifiedStore, childID, parentID string) {
	t.Helper()
	require.NoError(t, store.SetMeta(childID, session.MetaPatch{ParentSessionID: &parentID}))
}

// u18NewChild mints a real, store-backed delegate-type session with the
// exact id childID and persists parentID as its direct parent — the shape a
// delegated child has post-D1 (FR-005/FR-008).
func u18NewChild(t *testing.T, store *session.UnifiedStore, childID, parentID string) *session.UnifiedMeta {
	t.Helper()
	meta, err := store.CreateSessionWithID(childID, parentID, session.SessionTypeDelegate, "", "u18-agent")
	require.NoError(t, err, "CreateSessionWithID(%q) must succeed", childID)
	require.NotEqual(t, parentID, childID, "fixture defect: parent and child ids must be distinct")
	u18SetParent(t, store, childID, parentID)
	return meta
}

// ---------------------------------------------------------------------
// W16c — nested listing (FR-091/FR-097), test #98 equivalent
// ---------------------------------------------------------------------

// sessionPageWire is a minimal decode target for gen.SessionPage.
type sessionPageWire struct {
	Sessions      []map[string]any `json:"sessions"`
	NextCursor    *string          `json:"next_cursor"`
	PartialErrors []string         `json:"partial_errors"`
}

func u18DoListSessions(t *testing.T, api *restAPI, query string) (sessionPageWire, int) {
	t.Helper()
	w := httptest.NewRecorder()
	target := "/api/v1/sessions"
	if query != "" {
		target += "?" + query
	}
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.URL.Path = "/api/v1/sessions"
	api.listSessions(w, r)
	var page sessionPageWire
	if w.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page), "response body: %s", w.Body.String())
	}
	return page, w.Code
}

// TestU18ListSessions_RootsOnlyWithChildCount is test #98
// (TestSessionList_RootsOnlyWithChildCount, BDD-101): a parent chat with 24
// child sessions must appear as exactly ONE root row carrying
// child_count == 24, with ZERO of the 24 children inline in the same
// response — but they remain reachable via parent_session_id (proven by the
// companion paging test below), never silently dropped.
func TestU18ListSessions_RootsOnlyWithChildCount(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "shared session store must be available")

	parent, err := store.NewSession(session.SessionTypeChat, "webchat", "u18-agent")
	require.NoError(t, err)

	const n = 24
	childIDs := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		child := u18NewChild(t, store, "u18-rootcount-child-"+strconv.Itoa(i), parent.ID)
		childIDs[child.ID] = true
	}
	require.Len(t, childIDs, n, "fixture defect: must have minted exactly %d distinct children", n)

	page, code := u18DoListSessions(t, api, "limit=100")
	require.Equal(t, http.StatusOK, code)

	// Positive lower bound: the parent itself must be present before any
	// exclusion assertion about its children is trusted.
	var parentRow map[string]any
	for _, s := range page.Sessions {
		if s["id"] == parent.ID {
			parentRow = s
		}
		sID, sIDOk := s["id"].(string)
		if sIDOk && childIDs[sID] {
			t.Fatalf("child session %v must NOT appear as a top-level row in the default (roots-only) listing", s["id"])
		}
	}
	require.NotNilf(t, parentRow, "the parent chat must appear in the default listing; got %d rows", len(page.Sessions))

	ccRaw, ok := parentRow["child_count"]
	require.True(t, ok, "parent row must carry child_count (FR-091/FR-097)")
	assert.EqualValues(t, n, ccRaw, "parent's child_count must equal the number of direct children minted")
}

// TestU18ListSessions_ExpandReturnsDirectChildrenPaged is test #99
// (TestSessionList_ExpandReturnsDirectChildrenPaged, BDD-103): expanding a
// node via ?parent_session_id=<id> returns exactly that node's DIRECT
// children, a page at a time, at every depth — never a grandchild.
func TestU18ListSessions_ExpandReturnsDirectChildrenPaged(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "shared session store must be available")

	root, err := store.NewSession(session.SessionTypeChat, "webchat", "u18-agent")
	require.NoError(t, err)

	const rootChildren = 5
	var lastChildID string
	for i := 0; i < rootChildren; i++ {
		c := u18NewChild(t, store, "u18-expand-child-"+strconv.Itoa(i), root.ID)
		lastChildID = c.ID
	}
	grandchild := u18NewChild(t, store, "u18-expand-grandchild", lastChildID)

	// Page 1: limit smaller than the total direct-child count.
	page1, code := u18DoListSessions(t, api, "parent_session_id="+root.ID+"&limit=2")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, page1.Sessions, 2, "must return exactly `limit` rows when more remain")
	require.NotNil(t, page1.NextCursor, "a page short of the total must report a next_cursor")
	for _, s := range page1.Sessions {
		assert.NotEqual(t, grandchild.ID, s["id"], "an expand call must never return a grandchild")
	}

	// Page 2, following the cursor.
	page2, code := u18DoListSessions(t, api, "parent_session_id="+root.ID+"&limit=2&offset="+*page1.NextCursor)
	require.Equal(t, http.StatusOK, code)
	require.Len(t, page2.Sessions, 2)

	// Page 3, the remainder.
	require.NotNil(t, page2.NextCursor)
	page3, code := u18DoListSessions(t, api, "parent_session_id="+root.ID+"&limit=2&offset="+*page2.NextCursor)
	require.Equal(t, http.StatusOK, code)
	require.Len(t, page3.Sessions, 1, "the final page must carry the remaining 1 of 5 direct children")
	assert.Nil(t, page3.NextCursor, "the final page must report no further cursor")

	seen := map[string]bool{}
	for _, p := range [][]map[string]any{page1.Sessions, page2.Sessions, page3.Sessions} {
		for _, s := range p {
			id, idOk := s["id"].(string)
			require.True(t, idOk, "session id must be a string")
			assert.Falsef(t, seen[id], "session %s must not be duplicated across pages", id)
			seen[id] = true
		}
	}
	assert.Len(t, seen, rootChildren, "concatenating all pages must reproduce exactly the %d direct children, no more, no less", rootChildren)

	// Expanding the DEEPEST child returns exactly the grandchild.
	childPage, code := u18DoListSessions(t, api, "parent_session_id="+lastChildID)
	require.Equal(t, http.StatusOK, code)
	require.Len(t, childPage.Sessions, 1)
	assert.Equal(t, grandchild.ID, childPage.Sessions[0]["id"])
}

// TestU18ListSessions_OrphanListedAsRoot is test #108
// (TestOrphanSession_ListedAsRoot, BDD-106): a session whose ParentSessionID
// names an id absent from the store must be surfaced as a ROOT row, never
// silently dropped — the R-7 shape (exists on disk, unreachable in the UI)
// with a different surface.
func TestU18ListSessions_OrphanListedAsRoot(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "shared session store must be available")

	orphan, err := store.NewSession(session.SessionTypeDelegate, "", "u18-agent")
	require.NoError(t, err)
	missingParent := "u18-vanished-parent-" + orphan.ID
	u18SetParent(t, store, orphan.ID, missingParent)

	page, code := u18DoListSessions(t, api, "limit=100")
	require.Equal(t, http.StatusOK, code)

	var found bool
	for _, s := range page.Sessions {
		if s["id"] == orphan.ID {
			found = true
		}
	}
	assert.True(t, found, "an orphan (unresolvable ParentSessionID) must be returned as a ROOT row, not dropped")
}

// TestU18ListSessions_FlatAndParentSessionID_Returns400 pins FR-104's
// mutual-exclusivity rule: flat=true and parent_session_id together is a
// 400, not a silent "one wins".
func TestU18ListSessions_FlatAndParentSessionID_Returns400(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)
	parent, err := store.NewSession(session.SessionTypeChat, "webchat", "u18-agent")
	require.NoError(t, err)

	_, code := u18DoListSessions(t, api, "flat=true&parent_session_id="+parent.ID)
	assert.Equal(t, http.StatusBadRequest, code, "flat=true combined with parent_session_id must 400 (FR-104)")
}

// TestU18ListSessions_InvalidLimitAndOffset_Return400 covers the REST-layer
// input validation this handler owns: a non-positive limit or a negative
// offset must 400 rather than silently falling back to a default.
func TestU18ListSessions_InvalidLimitAndOffset_Return400(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	_, code := u18DoListSessions(t, api, "limit=0")
	assert.Equal(t, http.StatusBadRequest, code, "limit=0 must 400")

	_, code = u18DoListSessions(t, api, "limit=-1")
	assert.Equal(t, http.StatusBadRequest, code, "a negative limit must 400")

	_, code = u18DoListSessions(t, api, "offset=-1")
	assert.Equal(t, http.StatusBadRequest, code, "a negative offset must 400")
}

// TestU18ListSessions_BoundaryCostIsBoundedByLimit is this unit's slice of
// test #100 (TestSessionListLayers_EachHonoursPaging, BDD-102) at the REST
// boundary: the response body carries AT MOST `limit` rows even when far
// more sessions exist — FR-092(b)'s "response body scales with limit, not
// with total session count".
func TestU18ListSessions_BoundaryCostIsBoundedByLimit(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)

	const total = 12
	for i := 0; i < total; i++ {
		_, err := store.NewSession(session.SessionTypeChat, "webchat", "u18-agent")
		require.NoError(t, err)
	}

	const limit = 3
	page, code := u18DoListSessions(t, api, "limit="+strconv.Itoa(limit))
	require.Equal(t, http.StatusOK, code)
	require.Len(t, page.Sessions, limit,
		"positive lower bound: the store holds %d sessions (> limit), so a correctly-bounded page must return exactly %d, not fewer (empty) and not %d (unbounded)", total, limit, total)
	require.NotNil(t, page.NextCursor, "a page short of the total must report a next_cursor")
}

// ---------------------------------------------------------------------
// W19a — drill-down surface for a REAL delegated child (BDD-71/BDD-37)
// ---------------------------------------------------------------------

// TestU18GetSession_DrillDownReachesRealDelegatedChildUnfiltered proves the
// drill-down surface (GET /api/v1/sessions/{childID}) resolves a REAL
// delegated child — a distinct session from its parent, per FR-005/FR-010 —
// and returns its own transcript in full. Distinct from
// rest_delegate_child_filter_test.go's inverted tests (which seed a single
// session with a legacy ParentSpawnCallID-tagged entry): this test exercises
// the POST-cutover shape directly, two real sessions, no shared transcript.
func TestU18GetSession_DrillDownReachesRealDelegatedChildUnfiltered(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)

	parent, err := store.NewSession(session.SessionTypeChat, "webchat", "u18-agent")
	require.NoError(t, err)
	child := u18NewChild(t, store, "u18-drilldown-child", parent.ID)

	require.NoError(t, store.AppendTranscript(child.ID, session.TranscriptEntry{
		ID: "child-entry-1", Role: "assistant", Content: "delegated work result",
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+child.ID, nil)
	r.URL.Path = "/api/v1/sessions/" + child.ID
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code, "GET /sessions/{childID} must resolve a real delegated child; body=%s", w.Body.String())

	var detail struct {
		Session  map[string]any            `json:"session"`
		Messages []session.TranscriptEntry `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	require.Len(t, detail.Messages, 1, "the child's own transcript must be returned in full, unfiltered")
	assert.Equal(t, "delegated work result", detail.Messages[0].Content)
	assert.Equal(t, child.ID, detail.Session["id"])
	if psid, ok := detail.Session["parent_session_id"]; ok {
		assert.Equal(t, parent.ID, psid, "the child's own session detail should surface its parent_session_id")
	}
}

// ---------------------------------------------------------------------
// W18b — uploads cascade wiring (FR-071/BDD-78), test #62 equivalent
// ---------------------------------------------------------------------

// u18NewRestAPIWithRealHome builds a restAPI exactly like newTestRestAPI
// (rest_test.go), EXCEPT it keeps OMNIPUS_HOME and the config's
// Agents.Defaults.Home consistent with each other — which newTestRestAPI
// does not, and no prior test needed it to, since this is the first test
// exercising an uploads-cascade delete through the REST layer.
//
// Root cause this works around, found while writing this test: production
// resolves AgentLoop.homePath (pkg/agent/loop.go:724-725,
// filepath.Dir(cfg.AgentHomeBasePath())) from cfg.Agents.Defaults.Home,
// which config.go:3616 sets to filepath.Join(OmnipusHomeDir(), "workspace")
// at boot — so in production, AgentLoop.homePath and OMNIPUS_HOME always
// agree. media.SessionUploadsDir/RemoveSessionUploadsTree instead read
// os.Getenv("OMNIPUS_HOME") directly. newTestRestAPI sets
// Agents.Defaults.Home to its OWN bare t.TempDir() (not
// "<home>/workspace"), so AgentLoop.homePath there resolves to that
// tempdir's PARENT — a path OMNIPUS_HOME (set independently by
// mustAgentLoop's ensureIsolatedTestOmnipusHome) never matches. Reproducing
// production's own "<OMNIPUS_HOME>/workspace" layout here makes the two
// agree, exactly as they do outside tests.
func u18NewRestAPIWithRealHome(t *testing.T) (*restAPI, func()) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	homeDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", homeDir)

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         filepath.Join(homeDir, "workspace"),
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	seedTestAgents(cfg)

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		homePath:      homeDir,
	}
	return api, func() {}
}

// u18SeedUploads creates <OMNIPUS_HOME>/uploads/<id> with a real, non-empty
// marker file and returns its path. Mirrors pkg/media/tempdir_adr057_test.go's
// mustSeedUploadsDir (U20) — duplicated here rather than imported because
// it is an unexported helper in a different package.
func u18SeedUploads(t *testing.T, id string) string {
	t.Helper()
	dir, ok := media.SessionUploadsDir(id)
	require.True(t, ok, "SessionUploadsDir(%q) rejected a supposedly-safe id", id)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "upload.bin"), []byte("payload"), 0o600))
	return dir
}

// u18PersistLifecycleChild persists a minimal, valid LifecycleRecord for
// childID as a DIRECT child of parentID — the real shape a delegated child
// mints (mirrors pkg/gateway/websocket_adr057_test.go's u11PersistLifecycleChild,
// duplicated here per Rule 6/file-per-unit rather than cross-file reuse of
// another unit's unexported test helper).
func u18PersistLifecycleChild(t *testing.T, ls *session.LifecycleStore, childID, parentID string) {
	t.Helper()
	rec := &session.LifecycleRecord{
		SessionID:        childID,
		State:            session.LifecycleRunning,
		OwnerScopeKind:   session.OwnerScopeParentSession,
		OwnerScopeID:     parentID,
		ParentDurableKey: parentID,
		ParentAgentID:    "u18-agent",
	}
	require.NoError(t, ls.Persist(rec), "fixture: persist lifecycle record %q (parent %q)", childID, parentID)
}

// TestU18DeleteSession_CascadesUploadsAcrossDescendants is test #62
// (TestUploadsCascadeDeleteAcrossDescendants, BDD-78): deleting a parent
// session must remove EVERY descendant's own uploads directory — depths 1
// AND 2 — not just the parent's own (which store.DeleteSession already
// cascaded pre-ADR-057, ADR-017 D5). Per binding rule 4, each descendant's
// upload marker is asserted present BEFORE the delete, never just "gone
// after" (which would pass vacuously against a directory that never
// existed).
func TestU18DeleteSession_CascadesUploadsAcrossDescendants(t *testing.T) {
	api, cleanup := u18NewRestAPIWithRealHome(t)
	defer cleanup()
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)

	ls := session.NewLifecycleStore(t.TempDir())
	api.agentLoop.SetSessionMessagingStores(nil, ls)

	parent, err := store.NewSession(session.SessionTypeChat, "webchat", "u18-agent")
	require.NoError(t, err)
	child := u18NewChild(t, store, "u18-cascade-child", parent.ID)
	grandchild := u18NewChild(t, store, "u18-cascade-grandchild", child.ID)
	u18PersistLifecycleChild(t, ls, child.ID, parent.ID)
	u18PersistLifecycleChild(t, ls, grandchild.ID, child.ID)

	parentUploads := u18SeedUploads(t, parent.ID)
	childUploads := u18SeedUploads(t, child.ID)
	grandchildUploads := u18SeedUploads(t, grandchild.ID)

	// Positive lower bound (binding rule 4): all three real, on-disk upload
	// trees exist BEFORE the delete.
	for _, dir := range []string{parentUploads, childUploads, grandchildUploads} {
		entries, statErr := os.ReadDir(dir)
		require.NoErrorf(t, statErr, "precondition: %s must exist before delete", dir)
		require.NotEmptyf(t, entries, "precondition: %s must be seeded with real content", dir)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/"+parent.ID, nil)
	r.URL.Path = "/api/v1/sessions/" + parent.ID
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code, "DELETE /sessions/{parentID} must succeed; body=%s", w.Body.String())

	for _, dir := range []string{parentUploads, childUploads, grandchildUploads} {
		_, statErr := os.Stat(dir)
		assert.Truef(t, os.IsNotExist(statErr), "%s must be removed after the parent session is deleted (FR-071)", dir)
	}
}

// TestU18DeleteSession_NoLifecycleStore_DegradesToOwnUploadsOnly covers the
// degenerate case: no delegation lifecycle store wired (most webchat-only
// installs never mint one). The cascade must degrade to zero descendants —
// deleting the session's OWN uploads (pre-existing ADR-017 behavior) must
// still succeed, and must not panic for want of a lifecycle store.
func TestU18DeleteSession_NoLifecycleStore_DegradesToOwnUploadsOnly(t *testing.T) {
	api, cleanup := u18NewRestAPIWithRealHome(t)
	defer cleanup()
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)
	require.Nil(t, api.agentLoop.GetSessionLifecycleStore(), "fixture: no lifecycle store wired in this test")

	solo, err := store.NewSession(session.SessionTypeChat, "webchat", "u18-agent")
	require.NoError(t, err)
	soloUploads := u18SeedUploads(t, solo.ID)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/"+solo.ID, nil)
	r.URL.Path = "/api/v1/sessions/" + solo.ID
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code, "DELETE must succeed even with no lifecycle store wired; body=%s", w.Body.String())

	_, statErr := os.Stat(soloUploads)
	assert.True(t, os.IsNotExist(statErr), "the session's own uploads dir must still be removed (pre-existing ADR-017 cascade)")
}
