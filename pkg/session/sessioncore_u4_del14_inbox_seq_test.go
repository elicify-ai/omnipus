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
// given an implied sequence.

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
// field, and with the backfill deleted it must be refused visibly rather than
// silently assigned an implied sequence (which would make the Drain cursor
// skip or mis-address it).
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
		t.Fatalf("Entries on a sequence-less (legacy) inbox line returned no error — DEL-14 requires " +
			"legacy input to be refused visibly, not silently mapped (backfillSeq must be gone)")
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

// TestMessageInbox_NoLegacySeqBackfillSymbol is DEL-14's K (source) deletion
// guard: the backfillSeq symbol must be gone, not merely unused, so no later
// reader can reintroduce the silent legacy sequence rewrite.
//
// RED today: message_inbox.go still declares backfillSeq.
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
}
