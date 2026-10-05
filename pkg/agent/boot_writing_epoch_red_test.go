package agent

// Independent oracles: frozen ADR-20260928 D2/D4/D8.3/D8.5, the October 4
// amendment C3/C5, and BOOT-EPOCH-RULING.md. IDs come only from real admission.
// This pack targets the production carrier seam 5c2c7e6a3, not a QA production edit.
import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestSteerBootRecovery_WritingEpochAndRestartActor(t *testing.T) {
	for _, mode := range []string{"registered", "target_unregistered", "loopless_minted_store"} {
		t.Run(mode, func(t *testing.T) {
			f := newBootNoticeControlFixture(t)
			reopenBootNoticeControlStores(t, f)
			before := bootWritingEpochFile(t, f)
			if f.writingBoot.Current() != f.bootSeq || f.bootSeq <= f.resumed.ExecutionID.BootSeq {
				t.Fatalf("SETUP: genuine next writing boot=%d current=%d, interrupted admitting boot=%d", f.bootSeq, f.writingBoot.Current(), f.resumed.ExecutionID.BootSeq)
			}
			if mode == "target_unregistered" {
				if !f.al.GetRegistry().RemoveAgent(f.resumed.AgentID) {
					t.Fatal("SETUP: actual admitted target profile was not removed")
				}
				if _, exists := f.al.GetRegistry().GetAgent(f.resumed.AgentID); exists {
					t.Fatal("SETUP: removed target remains registered")
				}
			}
			var notices []string
			recovery := u1BootRecovery(t, f.al, &notices)
			if recovery.BootEpoch != f.writingBoot {
				t.Fatal("SETUP: recovery did not retain the exact minted writing-boot store")
			}
			if mode == "loopless_minted_store" {
				// Genuine public recovery constructor has no loop dependency.
				// The separate notice publisher may refuse its missing live loop;
				// that cannot waive the real restart writer's epoch/actor obligation.
				recovery.Deliverer = NewSteerUpwardDeliverer()
			}
			if err := recovery.Run(context.Background()); err != nil {
				t.Fatalf("actual full recovery: %v", err)
			}
			transitions := assertBootNoticeCurrentRunStoppedWithoutDispatch(t, f)
			assertBootNoticeNoFatalFinal(t, f)
			if mode != "loopless_minted_store" {
				if len(notices) != 0 {
					t.Errorf("actual boot emitted an unrelated refusal: %q", notices)
				}
				if f.wakes(messageIDOf(f.canonical)) != 2 {
					t.Errorf("C5: historical A notice did not independently re-ring: %d, want original+boot=2", f.wakes(messageIDOf(f.canonical)))
				}
			}
			bootAssertInterruptedLedgerIdentity(t, f, transitions[1])
			bootAssertEpochUnchanged(t, f, before)
		})
	}
}

func TestSteerBootRecovery_MissingWritingEpochRefusesBeforeAnyWrite(t *testing.T) {
	for _, mode := range []string{"nil_without_history", "unminted_existing_counter", "nil_with_historical_notice"} {
		t.Run(mode, func(t *testing.T) {
			f := newBootNoticeControlFixture(t)
			if mode != "nil_with_historical_notice" {
				// A distinct actual goal run with no prior stop history forces the
				// full caller's legacy fatal fallback, not just the writer check.
				child := launchGoalBearingChild(t, f.al, f.parent, "boot-missing-epoch-live-run")
				f.resumed = child
			}
			reopenBootNoticeControlStores(t, f)
			before := rootReopenedRecord(t, f.al, f.resumed.SessionID)
			goalBefore := mustGoalRecord(t, before.GoalRef)
			ledgerBefore := bootLedgerBytes(t, f)
			epochBefore := bootWritingEpochFile(t, f)
			historicalBefore := f.wakes(messageIDOf(f.canonical))
			inboxBefore, inboxErr := f.al.GetMessageInboxStore().Entries(f.parent)
			if inboxErr != nil {
				t.Fatalf("read actual parent inbox before refusal: %v", inboxErr)
			}
			f.provider.mu.Lock()
			callsBefore := f.provider.next
			f.provider.mu.Unlock()
			var notices []string
			recovery := u1BootRecovery(t, f.al, &notices)
			recovery.BootEpoch = nil
			if mode == "unminted_existing_counter" {
				recovery.BootEpoch = session.NewBootEpochStore(filepath.Join(f.al.GetConfig().Agents.Defaults.Home, "boot_epoch"))
				if recovery.BootEpoch.Current() != 0 {
					t.Fatal("SETUP: newly constructed unminted instance must have Current zero")
				}
			}
			// Deliberately leave the loop's valid epoch wired: it is NOT the
			// recovery carrier and cannot rescue a nil/unminted dependency.
			if err := recovery.Run(context.Background()); err == nil {
				t.Error("required-current-boot refusal must reach the full recovery/BootHook error channel, got nil")
			} else if !strings.Contains(err.Error(), "boot") || !strings.Contains(err.Error(), "epoch") {
				t.Errorf("full recovery returned an unrelated error instead of the required current boot epoch: %v", err)
			}
			// The architect requires a visible per-record required-current-boot
			// refusal. Its exact copy/sentinel is not approved yet; do not invent
			// one or derive it from observed output. These semantic fields come
			// from that requirement and the existing per-record notice channel.
			count := 0
			for _, notice := range notices {
				if strings.Contains(notice, before.SessionID) && strings.Contains(notice, "refus") && strings.Contains(notice, "boot") && strings.Contains(notice, "epoch") {
					count++
				}
			}
			if count != 1 {
				t.Errorf("required-current-boot refusal must be surfaced exactly once for actual session %s, got %q", before.SessionID, notices)
			}
			if after := rootReopenedRecord(t, f.al, before.SessionID); !reflect.DeepEqual(after, before) {
				t.Errorf("missing writing epoch mutated actual lifecycle/note/state before refusal: before=%+v after=%+v", before, after)
			}
			if after := bootLedgerBytes(t, f); !bytes.Equal(after, ledgerBefore) {
				t.Errorf("missing writing epoch appended a control/landed transition: before=%s after=%s", ledgerBefore, after)
			}
			if after := mustGoalRecord(t, before.GoalRef); !reflect.DeepEqual(after, goalBefore) || after.State != generated.GoalStateActive {
				t.Errorf("missing writing epoch altered the actual goal: before=%+v after=%+v", goalBefore, after)
			}
			assertBootNoticeNoFatalFinal(t, f)
			inboxAfter, inboxErr := f.al.GetMessageInboxStore().Entries(f.parent)
			if inboxErr != nil || !reflect.DeepEqual(bootMessagesForChild(t, inboxAfter, before.SessionID), bootMessagesForChild(t, inboxBefore, before.SessionID)) {
				t.Errorf("required-epoch refusal must not deliver a new envelope for the refused current record: before=%+v after=%+v err=%v", inboxBefore, inboxAfter, inboxErr)
			}
			f.provider.mu.Lock()
			callsAfter := f.provider.next
			f.provider.mu.Unlock()
			if callsAfter != callsBefore {
				t.Errorf("boot dispatched model calls after refusal: before=%d after=%d", callsBefore, callsAfter)
			}
			if mode == "nil_with_historical_notice" && f.wakes(messageIDOf(f.canonical)) != historicalBefore+1 {
				t.Errorf("C5: current-write refusal concealed historical notice replay: before=%d after=%d", historicalBefore, f.wakes(messageIDOf(f.canonical)))
			}
			if f.al.steerAdmission().activeCount() != 0 || f.al.steerAdmission().queueLen() != 0 || f.al.getActiveTurnState(before.SessionID) != nil {
				t.Error("missing writing epoch admitted or resumed work")
			}
			bootAssertEpochUnchanged(t, f, epochBefore)
		})
	}
}

func bootMessagesForChild(t *testing.T, entries []session.InboxEntry, child string) [][]byte {
	t.Helper()
	var messages [][]byte
	for _, entry := range entries {
		if entry.Message == nil {
			continue
		}
		raw := bootNoticeMessageBytes(t, *entry.Message)
		if u1DecodeMessageFields(t, raw)["session_id"] == child {
			messages = append(messages, raw)
		}
	}
	return messages
}

func bootWritingEpochFile(t *testing.T, f *bootNoticeControlFixture) []byte {
	t.Helper()
	path := filepath.Join(f.al.GetConfig().Agents.Defaults.Home, "boot_epoch", "boot_epoch.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read real persisted writing epoch: %v", err)
	}
	var file struct {
		BootEpoch uint64 `json:"boot_epoch"`
	}
	if err := json.Unmarshal(raw, &file); err != nil || file.BootEpoch != f.writingBoot.Current() {
		t.Fatalf("persisted/current writing epoch=%d/%d err=%v", file.BootEpoch, f.writingBoot.Current(), err)
	}
	return raw
}

func bootAssertEpochUnchanged(t *testing.T, f *bootNoticeControlFixture, before []byte) {
	t.Helper()
	if after := bootWritingEpochFile(t, f); !bytes.Equal(after, before) || f.writingBoot.Current() != f.bootSeq {
		t.Errorf("recovery minted/reopened/substituted the boot counter: before=%s after=%s current=%d want=%d", before, after, f.writingBoot.Current(), f.bootSeq)
	}
}

func bootLedgerBytes(t *testing.T, f *bootNoticeControlFixture) []byte {
	t.Helper()
	path := filepath.Join(f.al.GetConfig().Agents.Defaults.Home, "session_lifecycle", "controls", f.resumed.SessionID+".jsonl")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read actual control ledger: %v", err)
	}
	return raw
}

func bootAssertInterruptedLedgerIdentity(t *testing.T, f *bootNoticeControlFixture, tr session.StoppedTransition) {
	t.Helper()
	var found []struct {
		RunID   string
		BootSeq uint64
		Actor   string
	}
	for _, line := range bytes.Split(bootLedgerBytes(t, f), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var row struct {
			Seq    uint64 `json:"seq"`
			Landed *struct {
				RunID   string `json:"run_id"`
				BootSeq uint64 `json:"boot_seq"`
				Actor   string `json:"actor"`
			} `json:"landed_stop"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			t.Fatalf("decode actual ledger: %v", err)
		}
		if row.Seq == tr.StopSeq && row.Landed != nil {
			found = append(found, struct {
				RunID   string
				BootSeq uint64
				Actor   string
			}{row.Landed.RunID, row.Landed.BootSeq, row.Landed.Actor})
		}
	}
	want := []struct {
		RunID   string
		BootSeq uint64
		Actor   string
	}{{f.resumed.ExecutionID.RunID, f.resumed.ExecutionID.BootSeq, session.StopActorRestart}}
	if !slices.Equal(found, want) {
		t.Errorf("D8/C-pure: ledger must retain interrupted run/admitting boot but use restart actor: got=%+v want=%+v", found, want)
	}
}
