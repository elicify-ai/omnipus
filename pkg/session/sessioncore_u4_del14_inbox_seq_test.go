// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session-core U4 — DEL-14, inbox sequence (§Explicit DELETE Requirements).
//
// RED pack, qa-lead. DEL-14 removes pkg/session/message_inbox.go::backfillSeq
// and rules: "Current persisted inbox sequence ... No legacy sequence rewrite.
// Preserve normal dedupe/ack." Under the founder's greenfield rule (no upgrade
// path — root CLAUDE.md "Greenfield, no upgrade path") a sequence-less
// ("legacy") inbox line has no migration: it is refused VISIBLY, never silently
// given an implied sequence. The architect's Q3 fixes the shape: a HARD read
// error, new sentinel ErrInboxUnsequencedEntry; the skip of a torn/unparseable
// line stays.
//
// COMPILE NOTE: ErrInboxUnsequencedEntry is declared by the U4 implementation,
// so this pack cannot `errors.Is` it yet (that would not compile). The
// behaviour assertion is the architect-confirmed `err != nil`; the sentinel's
// exact identity is pinned by the source-presence check below, which the
// developer may tighten to `errors.Is(err, ErrInboxUnsequencedEntry)` once the
// symbol exists (ARCHITECT-ANSWER-U4-GAPS Q3).

package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestMessageInbox_LegacySeqlessEntry_RefusedNotBackfilled covers DEL-14's
// behaviour half: an inbox line persisted without a `seq` field predates the
// field, and with the backfill deleted it must be refused with a hard read
// error rather than silently assigned an implied sequence (which would make the
// Drain cursor skip or mis-address it).
//
// RED today: readEntries calls backfillSeq, silently giving the entry Seq=1 and
// returning no error — exactly the silent mapping DEL-14 deletes.
func TestMessageInbox_LegacySeqlessEntry_RefusedNotBackfilled(t *testing.T) {
	const ownerKey, childID = "parent-1", "child-1"
	dir := t.TempDir()
	store := NewMessageInboxStore(dir)

	msg := u4ProgressMessage(t, childID, "legacy-1")
	// A zero Seq is omitted by the `json:"seq,omitempty"` tag, producing a
	// genuine pre-field ("legacy") line.
	line, err := json.Marshal(InboxEntry{Kind: InboxEntryMessage, Message: &msg, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("marshal legacy entry: %v", err)
	}
	if strings.Contains(string(line), `"seq"`) {
		t.Fatalf("fixture is not a legacy line (it carries a seq): %s", line)
	}
	path := filepath.Join(dir, ownerKey+".jsonl")
	if werr := os.WriteFile(path, append(line, '\n'), 0o600); werr != nil {
		t.Fatalf("write legacy inbox file: %v", werr)
	}

	if _, err := store.Entries(ownerKey); err == nil {
		t.Fatalf("Entries on a sequence-less (legacy) inbox line returned no error — DEL-14/Q3 require " +
			"legacy input to be refused with a HARD read error (ErrInboxUnsequencedEntry), not silently " +
			"mapped (backfillSeq must be gone)")
	}

	// Positive control: a properly sequenced line reads back cleanly.
	good := NewMessageInboxStore(t.TempDir())
	if _, err := good.Append(ownerKey, u4ProgressMessage(t, childID, "current-1")); err != nil {
		t.Fatalf("Append (positive control): %v", err)
	}
	entries, err := good.Entries(ownerKey)
	if err != nil {
		t.Fatalf("Entries (positive control): %v", err)
	}
	if len(entries) != 1 || entries[0].Seq == 0 {
		t.Fatalf("positive control entries = %+v, want exactly one sequenced entry", entries)
	}
}

// TestMessageInbox_TornLineStillSkipped is the Q3 regression pin: a
// torn/unparseable line is still SKIPPED (crash tolerance, not DEL-14), even
// alongside the new unsequenced-entry refusal. Green on the pre-change code and
// must stay green — DEL-14 deletes sequence backfill, never crash tolerance.
func TestMessageInbox_TornLineStillSkipped(t *testing.T) {
	const ownerKey, childID = "parent-1", "child-1"
	dir := t.TempDir()
	store := NewMessageInboxStore(dir)

	// A valid sequenced line, a torn (unparseable) line, then another valid one.
	if _, err := store.Append(ownerKey, u4ProgressMessage(t, childID, "p-1")); err != nil {
		t.Fatalf("Append p-1: %v", err)
	}
	path := filepath.Join(dir, ownerKey+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open inbox for torn line: %v", err)
	}
	if _, werr := f.WriteString("{ this is not valid json\n"); werr != nil {
		t.Fatalf("write torn line: %v", werr)
	}
	f.Close()
	if _, err := store.Append(ownerKey, u4ProgressMessage(t, childID, "p-2")); err != nil {
		t.Fatalf("Append p-2: %v", err)
	}

	entries, err := store.Entries(ownerKey)
	if err != nil {
		t.Fatalf("Entries returned an error for a torn line — the torn-line skip must stay (Q3): %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("Entries = %d entries, want exactly the 2 valid ones (torn line skipped)", len(entries))
	}
}

// TestMessageInbox_NoLegacySeqBackfillSymbol is DEL-14/Q3's K (source) deletion
// guard: the backfillSeq symbol must be gone AND the new refusal sentinel
// ErrInboxUnsequencedEntry must exist, so no later reader can reintroduce the
// silent legacy sequence rewrite.
//
// RED today: message_inbox.go still declares backfillSeq and has no
// ErrInboxUnsequencedEntry.
func TestMessageInbox_NoLegacySeqBackfillSymbol(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	src, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "message_inbox.go"))
	if err != nil {
		t.Fatalf("read message_inbox.go: %v", err)
	}
	if strings.Contains(string(src), "backfillSeq") {
		t.Fatal("pkg/session/message_inbox.go still declares backfillSeq — DEL-14 requires this legacy " +
			"sequence-backfill symbol to be deleted outright (greenfield: no upgrade path)")
	}
	if !strings.Contains(string(src), "ErrInboxUnsequencedEntry") {
		t.Fatal("pkg/session/message_inbox.go declares no ErrInboxUnsequencedEntry — Q3 requires a hard " +
			"read error sentinel for a sequence-less (legacy) line")
	}
}
