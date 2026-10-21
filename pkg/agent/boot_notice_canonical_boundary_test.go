package agent

// ADR-20261004 C3/C5 and the bounded boot-12762 brief: canonical notices
// belong to the ledger publisher, never the generic error replay. Real admitted
// executions, Stop, same-generation Resume, reopened stores and full boot are
// exercised. Lookalike errors do not acquire that authority from a prefix/ID.
// The plan and deliberate gaps are in the boot-test-plan.md recovery artifact.
// GREEN/mutation/full-suite proof is deferred to a different CHECK author.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

type bootNoticeControlFixture struct {
	al          *AgentLoop
	parent      string
	otherParent string
	resumed     *session.LifecycleRecord
	historical  session.StoppedTransition
	canonical   generated.SessionMessage
	wakes       func(string) int
	provider    *goalRunProvider
	bootGate    *goalRunGate
	bootSeq     uint64
	writingBoot *session.BootEpochStore
}

func newBootNoticeControlFixture(t *testing.T) *bootNoticeControlFixture {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	oldGate := newGoalRunGate("selected old request", nil)
	freshGate := newGoalRunGate("actual admitted goal child remains resumable", nil)
	// PW-D6 (founder 2026-10-15): hold the native boot resume at the provider edge.
	bootGate := newGoalRunGate("native helper continued from saved history", nil)
	installGoalRunProvider(t, al, oldGate, freshGate, bootGate)
	instance, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: registered agent missing")
	}
	provider, ok := instance.Provider.(*goalRunProvider)
	if !ok {
		t.Fatal("SETUP: real model-edge gate was not installed")
	}
	// PW-SEC-01 fixture provisioning: restart resume continues saved work only
	// in a workspace that exists and still holds the helper's agent.
	provisionTestWorkspace(t, "ws-boot-canonical-boundary", testDefaultAgentID)
	parent := newTestSteeringSession(t, al, "ws-boot-canonical-boundary")
	otherParent := newTestSteeringSession(t, al, "ws-boot-unrelated-parent")
	wakes := observeU1ParentNoticeWakes(t, al, parent)
	old := launchGoalBearingChild(t, al, parent, "call-boot-canonical-boundary", goalChildLaunchOptions{live: true})
	awaitGoalProvider(t, oldGate)
	report, err := al.steerCanceller().StopTurns(context.Background(), old.SessionID,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: "boot-control-owner"}, true, al.SteerGenerationCancel)
	if err != nil || len(report.Unreachable) != 0 || !slices.Equal(report.Reached, []string{old.SessionID}) {
		t.Fatalf("SETUP: actual selected Stop failed: report=%+v err=%v", report, err)
	}
	joinGoalFixtureRuns(t, al)
	stopped := rootReopenedRecord(t, al, old.SessionID)
	if stopped.State != session.LifecycleStopped || stopped.Stop != nil || stopped.StopNote == nil {
		t.Fatalf("SETUP: actual selected Stop did not land: %+v", stopped)
	}
	transitions, err := al.GetSessionLifecycleStore().ListStoppedTransitions(old.SessionID)
	if err != nil || len(transitions) != 1 {
		t.Fatalf("SETUP: actual landed stop history=%+v err=%v, want one", transitions, err)
	}
	tr := transitions[0]
	id := w1hNoticeID(parent, old.SessionID, old.Generation, tr.StopSeq)
	notices := w1hNoticesWithID(t, al, parent, id)
	// PW-D13 (founder 2026-10-15): retain the canonical Stop notice, never its ring.
	if len(notices) != 1 {
		t.Fatalf("SETUP: canonical notice count=%d, want exactly 1", len(notices))
	}
	if wakes(id) != 0 {
		t.Errorf("intentional Stop wakes=%d, want 0 (PW-D13)", wakes(id))
	}
	w1hAssertNoticeMatchesTransition(t, notices[0], parent, tr)
	generation, err := al.steerCanceller().Revive(context.Background(), old.SessionID,
		steer.Principal{Kind: steer.PrincipalKindAgent, ID: parent})
	if err != nil || generation != old.Generation {
		t.Fatalf("SETUP: real same-generation Resume=%d err=%v", generation, err)
	}
	freshGate.open()
	dispatchChild(t, al, old.SessionID, generation, true)
	joinGoalFixtureRuns(t, al)
	resumed := rootReopenedRecord(t, al, old.SessionID)
	if resumed.State != session.LifecycleRunning || resumed.Generation != old.Generation || resumed.Stop != nil || resumed.StopNote != nil || resumed.ExecutionID == nil || resumed.ExecutionID.RunID == old.ExecutionID.RunID || resumed.ExecutionID.BootSeq != old.ExecutionID.BootSeq {
		t.Fatalf("SETUP: Resume did not install an actual fresh same-G execution: %+v", resumed)
	}
	// The model call and entire dispatch tail have joined. No old goroutine
	// is left active at this genuine next-boot/store-reopen boundary.
	boot := session.NewBootEpochStore(filepath.Join(al.GetConfig().Agents.Defaults.Home, "boot_epoch"))
	epoch, err := boot.Mint()
	if err != nil || epoch <= resumed.ExecutionID.BootSeq {
		t.Fatalf("SETUP: actual next boot Mint=%d err=%v, previous=%d", epoch, err, resumed.ExecutionID.BootSeq)
	}
	al.SetBootEpochStore(boot)
	return &bootNoticeControlFixture{al: al, parent: parent, otherParent: otherParent,
		resumed: resumed, historical: tr, canonical: notices[0], wakes: wakes, provider: provider, bootGate: bootGate, bootSeq: epoch, writingBoot: boot}
}

func reopenBootNoticeControlStores(t *testing.T, f *bootNoticeControlFixture) {
	t.Helper()
	home := f.al.GetConfig().Agents.Defaults.Home
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "session_lifecycle"))
	inbox := session.NewMessageInboxStore(filepath.Join(home, "session_messages"))
	f.al.SetSessionMessagingStores(inbox, lifecycle)
	wireSteerCompletionDeps(t, f.al)
	if got := rootReopenedRecord(t, f.al, f.resumed.SessionID); !reflect.DeepEqual(got, f.resumed) {
		t.Fatalf("SETUP: reopened actual current execution changed before boot: before=%+v after=%+v", f.resumed, got)
	}
}

func assertBootNoticeCurrentRunStoppedWithoutDispatch(t *testing.T, f *bootNoticeControlFixture, wantResume bool) []session.StoppedTransition {
	t.Helper()
	// PW-D6 (founder 2026-10-15): preserve the historical stop checks, not a stopped tail.
	current := pwLegacyRestartLanding(t, f.al, f.resumed.SessionID, f.bootSeq)
	if current.State != session.LifecycleStopped || current.Terminal() || current.Generation != f.resumed.Generation || !reflect.DeepEqual(current.ExecutionID, f.resumed.ExecutionID) || current.Stop != nil || current.StopNote == nil || current.StopNote.Cause != session.StopCauseRestart || current.FinalDelivery != nil {
		t.Fatalf("C5/D8: boot did not stop the actual current B without a fatal final: current=%+v note=%+v actual B=%+v", current, current.StopNote, f.resumed.ExecutionID)
	}
	// Preserve the independent D8.3 epoch oracle, but collect the routing
	// assertions as well: a note-field failure must not mask the boot-cut bug.
	if current.StopNote.BootSeq != f.bootSeq {
		t.Errorf("D8.3: restart stop note boot_seq=%d, want the actual persisted current boot=%d; note=%+v", current.StopNote.BootSeq, f.bootSeq, current.StopNote)
	}
	if current.StopNote.By != session.StopActorRestart {
		t.Errorf("D8.3: actual boot note actor=%q, want existing restart actor=%q", current.StopNote.By, session.StopActorRestart)
	}
	transitions, err := f.al.GetSessionLifecycleStore().ListStoppedTransitions(current.SessionID)
	if err != nil || len(transitions) != 2 || !reflect.DeepEqual(transitions[0], f.historical) || transitions[1].StopSeq <= f.historical.StopSeq || transitions[1].Cause != session.StopCauseRestart {
		t.Fatalf("C3/C5: historical and current-run stop obligations diverged: history=%+v err=%v", transitions, err)
	}
	if transitions[1].Actor != session.StopActorRestart || current.StopNote.Seq != transitions[1].StopSeq || !current.StopNote.At.Equal(transitions[1].At) {
		t.Errorf("D8.3/C3: actual restart note and landed ledger must carry the same restart actor/sequence/instant: note=%+v transition=%+v", current.StopNote, transitions[1])
	}
	resumed := rootReopenedRecord(t, f.al, current.SessionID)
	if wantResume {
		if resumed.State != session.LifecycleRunning || resumed.ExecutionID == nil ||
			resumed.ExecutionID.RunID == f.resumed.ExecutionID.RunID || resumed.ExecutionID.BootSeq != f.bootSeq {
			t.Fatalf("PW-D6: native helper was not admitted again after its durable restart stop: state=%q execution=%+v", resumed.State, resumed.ExecutionID)
		}
		awaitGoalProvider(t, f.bootGate)
	}
	f.provider.mu.Lock()
	calls := f.provider.next
	f.provider.mu.Unlock()
	if wantResume {
		if calls != 3 || f.al.getActiveTurnState(current.SessionID) == nil || f.al.steerAdmission().activeCount() != 1 || f.al.steerAdmission().queueLen() != 0 {
			t.Fatalf("PW-D6: native boot resume calls=%d, active/queued=%d/%d, want A, B and one resumed call with one active owner", calls, f.al.steerAdmission().activeCount(), f.al.steerAdmission().queueLen())
		}
	} else if calls != 2 || resumed.State != session.LifecycleStopped || f.al.getActiveTurnState(current.SessionID) != nil || f.al.steerAdmission().activeCount() != 0 || f.al.steerAdmission().queueLen() != 0 {
		t.Fatalf("PW-D6/FR-015: refused resume started work: calls=%d state=%q active/queued=%d/%d, want only A/B and stopped", calls, resumed.State, f.al.steerAdmission().activeCount(), f.al.steerAdmission().queueLen())
	}
	if goal := mustGoalRecord(t, current.GoalRef); goal.State != generated.GoalStateActive {
		t.Fatalf("D6/C1: boot stop ended the active goal: %+v", goal)
	}
	return transitions
}

func TestSteerBootRecovery_CanonicalStoppedNoticeUsesOnlyLedgerPublisher(t *testing.T) {
	f := newBootNoticeControlFixture(t)
	reopenBootNoticeControlStores(t, f)
	historicalID := messageIDOf(f.canonical)
	if got := f.wakes(historicalID); got != 0 {
		t.Errorf("PW-D13: historical intentional Stop already rang %d time(s), want 0", got)
	}
	wantWakes := make(map[string]int)
	var operatorNotices []string
	recovery := u1BootRecovery(t, f.al, &operatorNotices)
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("actual full boot: %v", err)
	}
	transitions := assertBootNoticeCurrentRunStoppedWithoutDispatch(t, f, true)
	if len(operatorNotices) != 0 {
		t.Errorf("canonical ledger notices were replayed as generic failure envelopes after their own publisher: %q", operatorNotices)
	}
	wantIDs := make([]string, 0, len(transitions))
	for _, tr := range transitions {
		id := w1hNoticeID(f.parent, tr.SessionID, tr.Generation, tr.StopSeq)
		wantIDs = append(wantIDs, id)
		messages := w1hNoticesWithID(t, f.al, f.parent, id)
		if len(messages) != 1 {
			t.Fatalf("C3/D6: notice %q has %d durable rows, want one", id, len(messages))
		}
		w1hAssertNoticeMatchesTransition(t, messages[0], f.parent, tr)
		want, err := stoppedChildNoticeMessage(f.resumed, f.parent, id, tr)
		if err != nil {
			t.Fatalf("existing canonical builder: %v", err)
		}
		if !bytes.Equal(bootNoticeMessageBytes(t, messages[0]), bootNoticeMessageBytes(t, want)) {
			t.Errorf("C3: canonical envelope %q diverged from the real ledger transition/builder", id)
		}
		// PW-D13/PW-D17 (founder 2026-10-15): Stop never rings; a restart is its own one wake.
		if tr.Cause == session.StopCauseRestart {
			wantWakes[id] = 1
		}
		if got := f.wakes(id); got != wantWakes[id] {
			t.Errorf("canonical notice %q rang %d times, want %d under PW-D13/PW-D17", id, got, wantWakes[id])
		}
	}
	if got := w1hStoppedNoticeIDsIn(t, f.al, f.parent); !slices.Equal(got, wantIDs) {
		t.Errorf("canonical notice set/order=%q, want exactly historical then current restart=%q", got, wantIDs)
	}
	assertBootNoticeNoFatalFinal(t, f)
	landed := rootReopenedRecord(t, f.al, f.resumed.SessionID)
	operatorNotices = nil
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("repeated boot: %v", err)
	}
	if len(operatorNotices) != 0 || !reflect.DeepEqual(rootReopenedRecord(t, f.al, f.resumed.SessionID), landed) {
		t.Errorf("repeated boot rewrote the landed stop or produced a false generic refusal: %q", operatorNotices)
	}
	for _, id := range wantIDs {
		if got := f.wakes(id); got != wantWakes[id] || len(w1hNoticesWithID(t, f.al, f.parent, id)) != 1 {
			t.Errorf("repeated delivery changed notice %q: wakes=%d want=%d, with one durable row (PW-D13/PW-D17)", id, got, wantWakes[id])
		}
	}
	if err := f.al.GetMessageInboxStore().Ack(f.parent, wantIDs); err != nil {
		t.Fatalf("take real canonical notices: %v", err)
	}
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("boot after taking notices: %v", err)
	}
	for _, id := range wantIDs {
		if got := f.wakes(id); got != wantWakes[id] {
			t.Errorf("taken notice %q rang again: got=%d want=%d (PW-D13/PW-D17)", id, got, wantWakes[id])
		}
	}
}

func bootNoticeMessageBytes(t *testing.T, msg generated.SessionMessage) []byte {
	t.Helper()
	raw, err := msg.MarshalJSON()
	if err != nil {
		t.Fatalf("generated notice marshal: %v", err)
	}
	return raw
}

func assertBootNoticeNoFatalFinal(t *testing.T, f *bootNoticeControlFixture) {
	t.Helper()
	entries, err := f.al.GetMessageInboxStore().Entries(f.parent)
	if err != nil {
		t.Fatalf("parent inbox: %v", err)
	}
	finalID := fmt.Sprintf("%s:%d:final", f.resumed.SessionID, f.resumed.Generation)
	for _, entry := range entries {
		if entry.Kind != session.InboxEntryMessage || entry.Message == nil {
			continue
		}
		fields := u1DecodeMessageFields(t, bootNoticeMessageBytes(t, *entry.Message))
		if fields["session_id"] == f.resumed.SessionID && (fields["message_id"] == finalID || fields["fatal"] == true) {
			t.Errorf("C5/D8: nonfatal automatic Stop produced a fatal/terminal final: %s", bootNoticeMessageBytes(t, *entry.Message))
		}
	}
}

// Characterization controls: dispatcher Q1=A (2026-10-05) approves the precise
// ledger-authentication rejection, replacing the incidental generic decoder
// text, NOT a state/provenance oracle. Parseable prefix/nonfatal lookalikes
// still require an exact cause and ID; no broad nil/error fallback is allowed.
// The independent canonical/current-run and retention oracles stay unchanged.
func TestSteerBootRecovery_NoticeLookalikesNeverGainCanonicalExemption(t *testing.T) {
	cases := []string{"ordinary_nonfatal", "text_prefix_only", "id_prefix_unledgered_seq_zero", "same_id_wrong_body", "same_id_wrong_event_time", "same_id_untrusted_origin", "same_id_wrong_current_parent"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			f := newBootNoticeControlFixture(t)
			bad, decodeErr := f.canonical.AsSessionMessageError()
			if decodeErr != nil {
				t.Fatalf("SETUP: actual canonical error envelope: %v", decodeErr)
			}
			replace := true
			switch name {
			case "ordinary_nonfatal":
				bad.MessageId = "ordinary-nonfatal:" + bad.MessageId
				bad.Text = "ordinary nonfatal warning; no stop transition occurred"
				bad.UntrustedOrigin = true
				replace = false
			case "text_prefix_only":
				bad.MessageId = "unledgered:" + bad.MessageId
				replace = false
			case "id_prefix_unledgered_seq_zero":
				// A real landed sequence starts at one; zero can never identify
				// this actual stop or the subsequent restart stop.
				bad.MessageId = w1hNoticeID(f.parent, f.resumed.SessionID, f.resumed.Generation, 0)
				replace = false
			case "same_id_wrong_body":
				bad.Text = "stopped_child: forged actor and stop event, not the landed transition"
			case "same_id_wrong_event_time":
				bad.CreatedAt = bad.CreatedAt.Add(time.Nanosecond)
			case "same_id_untrusted_origin":
				bad.UntrustedOrigin = true
			case "same_id_wrong_current_parent":
				bad.ParentSessionId = &f.otherParent
			}
			var msg generated.SessionMessage
			if err := msg.FromSessionMessageError(bad); err != nil {
				t.Fatalf("SETUP: encode parseable negative input: %v", err)
			}
			if replace {
				corruptBootNoticeInboxEnvelope(t, f, msg)
			} else {
				result, err := f.al.GetMessageInboxStore().Append(f.parent, msg)
				if err != nil || result == nil || !result.Accepted || result.Deduped {
					t.Fatalf("SETUP: append negative input=%+v err=%v", result, err)
				}
			}
			reopenBootNoticeControlStores(t, f)
			stored := w1hNoticesWithID(t, f.al, f.parent, bad.MessageId)
			if len(stored) != 1 || !bytes.Equal(bootNoticeMessageBytes(t, stored[0]), bootNoticeMessageBytes(t, msg)) {
				t.Fatal("SETUP: reopened negative envelope is not exactly the intended input")
			}
			class, err := session.ClassifySessionMessage(msg)
			isStopNotice := name != "ordinary_nonfatal"
			if err != nil || class.Fatal || class.Kind != "error" {
				t.Fatalf("SETUP: negative input does not reach the intended error class: %+v err=%v", class, err)
			}
			// PW-D13 (founder 2026-10-15): a stopped_child prefix grants no wake.
			if class.WakeEligible {
				t.Errorf("notice lookalike is wake-eligible: %+v, want false (PW-D13)", class)
			}
			baseline := f.wakes(bad.MessageId)
			var operatorNotices []string
			recovery := u1BootRecovery(t, f.al, &operatorNotices)
			if err := recovery.Run(context.Background()); err != nil {
				t.Fatalf("actual full boot: %v", err)
			}
			assertBootNoticeCurrentRunStoppedWithoutDispatch(t, f, true)
			assertBootNoticeNoFatalFinal(t, f)
			cause := bad.MessageId + " does not match its landed transition's canonical message"
			if name == "text_prefix_only" || name == "id_prefix_unledgered_seq_zero" {
				cause = bad.MessageId + " has no matching landed transition"
			}
			want := fmt.Sprintf("session %s stopped-notice replay refused: steer: stopped notice: %s", f.resumed.SessionID, cause)
			if isStopNotice && !slices.Contains(operatorNotices, want) {
				t.Errorf("lookalike %q did not return its precise authenticated-domain refusal: want %q; got %q", name, want, operatorNotices)
			}
			if !isStopNotice {
				for _, notice := range operatorNotices {
					if strings.Contains(notice, bad.MessageId) {
						t.Errorf("ordinary nonfatal input was incorrectly replayed: %q", notice)
					}
				}
				if got := f.wakes(bad.MessageId); got != baseline {
					t.Errorf("ordinary nonfatal error rang parent: %d -> %d", baseline, got)
				}
			}
			after := w1hNoticesWithID(t, f.al, f.parent, bad.MessageId)
			if len(after) != 1 || !bytes.Equal(bootNoticeMessageBytes(t, after[0]), bootNoticeMessageBytes(t, msg)) {
				t.Error("lookalike input was rewritten, dropped, or duplicated instead of retained verbatim")
			}
			if taken, err := stopNoticeTaken(f.al.GetMessageInboxStore(), f.parent, bad.MessageId); err != nil || taken {
				t.Errorf("boot silently took the unmatched input: taken=%v err=%v", taken, err)
			}
		})
	}
}

// Physical corruption at the external file boundary changes exactly one
// actual temporary inbox envelope. No lifecycle/ledger/execution identity is
// written. Reopening above proves the matcher sees these persisted bytes.
func corruptBootNoticeInboxEnvelope(t *testing.T, f *bootNoticeControlFixture, replacement generated.SessionMessage) {
	t.Helper()
	path := w1hInboxPath(t, f.al, f.parent)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read actual parent inbox: %v", err)
	}
	lines := bytes.Split(raw, []byte("\n"))
	changed := 0
	for i, line := range lines {
		if len(line) == 0 {
			continue
		}
		var entry session.InboxEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatalf("decode actual inbox line: %v", err)
		}
		if entry.Kind != session.InboxEntryMessage || entry.Message == nil || messageIDOf(*entry.Message) != messageIDOf(replacement) {
			continue
		}
		entry.Message = &replacement
		encoded, err := json.Marshal(entry)
		if err != nil {
			t.Fatalf("encode altered existing envelope: %v", err)
		}
		lines[i] = encoded
		changed++
	}
	if changed != 1 {
		t.Fatalf("SETUP: physical envelope fault changed %d rows, want exactly one actual canonical notice", changed)
	}
	if err := os.WriteFile(path, bytes.Join(lines, []byte("\n")), 0o600); err != nil {
		t.Fatalf("write external temporary inbox fault: %v", err)
	}
}

// A direct negative-input control for the explicit CURRENT-record edge guard.
// Every execution/message/ledger ID is real. Only a copied input's parent edge
// is intentionally mismatched; it is never persisted or called a valid run.
func TestSteerBootRecovery_CanonicalNoticeRejectsChangedCurrentParentEdge(t *testing.T) {
	f := newBootNoticeControlFixture(t)
	reopenBootNoticeControlStores(t, f)
	var notices []string
	recovery := u1BootRecovery(t, f.al, &notices)
	owned, err := recovery.landedStopNoticeOwnsReplay(f.resumed, f.canonical)
	if err != nil || !owned {
		t.Fatalf("instrument: genuine ledger-backed canonical notice not recognized: owned=%v err=%v", owned, err)
	}
	wrong := *f.resumed
	edge := *f.resumed.SteeredBy
	edge.SteeringSessionID = f.otherParent
	wrong.SteeredBy = &edge
	owned, err = recovery.landedStopNoticeOwnsReplay(&wrong, f.canonical)
	want := "steer: stopped notice: " + messageIDOf(f.canonical) + " does not match the current steering edge"
	if owned || err == nil || err.Error() != want {
		t.Fatalf("canonical history bypassed the current-parent authority: owned=%v err=%v, want false/%q", owned, err, want)
	}
	if current := rootReopenedRecord(t, f.al, f.resumed.SessionID); !reflect.DeepEqual(current, f.resumed) {
		t.Error("negative matcher input changed the real current execution record")
	}
	rows := w1hNoticesWithID(t, f.al, f.parent, messageIDOf(f.canonical))
	if len(rows) != 1 || !bytes.Equal(bootNoticeMessageBytes(t, rows[0]), bootNoticeMessageBytes(t, f.canonical)) {
		t.Error("negative matcher input rewrote or lost the authentic durable notice")
	}
}
