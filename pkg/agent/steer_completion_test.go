package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// depthEchoProvider returns the last "user" role message content verbatim as
// its final answer. Used by TestCompletion_LastChildCompletesWaitingParent
// (the finding-A/B exit proof) so a re-entered turn's real output is
// traceably derived from what it was actually asked — never a value the
// test invents — proving the chain carries the CHILD's real result, not a
// stale value read off the parent's own prior transcript.
type depthEchoProvider struct{}

func (p *depthEchoProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	var last string
	for _, m := range messages {
		if m.Role == "user" {
			last = m.Content
		}
	}
	return &providers.LLMResponse{Content: last}, nil
}

func (p *depthEchoProvider) GetDefaultModel() string { return "depth-echo-test" }

// newSteerALWithProvider is newSteerAL, plus injecting a caller-chosen
// provider for testDefaultAgentID instead of the default mockProvider —
// needed to drive a REAL re-entered turn (steer_launcher.go::
// steeredTurnRunContext / loop_inbound.go::processSteeredSystemWake)
// through al.runTurn and observe its actual output.
func newSteerALWithProvider(t *testing.T, provider providers.LLMProvider) (*AgentLoop, func()) {
	t.Helper()
	tmpDir := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: testDefaultAgentID, Home: tmpDir}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	home := al.GetConfig().Agents.Defaults.Home
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "session_lifecycle"))
	inbox := session.NewMessageInboxStore(filepath.Join(home, "session_messages"))
	al.SetSessionMessagingStores(inbox, lifecycle)
	return al, func() {}
}

type steeredInputCaptureProvider struct {
	once     sync.Once
	messages []providers.Message
	done     chan struct{}
}

func (p *steeredInputCaptureProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.once.Do(func() {
		p.messages = append([]providers.Message(nil), messages...)
		close(p.done)
	})
	return &providers.LLMResponse{Content: "child answer"}, nil
}

func (p *steeredInputCaptureProvider) GetDefaultModel() string { return "steered-input-capture" }

func wireSteerCompletionDeps(t *testing.T, al *AgentLoop) {
	t.Helper()
	classifier := NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())
	al.SetSteerAudienceDeps(
		NewSteerAudienceResolver(classifier),
		steer.NopBoundaryObserver{},
		NewSteerUpwardDeliverer(),
	)
}

func launchRunningChild(t *testing.T, al *AgentLoop, parentID, callID string) *session.LifecycleRecord {
	t.Helper()
	res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID,
		TargetAgentID:     testDefaultAgentID,
		Task:              "do delegated work",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	rec, err := al.GetSessionLifecycleStore().Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	rec.State = session.LifecycleRunning
	if err := al.GetSessionLifecycleStore().Persist(rec); err != nil {
		t.Fatalf("Persist(running): %v", err)
	}
	return rec
}

func TestCompletion_Disposition_PersistedAndValidated(t *testing.T) {
	tests := []struct {
		name       string
		answer     string
		turnFailed bool
		withQueued bool
		wantState  session.LifecycleState
		wantKind   string
		wantText   string
		wantFatal  bool
	}{
		{name: "non-empty quiet", answer: "finished", wantState: session.LifecycleCompleted, wantKind: "handback"},
		{name: "empty quiet", answer: "   ", wantState: session.LifecycleFailed, wantKind: "error", wantText: "empty_answer:", wantFatal: true},
		// ADR-091 fix lane RX-HANG: state/kind/text/fatal are UNCHANGED by
		// this lane's fix — the child stays resumable (LifecycleRunning,
		// fatal: false is still correct; this is not a crash). What
		// changed is WHETHER the parent is woken by this exact message,
		// which this table does not assert — see
		// TestCompletion_IterationLimit_NonFatal_WakesParent for that
		// (deliberately flipped) pinned expectation.
		{name: "iteration limit is a non-fatal lifecycle notice", answer: toolLimitResponse, turnFailed: true, wantState: session.LifecycleRunning, wantKind: "error", wantText: "max_tool_iterations:", wantFatal: false},
		{name: "non-empty queued descendant", answer: "parent answer", withQueued: true, wantState: session.LifecycleRunning},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			al, cleanup := newSteerAL(t)
			defer cleanup()
			wireSteerCompletionDeps(t, al)
			parentID := newTestSteeringSession(t, al, "ws-1")
			rec := launchRunningChild(t, al, parentID, "call-complete")
			if tc.withQueued {
				if _, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
					SteeringSessionID: rec.SessionID,
					TargetAgentID:     testDefaultAgentID,
					Task:              "queued grandchild",
					Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-grandchild"},
				}); err != nil {
					t.Fatalf("Launch(grandchild): %v", err)
				}
			}

			al.completeSteeredTurn(context.Background(), rec, turnResult{finalContent: tc.answer, turnFailed: tc.turnFailed}, nil)

			got, err := al.GetSessionLifecycleStore().Load(rec.SessionID)
			if err != nil {
				t.Fatalf("Load(after completion): %v", err)
			}
			if got.State != tc.wantState {
				t.Fatalf("state = %q, want %q", got.State, tc.wantState)
			}
			msgs, _, _, err := al.GetMessageInboxStore().Drain(parentID, rec.SessionID, "", 10)
			if err != nil {
				t.Fatalf("Drain(parent): %v", err)
			}
			if tc.wantKind == "" {
				if len(msgs) != 0 {
					t.Fatalf("messages = %d, want none while descendant is queued", len(msgs))
				}
				return
			}
			if len(msgs) != 1 {
				t.Fatalf("messages = %d, want 1", len(msgs))
			}
			kind, err := msgs[0].Discriminator()
			if err != nil || kind != tc.wantKind {
				t.Fatalf("message kind = %q (%v), want %q", kind, err, tc.wantKind)
			}
			if tc.wantText != "" {
				v, err := msgs[0].AsSessionMessageError()
				if err != nil || !strings.Contains(v.Text, tc.wantText) {
					t.Fatalf("error message = %q (%v), want contains %q", v.Text, err, tc.wantText)
				}
				if v.Fatal != tc.wantFatal {
					t.Fatalf("error fatal = %v, want %v", v.Fatal, tc.wantFatal)
				}
			}
		})
	}
}

// TestCompletion_IterationLimit_NonFatal_WakesParent covers landing order
// §2 I-5's "max-iterations lifecycle notice" row: a steered child whose
// turn ends at the tool-iteration ceiling (loop_run_turn.go::finalizeTurn
// sets finalContent to the toolLimitResponse sentinel and marks the turn
// failed) is delivered to its parent as an `error` entry with `fatal: false` —
// a notice that the child stopped early, not a crash.
//
// ADR-091 fix lane RX-HANG (deliberate behavior change, round-2 fix): this
// test used to be named …_DoesNotWakeParent and pinned the OLD, defective
// rule that a non-fatal error never wakes the parent — for THIS specific
// outcome, that meant the parent was never told the child had stopped at
// all, hasRunningOrQueuedDescendant then saw the child `running` forever,
// and the whole ancestor chain hung silently up to the user's chat with no
// log line and no UI signal (recoverable only by a gateway restart). The
// parent MUST be woken so it can decide to retry, raise the tool-iteration
// budget, or give up — Fatal stays false (see below) so the child does not
// look dead; a genuine failure is still the contrast: `fatal: true` and the
// parent is woken, as before.
func TestCompletion_IterationLimit_NonFatal_WakesParent(t *testing.T) {
	tests := []struct {
		name      string
		result    turnResult
		runErr    error
		wantFatal bool
		wantWake  bool
		wantText  string
		wantState session.LifecycleState
	}{
		{
			name:      "iteration limit produces a non-fatal notice and DOES wake the parent",
			result:    turnResult{finalContent: toolLimitResponse, turnFailed: true},
			wantFatal: false,
			wantWake:  true,
			wantText:  "max_tool_iterations:",
			wantState: session.LifecycleRunning,
		},
		{
			name:      "genuine failure is fatal and wakes the parent",
			runErr:    errors.New("downstream provider failure"),
			wantFatal: true,
			wantWake:  true,
			wantText:  "failed:",
			wantState: session.LifecycleFailed,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			al, cleanup := newSteerAL(t)
			defer cleanup()
			wireSteerCompletionDeps(t, al)
			parentID := newTestSteeringSession(t, al, "ws-1")
			rec := launchRunningChild(t, al, parentID, "call-"+tc.name)

			// The plain webchat parent carries an empty PeerID, so the wake
			// destination would be empty and WakeParentAlways would refuse.
			// Give the child's reporting target a routable address so the
			// wake-eligibility contrast is genuinely observable.
			if err := al.GetSessionLifecycleStore().Mutate(rec.SessionID, func(r *session.LifecycleRecord) error {
				r.SteeredBy.ReportingTarget = session.ReportingTarget{Channel: "webchat", ChatID: parentID}
				return nil
			}); err != nil {
				t.Fatalf("Mutate(reporting target): %v", err)
			}

			var wakes []string
			al.asyncNotifier.registerObserver(func(e AsyncNotifyEvent) {
				if strings.HasPrefix(e.SourceKind, "message_parent:") {
					wakes = append(wakes, e.SourceKind)
				}
			})

			if err := al.completeSteeredTurn(context.Background(), rec, tc.result, tc.runErr); err != nil {
				t.Fatalf("completeSteeredTurn: %v", err)
			}

			msgs, _, _, err := al.GetMessageInboxStore().Drain(parentID, rec.SessionID, "", 10)
			if err != nil {
				t.Fatalf("Drain(parent): %v", err)
			}
			if len(msgs) != 1 {
				t.Fatalf("parent messages = %d, want 1", len(msgs))
			}
			kind, err := msgs[0].Discriminator()
			if err != nil || kind != "error" {
				t.Fatalf("message kind = %q (%v), want error", kind, err)
			}
			e, err := msgs[0].AsSessionMessageError()
			if err != nil {
				t.Fatalf("AsSessionMessageError: %v", err)
			}
			if e.Fatal != tc.wantFatal {
				t.Fatalf("error fatal = %v, want %v", e.Fatal, tc.wantFatal)
			}
			if !strings.Contains(e.Text, tc.wantText) {
				t.Fatalf("error text = %q, want contains %q", e.Text, tc.wantText)
			}

			if gotWake := len(wakes) > 0; gotWake != tc.wantWake {
				t.Fatalf("parent woke = %v (sources %v), want %v", gotWake, wakes, tc.wantWake)
			}

			got, err := al.GetSessionLifecycleStore().Load(rec.SessionID)
			if err != nil {
				t.Fatalf("Load(child after completion): %v", err)
			}
			if got.State != tc.wantState {
				t.Fatalf("child state = %q, want %q", got.State, tc.wantState)
			}
		})
	}
}

// TestCompletion_LastChildCompletesWaitingParent is ADR-091 fix lane 1's
// finding-A/B exit proof: a REAL two-hop delegation (root -> waitingParent
// -> lastChild) where the grandchild's result must reach the grandparent
// (root) — not a stale value read off the parent's own prior answer — and
// every non-parked, non-root session lands terminal.
//
// Finding A (CRITICAL, the release blocker): lastChild's own
// SteeredBy.ReportingTarget used to be EMPTY whenever its steering session
// (waitingParent) had no real external Channel/PeerID of its own — true for
// every steered session, since a steered session's own identity is minted
// with no channel and is never given a PeerID at all
// (steer_launcher.go::reportingTargetFor's doc comment). async_notifier.go's
// WakeParentAlways refused that empty destination outright, so
// waitingParent was never re-entered, and the now-DELETED
// completeWaitingAncestors shortcut silently substituted waitingParent's
// OWN stale last answer instead of the real one. This test seeds exactly
// that stale text (staleText, below) to prove it is never read again.
//
// Finding B (CRITICAL, latent): once A is fixed, the wake that reaches
// waitingParent must itself run a real turn AND complete on that same exit
// path — loop_inbound.go::processSteeredSystemWake used to return after
// running the woken turn without ever calling completeSteeredTurn/
// finishSteeredGoalTurn, leaving a successfully re-entered session `running`
// forever.
func TestCompletion_LastChildCompletesWaitingParent(t *testing.T) {
	al, cleanup := newSteerALWithProvider(t, &depthEchoProvider{})
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-1")
	waitingParent := launchRunningChild(t, al, rootID, "call-parent")
	const staleText = "STALE PARENT TEXT MUST NOT REACH ROOT"
	if err := al.GetSessionStore().AppendTranscriptStrict(waitingParent.SessionID, session.TranscriptEntry{
		ID: "parent-answer", Role: "assistant", Content: staleText, Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("AppendTranscriptStrict(parent answer): %v", err)
	}
	lastChild := launchRunningChild(t, al, waitingParent.SessionID, "call-child")
	parked := launchRunningChild(t, al, waitingParent.SessionID, "call-parked")
	parked.State = session.LifecycleNeedsInput
	parked.NeedsInput = &session.NeedsInput{CorrelationID: "q-1", Reconstructable: true}
	if err := al.GetSessionLifecycleStore().Persist(parked); err != nil {
		t.Fatalf("Persist(parked): %v", err)
	}
	var question generated.SessionMessage
	if err := question.FromSessionMessageQuestion(generated.SessionMessageQuestion{
		Kind: generated.SessionMessageQuestionKindQuestion, MessageId: "q-1", SessionId: parked.SessionID,
		SenderIdentity: parked.AgentID, CreatedAt: time.Now().UTC(), Depth: 1, Text: "Which region?",
	}); err != nil {
		t.Fatalf("encode question: %v", err)
	}
	if _, err := al.GetMessageInboxStore().Append(waitingParent.SessionID, question); err != nil {
		t.Fatalf("Append(question): %v", err)
	}

	// The grandchild completes for real — this is where Finding A's fix is
	// exercised: lastChild's ReportingTarget (stamped at launch under
	// waitingParent, which has no real external address of its own) must
	// still let the wake below actually reach waitingParent.
	if err := al.completeSteeredTurn(context.Background(), lastChild, turnResult{finalContent: "child result"}, nil); err != nil {
		t.Fatalf("completeSteeredTurn(lastChild): %v", err)
	}

	gotChild, err := al.GetSessionLifecycleStore().Load(lastChild.SessionID)
	if err != nil {
		t.Fatalf("Load(lastChild): %v", err)
	}
	if gotChild.State != session.LifecycleCompleted {
		t.Fatalf("lastChild state = %q, want completed", gotChild.State)
	}

	// waitingParent must NOT be silently completed by any shortcut — it
	// stays `running` until it is genuinely re-entered by the wake below
	// (proves completeWaitingAncestors is really gone, not just unreachable
	// on this path by coincidence).
	gotParentBefore, err := al.GetSessionLifecycleStore().Load(waitingParent.SessionID)
	if err != nil {
		t.Fatalf("Load(waitingParent) before its wake: %v", err)
	}
	if gotParentBefore.State != session.LifecycleRunning {
		t.Fatalf("waitingParent state before its own wake ran = %q, want running (nothing may complete it early)", gotParentBefore.State)
	}

	// Drive waitingParent's real re-entry: read the wake lastChild's
	// completion enqueued on the bus and feed it through the SAME
	// processSystemMessage entry point production uses (this test's
	// AgentLoop never started Run's own InboundChan consumer loop).
	var wake bus.InboundMessage
	select {
	case wake = <-al.bus.InboundChan():
	case <-time.After(5 * time.Second):
		t.Fatal("lastChild's completion never woke waitingParent — Finding A's ReportingTarget fix did not take effect")
	}
	if _, err := al.processSystemMessage(context.Background(), wake); err != nil {
		t.Fatalf("processSystemMessage(wake waitingParent): %v", err)
	}

	// Finding B: waitingParent's own turn must have completed on this exit
	// path too — not left `running` forever after a successful re-entry.
	got, err := al.GetSessionLifecycleStore().Load(waitingParent.SessionID)
	if err != nil {
		t.Fatalf("Load(waiting parent): %v", err)
	}
	if got.State != session.LifecycleCompleted {
		t.Fatalf("waiting parent state after its wake ran = %q, want completed", got.State)
	}

	msgs, _, _, err := al.GetMessageInboxStore().Drain(rootID, waitingParent.SessionID, "", 10)
	if err != nil {
		t.Fatalf("Drain(root): %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("root messages = %d, want 1", len(msgs))
	}
	handback, err := msgs[0].AsSessionMessageHandback()
	if err != nil {
		t.Fatalf("handback: %v", err)
	}
	// depthEchoProvider makes waitingParent's real final answer exactly the
	// wake content it was re-entered with, which carries the GRANDCHILD's
	// real result — never the stale text seeded above.
	if !strings.Contains(handback.ResultSoFar, "child result") {
		t.Fatalf("root's result_so_far = %q, want it to contain the grandchild's real result %q", handback.ResultSoFar, "child result")
	}
	if strings.Contains(handback.ResultSoFar, staleText) {
		t.Fatalf("root's result_so_far = %q leaked waitingParent's OWN stale pre-existing answer instead of a real re-entry", handback.ResultSoFar)
	}
	if len(handback.OpenQuestions) != 1 || handback.OpenQuestions[0] != "Which region?" {
		t.Errorf("open_questions = %#v, want [Which region?]", handback.OpenQuestions)
	}

	// Nothing is left running: the grandchild and its parent both landed
	// terminal (the parked sibling is a legitimate, separate exemption —
	// its own coverage is TestCompletion_LastChildCompletesWaitingParent's
	// OpenQuestions assertion above, not a terminal-state one).
	for _, id := range []string{lastChild.SessionID, waitingParent.SessionID} {
		rec, err := al.GetSessionLifecycleStore().Load(id)
		if err != nil {
			t.Fatalf("Load(%s): %v", id, err)
		}
		if !rec.Terminal() {
			t.Errorf("session %s state = %q, want terminal (nothing may be left running)", id, rec.State)
		}
	}
}

// TestCompletion_ToolIterationLimit_WakesAndUnblocksParent is ADR-091 fix
// lane RX-HANG's exit proof: it is the SAME real two-hop shape as
// TestCompletion_LastChildCompletesWaitingParent above (root -> waitingParent
// -> lastChild, a genuine re-entered turn through al.processSystemMessage,
// never a mock of the completion or delivery path) but exercises the defect
// this lane fixes instead — lastChild does not finish with an answer, it
// exhausts its tool-iteration budget (steer.OutcomeLifecycleNotice).
//
// Before this lane's fix this test hangs the way the bug report describes:
// completeSteeredTurn(lastChild, ...) returns normally (the record stays
// `running`, by design — a lifecycle notice is not a crash), but the
// `error`/fatal:false inbox entry it delivers is not wake-eligible, so
// asyncNotifier never publishes anything to al.bus.InboundChan() — the read
// below times out. waitingParent is left `running` forever with a `running`
// descendant, exactly the ancestor-chain hang three independent reviewers
// and the lead confirmed at HEAD.
//
// After the fix: message_inbox.go::classifyEnvelope recognizes the
// "max_tool_iterations:" failureReason prefix as wake-eligible even though
// Fatal stays false, so the wake IS published; waitingParent is re-entered
// with a real turn (depthEchoProvider echoes the wake's own content back,
// so a non-empty answer is genuinely produced, not invented by the test);
// and steer_completion.go::hasRunningOrQueuedDescendant no longer counts
// lastChild's `running`-but-no-live-turn record as blocking, so
// waitingParent's own completeSteeredTurn call actually lands it
// LifecycleCompleted instead of returning nil forever. lastChild itself
// stays LifecycleRunning throughout (D7/the founder's decision: resumable,
// not dead) — this test's own assertion on that is the proof the "keep it
// resumable" intent survived the fix, not just "not blocking" in isolation.
func TestCompletion_ToolIterationLimit_WakesAndUnblocksParent(t *testing.T) {
	al, cleanup := newSteerALWithProvider(t, &depthEchoProvider{})
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-1")
	waitingParent := launchRunningChild(t, al, rootID, "call-parent")
	lastChild := launchRunningChild(t, al, waitingParent.SessionID, "call-child")

	// lastChild's turn ends at the tool-iteration ceiling — the exact
	// shape loop_run_turn.go::finalizeTurn produces (finalContent set to
	// the toolLimitResponse sentinel, turnFailed true) — never a normal
	// final answer.
	if err := al.completeSteeredTurn(context.Background(), lastChild, turnResult{finalContent: toolLimitResponse, turnFailed: true}, nil); err != nil {
		t.Fatalf("completeSteeredTurn(lastChild): %v", err)
	}

	gotChild, err := al.GetSessionLifecycleStore().Load(lastChild.SessionID)
	if err != nil {
		t.Fatalf("Load(lastChild): %v", err)
	}
	if gotChild.State != session.LifecycleRunning {
		t.Fatalf("lastChild state = %q, want running (a tool-iteration notice keeps the child resumable, not dead)", gotChild.State)
	}

	// The grandchild's notice must actually wake waitingParent — the exact
	// point this lane fixes. On unfixed code this read times out because
	// nothing was ever wake-eligible for a non-fatal error, and the whole
	// ancestor chain hangs silently forever with no log line and no UI
	// signal, exactly as the bug report describes.
	var wake bus.InboundMessage
	select {
	case wake = <-al.bus.InboundChan():
	case <-time.After(5 * time.Second):
		t.Fatal("lastChild's tool-iteration notice never woke waitingParent — the parent is never told anything happened and hangs forever (ADR-091 RX-HANG)")
	}
	if _, err := al.processSystemMessage(context.Background(), wake); err != nil {
		t.Fatalf("processSystemMessage(wake waitingParent): %v", err)
	}

	// waitingParent must actually LAND terminal — proving
	// hasRunningOrQueuedDescendant no longer blocks on lastChild's
	// `running`-but-idle record. On unfixed hasRunningOrQueuedDescendant
	// (even if the wake above were somehow delivered by another means)
	// this would stay `running` forever: the child never left `running`,
	// so the parent's own completeSteeredTurn call would keep returning
	// nil.
	got, err := al.GetSessionLifecycleStore().Load(waitingParent.SessionID)
	if err != nil {
		t.Fatalf("Load(waitingParent): %v", err)
	}
	if got.State != session.LifecycleCompleted {
		t.Fatalf("waitingParent state after its wake ran = %q, want completed (it must not stay blocked on a running-but-idle child)", got.State)
	}

	msgs, _, _, err := al.GetMessageInboxStore().Drain(rootID, waitingParent.SessionID, "", 10)
	if err != nil {
		t.Fatalf("Drain(root): %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("root messages = %d, want 1", len(msgs))
	}
	if kind, err := msgs[0].Discriminator(); err != nil || kind != "handback" {
		t.Fatalf("root message kind = %q (%v), want handback — the root must genuinely learn waitingParent completed", kind, err)
	}
}

func TestSubagentLifecycleFrames_StartQueuedRunningTerminalEndOrder(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	rec := launchRunningChild(t, al, parentID, "call-frame-order")
	al.deliverSubagentState(parentID, rec, string(session.LifecycleRunning))
	if err := al.completeSteeredTurn(context.Background(), rec, turnResult{finalContent: "done"}, nil); err != nil {
		t.Fatalf("completeSteeredTurn: %v", err)
	}

	entries, err := al.GetSessionStore().ReadTranscript(parentID)
	if err != nil {
		t.Fatalf("ReadTranscript(parent): %v", err)
	}
	var got []string
	for _, entry := range entries {
		switch entry.SystemSubtype {
		case session.SystemSubtypeSubagentStart:
			got = append(got, "start")
		case session.SystemSubtypeSubagentState:
			got = append(got, entry.SubagentState.State)
		case session.SystemSubtypeSubagentEnd:
			got = append(got, "end")
		}
	}
	want := []string{"start", "queued", "running", "completed", "end"}
	if len(got) != len(want) {
		t.Fatalf("frame order = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("frame order = %#v, want %#v", got, want)
		}
	}
}

func TestGoalDelegation_Judged(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)
	wireSteerCompletionDeps(t, al)

	parentMeta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
	if err != nil {
		t.Fatalf("NewSession(parent): %v", err)
	}
	res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentMeta.ID,
		TargetAgentID:     "native-agent",
		Task:              "prove the goal",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-judged"},
		Goal: &steer.GoalSpec{
			Criteria: []steer.Criterion{{Text: "the work is complete"}},
			DoD:      []steer.Criterion{{Text: "the evidence is sufficient"}},
		},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	rec, err := lifecycle.Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	rec.State = session.LifecycleRunning
	if err := lifecycle.Persist(rec); err != nil {
		t.Fatalf("Persist(running): %v", err)
	}

	g, err := resolveGoalRecordStore().Get(rec.GoalRef)
	if err != nil {
		t.Fatalf("Get(goal): %v", err)
	}
	type criterionVerdict struct {
		ID     string `json:"id"`
		Met    bool   `json:"met"`
		Reason string `json:"reason"`
	}
	verdicts := make([]criterionVerdict, 0, len(g.Criteria)+len(g.DoD))
	for _, criterion := range append(append([]task.AcceptanceCriterion{}, g.Criteria...), g.DoD...) {
		verdicts = append(verdicts, criterionVerdict{ID: criterion.ID, Met: true, Reason: "verified"})
	}
	body, err := json.Marshal(map[string]any{"met": true, "criteria": verdicts})
	if err != nil {
		t.Fatalf("Marshal(verdict): %v", err)
	}
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: string(body)}, nil
	}}

	ts, err := al.reconstructSteeredTurn(rec, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn: %v", err)
	}
	if !ts.opts.UserInitiated {
		t.Fatal("first steered turn is not marked user-initiated; goal claim would be ignored")
	}
	done := make(chan string, 1)
	oldDone := goalDeferredAdjudicationDoneFn
	goalDeferredAdjudicationDoneFn = func(sessionID string) { done <- sessionID }
	t.Cleanup(func() { goalDeferredAdjudicationDoneFn = oldDone })
	result := turnResult{finalContent: "[goal:evidence] verified the work\nGOAL_STATUS: met"}
	al.finishSteeredGoalTurn(ts, rec, &result, nil)
	select {
	case got := <-done:
		if got != rec.SessionID {
			t.Fatalf("adjudicated session = %q, want %q", got, rec.SessionID)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for delegated goal adjudication")
	}

	messages, _, _, err := inbox.Drain(parentMeta.ID, rec.SessionID, "", 10)
	if err != nil {
		t.Fatalf("Drain(parent): %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("parent messages = %d, want one goal_status", len(messages))
	}
	status, err := messages[0].AsSessionMessageGoalStatus()
	if err != nil {
		t.Fatalf("AsSessionMessageGoalStatus: %v", err)
	}
	if status.Condition != generated.SessionMessageGoalStatusConditionMet ||
		status.Direction != generated.SessionMessageGoalStatusDirectionSessionToParent ||
		status.Evidence == nil || len(*status.Evidence) != len(verdicts) {
		t.Fatalf("goal_status = %+v, want met/session_to_parent with %d evidence rows", status, len(verdicts))
	}
}

// TestFinishSteeredGoalTurn_BareClaimFollowUpRoutesThroughAsyncNotifier proves
// the goal-loop follow-up finishSteeredGoalTurn dispatches for a steered
// child (checkGoalLoopAfterTurn's bare-claim teaching steer, G-4) is
// delivered through the SAME async-notifier re-inject primitive
// dispatchGoalAsyncFollowUp already uses for the idle-tick and deferred-
// claim paths (goal_triggers.go), rather than a second direct
// bus.MessageBus.PublishInbound call site — the guard's exactly-six census
// (scripts/check-operator-prompt-sites.sh, FR-029a) pins PublishInbound's
// call sites to the async-notifier's own site plus the five others; a
// steered turn runs through steer_launcher.go's own dispatch goroutine
// (never runAgentLoop's inline post-turn block, loop.go), so it needs its
// own re-inject seam — but that seam must reuse the existing primitive, not
// open a fresh, unclassified PublishInbound call.
func TestFinishSteeredGoalTurn_BareClaimFollowUpRoutesThroughAsyncNotifier(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)
	wireSteerCompletionDeps(t, al)

	parentMeta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
	if err != nil {
		t.Fatalf("NewSession(parent): %v", err)
	}
	res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentMeta.ID,
		TargetAgentID:     "native-agent",
		Task:              "prove the goal",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-bare-claim"},
		Goal: &steer.GoalSpec{
			Criteria: []steer.Criterion{{Text: "the work is complete"}},
			DoD:      []steer.Criterion{{Text: "the evidence is sufficient"}},
		},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	rec, err := lifecycle.Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	rec.State = session.LifecycleRunning
	if err := lifecycle.Persist(rec); err != nil {
		t.Fatalf("Persist(running): %v", err)
	}

	ts, err := al.reconstructSteeredTurn(rec, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn: %v", err)
	}
	if !ts.opts.UserInitiated {
		t.Fatal("first steered turn is not marked user-initiated; the bare-claim gate would ignore it")
	}

	var mu sync.Mutex
	var captured []AsyncNotifyEvent
	al.asyncNotifier.registerObserver(func(event AsyncNotifyEvent) {
		mu.Lock()
		defer mu.Unlock()
		captured = append(captured, event)
	})

	// GOAL_STATUS: met with no [goal:evidence] line is a bare claim (G-4):
	// checkGoalLoopAfterTurn's handleBareGoalClaim appends a teaching-steer
	// follow-up to result.followUps on the first offense.
	result := turnResult{finalContent: "GOAL_STATUS: met"}
	al.finishSteeredGoalTurn(ts, rec, &result, nil)

	mu.Lock()
	defer mu.Unlock()
	if len(captured) != 1 {
		t.Fatalf("async notifier observed %d events, want exactly 1 (the bare-claim follow-up)", len(captured))
	}
	got := captured[0]
	if got.SenderCanonicalID != goalLoopFollowUpSenderID {
		t.Fatalf("SenderCanonicalID = %q, want %q (checkGoalLoopAfterTurn's origin gate requires the sentinel)",
			got.SenderCanonicalID, goalLoopFollowUpSenderID)
	}
	if got.AgentID != ts.agentID {
		t.Fatalf("AgentID = %q, want %q (the steered child's own agent, never guessed)", got.AgentID, ts.agentID)
	}
	if got.TranscriptSessionID != rec.SessionID {
		t.Fatalf("TranscriptSessionID = %q, want %q", got.TranscriptSessionID, rec.SessionID)
	}
	// A bare delegate launch (steer_launcher.go::Launch) seeds the child
	// session's own meta.Channel/PeerID empty — ts.channel/ts.chatID are ""
	// here, so finishSteeredGoalTurn falls back to the same channel-less
	// "system"/synthetic-chat-id destination TaskExecutor.
	// wakeOwnerAttemptsExhausted (task_executor_judge.go) already uses.
	wantChatID := "steer:" + rec.SessionID
	if got.Channel != "system" || got.ChatID != wantChatID {
		t.Fatalf("Channel/ChatID = %q/%q, want %q/%q (the channel-less fallback destination)",
			got.Channel, got.ChatID, "system", wantChatID)
	}
	if got.Content == "" {
		t.Fatal("Content is empty; expected the bare-claim teaching steer text")
	}
	// Finding F (ADR-091 fix lane 1, MEDIUM): a follow-up published with no
	// steer_message_id/steer_generation metadata falls through
	// processSystemMessage's routing check (msg.AsyncTranscriptSessionID != ""
	// && inboundMetadata(msg, "steer_message_id") != "") into the legacy,
	// hand-built-SendResponse tail instead of processSteeredSystemWake —
	// bypassing reconstruction (I-3), the Stop reservation, admission and
	// generation-aware cancel for every re-injected goal follow-up.
	if got.Metadata == nil || fmt.Sprint(got.Metadata["steer_message_id"]) == "" {
		t.Fatalf("Metadata[steer_message_id] is unset (%#v) — this follow-up would fall through to the legacy tail instead of routing through processSteeredSystemWake", got.Metadata)
	}
	if gotGen := fmt.Sprint(got.Metadata["steer_generation"]); gotGen != fmt.Sprint(rec.Generation) {
		t.Fatalf("Metadata[steer_generation] = %q, want %q (the CURRENT generation of the session this follow-up continues)", gotGen, fmt.Sprint(rec.Generation))
	}
}

func TestGoalDelegation_ParentGoalAbsentFromChildInput(t *testing.T) {
	provider := &steeredInputCaptureProvider{done: make(chan struct{})}
	al, _ := newGoalLoopTestLoop(t, provider, nil)
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)
	wireSteerCompletionDeps(t, al)

	parentMeta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
	if err != nil {
		t.Fatalf("NewSession(parent): %v", err)
	}
	const parentGoalSecret = "PARENT-GOAL-MUST-NOT-CROSS-EDGE"
	activateTestGoalRecord(t, parentMeta.ID, parentGoalSecret)

	launcher := NewSteerLauncher(al)
	res, err := launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentMeta.ID,
		TargetAgentID:     "native-agent",
		Task:              "child-only instruction",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-isolation"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if _, err := launcher.Dispatch(context.Background(), res.SessionID, res.Generation); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	select {
	case <-provider.done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for child model input")
	}

	var assembled strings.Builder
	for _, message := range provider.messages {
		assembled.WriteString(message.Content)
		assembled.WriteByte('\n')
	}
	input := assembled.String()
	if strings.Contains(input, parentGoalSecret) {
		t.Fatalf("assembled child input leaked the parent's goal:\n%s", input)
	}
	if !strings.Contains(input, "child-only instruction") {
		t.Fatalf("assembled child input omitted its own instruction:\n%s", input)
	}
}
