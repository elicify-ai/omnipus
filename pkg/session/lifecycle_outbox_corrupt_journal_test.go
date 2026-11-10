// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// lifecycle_outbox_corrupt_journal_test.go — W3 RED reconcile (fresh-context
// qa-lead), ADR-20260928 sub-agent control plane (frozen asset cd20cf8b),
// readJournal's corruption rule:
//
//   - an UNTERMINATED torn trailing line is the crash-window tolerance: the
//     append died mid-line, nothing durable was lost, the journal reads
//     normally without it (D4 retention: "a crash can reread either old
//     ledger or complete summary");
//   - a NEWLINE-TERMINATED malformed journal line is a VISIBLE consistency
//     error — never silently skipped: a skipped line can swallow a committed
//     final's record so its payload silently disappears from discovery, and
//     D2 forbids exactly that ("an orphan, corrupt or mismatching envelope is
//     a visible consistency error and cannot authorize publication or
//     retirement").
//
// Test plan (elicify-test-writing step 1)
//
// Behaviour under test: LifecycleStore.readJournal, exercised only through
// its public surfaces — ListPendingFinalDeliveries and
// CommittedFinalDelivery (the discovery reads a publisher/boot retry runs),
// plus UpdateFinalDelivery (retirement must not be authorized through a
// corrupt journal).
//
// Unit boundary — REAL: one LifecycleStore; the committed final is written
// through the store's own single-mutation boundary (the same ONE Mutate the
// production commit boundary performs: terminal state + protected
// FinalDeliveryCommit). The corruption is applied by ordinary file appends
// to the journal — no production hook, no fake commit writer. The two cases
// differ ONLY in the trailing newline of one appended malformed line, so the
// positive instrument proves the demanded rule is not a fail-all parser.
//
// Case table:
//
//  1. RED case: committed final + `{not-json}\n` (terminated, appended at a
//     controlled position after the committed record line) —
//     ListPendingFinalDeliveries and CommittedFinalDelivery must return a
//     caller-visible error naming the session, and a retirement demand must
//     be refused AS THE CORRUPTION (not merely as unearned retirement).
//     Expected RED on the pinned tree: decodeJournalLine returns nil for the
//     unparseable line and readJournal skips it, so the readers report the
//     committed final as if the journal were intact and the retire demand is
//     refused only for the unrelated not-earned reason.
//  2. Positive instrument (must PASS): otherwise identical fixture plus only
//     an UNTERMINATED `{not-json}` torn trailing line — discovery still
//     returns exactly the committed final with its payload bytes and hash
//     intact. Reads only: the instrument never writes, so it stays
//     independent of how a later append would normalize the torn tail.
//
// What would break these tests (CHECK mutations — deferred to CHECK, not run
// in RED): skipping terminated corrupt lines again (case 1 stays red);
// erroring on torn tails too (case 2 dies); an error that does not identify
// the session; a refusal that still lets retirement through.
//
// Known gaps (deliberate): tail()'s own malformed-line tolerance for RECORD
// lines (the Load path) is the same rule family but a separate surface — the
// brief scopes this unit to the outbox readers; multi-line journals (several
// commits/envelopes around the corrupt line) are not covered — the minimal
// one-commit fixture isolates the rule.

package session

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// commitCorruptJournalFinal writes one real committed final/outbox record
// through the store's own mutation boundary — terminal done state AND the
// protected FinalDeliveryCommit in ONE Mutate, exactly the single-mutation
// commit the production boundary performs — and returns the committed
// payload bytes. No hand-seeded fake final: the tuple goes through
// persistLocked's validation like every production write. (The outcome
// string is opaque at this store layer — pkg/session does not import
// pkg/steer; the store never interprets it.)
func commitCorruptJournalFinal(t *testing.T, store *LifecycleStore, sessionID, commitID string) []byte {
	t.Helper()
	seed := &LifecycleRecord{
		SessionID:      sessionID,
		Generation:     1,
		State:          LifecycleRunning,
		OwnerScopeKind: OwnerScopeHuman,
		WorkspaceID:    "ws",
		AgentID:        "agent-1",
	}
	if err := store.Persist(seed); err != nil {
		t.Fatalf("Persist(seed): %v", err)
	}
	payload, err := json.Marshal(map[string]string{"result": "committed before the corrupt line landed"})
	if err != nil {
		t.Fatalf("encode committed payload: %v", err)
	}
	payloadHash := fmt.Sprintf("%x", sha256.Sum256(payload))
	if err := store.Mutate(sessionID, func(cur *LifecycleRecord) error {
		if cur == nil || cur.Generation != 1 {
			return fmt.Errorf("fixture: generation 1 record not current (cur=%v)", cur)
		}
		cur.State = LifecycleCompleted
		cur.FinalDelivery = &FinalDeliveryCommit{
			Generation:      1,
			CommitID:        commitID,
			MessageID:       fmt.Sprintf("%s:1:final", sessionID),
			Outcome:         "final_answer",
			ParentSessionID: "parent-" + sessionID,
			PayloadHash:     payloadHash,
			Payload:         payload,
		}
		return nil
	}); err != nil {
		t.Fatalf("commit done+outbox in one Mutate: %v", err)
	}
	return payload
}

// appendJournalCorruption appends one raw line to the session's journal file
// through ordinary file operations. trailingNewline controls the fault class:
// true writes a NEWLINE-TERMINATED line (a durable, complete, malformed
// line); false writes an UNTERMINATED torn trailing line (a crash cut the
// append mid-line). A fixture control verifies the journal really ends at a
// line boundary first, so the appended bytes can never merge into the last
// valid line — the corruption lands exactly where the case table says.
func appendJournalCorruption(t *testing.T, store *LifecycleStore, sessionID, rawLine string, trailingNewline bool) {
	t.Helper()
	journal := filepath.Join(store.Dir(), sessionID+".jsonl")
	raw, err := os.ReadFile(journal)
	if err != nil {
		t.Fatalf("read journal %s: %v", journal, err)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Fatalf("fixture control: journal %s does not end at a line boundary — the corruption would merge into the last valid line instead of landing as its own line", journal)
	}
	appended := rawLine
	if trailingNewline {
		appended = rawLine + "\n"
	}
	f, err := os.OpenFile(journal, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open journal for append: %v", err)
	}
	if _, err := f.WriteString(appended); err != nil {
		f.Close()
		t.Fatalf("append corruption: %v", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		t.Fatalf("sync corruption: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close journal: %v", err)
	}
}

// TestLifecycleOutboxCorruptJournal pins readJournal's corruption rule
// through the public readers: a newline-terminated malformed line is a
// visible consistency error that names the session and can never authorize
// retirement, while an unterminated torn tail stays tolerated with normal
// discovery.
func TestLifecycleOutboxCorruptJournal(t *testing.T) {
	t.Run("newline-terminated corrupt line is a visible consistency error", func(t *testing.T) {
		store := NewLifecycleStore(t.TempDir())
		sessionID := "crptjnl-terminated"
		commitID := "commit-corrupt-journal-visible"
		commitCorruptJournalFinal(t, store, sessionID, commitID)
		appendJournalCorruption(t, store, sessionID, "{not-json}", true)

		pending, err := store.ListPendingFinalDeliveries()
		if err == nil {
			t.Fatalf("ListPendingFinalDeliveries silently skipped a newline-terminated corrupt journal line and returned %+v — a durable malformed line must be a visible consistency error, never silently skipped so a committed final disappears", pending)
		}
		if !strings.Contains(err.Error(), sessionID) {
			t.Fatalf("the corrupt-journal error does not identify the session: %v", err)
		}
		if _, _, _, _, err := store.CommittedFinalDelivery(sessionID, 1, commitID); err == nil {
			t.Fatal("CommittedFinalDelivery read past a newline-terminated corrupt line without error — the corruption must be visible to every reader of the journal, or a committed final can silently disappear")
		}
		if err := store.UpdateFinalDelivery(sessionID, 1, commitID, 0, FinalDeliveryCommand{Retire: true}); err == nil {
			t.Fatal("payload retirement succeeded on a journal with a newline-terminated corrupt line — a corrupt journal can never authorize publication or retirement (D2)")
		} else if errors.Is(err, ErrFinalDeliveryRetireNotEarned) {
			t.Fatalf("retirement on a corrupt journal was refused only as unearned retirement (%v) — the refusal must be the visible corruption itself, or the reader is still silently skipping the malformed line", err)
		}
	})

	t.Run("unterminated torn tail retains normal discovery", func(t *testing.T) {
		store := NewLifecycleStore(t.TempDir())
		sessionID := "crptjnl-torntail"
		commitID := "commit-corrupt-journal-torn-tail"
		payload := commitCorruptJournalFinal(t, store, sessionID, commitID)
		appendJournalCorruption(t, store, sessionID, "{not-json}", false)

		pending, err := store.ListPendingFinalDeliveries()
		if err != nil {
			t.Fatalf("an unterminated torn trailing line must stay tolerated (the crash window D4 protects), got error: %v", err)
		}
		if len(pending) != 1 {
			t.Fatalf("pending finals = %d entries (%+v), want exactly the committed final", len(pending), pending)
		}
		item := pending[0]
		if item.SessionID != sessionID || item.Generation != 1 || item.Commit.CommitID != commitID || !item.Pending() {
			t.Fatalf("discovered final = %+v, want the committed generation-1 final still pending", item)
		}
		if string(item.Commit.Payload) != string(payload) {
			t.Fatalf("discovered payload bytes diverge from the committed payload (%d vs %d bytes) — the torn tail must not disturb the committed record", len(item.Commit.Payload), len(payload))
		}
		commit, _, _, retired, err := store.CommittedFinalDelivery(sessionID, 1, commitID)
		if err != nil {
			t.Fatalf("CommittedFinalDelivery on a torn-tail journal: %v", err)
		}
		if retired || commit.PayloadHash == "" || string(commit.Payload) != string(payload) {
			t.Fatalf("committed final = hash %q retired %v payload %d bytes — want the intact protected tuple", commit.PayloadHash, retired, len(commit.Payload))
		}
	})
}
