// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// W1 intentional stopped-child notice crash/replay regression pack.
// PW-D13 (founder 2026-10-15): intentional Stop never rings, even on boot replay.
// Oracle: parent-wake-visibility-spec.md §9.6 supersedes the 2026-10-04
// ring-until-taken rule for intentional Stop. Legacy names stay traceable.
// Real stores, real Stop, real boot retry, provider-edge fixture and notifier
// observer stay. Every pass retains exactly one notice/ledger transition and
// never resumes or re-stops this intentionally stopped helper. Ack stays durable.
// The stopped completion tail is joined before asserting absence of a ring.
// CHECK owns GREEN and mutation probes: allow a Stop ring, duplicate the row,
// or alter the retained stop on replay; each must fail this pack.
package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// rerCrashedWorld is the durable state a crashed process leaves behind: the
// saved note is in the parent's inbox, the dying process's in-memory doorbell
// has rung exactly once, and the parent never took the note.
type rerCrashedWorld struct {
	home       string
	parentID   string
	childID    string
	generation int
	noticeID   string
	tr         session.StoppedTransition
}

// rerSetupCrashedWorld runs one production stop to the exact crashed state.
// Every step is a precondition (Fatalf): the tests' subject is the restart
// ring, so the only Errorf failures must be ring oracles.
func rerSetupCrashedWorld(t *testing.T, workspace, ownerID, callID string) rerCrashedWorld {
	t.Helper()
	al, _, _ := w1hSetup(t)
	provider, releaseTurns := w1hInstallSignalProvider(t, al)
	t.Cleanup(releaseTurns)
	lifecycle := al.GetSessionLifecycleStore()

	parentID := newTestSteeringSession(t, al, workspace)
	owner := w1hOwner(ownerID)
	wakes := w1hObserveWakes(t, al)
	rec := w1hLaunchLiveChild(t, al, provider.entered, parentID, callID)
	childID, generation := rec.SessionID, rec.Generation

	// A real accepted stop interrupts the real live turn; the completion
	// lands the stop, appends the saved note, and rings the parent's
	// in-memory doorbell once. The process then "dies": nothing ever
	// consumes the ring and nobody acks the note.
	if _, err := NewSteerCanceller(lifecycle).StopTurns(context.Background(), childID, owner, false, al.SteerGenerationCancel); err != nil {
		t.Fatalf("StopTurns: %v", err)
	}
	select {
	case <-provider.exited:
	case <-time.After(15 * time.Second):
		t.Fatal("the stop never reached the live turn — no completing stop to witness")
	}

	world := rerCrashedWorld{home: al.GetConfig().Agents.Defaults.Home, parentID: parentID, childID: childID, generation: generation}
	settled := w1hWaitFor(t, 10*time.Second, "crashed state (landed stop + saved note)", func() bool {
		cur, err := lifecycle.Load(childID)
		if err != nil || cur.State != session.LifecycleStopped {
			return false
		}
		trs, err := lifecycle.ListStoppedTransitions(childID)
		if err != nil || len(trs) != 1 {
			return false
		}
		world.tr = trs[0]
		world.noticeID = w1hNoticeID(parentID, childID, generation, world.tr.StopSeq)
		return len(w1hNoticesWithID(t, al, parentID, world.noticeID)) == 1
	})
	if !settled {
		cur, _ := lifecycle.Load(childID)
		trs, _ := lifecycle.ListStoppedTransitions(childID)
		t.Fatalf("setup: the crashed state never settled: state=%q transitions=%s — no crash-after-doorbell state to restart from", cur.State, w1hFormatTransitions(trs))
	}

	// Preconditions that make this THE crashed state (all Fatalf — setup).
	wantActor := "human:" + owner.ID
	if world.tr.StopSeq != 1 || world.tr.Generation != generation || world.tr.ParentSessionID != parentID || world.tr.Cause != session.StopCauseStop || world.tr.Actor != wantActor {
		t.Fatalf("setup: landed transition = %s, want {seq:1 gen:%d parent:%q cause:stop actor:%q}", w1hFormatTransitions([]session.StoppedTransition{world.tr}), generation, parentID, wantActor)
	}
	// PW-D13 (founder 2026-10-15): join the real tail before checking no wake.
	joinGoalFixtureRuns(t, al)
	if c := wakes.count(world.noticeID); c != 0 {
		t.Errorf("intentional Stop rang %d time(s) for %s before the crash, want 0 (PW-D13)", c, world.noticeID)
	}
	if rerNoteAcked(t, al, parentID, world.noticeID) {
		t.Fatalf("setup: the saved note %s is already acked — the crash scenario requires an untaken note", world.noticeID)
	}
	return world
}

// rerRestartLoop builds the NEXT STARTUP: a second full AgentLoop over the
// SAME durable home — its own fresh notifier (the new process's doorbell,
// born silent), fresh store instances over the same files, zero carried
// runtime state. Only what is on disk crosses this restart.
func rerRestartLoop(t *testing.T, home string) *AgentLoop {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              home,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: testDefaultAgentID, Home: home}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	inbox := session.NewMessageInboxStore(filepath.Join(home, "session_messages"))
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "session_lifecycle"))
	al.SetSessionMessagingStores(inbox, lifecycle)
	wireSteerCompletionDeps(t, al)
	t.Cleanup(al.Close)
	return al
}

// rerStartupPass runs one restart delivery pass through the only retry entry
// W1 has — boot_sweep.go::SteerBootRecovery.recoverStoppedChildNotice — with
// the record loaded FRESH from the restart process's own store (a restarted
// process reads only durable state). The rings it fires are observed on the
// restart loop's own notifier by the caller's w1hObserveWakes. The operator
// notice callback is collected and deliberately unasserted: a normal re-ring
// is delivery, not a failure report, and the founder decision fixes no
// operator-report requirement for it.
func rerStartupPass(t *testing.T, al *AgentLoop, childID string) {
	t.Helper()
	recovery := &SteerBootRecovery{
		Lifecycle:  al.GetSessionLifecycleStore(),
		Sessions:   al.GetSessionStore(),
		Inbox:      al.GetMessageInboxStore(),
		Classifier: NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore()),
		Deliverer:  al.getUpwardDeliverer(),
	}
	rec, err := al.GetSessionLifecycleStore().Load(childID)
	if err != nil {
		t.Fatalf("restart Load(%s): %v", childID, err)
	}
	recovery.recoverStoppedChildNotice(context.Background(), rec, func(string, string) {})
}

// rerNoteAcked reads the parent's durable inbox and reports whether an ack
// entry covers id — the durable "the parent took the note" fact.
func rerNoteAcked(t *testing.T, al *AgentLoop, ownerKey, id string) bool {
	t.Helper()
	entries, err := al.GetMessageInboxStore().Entries(ownerKey)
	if err != nil {
		t.Fatalf("inbox Entries(%s): %v", ownerKey, err)
	}
	for _, e := range entries {
		if e.Kind != session.InboxEntryAck {
			continue
		}
		for _, acked := range e.AckedIDs {
			if acked == id {
				return true
			}
		}
	}
	return false
}

// PW-D13 (founder 2026-10-15): a crash after saving an intentional Stop note
// must not turn its replay into a parent wake. Durable identity stays singular.
func TestStoppedNotice_CrashAfterInMemoryDoorbell_StartupReringsTheSavedNote(t *testing.T) {
	world := rerSetupCrashedWorld(t, "ws-rering-crash", "rer-owner-crash", "call-rering-crash")

	// The crash: everything in the dying process is gone. The restart is a
	// fresh loop over the same home; its doorbell has never rung.
	restart := rerRestartLoop(t, world.home)
	rings := w1hObserveWakes(t, restart)
	if c := rings.count(world.noticeID); c != 0 {
		t.Fatalf("setup: the fresh process's doorbell already rang %d time(s) for %s — it must start silent so the startup ring is attributable to the replay", c, world.noticeID)
	}

	// ORACLE (founder decision — startup): the next startup rings the parent
	// again for the saved, still-unacked note — exactly once.
	rerStartupPass(t, restart, world.childID)
	// PW-D13 (founder 2026-10-15): an intentional Stop never re-rings at boot.
	if got := rings.count(world.noticeID); got != 0 {
		t.Errorf("startup replay rang the parent %d time(s) for intentional Stop %s, want 0 (PW-D13)", got, world.noticeID)
	}

	// ORACLE (D8.4 as amended — the re-ring rides the STORED note): the
	// inbox still holds exactly one line, and no second id was invented.
	if got := len(w1hNoticesWithID(t, restart, world.parentID, world.noticeID)); got != 1 {
		t.Errorf("parent inbox holds %d line(s) of %s after the re-ring, want exactly 1 — the re-ring re-rings the stored note, it never re-appends it", got, world.noticeID)
	}
	if ids := w1hStoppedNoticeIDsIn(t, restart, world.parentID); len(ids) != 1 || ids[0] != world.noticeID {
		t.Errorf("parent inbox stopped-notice ids = %v, want exactly [%s] — the restart invents no second notice", ids, world.noticeID)
	}
}

// PW-D13 (founder 2026-10-15): repeated delivery of an intentional Stop is
// silent both before and after ack. Keep one durable line, one transition,
// the unchanged stopped child, and no synthetic identity.
func TestStoppedNotice_ReringsUntilTaken_NeverDuplicateParentWork(t *testing.T) {
	world := rerSetupCrashedWorld(t, "ws-rering-dup", "rer-owner-dup", "call-rering-dup")
	restart := rerRestartLoop(t, world.home)
	rings := w1hObserveWakes(t, restart)
	if c := rings.count(world.noticeID); c != 0 {
		t.Fatalf("setup: the fresh process's doorbell already rang %d time(s) — it must start silent", c)
	}

	// Startup pass — the first re-ring (one ring per delivery pass while the
	// note is untaken).
	rerStartupPass(t, restart, world.childID)
	// PW-D13 (founder 2026-10-15): first and later retries of Stop stay silent.
	if got := rings.count(world.noticeID); got != 0 {
		t.Errorf("startup pass rang the parent %d time(s) for intentional Stop %s, want 0 (PW-D13)", got, world.noticeID)
	}

	// ORACLE (founder decision — the regular retry cycle): the next delivery
	// pass rings AGAIN, exactly once, while the note is still untaken. (The
	// periodic timer itself has no seam today — this is the next delivery
	// pass, the rule any such timer must satisfy.)
	rerStartupPass(t, restart, world.childID)
	if got := rings.count(world.noticeID); got != 0 {
		t.Errorf("after the second delivery pass, intentional Stop %s rang %d time(s), want 0 (PW-D13)", world.noticeID, got)
	}

	// ORACLE (no duplicate parent work under repeated rings): one durable
	// notice line, exactly the one ledger transition, the child untouched by
	// the rings.
	if got := len(w1hNoticesWithID(t, restart, world.parentID, world.noticeID)); got != 1 {
		t.Errorf("parent inbox holds %d line(s) of %s after two rings, want exactly 1 — repeated rings never duplicate the note", got, world.noticeID)
	}
	if ids := w1hStoppedNoticeIDsIn(t, restart, world.parentID); len(ids) != 1 || ids[0] != world.noticeID {
		t.Errorf("parent inbox stopped-notice ids = %v after two rings, want exactly [%s] — no synthetic second notice", ids, world.noticeID)
	}
	gotTrs, err := restart.GetSessionLifecycleStore().ListStoppedTransitions(world.childID)
	if err != nil {
		t.Fatalf("ListStoppedTransitions(after two rings): %v", err)
	}
	w1hAssertTransitions(t, world.childID, gotTrs, []session.StoppedTransition{world.tr})
	cur, err := restart.GetSessionLifecycleStore().Load(world.childID)
	if err != nil {
		t.Fatalf("Load(after two rings): %v", err)
	}
	if cur.State != session.LifecycleStopped || cur.Generation != world.generation {
		t.Errorf("after two rings the child record is state=%q generation=%d, want stopped at generation %d — a ring is a doorbell, never a restart stop (D8.5)", cur.State, cur.Generation, world.generation)
	}

	// The parent TAKES the note — the production ack the parent's wake
	// consumer makes (loop_inbound.go's inbox.Ack of the message id), through
	// the restart process's own real store.
	if err := restart.GetMessageInboxStore().Ack(world.parentID, []string{world.noticeID}); err != nil {
		t.Fatalf("parent ack of %s: %v", world.noticeID, err)
	}
	if !rerNoteAcked(t, restart, world.parentID, world.noticeID) {
		t.Fatalf("the ack of %s did not persist — the taken-note stop condition is unobservable", world.noticeID)
	}

	// ORACLE (founder decision — "until the parent actually takes it"): a
	// further delivery pass after the note is taken rings ZERO times.
	rerStartupPass(t, restart, world.childID)
	if got := rings.count(world.noticeID); got != 0 {
		t.Errorf("after the intentional Stop note was taken, ring count = %d, want still 0 (PW-D13)", got)
	}
}
