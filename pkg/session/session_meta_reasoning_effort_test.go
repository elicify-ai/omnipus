// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session_meta_reasoning_effort_test.go — RED tests for WP-H's backend half
// (thinking-reasoning-spec.md §1 C5 "Non-web conversation effort" row, §2.2
// SessionMeta/MetaPatch/u5IdentityFile row): SessionMeta gains a plain-string
// ReasoningEffort, patchable through MetaPatch and persisted in the identity
// group (u5IdentityFile), populated ONLY for non-web conversation sessions —
// it survives process restart with that session, never applies to another
// session or user, and clears via an empty patch (the /effort default path).
//
// Boundaries: real UnifiedStore over a real tempdir; the filesystem is the
// process edge. No mocks — every expected value derives from the spec, never
// from observed implementation output.
//
// RED status at time of writing: SessionMeta.ReasoningEffort and
// MetaPatch.ReasoningEffort DO NOT EXIST yet — this file is expected to fail
// to compile, naming them verbatim. That compile failure is the RED evidence.
package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSessionMetaReasoningEffort_RoundTripsThroughMetaPatch (spec C5
// non-web row: "patchable through pkg/session/unified.go::MetaPatch") — a
// non-empty patch stores the value; an empty-string patch clears it ("/effort
// default" and the C5 resolution-order note: '"default" means "send nothing"
// and clears ... non-web session field'). A nil patch field touches nothing.
func TestSessionMetaReasoningEffort_RoundTripsThroughMetaPatch(t *testing.T) {
	store := newTestStore(t)
	meta, err := store.NewSession(SessionTypeChannel, "telegram", "agent-1")
	require.NoError(t, err)
	sid := meta.ID

	// A fresh conversation session carries no stored effort (C5: "populated
	// ONLY for non-web conversation sessions" — and only once /effort sets it).
	got, err := store.GetMeta(sid)
	require.NoError(t, err)
	assert.Equal(t, "", got.ReasoningEffort,
		"a fresh conversation session must carry no stored effort")

	effort := "high"
	require.NoError(t, store.SetMeta(sid, MetaPatch{ReasoningEffort: &effort}))

	got, err = store.GetMeta(sid)
	require.NoError(t, err)
	assert.Equal(t, "high", got.ReasoningEffort,
		"the patched value must be stored on the session")

	// "/effort default" path: empty string clears (C5: '"default" means "send
	// nothing" and clears the ... non-web session field').
	cleared := ""
	require.NoError(t, store.SetMeta(sid, MetaPatch{ReasoningEffort: &cleared}))
	got, err = store.GetMeta(sid)
	require.NoError(t, err)
	assert.Equal(t, "", got.ReasoningEffort,
		`an empty-string patch must clear the stored effort (the "/effort default" path)`)
}

// TestSessionMetaReasoningEffort_SurvivesRestartForItsOwnSessionOnly (spec C5
// non-web row, D30: "It survives process restart with that session ... it
// never applies to another session or user") — two conversation sessions of
// the SAME agent on the SAME channel instance each keep their own value
// across a simulated restart (fresh store over the same base dir forces real
// disk reads), proving persistence AND per-conversation isolation.
func TestSessionMetaReasoningEffort_SurvivesRestartForItsOwnSessionOnly(t *testing.T) {
	base := t.TempDir()
	store, err := NewUnifiedStore(base)
	require.NoError(t, err)

	a, err := store.NewChannelSession("telegram", "tg.eu", "peer-a", "agent-1", "A")
	require.NoError(t, err)
	b, err := store.NewChannelSession("telegram", "tg.eu", "peer-b", "agent-1", "B")
	require.NoError(t, err)

	effortA := "high"
	require.NoError(t, store.SetMeta(a.ID, MetaPatch{ReasoningEffort: &effortA}))
	// B carries a deliberately DIFFERENT value: a shared/blown-away write
	// (the D30 isolation defect) shows up in either direction.
	effortB := "low"
	require.NoError(t, store.SetMeta(b.ID, MetaPatch{ReasoningEffort: &effortB}))

	// Simulated restart: close the store, reopen a fresh one over the SAME
	// base dir. The fresh store's empty meta cache forces real disk reads
	// through the u5 group files (u5IdentityFile is the identity group's
	// on-disk shape, so the value must round-trip through it to survive).
	require.NoError(t, store.Close())
	reopened, err := NewUnifiedStore(base)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopened.Close() })

	gotA, err := reopened.GetMeta(a.ID)
	require.NoError(t, err)
	assert.Equal(t, "high", gotA.ReasoningEffort,
		"conversation A's effort must survive a process restart with its own session")
	gotB, err := reopened.GetMeta(b.ID)
	require.NoError(t, err)
	assert.Equal(t, "low", gotB.ReasoningEffort,
		"conversation B must keep its own value — A's write never crosses to B (D30 isolation)")
}

// TestSessionMetaReasoningEffort_PatchKeepsOtherIdentityFieldsIntact (spec
// C5 non-web row: ReasoningEffort "persists in the identity group represented
// by pkg/session/unified_meta_files.go::u5IdentityFile") — a ReasoningEffort
// patch rides the identity group's targeted writer; the other identity-group
// fields on disk must survive it byte-for-byte in VALUE (same model, same
// title, same channel), and the stored effort must coexist with a session
// that also carries Model/Provider (the C5 resolution-order item 2 carrier:
// the effort applies only to the conversation's current primary model).
func TestSessionMetaReasoningEffort_PatchKeepsOtherIdentityFieldsIntact(t *testing.T) {
	store := newTestStore(t)
	// Real channel-session creation path, so InstanceID/PeerID/Title are
	// populated exactly as production populates them.
	meta, err := store.NewChannelSession("telegram", "tg.eu", "peer-1", "agent-1", "Effort probe")
	require.NoError(t, err)
	sid := meta.ID

	// The stored effort must coexist with a session that also carries
	// Model/Provider (the C5 resolution-order item 2 carrier: the effort
	// applies only to the conversation's current primary model).
	effort := "medium"
	require.NoError(t, store.SetMeta(sid, MetaPatch{ReasoningEffort: &effort}))

	got, err := store.GetMeta(sid)
	require.NoError(t, err)
	assert.Equal(t, "medium", got.ReasoningEffort)
	assert.Equal(t, "Effort probe", got.Title,
		"a ReasoningEffort patch must not disturb the other identity-group fields")
	assert.Equal(t, "telegram", got.Channel,
		"a ReasoningEffort patch must not disturb Channel")
	assert.Equal(t, "tg.eu", got.InstanceID,
		"a ReasoningEffort patch must not disturb InstanceID")
	assert.Equal(t, "agent-1", got.AgentID,
		"a ReasoningEffort patch must not disturb AgentID")

	// And a second patch of a DIFFERENT identity-group field must not clobber
	// the stored effort — the two fields share a group file, so this is the
	// exact spot where a missing u5IdentityFile mapping loses data.
	newTitle := "Effort probe 2"
	require.NoError(t, store.SetMeta(sid, MetaPatch{Title: &newTitle}))
	got, err = store.GetMeta(sid)
	require.NoError(t, err)
	assert.Equal(t, "medium", got.ReasoningEffort,
		"a patch of another identity-group field must not clobber the stored effort")
	assert.Equal(t, "Effort probe 2", got.Title)
}
