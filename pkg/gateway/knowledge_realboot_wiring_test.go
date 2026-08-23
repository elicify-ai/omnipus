// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// knowledge_realboot_wiring_test.go — the ADR-067 W3 lifecycle observed
// through the REAL boot function and the REAL mount handlers
// (FR-030, FR-031, FR-038a, FR-039, FR-080).
//
// # What this adds that knowledge_boot_wiring_test.go cannot
//
// Its sibling calls startKnowledgeLifecycle itself and then reads gateway.go's
// source to check the call exists and is unconditional. That is a good guard
// and it is still a source-text claim: it cannot see the call being made into
// an object boot discards afterwards, which is exactly what happened one lane
// over (registerKnowledgeBuiltinMetadata wrote into a BuiltinRegistry gateway.go
// replaced 220 lines later, and the AST guard stayed green).
//
// These tests run setupAndStartServices — gateway.go's actual boot function —
// against a temp $OMNIPUS_HOME, then drive the actual REST mount handlers and
// the actual shutdown function. Nothing here asserts about source text.
//
// # The gap they close
//
// Every test in knowledge_lifecycle_test.go constructs KnowledgeLifecycle
// itself, so all sixteen of its internal mutations stayed green with all four
// production call sites deleted (measured RUN=311 PASS=311 FAIL=0). Under that
// mutation mounting a knowledge base indexes nothing, boot reopens nothing,
// shutdown leaks every index handle and drift goroutine, and unmounting
// releases nothing. The only test reference to AttachMountAsync was inside an
// assert.NotPanics proving nil-safety — it never asserted a HANDLER calls it.
//
// Cost: about one second of real boot per test.

package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// --- harness ---------------------------------------------------------------

// krbBoot runs the REAL boot function against home. It returns the restAPI
// boot built and a once-guarded shutdown that uses the REAL shutdown function.
//
// stopAndCleanupServices is NOT idempotent — a second call panics inside
// channels.Manager.StopAll ("close of closed channel") — so the Once makes the
// returned stop and the t.Cleanup the same single shutdown. A test may
// therefore shut the gateway down mid-test and still be torn down safely.
func krbBoot(t *testing.T, home string) (*restAPI, func()) {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	cfg := seededBootConfig(t)
	cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
	cfg.Gateway.Host = "127.0.0.1"
	// Port 0 lets the kernel pick a free port, so two of these tests running
	// back to back cannot collide on a fixed one.
	cfg.Gateway.Port = 0

	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})

	credStore := credentials.NewStore(filepath.Join(home, "credentials.json"))
	// A deterministic 256-bit key. The store must be UNLOCKED before boot:
	// the intent-log HMAC chain key is derived from it and boot aborts otherwise.
	require.NoError(t, credStore.UnlockWithKey(bytes.Repeat([]byte{0x2b}, 32)))

	rs, err := setupAndStartServices(
		cfg, credentials.SecretBundle{}, al, bus.NewMessageBus(), home,
		credStore, &SandboxApplyResult{}, tools.NewBuiltinRegistry(), tools.NewMCPRegistry(), false,
	)
	require.NoError(t, err, "boot must succeed for this test to be able to say anything")
	require.NotNil(t, rs.restAPIRef, "boot must have constructed a restAPI")

	var once sync.Once
	stop := func() { once.Do(func() { stopAndCleanupServices(rs, 5*time.Second, false) }) }
	t.Cleanup(stop)
	return rs.restAPIRef, stop
}

// krbLifecycleFor reads the process registry the way restAPI.knowledgeLifecycle
// does. This is the object boot is supposed to have published.
func krbLifecycleFor(home string) *KnowledgeLifecycle {
	knowledgeLifecycles.mu.Lock()
	defer knowledgeLifecycles.mu.Unlock()
	return knowledgeLifecycles.byHome[home]
}

// krbEventually polls until cond holds or the deadline passes. Indexing is
// asynchronous by design — a first index of a large collection must not hold
// the mount-create response open — so a wiring test has to wait for it rather
// than assume it has finished.
func krbEventually(t *testing.T, why string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", why)
}

// --- 1. real boot publishes the lifecycle and reopens recorded mounts ------

// TestRealBoot_PublishesKnowledgeLifecycleAndReopensRecordedMounts is FR-039's
// wiring seen from outside: an index that already exists is reopened at boot,
// and the object owning it is published where every mount handler looks.
//
// DIES ON: deleting startKnowledgeLifecycle(homePath, wsHandler, 0) from
// setupAndStartServices. Unlike the AST guard, it ALSO dies if the call is
// moved somewhere that never runs, or if its result is registered into
// something boot then replaces.
func TestRealBoot_PublishesKnowledgeLifecycleAndReopensRecordedMounts(t *testing.T) {
	home := kltHome(t)
	vault := kltVault(t, map[string]string{
		"notes/brachiosaurus.md": "# Brachiosaurus\n\nA long-necked sauropod.\n",
	})

	// A workspace with a knowledge-base mount, recorded BEFORE boot, through
	// the real mount store rather than hand-marshalled JSON — a fabricated
	// record cannot notice the persisted format changing underneath it.
	wsID := krbSeedWorkspace(t, home)
	_, _, err := workspace.CreateMount(home, wsID, "vault", vault)
	require.NoError(t, err)

	_, _ = krbBoot(t, home)

	kl := krbLifecycleFor(home)
	require.NotNilf(t, kl,
		"boot published no knowledge lifecycle for %s. Every mount handler reaches the "+
			"lifecycle through this registry, so nil here means creating a mount indexes "+
			"nothing, deleting one releases nothing, and no drift schedule ever starts "+
			"(ADR-067 FR-030/FR-038a/FR-039)", home)

	kl.WaitForAttaches()
	assert.Containsf(t, kl.AttachedRoots(), vault,
		"the mount recorded before boot was not reopened. FR-039 requires an existing "+
			"index to be reopened at restart rather than rebuilt, and AttachAllMounts is "+
			"the only thing that does it")
	assert.Equalf(t, 1, kl.HoldersFor(vault),
		"one mount means exactly one holder (FR-031)")
}

// TestRealBoot_ShutdownReleasesEveryKnowledgeCollection is the other half.
//
// DIES ON: deleting stopKnowledgeLifecycles() from stopAndCleanupServices.
// Under that mutation every drift goroutine and every open index handle
// outlives the gateway that opened them, and the next boot for the same home
// races them.
func TestRealBoot_ShutdownReleasesEveryKnowledgeCollection(t *testing.T) {
	home := kltHome(t)
	vault := kltVault(t, map[string]string{"notes/n.md": "# N\n"})
	wsID := krbSeedWorkspace(t, home)
	_, _, err := workspace.CreateMount(home, wsID, "vault", vault)
	require.NoError(t, err)

	_, stopGateway := krbBoot(t, home)
	kl := krbLifecycleFor(home)
	require.NotNil(t, kl, "precondition: boot published a lifecycle")
	kl.WaitForAttaches()
	require.Contains(t, kl.AttachedRoots(), vault, "precondition: boot attached the collection")

	stopGateway()

	assert.Nilf(t, krbLifecycleFor(home),
		"shutdown left a knowledge lifecycle registered for %s", home)
	assert.Emptyf(t, kl.AttachedRoots(),
		"shutdown left collections attached — every index handle and drift goroutine "+
			"they hold is leaked")
}

// --- 2. the mount HANDLERS attach and release ------------------------------

// TestKnowledgeMountHandlers_AttachOnCreateAndReleaseOnDelete drives the REAL
// REST handlers rather than the lifecycle methods.
//
// DIES ON, independently:
//   - deleting a.knowledgeLifecycle().AttachMountAsync(...) from
//     handleWorkspaceMountCreate — mounting a knowledge base then indexes
//     nothing, and every search over it returns a silence that reads as "you
//     have no notes about that";
//   - deleting the a.knowledgeLifecycle().RevokeMount(...) block from
//     handleWorkspaceMountDelete — unmounting then releases nothing, so the
//     index handle and the drift schedule survive a revoked grant.
func TestKnowledgeMountHandlers_AttachOnCreateAndReleaseOnDelete(t *testing.T) {
	home := kltHome(t)
	vault := kltVault(t, map[string]string{
		"notes/stegosaurus.md": "# Stegosaurus\n\nPlated, and slow.\n",
	})
	wsID := krbSeedWorkspace(t, home)

	api, _ := krbBoot(t, home)
	kl := krbLifecycleFor(home)
	require.NotNil(t, kl, "precondition: boot published a lifecycle")
	require.NotContains(t, kl.AttachedRoots(), vault,
		"precondition: nothing has mounted this vault yet")

	// --- create, through the handler -------------------------------------
	body, err := json.Marshal(gen.WorkspaceMountCreateRequest{Name: "vault", HostPath: vault})
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/workspaces/"+wsID+"/mounts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	api.HandleWorkspaceMounts(rec, req)
	require.Equalf(t, http.StatusCreated, rec.Code, "mount create failed: %s", rec.Body.String())

	krbEventually(t, "the created mount to be attached", func() bool {
		return kl.HoldersFor(vault) == 1
	})

	// The index is real, and it lives OUTSIDE the operator's collection
	// (FR-030) — asserted because a handler that wrote an index INTO the vault
	// would satisfy the holder count above just as well.
	indexDir, err := knowledge.IndexDirFor(home, vault)
	require.NoError(t, err)
	_, statErr := os.Stat(indexDir)
	assert.NoErrorf(t, statErr,
		"no index directory at %s: the mount handler attached nothing", indexDir)
	assert.Truef(t, strings.HasPrefix(indexDir, home),
		"the index must live under $OMNIPUS_HOME (FR-030): %s is not under %s", indexDir, home)
	assert.NotContainsf(t, kltTree(t, vault), filepath.Base(indexDir),
		"the index must never be written inside the operator's collection (FR-030)")

	// --- delete, through the handler -------------------------------------
	delRec := httptest.NewRecorder()
	delReq := httptest.NewRequest(http.MethodDelete,
		"/api/v1/workspaces/"+wsID+"/mounts/vault", nil)
	api.HandleWorkspaceMounts(delRec, delReq)
	require.Truef(t, delRec.Code == http.StatusNoContent || delRec.Code == http.StatusOK,
		"mount delete returned %d: %s", delRec.Code, delRec.Body.String())

	assert.Equalf(t, 0, kl.HoldersFor(vault),
		"the deleted mount still holds its collection open. The grant is revoked while "+
			"the index handle and the drift schedule keep running (FR-031, US-16 AS-2)")
	assert.NotContains(t, kl.AttachedRoots(), vault,
		"the last holder's release must close the collection")
}

// krbSeedWorkspace writes a workspace record straight to disk, so it exists
// before boot runs and AttachAllMounts can find its mount file.
func krbSeedWorkspace(t *testing.T, home string) string {
	t.Helper()
	id := ulidLikeID(t)
	ws := storedWorkspace{
		ID:        id,
		Name:      "Knowledge Wiring WS",
		Status:    string(gen.WorkspaceStatusActive),
		CoreTeam:  []string{"mia"},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	require.NoError(t, writeWorkspaceFile(home, ws))
	return id
}

// --- 3. FR-080 delivery: the frame actually reaches connected clients ------

// TestBroadcastKnowledgeIndexProgress_ReachesEveryConnectedClient covers the
// last link in the FR-080 chain, which had ZERO test references of any kind.
//
// The chain is: reconcile → emit → WSHandler.broadcastKnowledgeIndexProgress →
// every wsConn.sendCh → the browser → src/store/chat.ts's
// knowledge_index_progress case → useKnowledgeIndexStore → KnowledgePanel.
// Every other link is now covered; this one was not, so a broadcast that
// marshalled nothing, sent to nobody, or sent to only the first connection
// would have been invisible from Go.
//
// The oracle is the CONTRACT, not the struct: the bytes on the wire must carry
// `"type":"knowledge_index_progress"` (the discriminator asyncapi.yaml declares
// as a const) and the collection id, because that discriminator is what the
// SPA's generated Zod union switches on. Asserting on the Go struct instead
// would pass even if the JSON went out under the wrong name.
func TestBroadcastKnowledgeIndexProgress_ReachesEveryConnectedClient(t *testing.T) {
	h := &WSHandler{sessions: map[string]*wsConn{}}
	a := &wsConn{sendCh: make(chan []byte, 4)}
	b := &wsConn{sendCh: make(chan []byte, 4)}
	h.sessions["chat-a"] = a
	h.sessions["chat-b"] = b

	frame := newKnowledgeIndexProgressFrame("kb_abc", "ws_1", knowledgeIndexUpdate{
		Phase:      knowledgeIndexPhaseIndexing,
		Indexed:    12,
		Total:      40,
		TotalKnown: true,
	}, time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC))

	h.broadcastKnowledgeIndexProgress(frame)

	for name, wc := range map[string]*wsConn{"chat-a": a, "chat-b": b} {
		select {
		case raw := <-wc.sendCh:
			var got map[string]any
			require.NoErrorf(t, json.Unmarshal(raw, &got), "%s: %s", name, raw)
			assert.Equalf(t, "knowledge_index_progress", got["type"],
				"%s: the discriminator on the wire is what the SPA's generated Zod union "+
					"switches on; any other value is dropped before it reaches a store", name)
			assert.Equalf(t, "kb_abc", got["collection_id"],
				"%s: the frame must name the collection it is about — the SPA keys its "+
					"index store on exactly this field", name)
			assert.EqualValuesf(t, 12, got["indexed_files"], "%s", name)
		default:
			t.Fatalf("%s received no frame: every connected client must get one (FR-080)", name)
		}
	}
}

// TestBroadcastKnowledgeIndexProgress_IsNilSafeAndCountsDrops pins the two
// degraded paths, because the honest behaviour under backpressure is what
// keeps a client from being stuck showing a bar that stopped moving.
func TestBroadcastKnowledgeIndexProgress_IsNilSafeAndCountsDrops(t *testing.T) {
	frame := newKnowledgeIndexProgressFrame("kb_x", "ws_1",
		knowledgeIndexUpdate{Phase: knowledgeIndexPhaseIdle}, time.Now())

	// A gateway with no WebSocket handler still indexes; broadcasting must not
	// panic. startKnowledgeLifecycle passes a nil *WSHandler in exactly that case.
	assert.NotPanics(t, func() { (*WSHandler)(nil).broadcastKnowledgeIndexProgress(frame) })

	// A full send buffer drops the frame and COUNTS it. Dropping silently is
	// what makes a stalled progress bar indistinguishable from a finished one.
	full := &wsConn{sendCh: make(chan []byte)} // unbuffered, nobody reading
	h := &WSHandler{sessions: map[string]*wsConn{"chat": full}}
	h.broadcastKnowledgeIndexProgress(frame)
	assert.EqualValues(t, 1, full.droppedFrames.Load(),
		"a dropped progress frame must be counted, or backpressure is invisible")
}
