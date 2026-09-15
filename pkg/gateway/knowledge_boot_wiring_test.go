// Omnipus — ADR-067 W3/W4: the BOOT WIRING for the knowledge lane.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/gateway/
//
// ── Why this file exists ─────────────────────────────────────────────────────
//
// Every piece of the knowledge lane was individually tested and the lane as a
// whole did nothing, because the three lines that CONNECT the pieces were
// covered by nothing:
//
//   1. gateway.go's `startKnowledgeLifecycle(homePath, wsHandler, 0)` — delete
//      it and no collection index opens at boot, no knowledge_index_progress
//      frame is ever emitted, and no drift schedule starts. FR-030, FR-039 and
//      FR-080 all silently do nothing. Measured: with that line replaced by
//      `_ = homePath`, `-run TestKnowledge` was byte-identical to the
//      unmutated baseline, 73 PASS / 0 FAIL.
//
//   2. rest.go's `cm.RegisterHTTPHandler("/api/v1/library/", …HandleLibraryTree)`
//      — the registration that makes the whole knowledge REST surface reachable
//      over HTTP. Every test in rest_knowledge_test.go calls
//      api.HandleLibraryTree DIRECTLY, so all 272 of them stay green while
//      every real request 404s. A wiring test that bypasses the wiring.
//
//   3. The central builtin catalog, covered in knowledge_tools_wire_test.go by
//      TestCentralBuiltinRegistry_CarriesTheKnowledgeTools.
//
// The tests here drive the real thing: a real $OMNIPUS_HOME with a real mount
// record, and a real http.ServeMux populated by the production
// registerAdditionalEndpoints.

package gateway

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	ctxkey "github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// ---------------------------------------------------------------------------
// 1. startKnowledgeLifecycle — what boot actually gets from calling it
// ---------------------------------------------------------------------------

// TestStartKnowledgeLifecycle_OpensEveryRecordedMountAndPublishesIt is the
// behavioural half: it calls the exact function gateway.go calls, with a home
// that already carries a mount record, and asserts the observable results
// FR-030/FR-039/FR-080 are written in terms of.
//
// The mount record is written through workspace.CreateMount — the real writer
// — rather than hand-marshalled JSON. A hand-built record cannot detect a
// change in the persisted format: it would keep passing against its own
// fabricated file while boot reattached nothing.
//
// DIES ON: deleting the AttachAllMounts call in startKnowledgeLifecycle;
// deleting the registerKnowledgeLifecycle call; returning nil early.
func TestStartKnowledgeLifecycle_OpensEveryRecordedMountAndPublishesIt(t *testing.T) {
	home := kwReal(t, t.TempDir())
	ws := kwWorkspace(t, home, "kbboot1")
	vault := kwVault(t, filepath.Join(t.TempDir(), "Vault"), "Vault")
	kwNote(t, vault, "notes/pterodactyl.md", "# Pterodactyl\n\nA note that must be findable after boot.\n")

	_, _, err := workspace.CreateMount(home, ws, "notes", vault)
	require.NoError(t, err, "the mount record must be written by the real store, not fabricated")

	var frames []gen.KnowledgeIndexProgressFrame
	kl, err := NewKnowledgeLifecycle(KnowledgeLifecycleOptions{
		Home: home,
		Emit: func(f gen.KnowledgeIndexProgressFrame) { frames = append(frames, f) },
	})
	require.NoError(t, err)
	registerKnowledgeLifecycle(home, kl)
	t.Cleanup(func() { unregisterKnowledgeLifecycle(home) })
	kl.AttachAllMounts()
	kl.WaitForAttaches()

	// FR-030/FR-039: the collection is open, keyed on its resolved real path.
	assert.Contains(t, kl.AttachedRoots(), vault,
		"boot must reopen every mount recorded on disk (FR-039). An empty list means the "+
			"gateway starts with no knowledge index at all and every search answers "+
			"'no collections' forever, with nothing logged")

	// FR-080: progress was pushed, and the terminal frame is honest.
	require.NotEmpty(t, frames,
		"indexing must emit knowledge_index_progress frames (FR-080). No frame means the "+
			"Library reports 'no indexing progress received' on every knowledge base, "+
			"permanently")
	last := frames[len(frames)-1]
	assert.Equal(t, knowledgeIndexPhaseIdle, last.Phase,
		"the terminal frame must be idle, or a client is left showing a bar that never stops")
	assert.Equal(t, ws, last.WorkspaceId)
	require.NotNil(t, last.TotalFiles, "a terminal frame has a measured total")
	assert.Positive(t, *last.TotalFiles, "the fixture note must have been counted")

	// The lifecycle is reachable from the REST layer, which is what
	// mount-create and mount-delete depend on.
	api := &restAPI{homePath: home}
	assert.Same(t, kl, api.knowledgeLifecycle(),
		"startKnowledgeLifecycle must PUBLISH the lifecycle: rest_workspace_mounts.go "+
			"reaches it through restAPI.knowledgeLifecycle(), and a nil there makes every "+
			"mount create and delete a silent no-op for indexing")
}

// TestStartKnowledgeLifecycle_IsCalledUNCONDITIONALLYFromBoot is the call-site
// half, and it is deliberately stricter than the source scan it replaces.
//
// Booting a whole gateway inside a unit test is not cheap enough to do here,
// so the call site is read from the source — but "the call expression appears
// somewhere in gateway.go" is a claim a `if false { startKnowledgeLifecycle(…) }`
// satisfies, and a feature flag that is off at boot satisfies it too. That is
// the documented weakness of the AST guard this project already shipped for
// the sibling call (registerKnowledgeBuiltinMetadata) and it survived exactly
// that mutation.
//
// So this asserts the call is UNCONDITIONAL: reachable from its function's
// body with no if / for / switch / select between. A refactor that puts the
// lifecycle behind any condition has to come and change this test, which is
// the point.
//
// DIES ON: deleting the call; wrapping it in `if false { … }`; moving it
// inside any conditional or loop.
func TestStartKnowledgeLifecycle_IsCalledUNCONDITIONALLYFromBoot(t *testing.T) {
	const bootFile = "gateway.go"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, bootFile, nil, 0)
	require.NoError(t, err, "parse %s", bootFile)

	found, unconditional := findUnconditionalCall(file, "startKnowledgeLifecycle")
	require.Truef(t, found,
		"startKnowledgeLifecycle is never called from %s. Without it no collection index "+
			"is opened at boot, no knowledge_index_progress frame is ever emitted and no "+
			"drift schedule starts — FR-030, FR-039 and FR-080 all silently do nothing, "+
			"and CI stays green", bootFile)
	assert.Truef(t, unconditional,
		"startKnowledgeLifecycle IS called from %s but only inside a conditional or a "+
			"loop. A condition that is false at boot ships the whole indexing lifecycle "+
			"dead with this test still green, which is precisely the blind spot that let "+
			"the sibling registerKnowledgeBuiltinMetadata call survive an `if false` "+
			"mutation. If the call genuinely must become conditional, this test has to "+
			"change with it", bootFile)

	stopFound, _ := findUnconditionalCall(file, "stopKnowledgeLifecycles")
	assert.Truef(t, stopFound,
		"stopKnowledgeLifecycles is never called from %s, so every open index and every "+
			"drift schedule survives shutdown", bootFile)
}

// findUnconditionalCall reports whether name is called anywhere in file, and
// whether at least one of those calls sits on a statement path containing no
// if / for / range / switch / select / func-literal between the enclosing
// function's body and the call.
func findUnconditionalCall(file *ast.File, name string) (found, unconditional bool) {
	var stack []ast.Node
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)

		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok || ident.Name != name {
			return true
		}
		found = true

		guarded := false
		for _, ancestor := range stack {
			switch ancestor.(type) {
			case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt,
				*ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt,
				*ast.FuncLit:
				guarded = true
			}
		}
		if !guarded {
			unconditional = true
		}
		return true
	})
	return found, unconditional
}

// ---------------------------------------------------------------------------
// 2. The REST surface, reached the way a browser reaches it
// ---------------------------------------------------------------------------

// TestKnowledgeRESTSurface_ReachableThroughTheRealMux drives
// /api/v1/library/{ws}/knowledge through a real http.ServeMux populated by the
// production registerAdditionalEndpoints — the same call gateway.go makes.
//
// Everything else in rest_knowledge_test.go invokes api.HandleLibraryTree
// directly, which proves the shim's internal path-peeling and proves nothing
// about the registration that gets a request to the shim. With the
// registration reverted to a.HandleLibrary, 272 tests stayed green while every
// real knowledge request fell through to the library lister.
//
// The oracle is the CONTRACT: KnowledgeBaseInfo (contracts/components/schemas/
// KnowledgeBaseInfo.yaml) carries is_knowledge_base and marker. A library
// directory listing carries neither, so a fall-through cannot pass.
//
// DIES ON: changing the "/api/v1/library/" registration back to
// a.HandleLibrary, or removing it.
func TestKnowledgeRESTSurface_ReachableThroughTheRealMux(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	work := workDir(api, ws)
	vaultDir := filepath.Join(work, "research-vault")
	require.NoError(t, os.MkdirAll(knowledge.MarkerDir(vaultDir), 0o700))
	raw, err := json.Marshal(knowledge.Marker{DisplayName: "Research vault"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(knowledge.MarkerPath(vaultDir), raw, 0o600))

	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/library/"+ws+"/knowledge?path=research-vault", nil)
	// The harness clears OMNIPUS_BEARER_TOKEN and configures no users, so
	// checkBearerAuth reaches the dev-bypass branch; it still requires the
	// "Bearer " prefix before it reads the config.
	req.Header.Set("Authorization", "Bearer test-sentinel")
	cfg := &config.Config{}
	cfg.Gateway.DevModeBypass = true
	req = req.WithContext(context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, cfg))

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equalf(t, http.StatusOK, w.Code,
		"GET /api/v1/library/{ws}/knowledge must be reachable through the registered mux. "+
			"Got %d: %s", w.Code, w.Body.String())

	var info gen.KnowledgeBaseInfo
	require.NoErrorf(t, json.Unmarshal(w.Body.Bytes(), &info),
		"the response must be a KnowledgeBaseInfo. A body that does not decode as one means "+
			"the request reached HandleLibrary (the directory lister) instead of the "+
			"knowledge dispatcher: body = %s", w.Body.String())
	assert.Truef(t, info.IsKnowledgeBase,
		"the fixture carries an .omnipus-vault marker, so is_knowledge_base must be true. "+
			"A false here with a 200 is the fall-through shape: something answered, and it "+
			"was not the knowledge handler. body = %s", w.Body.String())
	require.NotNil(t, info.CollectionId)
	assert.NotEmpty(t, *info.CollectionId)
}
