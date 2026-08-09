// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpawnSubTurn_PersistsDelegatedTaskToChildTranscript_WhenParentUsesLegacyStore
// is the regression coverage for the "wrong store" defect in RC-5b's fix
// (see subturn.go's RC-5b comment block, ~line 1178): spawnSubTurn always
// mints the CHILD session into sharedStore := al.GetSessionStore() (a few
// lines above the RC-5b write), but before this fix the RC-5b write itself
// went through parentTS.transcriptStore instead — a field that is NOT
// always sharedStore. In particular, a task-executor-triggered run
// (processTaskDirect/processTaskDirectExternalCLI, loop.go ~5749/~5865)
// wires TranscriptStore straight from al.GetAgentStore(agentID) — the
// legacy PER-AGENT store, rooted at a different directory
// (workspace/sessions) than sharedStore ($OMNIPUS_HOME/sessions) — never
// through al.ResolveSessionStore/al.GetSessionStore() at all. Writing the
// RC-5b entry through that store handed UnifiedStore.AppendTranscriptStrict
// a childID it had never heard of (childID was only ever created in
// sharedStore), so the strict "refuse an unknown session" contract silently
// swallowed the write (WARN-logged, not propagated, per
// AppendTranscriptStrict's own doc comment) — the fix did nothing for
// exactly the deployments/dispatch paths most likely to hit it.
//
// This test wires parentTS.transcriptStore to a SEPARATE UnifiedStore
// instance (simulating that legacy per-agent store) that has never heard of
// childID, while sharedStore (al.GetSessionStore()) is what actually mints
// and owns the child session — reproducing the store-identity mismatch
// itself, the precise defect the fix closes. (The parent session is minted
// in sharedStore too, purely so CreateSessionWithID's own parent-owner
// resolution — which always reads through sharedStore regardless of this
// bug — succeeds; that resolution path is unrelated to the RC-5b transcript
// write under test here.) It must FAIL on the pre-fix code (which wrote
// through parentTS.transcriptStore and left the child's real transcript in
// sharedStore empty) and PASS once the write goes through sharedStore.
func TestSpawnSubTurn_PersistsDelegatedTaskToChildTranscript_WhenParentUsesLegacyStore(t *testing.T) {
	al, _, _, provider, cleanup := newTestAgentLoop(t)
	_ = provider
	defer cleanup()

	sharedStore := al.GetSessionStore()
	require.NotNil(t, sharedStore, "test harness did not wire a shared session store")

	// Simulate the legacy per-agent store: a wholly separate UnifiedStore
	// rooted at its own temp dir, which never learns about the child session
	// sharedStore is about to mint. This mirrors al.GetAgentStore(agentID)'s
	// distinct-directory shape (workspace/sessions vs. $OMNIPUS_HOME/sessions)
	// without depending on AgentLoop's internal per-agent store wiring.
	legacyStore, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err, "session.NewUnifiedStore (legacy store)")

	// Mint the parent in sharedStore (CreateSessionWithID always resolves the
	// parent's owner through sharedStore itself — see unified_api.go's
	// u2ReadParentOwner — so the parent must be visible there for the child
	// create below to succeed at all; that's orthogonal to the RC-5b bug).
	// parentTS.transcriptStore is deliberately pointed at legacyStore instead,
	// reproducing the exact "store that resolved the parent's transcript
	// context is not sharedStore" condition the fix must not depend on.
	parentMeta, err := sharedStore.NewSession(session.SessionTypeChat, "test-channel", "main")
	require.NoError(t, err, "sharedStore.NewSession (parent)")
	parentSessionID := parentMeta.ID

	parentTS := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-T0-rc5b-legacy",
		depth:               0,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 4),
		session:             &ephemeralSessionStore{},
		agent:               al.registry.GetDefaultAgent(),
		transcriptSessionID: parentSessionID,
		routingSessionID:    session.RoutingSessionID(parentSessionID),
		transcriptStore:     legacyStore,
	}

	spawnCtx := withSpawnToolCallID(context.Background(), "test-spawn-call-rc5b-legacy")

	const taskText = "Investigate the checkout failure in module Y and report findings."
	expectedChildID := "child-rc5b-legacy-" + parentSessionID
	cfg := SubTurnConfig{
		Model:             "gpt-4o-mini",
		Tools:             []tools.Tool{},
		SystemPrompt:      taskText,
		DelegateSessionID: expectedChildID,
	}

	result, err := spawnSubTurn(spawnCtx, al, parentTS, cfg)
	require.NoError(t, err)
	require.NotNil(t, result, "expected the child's turn to actually run and return a result")

	// The child session was minted into sharedStore (al.GetSessionStore()),
	// not legacyStore — confirm that's where CreateSessionWithID put it.
	_, metaErr := sharedStore.GetMeta(expectedChildID)
	require.NoError(t, metaErr, "child session must have been minted into sharedStore, not legacyStore")

	childEntries, err := sharedStore.ReadTranscript(expectedChildID)
	require.NoError(t, err)
	require.NotEmpty(t, childEntries,
		"the child's own transcript file (in sharedStore, the store that actually owns childID) "+
			"must contain entries — writing through parentTS.transcriptStore (the legacy store) "+
			"would silently drop this entry")

	var found bool
	var gotEntries []session.TranscriptEntry
	for _, e := range childEntries {
		gotEntries = append(gotEntries, e)
		if e.Role == "user" && e.Content == taskText {
			found = true
		}
	}
	assert.True(t, found,
		"child's durable transcript in sharedStore must contain a role:\"user\" entry carrying "+
			"the delegated task text %q — got entries: %+v", taskText, gotEntries)
}
