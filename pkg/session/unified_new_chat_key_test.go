package session

// unified_new_chat_key_test.go — RED (#1428 step 2 option C, qa-lead):
// the new-chat key (K) must survive the store's identity codecs and every
// identity rewrite, on disk, across cold opens.
//
// SPEC SOURCES (expected values derive from these, never from the
// implementation — oracle independence):
//   - SPEC-C-server-binds-send-to-chat-r8.md S-5: "K saved in the creation
//     write and through both identity codecs; never returned; a failed write
//     yields no success signal."
//   - §4.3.3: "Saved in chat metadata (session.UnifiedMeta.NewChatKey) and
//     carried through the identity codecs u5IdentityFromMeta /
//     u5ComposeUnifiedMeta and every identity rewrite (F-ee)"; the Proof:
//     "cold-open the store, read K; perform an unrelated identity update;
//     cold-open again; K retained. An assertion on the object returned by
//     NewSession alone does not count."
//   - The wire name `new_chat_key` is pinned by the CONTRACT
//     (contracts/components/schemas/SendOrigin.yaml, commit 32802e2a6:
//     new_chat_key string 16..128, ^[A-Za-z0-9_-]+$), so the JSON-level
//     assertions here key on that name.
//
// RED STATE: UnifiedMeta/SessionMeta carry no NewChatKey field today, so
// json.Unmarshal drops the key and both tests fail at their first key
// assertion — the right reason (S-5 not implemented), not a build error.
// The tests are written against the PERSISTENCE CONTRACT (the JSON name and
// the cold-open proof), so they pass unchanged once GREEN adds the field and
// carries it through both codecs.

import (
	"encoding/json"
	"testing"
)

// newChatKeyTestKey follows the CONTRACT bounds (SendOrigin.yaml:
// new_chat_key string 16..128, ^[A-Za-z0-9_-]+$): exactly 16 chars of that
// alphabet.
const newChatKeyTestKey = "K0123456789abcd"

func newChatKeyTestMeta(t *testing.T, id string) *UnifiedMeta {
	t.Helper()
	raw := []byte(`{
		"id": "` + id + `",
		"agent_id": "mia",
		"title": "new-chat key persistence fixture",
		"status": "active",
		"channel": "webchat",
		"created_at": "2026-11-18T00:00:00Z",
		"updated_at": "2026-11-18T00:00:00Z",
		"new_chat_key": "` + newChatKeyTestKey + `"
	}`)
	var meta UnifiedMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("SETUP: fixture meta JSON must parse into UnifiedMeta: %v", err)
	}
	if meta.ID != id {
		t.Fatalf("SETUP: fixture meta lost its id (%q)", meta.ID)
	}
	return &meta
}

// metaMarshaledCarriesKey asserts the key survives a marshal of the meta —
// the shared oracle for "the key is still there" after any store round trip.
// It reads only the CONTRACT's JSON name, so it neither depends on the Go
// field's declaring struct (SessionMeta vs UnifiedMeta) nor breaks when the
// field moves between them.
func metaMarshaledCarriesKey(t *testing.T, meta *UnifiedMeta, phase string) {
	t.Helper()
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("%s: marshal meta: %v", phase, err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("%s: re-unmarshal marshaled meta: %v", phase, err)
	}
	got, present := wire["new_chat_key"]
	if !present {
		t.Fatalf("%s: S-5/4.3.3 violated: the meta's persisted shape carries no new_chat_key at all — "+
			"the key was dropped (json.Marshal saw %s)", phase, string(raw))
	}
	if got != newChatKeyTestKey {
		t.Fatalf("%s: new_chat_key = %v, want %q (the exact key the creation write saved)", phase, got, newChatKeyTestKey)
	}
}

// TestNewChatKey_CodecRoundTripRetainsKey proves the IDENTITY CODEC leg of
// S-5: a new-chat key present on the meta must survive
// u5IdentityFromMeta → u5IdentityFile → u5ComposeUnifiedMeta — the round trip
// every identity rewrite performs (F-ee). MU-22 ("K removed from the identity
// codec, in-memory field kept") must turn this red.
func TestNewChatKey_CodecRoundTripRetainsKey(t *testing.T) {
	meta := newChatKeyTestMeta(t, "session_newchatkey_codec")

	identity := u5IdentityFromMeta(meta)
	identityRaw, err := json.Marshal(identity)
	if err != nil {
		t.Fatalf("marshal identity file shape: %v", err)
	}
	var identityWire map[string]any
	if err := json.Unmarshal(identityRaw, &identityWire); err != nil {
		t.Fatalf("re-unmarshal identity file shape: %v", err)
	}
	if got, present := identityWire["new_chat_key"]; !present {
		t.Fatalf("S-5/4.3.3 violated: u5IdentityFromMeta drops the new-chat key — the identity file shape "+
			"carries no new_chat_key (marshalled keys: %v)", identityWire)
	} else if got != newChatKeyTestKey {
		t.Fatalf("u5IdentityFromMeta kept a wrong new_chat_key: got %v, want %q", got, newChatKeyTestKey)
	}

	// Compose back from the identity group (the read path readUnifiedMeta
	// takes), with empty sibling groups, exactly as the codec is used.
	stats := u5StatsFromMeta(meta)
	loop := u5LoopFromMeta(meta)
	composed := u5ComposeUnifiedMeta(identity, stats, loop, u5PendingAskFile{})
	metaMarshaledCarriesKey(t, composed, "u5ComposeUnifiedMeta round trip")
}

// TestNewChatKey_ColdOpenRetainsKeyAcrossIdentityUpdate is §4.3.3's Proof, on
// the real disk path: persist a keyed session, COLD-OPEN the store, read K,
// perform an unrelated identity update (a title rename), cold-open again —
// K retained. "An assertion on the object returned by NewSession alone does
// not count" (spec): both reads here come from freshly constructed stores.
func TestNewChatKey_ColdOpenRetainsKeyAcrossIdentityUpdate(t *testing.T) {
	baseDir := t.TempDir()
	const sessionID = "session_newchatkey_coldopen"

	first, err := NewUnifiedStore(baseDir)
	if err != nil {
		t.Fatalf("SETUP: first store: %v", err)
	}
	meta := newChatKeyTestMeta(t, sessionID)
	if _, err = first.persistNewSessionLocked(sessionID, meta); err != nil {
		t.Fatalf("SETUP: persist fixture session: %v", err)
	}

	// Cold open #1: a brand-new store instance over the same directory.
	cold1, err := NewUnifiedStore(baseDir)
	if err != nil {
		t.Fatalf("cold open #1: %v", err)
	}
	loaded1, err := cold1.GetMeta(sessionID)
	if err != nil {
		t.Fatalf("cold open #1: GetMeta: %v", err)
	}
	if loaded1 == nil {
		t.Fatalf("cold open #1: session %s not found after cold open", sessionID)
	}
	metaMarshaledCarriesKey(t, loaded1, "cold open #1")

	// Unrelated identity update: a title rename must not disturb the key.
	newTitle := "renamed by an unrelated identity update"
	if err = cold1.SetMeta(sessionID, MetaPatch{Title: &newTitle}); err != nil {
		t.Fatalf("unrelated identity update: SetMeta: %v", err)
	}

	// Cold open #2: after the unrelated identity rewrite.
	cold2, err := NewUnifiedStore(baseDir)
	if err != nil {
		t.Fatalf("cold open #2: %v", err)
	}
	loaded2, err := cold2.GetMeta(sessionID)
	if err != nil {
		t.Fatalf("cold open #2: GetMeta: %v", err)
	}
	if loaded2 == nil {
		t.Fatalf("cold open #2: session %s not found after the identity update", sessionID)
	}
	metaMarshaledCarriesKey(t, loaded2, "cold open #2 after unrelated identity update")
	if loaded2.Title != newTitle {
		t.Fatalf("control failed: the unrelated title update did not land (%q)", loaded2.Title)
	}
}
