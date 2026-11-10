// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Shared harness for the W1 historical direct-parent notice RED pack
// (ADR-20260928 sub-agent control plane, frozen asset cd20cf8b — D6 round-3
// MAJ-001, D2 CRIT-001's outbox-before-parent effect, D8.4, founder Q2=A).
//
// TEST PLAN (elicify-test-writing step 1, filled before the first assertion)
//
// Behaviour under test: one durable direct-parent stopped-child notice per
// ACTUALLY LANDED stopped transition — id (parent, child, child_generation,
// real stop_seq), content cause/actor/time taken from the persisted landed
// history, never delivered before the landed event exists, deduplicated per
// transition, retried from history after a same-generation RESUME, and never
// fabricated into a restart of a resumed child.
//
// Specification source for every expected value (never the code under test):
//   - D6 round-3 MAJ-001: the notice, its dedup key, "delivery failure
//     remains pending, visibly reported and retried ... never silently
//     considered applied", cause/actor/time content.
//   - D2 CRIT-001: "A producer must not call the upward deliverer before
//     this commit" — for a stop disposition the commit is the stopped
//     landing; no parent inbox append and no wake may precede it.
//   - D4: per-child monotonic stop seq; the ledger is the source of truth.
//   - D8.4/D8.5: boot notices deduped per transition; boot never resumes and
//     never fabricates a restart from a notice retry.
//   - Founder Q2=A: a pending notice survives a same-generation RESUME as a
//     historical event.
//   - T15: each actual stop creates one durable direct-parent notice,
//     including retry/reboot.
//   - T27 instrument rule: normal production-wired dependencies only; no
//     test-only hook and no fake store proves this behaviour.
//
// Unit boundary — what is real: the production AgentLoop completion
// (steer_completion.go::deliverSteeredCompletion), the production cascade
// (steer_cancel.go::SteerCanceller.StopTurns / stampStop), the real file-backed
// LifecycleStore + control ledger, and the real file-backed
// MessageInboxStore, all under t.TempDir(). Injected only at existing
// production seams: the provider (parked until cancelled), the
// GenerationCancelFunc adapter (a constructor/StopTurns parameter), and
// boot_sweep.go::SteerBootRecovery.recoverStoppedChildNotice's own notice
// callback parameter. Faults are plain file permissions (chmod 0400) on the
// JSONL files those real stores append to — normal dependencies of the code
// under test, never a test-only hook.
//
// Case table (one row per oracle; full derivation in each test file):
//   - landed event absent  -> zero notices, zero wakes for that child
//   - one landed stop      -> one notice, id (p,c,g,real seq), content = transition
//   - two landed stops (same generation, explicit RESUME between)
//     -> two distinct notices, per-transition content,
//     ledger seq strictly advancing, direct parent only
//   - repeat stop on landed child -> no third line, no third notice
//   - notice append fails  -> stop still lands + ledger event stays + failure visible
//   - RESUME + repair + normal retry publisher -> exactly ONE original notice,
//     original id/At, no fabricated restart, no duplicate wake
//
// Known gaps (deliberately not covered here): W3b boot-epoch behaviour of the
// automatic restart note, plan-stop fan-out notices, and the periodic (non-boot)
// delivery retry timer. The mutation probes that prove this pack's failability
// are CHECK's duty (RED stops at "see it red for the right reason").
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// w1hNoticeID spells D6's deterministic inbox id exactly as the frozen pack
// brief fixes it: stopped-notice:<parent>:<child>:<generation>:<stop_seq>.
// Every expected id in this pack is composed from a StoppedTransition read
// back from the control ledger (the persisted history), never from the
// record's in-memory note.
func w1hNoticeID(parentID, childID string, generation int, stopSeq uint64) string {
	return fmt.Sprintf("stopped-notice:%s:%s:%d:%d", parentID, childID, generation, stopSeq)
}

// w1hSetup wires one harness loop: real stores under t.TempDir, the
// production audience/deliverer deps, and a provider that parks every real
// turn until its context is cancelled — so a dispatched child is a genuinely
// live turn, not a record fixture.
func w1hSetup(t *testing.T) (*AgentLoop, *parkedProvider, func()) {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	provider, release := installParkedProvider(t, al)
	return al, provider, release
}

func w1hOwner(id string) steer.Principal {
	return steer.Principal{Kind: steer.PrincipalKindHuman, ID: id}
}

// w1hLaunchLiveChild launches a steered child of parentID, admits it through
// the production dispatch, and returns only after the child's turn is genuinely
// parked inside its provider (entered is the provider's entry signal, so both
// the shared parkedProvider and the signal-provider variant work). Setup
// precondition failures are Fatalf: they are harness faults, never the
// behaviour under test.
func w1hLaunchLiveChild(t *testing.T, al *AgentLoop, entered <-chan string, parentID, callID string) *session.LifecycleRecord {
	t.Helper()
	childID, generation := launchSteeredChild(t, al, parentID, callID, "historical-notice work")
	res, err := NewSteerLauncher(al).Dispatch(context.Background(), childID, generation)
	if err != nil {
		t.Fatalf("setup: Dispatch(%s): %v", childID, err)
	}
	if res.State != steer.DispatchRunning {
		t.Fatalf("setup: Dispatch(%s) = %+v, want running — the pack drives REAL live turns", childID, res)
	}
	select {
	case <-entered:
	case <-time.After(30 * time.Second):
		t.Fatalf("setup: child %s never reached its provider — no live turn to stop", childID)
	}
	rec, err := al.GetSessionLifecycleStore().Load(childID)
	if err != nil {
		t.Fatalf("setup: Load(child %s): %v", childID, err)
	}
	if rec.State != session.LifecycleRunning {
		t.Fatalf("setup: child state = %q, want running after dispatch", rec.State)
	}
	if edge := rec.SteeredBy; edge == nil || edge.ReportingTarget.Channel == "" || edge.ReportingTarget.ChatID == "" {
		t.Fatalf("setup: child %s has no wake destination in its steering edge (%+v) — the D6 wake rows would be unobservable", childID, edge)
	}
	return rec
}

func w1hChildLifecyclePath(t *testing.T, al *AgentLoop, sessionID string) string {
	t.Helper()
	return filepath.Join(al.GetConfig().Agents.Defaults.Home, "session_lifecycle", sessionID+".jsonl")
}

func w1hInboxPath(t *testing.T, al *AgentLoop, ownerKey string) string {
	t.Helper()
	return filepath.Join(al.GetConfig().Agents.Defaults.Home, "session_messages", ownerKey+".jsonl")
}

// w1hFaultWrites makes one existing JSONL store file unwritable (chmod 0400)
// and PROVES the fault can bite: an O_RDWR open of the same path must fail
// after the chmod (we never run as root). The restore is registered for
// t.Cleanup immediately so no exit path leaves the tree faulted.
func w1hFaultWrites(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("instrument: %s does not exist yet — there is nothing to fault: %v", path, err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatalf("instrument: chmod 0400 %s: %v", path, err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err == nil {
		_ = f.Close()
		t.Fatalf("instrument: O_RDWR open of %s succeeded after chmod 0400 — the fault cannot bite (running as root?)", path)
	}
}

// w1hRestoreWrites removes the fault mid-test (the RED-3 repair step).
func w1hRestoreWrites(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("repair: chmod 0600 %s: %v", path, err)
	}
	if f, err := os.OpenFile(path, os.O_RDWR, 0); err != nil {
		t.Fatalf("repair: O_RDWR open of %s still fails after restore: %v", path, err)
	} else {
		_ = f.Close()
	}
}

// w1hWaitFor polls cond every 20ms up to timeout. It reports whether the
// condition was reached; the CALLER decides whether reaching it is a setup
// precondition (Fatalf) or an oracle row (Errorf) — the wait itself never
// decides the verdict.
func w1hWaitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

func w1hNoticesWithID(t *testing.T, al *AgentLoop, ownerKey, id string) []generated.SessionMessage {
	t.Helper()
	entries, err := al.GetMessageInboxStore().Entries(ownerKey)
	if err != nil {
		t.Fatalf("inbox Entries(%s): %v", ownerKey, err)
	}
	var out []generated.SessionMessage
	for _, entry := range entries {
		if entry.Kind != session.InboxEntryMessage || entry.Message == nil {
			continue
		}
		if messageIDOf(*entry.Message) == id {
			out = append(out, *entry.Message)
		}
	}
	return out
}

// w1hStoppedNoticeIDsIn lists every message id in ownerKey's inbox that
// carries the D6 stopped-notice id prefix — the direct-parent-only check
// reads OTHER sessions' inboxes through this and demands zero.
func w1hStoppedNoticeIDsIn(t *testing.T, al *AgentLoop, ownerKey string) []string {
	t.Helper()
	entries, err := al.GetMessageInboxStore().Entries(ownerKey)
	if err != nil {
		t.Fatalf("inbox Entries(%s): %v", ownerKey, err)
	}
	var out []string
	for _, entry := range entries {
		if entry.Kind != session.InboxEntryMessage || entry.Message == nil {
			continue
		}
		if id := messageIDOf(*entry.Message); strings.HasPrefix(id, "stopped-notice:") {
			out = append(out, id)
		}
	}
	return out
}

// w1hWakeCounter counts every wake the process notifier publishes, keyed by
// its steer_message_id. D6: a working parent is woken once per notice — and
// never woken by a notice whose landed event does not exist.
type w1hWakeCounter struct {
	mu     sync.Mutex
	counts map[string]int
}

func w1hObserveWakes(t *testing.T, al *AgentLoop) *w1hWakeCounter {
	t.Helper()
	c := &w1hWakeCounter{counts: map[string]int{}}
	al.asyncNotifier.registerObserver(func(ev AsyncNotifyEvent) {
		id, _ := ev.Metadata["steer_message_id"].(string)
		if id == "" {
			return
		}
		c.mu.Lock()
		c.counts[id]++
		c.mu.Unlock()
	})
	return c
}

func (c *w1hWakeCounter) count(id string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[id]
}

// w1hAssertNoticeMatchesTransition is the D6 content oracle: the notice's id
// is (parent, child, generation, real stop_seq) composed from the LEDGER's
// transition, and the notice says that transition's cause, actor and original
// instant — never a time.Now reconstruction. The union is read through its
// committed JSON bytes, the same access pattern messageIDOf uses.
func w1hAssertNoticeMatchesTransition(t *testing.T, msg generated.SessionMessage, parentID string, tr session.StoppedTransition) {
	t.Helper()
	wantID := w1hNoticeID(parentID, tr.SessionID, tr.Generation, tr.StopSeq)
	if got := messageIDOf(msg); got != wantID {
		t.Errorf("notice id = %q, want %q (D6 dedup key (parent, child, generation, stop_seq) with the ledger's real stop_seq)", got, wantID)
	}
	raw, err := msg.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal notice %s: %v", wantID, err)
	}
	var envelope struct {
		MessageID       string  `json:"message_id"`
		SessionId       string  `json:"session_id"`
		ParentSessionId *string `json:"parent_session_id,omitempty"`
		Text            string  `json:"text"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal notice %s: %v", wantID, err)
	}
	if got := envelope.SessionId; got != tr.SessionID {
		t.Errorf("notice session_id = %q, want the stopped child %q", got, tr.SessionID)
	}
	if envelope.ParentSessionId == nil || *envelope.ParentSessionId != parentID {
		t.Errorf("notice parent_session_id = %v, want %q (the DIRECT parent)", envelope.ParentSessionId, parentID)
	}
	text := envelope.Text
	if !strings.Contains(text, "cause: "+string(tr.Cause)) {
		t.Errorf("notice text %q does not carry the transition's cause %q (D6: the notice says cause)", text, tr.Cause)
	}
	if !strings.Contains(text, "actor: "+tr.Actor) {
		t.Errorf("notice text %q does not carry the transition's actor %q (D6: the notice says actor)", text, tr.Actor)
	}
	if got := w1hNoticeAtInstant(t, text); !got.Equal(tr.At) {
		t.Errorf("notice states at = %s, want the transition's original instant %s (D6: the notice says time — from the persisted history, not time.Now)", got, tr.At)
	}
}

// w1hNoticeAtInstant extracts the "at: <RFC3339Nano>" instant the notice text
// carries (D6: "The notice says cause/actor/time") and parses it back.
func w1hNoticeAtInstant(t *testing.T, text string) time.Time {
	t.Helper()
	const marker = "at: "
	idx := strings.Index(text, marker)
	if idx < 0 {
		t.Fatalf("notice text %q carries no at: instant", text)
	}
	rest := text[idx+len(marker):]
	end := strings.Index(rest, ". You can resume")
	if end < 0 {
		t.Fatalf("notice text %q carries a malformed at: instant", text)
	}
	parsed, err := time.Parse(time.RFC3339Nano, rest[:end])
	if err != nil {
		t.Fatalf("notice text %q carries an unparsable at: instant: %v", text, err)
	}
	return parsed
}

// w1hAssertTransitions compares the ledger's landed history against the
// expected transitions exactly — order, seq, generation, parent, cause,
// actor, instant — and requires every control id to be present.
func w1hAssertTransitions(t *testing.T, childID string, got []session.StoppedTransition, want []session.StoppedTransition) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("landed-stop history of %s = %d transitions (%s), want %d",
			childID, len(got), w1hFormatTransitions(got), len(want))
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.SessionID != w.SessionID || g.ParentSessionID != w.ParentSessionID ||
			g.Generation != w.Generation || g.StopSeq != w.StopSeq ||
			g.Cause != w.Cause || g.Actor != w.Actor || !g.At.Equal(w.At) {
			t.Fatalf("landed-stop history of %s[%d] = %+v, want %+v (W2a D4/D6: one real historical event per landed stop)",
				childID, i, g, w)
		}
		if g.ControlID == "" {
			t.Fatalf("landed-stop history of %s[%d] carries no control_id — a landed stop without its accepted control is a divergent tuple", childID, i)
		}
	}
}

func w1hFormatTransitions(trs []session.StoppedTransition) string {
	parts := make([]string, 0, len(trs))
	for _, tr := range trs {
		parts = append(parts, fmt.Sprintf("{seq:%d gen:%d parent:%q cause:%s actor:%q at:%s}", tr.StopSeq, tr.Generation, tr.ParentSessionID, tr.Cause, tr.Actor, tr.At))
	}
	return strings.Join(parts, ", ")
}

// w1hProbeMessage appends one uniquely identified probe entry through the
// REAL MessageInboxStore and proves it landed — the instrument check that the
// inbox read/write path under test can see an append at all (an assertion
// over a store that could not have recorded the notice would prove nothing).
func w1hProbeMessage(t *testing.T, al *AgentLoop, ownerKey, childID string) string {
	t.Helper()
	id := fmt.Sprintf("w1h-inbox-probe:%s:%d", childID, time.Now().UnixNano())
	var msg generated.SessionMessage
	parent := ownerKey
	depth := 1
	err := msg.FromSessionMessageError(generated.SessionMessageError{
		MessageId:       id,
		SessionId:       childID,
		ParentSessionId: &parent,
		CreatedAt:       time.Now().UTC(),
		Depth:           depth,
		Kind:            generated.SessionMessageErrorKindError,
		Fatal:           false,
		Text:            "w1h instrument probe",
	})
	if err != nil {
		t.Fatalf("instrument: build probe message: %v", err)
	}
	res, err := al.GetMessageInboxStore().Append(ownerKey, msg)
	if err != nil {
		t.Fatalf("instrument: probe append to %s failed — the inbox path under test cannot write: %v", ownerKey, err)
	}
	if res == nil || !res.Accepted {
		t.Fatalf("instrument: probe append to %s was not accepted: %+v", ownerKey, res)
	}
	if got := len(w1hNoticesWithID(t, al, ownerKey, id)); got != 1 {
		t.Fatalf("instrument: probe entry visible %d times after append, want 1 — the inbox read path cannot see appends", got)
	}
	return id
}
