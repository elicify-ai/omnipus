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
	bootSeq     uint64
}

func newBootNoticeControlFixture(t *testing.T) *bootNoticeControlFixture {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	oldGate := newGoalRunGate("selected old request", nil)
	freshGate := newGoalRunGate("actual admitted goal child remains resumable", nil)
	installGoalRunProvider(t, al, oldGate, freshGate)
	instance, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: registered agent missing")
	}
	provider, ok := instance.Provider.(*goalRunProvider)
	if !ok {
		t.Fatal("SETUP: real model-edge gate was not installed")
	}
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
	if len(notices) != 1 || wakes(id) != 1 {
		t.Fatalf("SETUP: actual canonical notice=%d wakes=%d, want one durable notice and ring", len(notices), wakes(id))
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
		resumed: resumed, historical: tr, canonical: notices[0], wakes: wakes, provider: provider, bootSeq: epoch}
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

func assertBootNoticeCurrentRunStoppedWithoutDispatch(t *testing.T, f *bootNoticeControlFixture) []session.StoppedTransition {
	t.Helper()
	current := rootReopenedRecord(t, f.al, f.resumed.SessionID)
	if current.State != session.LifecycleStopped || current.Terminal() || current.Generation != f.resumed.Generation || !reflect.DeepEqual(current.ExecutionID, f.resumed.ExecutionID) || current.Stop != nil || current.StopNote == nil || current.StopNote.Cause != session.StopCauseRestart || current.FinalDelivery != nil {
		t.Fatalf("C5/D8: boot did not stop the actual current B without a fatal final: current=%+v note=%+v actual B=%+v", current, current.StopNote, f.resumed.ExecutionID)
	}
	// Preserve the independent D8.3 epoch oracle, but collect the routing
	// assertions as well: a note-field failure must not mask the boot-cut bug.
	if current.StopNote.BootSeq != f.bootSeq {
		t.Errorf("D8.3: restart stop note boot_seq=%d, want the actual persisted current boot=%d; note=%+v", current.StopNote.BootSeq, f.bootSeq, current.StopNote)
	}
	transitions, err := f.al.GetSessionLifecycleStore().ListStoppedTransitions(current.SessionID)
	if err != nil || len(transitions) != 2 || !reflect.DeepEqual(transitions[0], f.historical) || transitions[1].StopSeq <= f.historical.StopSeq || transitions[1].Cause != session.StopCauseRestart {
		t.Fatalf("C3/C5: historical and current-run stop obligations diverged: history=%+v err=%v", transitions, err)
	}
	f.provider.mu.Lock()
	calls := f.provider.next
	f.provider.mu.Unlock()
	if calls != 2 || f.al.getActiveTurnState(current.SessionID) != nil || f.al.steerAdmission().activeCount() != 0 || f.al.steerAdmission().queueLen() != 0 {
		t.Fatalf("D8.5: boot started work: model calls=%d (want original A and explicit B only), active/queued=%d/%d", calls, f.al.steerAdmission().activeCount(), f.al.steerAdmission().queueLen())
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
	baseline := f.wakes(historicalID)
	var operatorNotices []string
	recovery := u1BootRecovery(t, f.al, &operatorNotices)
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("actual full boot: %v", err)
	}
	transitions := assertBootNoticeCurrentRunStoppedWithoutDispatch(t, f)
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
		before := 0
		if id == historicalID {
			before = baseline
		}
		if got := f.wakes(id); got != before+1 {
			t.Errorf("C3: canonical notice %q rang %d times, want exactly its domain-publisher baseline+1=%d", id, got, before+1)
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
		before := 0
		if id == historicalID {
			before = baseline
		}
		if got := f.wakes(id); got != before+2 || len(w1hNoticesWithID(t, f.al, f.parent, id)) != 1 {
			t.Errorf("untaken notice %q must re-ring once per pass without duplicate work/append: wakes=%d want=%d", id, got, before+2)
		}
	}
	if err := f.al.GetMessageInboxStore().Ack(f.parent, wantIDs); err != nil {
		t.Fatalf("take real canonical notices: %v", err)
	}
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("boot after taking notices: %v", err)
	}
	for _, id := range wantIDs {
		before := 0
		if id == historicalID {
			before = baseline
		}
		if got := f.wakes(id); got != before+2 {
			t.Errorf("taken notice %q rang again: got=%d want=%d", id, got, before+2)
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
			wantEligible := name != "ordinary_nonfatal"
			if err != nil || class.WakeEligible != wantEligible || class.Fatal || class.Kind != "error" {
				t.Fatalf("SETUP: negative input does not reach the intended class: %+v err=%v", class, err)
			}
			baseline := f.wakes(bad.MessageId)
			var operatorNotices []string
			recovery := u1BootRecovery(t, f.al, &operatorNotices)
			if err := recovery.Run(context.Background()); err != nil {
				t.Fatalf("actual full boot: %v", err)
			}
			assertBootNoticeCurrentRunStoppedWithoutDispatch(t, f)
			assertBootNoticeNoFatalFinal(t, f)
			cause := bad.MessageId + " does not match its landed transition's canonical message"
			if name == "text_prefix_only" || name == "id_prefix_unledgered_seq_zero" {
				cause = bad.MessageId + " has no matching landed transition"
			}
			want := fmt.Sprintf("session %s stopped-notice replay refused: steer: stopped notice: %s", f.resumed.SessionID, cause)
			if wantEligible && !slices.Contains(operatorNotices, want) {
				t.Errorf("lookalike %q did not return its precise authenticated-domain refusal: want %q; got %q", name, want, operatorNotices)
			}
			if !wantEligible {
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
