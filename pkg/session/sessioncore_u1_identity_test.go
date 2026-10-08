// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, session-core U1 (main session identity), pkg/session slice.
//
// Spec: docs/internal/specs/session-core-spec.md (FR-002, FR-003; DEL-01,
// DEL-07, DEL-11; C-MAIN; BDD-01.1, BDD-12.3). ADR D1:
// docs/internal/architecture/ADR-20261006-session-core-with-an-agent-address-book.md.
// Every expected literal below is taken from the spec, never read off the
// implementation. Tests that drive only EXISTING symbols live here; tests that
// need a symbol the spec does not name live in
// sessioncore_u1_newsymbol_test.go so a rename touches one file.

package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BDD-12.3 / C-MAIN: "Session.type = main" is a server-minted session type.
// Instrument check is the control rows: chat stays valid, bogus stays invalid.
func TestSessionCoreU1_MainIsAValidServerMintedSessionType(t *testing.T) {
	assert.True(t, IsValidSessionType(UnifiedSessionType("main")),
		"C-MAIN: 'main' must be a valid session type (spec Contract Changes, C-MAIN)")
	// Positive controls (pass at baseline): the validator can both accept and reject.
	assert.True(t, IsValidSessionType(SessionTypeChat), "control: chat stays valid")
	assert.False(t, IsValidSessionType(UnifiedSessionType("bogus")), "control: unknown type stays invalid")
}

// DEL-11 (active_agent_id part) / C-MAIN "delete Session.active_agent_id":
// a freshly created session must not persist a mutable handover owner.
func TestSessionCoreU1_NewSessionPersistsNoActiveAgentID(t *testing.T) {
	store := newTestStore(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)

	raw, err := os.ReadFile(filepath.Join(store.BaseDir(), meta.ID, "meta.json"))
	require.NoError(t, err)
	var onDisk map[string]any
	require.NoError(t, json.Unmarshal(raw, &onDisk))

	// Control (passes at baseline): the immutable owner is persisted.
	assert.Equal(t, "mia", onDisk["agent_id"], "control: owner agent_id is persisted")
	_, has := onDisk["active_agent_id"]
	assert.False(t, has, "DEL-11/C-MAIN: meta.json must not carry active_agent_id; got %s", string(raw))
}

// DEL-11: the reader must not backfill a handover owner (SessionMeta.PostLoad).
// Observed through the marshalled form of what GetMeta returns, so the test
// compiles both before and after the field is deleted.
func TestSessionCoreU1_GetMetaDoesNotBackfillActiveAgentID(t *testing.T) {
	store := newTestStore(t)
	created, err := store.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)

	got, err := store.GetMeta(created.ID)
	require.NoError(t, err)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))

	assert.Equal(t, "mia", m["agent_id"], "control: owner survives the read")
	_, has := m["active_agent_id"]
	assert.False(t, has, "DEL-11: reader must not synthesise active_agent_id; got %s", string(b))
}

// DEL-07 / DEL-01 (source-proof class "K"): the handover method and the
// heartbeat-only identity constructor are deleted from UnifiedStore. Observed
// by reflection so the test compiles before and after deletion.
func TestSessionCoreU1_RetiredStoreMethodsAreGone(t *testing.T) {
	typ := reflect.TypeOf(&UnifiedStore{})

	// Instrument check (passes at baseline): reflection can see a method that must stay.
	_, ok := typ.MethodByName("NewSession")
	require.True(t, ok, "instrument check: reflection must see NewSession")

	for _, name := range []string{
		"SwitchAgent",         // DEL-07: pkg/session/unified_write.go::SwitchAgent
		"NewHeartbeatSession", // DEL-01: heartbeat-only identity matured into the computed main
	} {
		t.Run(name, func(t *testing.T) {
			_, present := typ.MethodByName(name)
			assert.False(t, present, "%s must be deleted (spec DEL-07 / DEL-01)", name)
		})
	}
}
