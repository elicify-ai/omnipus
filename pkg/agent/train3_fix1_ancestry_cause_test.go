package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Gate round 1, silent-failure SF-05: a helper whose ancestry cannot be read
// is refused like a task run (amendment R1, fail toward the visible refusal),
// but the cause — the acting session, the ancestor that failed and why — must
// be logged, not lost behind the fixed task/plan reason.
func TestTrain3Fix1_UnresolvedAncestryLogsItsCause(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	sessions := al.GetSessionStore()
	lifecycle := al.GetSessionLifecycleStore()
	if sessions == nil || lifecycle == nil {
		t.Fatal("fixture: the test loop must have session and lifecycle stores")
	}

	// An interactive parent chat whose lifecycle record is missing, and a
	// helper below it with a valid delegate record.
	parent := newTestSteeringSession(t, al, "")
	child, err := sessions.NewSession(session.SessionTypeDelegate, "webchat", testDefaultAgentID)
	if err != nil {
		t.Fatalf("NewSession(child): %v", err)
	}
	if err = sessions.SetMeta(child.ID, session.MetaPatch{ParentSessionID: &parent}); err != nil {
		t.Fatalf("SetMeta(parent): %v", err)
	}
	if err = lifecycle.Persist(&session.LifecycleRecord{
		SessionID: child.ID, Generation: 1, State: session.LifecycleRunning,
		AgentID: testDefaultAgentID, OwnerScopeKind: session.OwnerScopeHuman,
		Origin: &session.Origin{Kind: session.OriginKindDelegate},
	}); err != nil {
		t.Fatalf("persist child record: %v", err)
	}

	logPath := filepath.Join(t.TempDir(), "agent.log")
	if err = logger.EnableFileLogging(logPath); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	defer logger.DisableFileLogging()

	opts := processOptions{TranscriptSessionID: child.ID}
	al.applyTurnAttendance(&opts)
	if !opts.AutoDenyAsk || opts.askRefusalReason != autoDenyTaskPlanReason {
		t.Fatalf("unresolved ancestry must refuse like a task run: AutoDenyAsk=%v reason=%q",
			opts.AutoDenyAsk, opts.askRefusalReason)
	}

	logger.DisableFileLogging()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	text := string(data)
	for _, want := range []string{child.ID, parent, "lifecycle"} {
		if !strings.Contains(text, want) {
			t.Errorf("the unresolved-ancestry log must name %q; log was:\n%s", want, text)
		}
	}
}
