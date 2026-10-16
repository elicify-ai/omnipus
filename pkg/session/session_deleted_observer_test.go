// Omnipus — RED payload #1343/#1329 round1, file 5 of the backend QA pack.
// Target location when applied: pkg/session/session_deleted_observer_test.go
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// RED STATUS (qa-lead, 2026-10-16, revision 2 after backend round-1/2
// feedback): the INSTRUMENT payload for team-lead F4 Decision A's proposed
// UnifiedStore hook. The proposed method —
// SetSessionDeletedObserver(func(sessionID string)) — does not exist yet, so
// this payload asserts its presence at RUNTIME through an interface
// assertion (comma-ok over any(store), bound ONCE and reused) and fails
// with a loud BLOCKED (never t.Skip, never a compile error). Per the
// team-lead addendum this explicit runtime absence IS the documented
// instrument/precondition RED for the observer API, while the real REST /
// workspace / agent-cascade payloads carry the behavioral RED.
//
// Revision-2 corrections (none adapts an expectation to production):
//   - NewSession returns *session.UnifiedMeta — every fixture uses .ID
//     (the round-1 measured compile-failure class, fixed here too).
//   - The after-unlock proof is a RESULT probe, not a naked deadlock
//     timeout: inside the observer, the real store is re-entered twice
//     through bounded result channels — GetMeta(deleted) whose RESULT must
//     show the row already gone (post-success ordering), and a nested
//     DeleteSession whose COMPLETION proves the session's shard lock was
//     released before the observer ran (it acquires that shard at entry).
//     The timeouts are failure detectors only; the asserted oracle is the
//     delivered result.
//   - Panic containment asserts a panic DIAGNOSTIC WAS LOGGED (user
//     constraint: recovered AND logged), not merely that the result stayed
//     nil and the row stayed gone. The assertion matches a WARN/ERROR
//     record referencing the deleted session id — never the panic message
//     text, which an implementation may deliberately keep out of logs.
//
// Memory-only by design (no persistence, resume, sink or journal call from
// the hook itself) — that half is pinned by the pkg/askuser checkpoint pack
// on the registry side and by the always-on access counters in the pkg/agent
// wiring payload. Recommended narrow gate (serial):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestSessionDeletedObserverContract$' -count=1 -v -p 1 ./pkg/session/
//
// Skill note: per elicify-test-writing in RED, items 2 (see it green), 3
// (mutate and confirm it dies) and the Proof-of-failability checklist are
// DEFERRED TO CHECK — they are not claimed here.

package session_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// sessionDeletedObserverSetter is the PROPOSED hook surface (team-lead F4
// Decision A). Asserted at runtime (comma-ok over any(store)) so this file
// always compiles; absence is a loud BLOCKED, not a skip and not a build
// failure. The bound interface value is returned and reused — never
// re-asserted per call site.
type sessionDeletedObserverSetter interface {
	SetSessionDeletedObserver(observer func(sessionID string))
}

// qaObserverHarness is a real store plus its wired recording observer.
type qaObserverHarness struct {
	store  *session.UnifiedStore
	setter sessionDeletedObserverSetter
	ids    chan string
}

// qaProbeRead is the bounded result of the observer's real GetMeta probe.
type qaProbeRead struct {
	meta *session.UnifiedMeta
	err  error
}

// qaNewObserverStore builds a real store, asserts the hook exists (BLOCKED
// while unimplemented), and wires the recording observer. The observer runs
// two RESULT probes on every invocation:
//
//  1. post-success read: GetMeta(deleted) through a bounded result channel —
//     the delivered RESULT must show the row already gone, proving the
//     observer fired only after the deletion was committed;
//  2. shard release: a nested DeleteSession(deleted) through a bounded
//     result channel — its COMPLETION proves the session's shard lock was
//     released before the observer ran (DeleteSession acquires that shard at
//     entry; a held lock would deadlock the probe).
//
// The timeouts below are failure detectors with explicit diagnostics; the
// asserted oracle is always the delivered result, never elapsed time.
func qaNewObserverStore(t *testing.T) *qaObserverHarness {
	t.Helper()
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close(), "observer pack store.Close must succeed") })
	setter, ok := any(store).(sessionDeletedObserverSetter)
	if !ok {
		t.Fatal("BLOCKED: UnifiedStore.SetSessionDeletedObserver(func(string)) is not implemented — required by team-lead F4 Decision A (one per-store post-success deletion observer, invoked only AFTER the session shard lock is released; failure-silent with panic recovery and a logged diagnostic)")
	}
	h := &qaObserverHarness{store: store, setter: setter, ids: make(chan string, 8)}
	setter.SetSessionDeletedObserver(func(sessionID string) {
		// Probe 1: post-success ordering via a REAL read.
		readDone := make(chan qaProbeRead, 1)
		go func() {
			meta, readErr := store.GetMeta(sessionID)
			readDone <- qaProbeRead{meta: meta, err: readErr}
		}()
		select {
		case read := <-readDone:
			if read.err == nil && read.meta != nil {
				t.Error("the deletion observer ran while the deleted row was still readable — the observer must fire only AFTER the deletion succeeded")
			}
		case <-time.After(5 * time.Second):
			t.Error("the observer's real GetMeta probe never completed — the store was unreachable from inside the observer")
		}
		// Probe 2: shard release via re-entry on the SAME session's shard.
		probeDone := make(chan error, 1)
		go func() { probeDone <- store.DeleteSession(sessionID) }()
		select {
		case <-probeDone:
			// Completion is the oracle (the nested delete fails with
			// not-found — the row is already gone — and that error is the
			// probe's business, not an assertion target).
		case <-time.After(5 * time.Second):
			t.Error("the deletion observer ran with the session's shard lock still held — the nested DeleteSession probe could not acquire it (Decision A: invoke only AFTER the shard lock is released)")
		}
		select {
		case h.ids <- sessionID:
		default:
			t.Error("the deletion observer fired more times than the buffered channel admits")
		}
	})
	return h
}

func (h *qaObserverHarness) drainIDs() []string {
	var out []string
	for {
		select {
		case id := <-h.ids:
			out = append(out, id)
		default:
			return out
		}
	}
}

// TestSessionDeletedObserverContract is the single narrow entry point.
func TestSessionDeletedObserverContract(t *testing.T) {
	t.Run("fires_once_with_deleted_id_after_successful_delete", qaObserverCase_Success)
	t.Run("failed_delete_invokes_nothing", qaObserverCase_FailureSilent)
	t.Run("panicking_observer_recovered_logged_and_result_unchanged", qaObserverCase_PanicContainment)
	t.Run("deleted_session_absent_from_listing", qaObserverCase_BootListing)
}

func qaObserverCase_Success(t *testing.T) {
	h := qaNewObserverStore(t)
	created, err := h.store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	sid := created.ID
	createdOther, err := h.store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	other := createdOther.ID

	require.NoError(t, h.store.DeleteSession(sid))

	// Bounded read: the observer must already have fired for exactly the
	// deleted id (it runs synchronously inside DeleteSession).
	select {
	case got := <-h.ids:
		require.Equal(t, sid, got, "the observer must receive the deleted session's id")
	case <-time.After(5 * time.Second):
		t.Fatal("the deletion observer never fired for a successful delete")
	}
	require.Empty(t, h.drainIDs(), "exactly one invocation per successful delete")

	// The untouched session is unaffected and its own delete fires the
	// observer for ITS id only.
	require.NoError(t, h.store.DeleteSession(other))
	select {
	case got := <-h.ids:
		require.Equal(t, other, got)
	case <-time.After(5 * time.Second):
		t.Fatal("the deletion observer never fired for the second delete")
	}
	require.Empty(t, h.drainIDs())
}

func qaObserverCase_FailureSilent(t *testing.T) {
	h := qaNewObserverStore(t)

	// A delete that does nothing (row absent) must invoke nothing.
	require.Error(t, h.store.DeleteSession("session_never_existed_qa"),
		"deleting an absent session must fail")
	require.Empty(t, h.drainIDs(), "a failed delete must not invoke the observer (success-only)")
}

func qaObserverCase_PanicContainment(t *testing.T) {
	// Capture the default slog so the LOGGED-diagnostic constraint is
	// assertable: a recovered observer panic must be logged, not swallowed.
	logBuf := &bytes.Buffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close(), "observer pack store.Close must succeed") })
	setter, ok := any(store).(sessionDeletedObserverSetter)
	if !ok {
		t.Fatal("BLOCKED: UnifiedStore.SetSessionDeletedObserver(func(string)) is not implemented — required by team-lead F4 Decision A")
	}
	created, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	sid := created.ID
	setter.SetSessionDeletedObserver(func(string) {
		panic("qa-1343: observer panic must never escape the delete")
	})

	// The successful result is unchanged: nil error, row gone. If the panic
	// escapes, this test dies with it — which IS the failure the containment
	// contract forbids.
	require.NoError(t, store.DeleteSession(sid))
	goneMeta, goneErr := store.GetMeta(sid)
	require.True(t, goneErr != nil || goneMeta == nil, "the deleted session's meta must be gone despite the observer panic")

	// The panic must also have been LOGGED (recovered AND logged). The match
	// is on a WARN/ERROR record referencing the deleted session id — the
	// safe correlation token — never on the panic message text, which an
	// implementation may deliberately keep out of its diagnostic.
	require.Eventually(t, func() bool {
		for _, line := range strings.Split(logBuf.String(), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var record map[string]any
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				continue
			}
			if level, _ := record["level"].(string); level != "WARN" && level != "ERROR" {
				continue
			}
			if strings.Contains(line, sid) {
				return true
			}
		}
		return false
	}, 5*time.Second, 25*time.Millisecond,
		"a recovered observer panic must be logged as a WARN/ERROR diagnostic referencing the deleted session id; captured=%s", logBuf.String())
}

func qaObserverCase_BootListing(t *testing.T) {
	h := qaNewObserverStore(t)
	created, err := h.store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	sid := created.ID

	require.NoError(t, h.store.DeleteSession(sid))

	// Boot precondition (F4 addendum): a deleted session is absent from
	// ListSessions, so a fresh registry's boot re-arm sweep can never see it.
	metas, err := h.store.ListSessions()
	require.NoError(t, err)
	for _, meta := range metas {
		require.NotEqual(t, sid, meta.ID, "a deleted session must be absent from ListSessions — a fresh boot re-arm must have nothing to resurrect")
	}
}
