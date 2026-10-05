package agent

// D9's actual helper identity read must distinguish a genuinely absent root
// from an EXISTING unreadable lifecycle journal. All valid setup is admitted
// through Launch/Dispatch and stopped through the production selected callback.
// Corruption/permission faults are physical I/O; no store/identity is mocked.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/commands"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func TestD9_HelperIdentityRead_DistinguishesAbsentAndExistingUnreadableJournal(t *testing.T) {
	for _, mode := range []string{"valid_helper", "last_valid_with_torn_tail", "absent_root", "all_lines_corrupt", "physical_read_denied"} {
		t.Run(mode, func(t *testing.T) { d9IdentityReadCase(t, mode) })
	}
}
func d9IdentityReadCase(t *testing.T, mode string) {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	if mode == "absent_root" {
		rootID := newTestSteeringSession(t, al, "ws-d9-absent")
		if al.GetSessionLifecycleStore().Exists(rootID) {
			t.Fatal("SETUP: genuine absence control unexpectedly has lifecycle data")
		}
		_, loadErr := al.GetSessionLifecycleStore().Load(rootID)
		if !errors.Is(loadErr, session.ErrLifecycleNotFound) {
			t.Fatalf("absent owning store returned %v,want ErrLifecycleNotFound", loadErr)
		}
		helper, resolveErr := al.resolveHelperSession(rootID)
		if helper || resolveErr != nil {
			t.Errorf("genuine absent root helper=%v err=%v,want false,nil", helper, resolveErr)
		}
		return
	}
	gate := newGoalRunGate("goal helper before actual stopped read", nil)
	installGoalRunProvider(t, al, gate)
	parentID := newTestSteeringSession(t, al, "ws-d9-read-error")
	child := launchGoalBearingChild(t, al, parentID, "call-d9-real-read-helper", goalChildLaunchOptions{live: true})
	awaitGoalProvider(t, gate)
	report, stopErr := al.steerCanceller().StopTurns(context.Background(), child.SessionID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "read-owner"}, false, al.SteerGenerationCancel)
	if stopErr != nil || len(report.Unreachable) != 0 {
		t.Fatalf("real selected setup Stop: %+v err=%v", report, stopErr)
	}
	joinGoalFixtureRuns(t, al)
	valid := rootReopenedRecord(t, al, child.SessionID)
	if valid.State != session.LifecycleStopped || valid.Stop != nil || valid.ExecutionID == nil {
		t.Fatal("SETUP: real admitted helper did not actually settle before I/O fault")
	}
	journal := filepath.Join(al.GetConfig().Agents.Defaults.Home, "session_lifecycle", child.SessionID+".jsonl")
	original, readErr := os.ReadFile(journal)
	if readErr != nil || len(original) == 0 {
		t.Fatalf("read real journal before I/O fault: bytes=%d err=%v", len(original), readErr)
	}
	t.Cleanup(func() {
		if chmodErr := os.Chmod(journal, 0o600); chmodErr != nil {
			t.Errorf("restore real journal mode: %v", chmodErr)
		}
		if restoreErr := os.WriteFile(journal, original, 0o600); restoreErr != nil {
			t.Errorf("restore original genuine journal bytes: %v", restoreErr)
		}
	})
	switch mode {
	case "last_valid_with_torn_tail":
		corrupt := append(append([]byte(nil), original...), []byte("{incomplete-tail")...)
		if writeErr := os.WriteFile(journal, corrupt, 0o600); writeErr != nil {
			t.Fatalf("install actual torn tail: %v", writeErr)
		}
	case "all_lines_corrupt":
		if writeErr := os.WriteFile(journal, []byte("{invalid-lifecycle-json}\n"), 0o600); writeErr != nil {
			t.Fatalf("install actual all-lines corruption: %v", writeErr)
		}
	case "physical_read_denied":
		if chmodErr := os.Chmod(journal, 0o000); chmodErr != nil {
			t.Fatalf("install real read-denial: %v", chmodErr)
		}
		f, openErr := os.Open(journal)
		if openErr == nil {
			if closeErr := f.Close(); closeErr != nil {
				t.Errorf("close read-denial probe: %v", closeErr)
			}
			t.Fatal("BLOCKED: environment bypasses real read permissions; instrument cannot prove D9 I/O refusal")
		}
		if !errors.Is(openErr, os.ErrPermission) {
			t.Fatalf("read-denial instrument error=%v,want permission error", openErr)
		}
	}
	if !al.GetSessionLifecycleStore().Exists(child.SessionID) {
		t.Fatal("instrument: physical existing journal incorrectly disappeared")
	}
	_, loadErr := al.GetSessionLifecycleStore().Load(child.SessionID)
	helper, resolveErr := al.resolveHelperSession(child.SessionID)
	healthy := mode == "valid_helper" || mode == "last_valid_with_torn_tail"
	if healthy {
		if loadErr != nil || !helper || resolveErr != nil {
			t.Errorf("valid/last-valid helper read=%v helper=%v err=%v,want nil,true,nil", loadErr, helper, resolveErr)
		}
		return
	}
	if loadErr == nil || errors.Is(loadErr, session.ErrLifecycleNotFound) {
		t.Errorf("EXISTING unreadable lifecycle classified as absent/usable: Load=%v Exists=true; must return a real read/corruption error", loadErr)
	}
	if helper || resolveErr == nil || errors.Is(resolveErr, commands.ErrNotHelperSession) {
		t.Errorf("true D9 helper resolver hid existing read failure as absent root: helper=%v err=%v,want visible non-root read error", helper, resolveErr)
	}
	d9AssertRegisteredReadFailure(t, al, child.SessionID, resolveErr)
}

func d9AssertRegisteredReadFailure(t *testing.T, al *AgentLoop, childID string, resolveErr error) {
	t.Helper()
	instance, found := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !found {
		t.Fatal("SETUP: real D9 agent missing")
	}
	probe := &rootMessageProbe{gate: newGoalRunGate("must not start on unreadable saved state", nil)}
	instance.Provider = probe
	t.Cleanup(probe.gate.open)
	// Real registered command -> production runtime builder -> true error-bearing
	// helper resolver -> existing redirect handler. No boolean fake hides errors.
	opts := processOptions{SessionKey: childID, TranscriptSessionID: childID, TranscriptStore: al.GetSessionStore()}
	reply, handled := al.handleCommand(context.Background(), bus.InboundMessage{Channel: "webchat", ChatID: childID, SessionID: childID, Sender: bus.SenderInfo{CanonicalID: "read-owner"}, GatewayUserID: "read-owner", UserInitiated: true, Content: "/stop-redirect continue the existing work"}, instance, &opts)
	if !handled {
		t.Fatal("BLOCKED: actual registered D9 /stop-redirect was not handled — preserved own command is required")
	}
	if !strings.HasPrefix(reply, "Redirect request failed: ") || reply == commands.StopRedirectRootRefusal {
		t.Errorf("existing unreadable helper got root guidance instead of true read-error reply: %q", reply)
	}
	if resolveErr != nil && !strings.Contains(reply, resolveErr.Error()) {
		t.Errorf("actual D9 command lost real helper read cause: reply=%q cause=%v", reply, resolveErr)
	}
	if probe.calls.Load() != 0 || al.getActiveTurnState(childID) != nil {
		t.Errorf("D9 unreadable-helper command started a real model turn: calls=%d", probe.calls.Load())
	}
}
