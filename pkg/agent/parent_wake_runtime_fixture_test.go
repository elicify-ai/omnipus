package agent

// Acceptance-fixture wiring only. The legacy boot/delivery constructors stay
// deliberately minimal for their missing-runtime/refusal controls. Positive
// parent-wake fixtures opt into the same collaborators gateway boot installs.
// Only the external provider is fake; no launcher, dispatch, loop or store is.

import (
	"context"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// pwHeldProvider keeps an admitted execution observable at the process edge,
// rather than letting a fast fake finish before an assertion reads its state.
type pwHeldProvider struct {
	requests chan []providers.Message
	release  chan struct{}
	once     sync.Once
}

func newPWHeldProvider(t *testing.T) *pwHeldProvider {
	t.Helper()
	p := &pwHeldProvider{requests: make(chan []providers.Message, 16), release: make(chan struct{})}
	t.Cleanup(p.open)
	return p
}

func (p *pwHeldProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	select {
	case p.requests <- slices.Clone(messages):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-p.release:
		return &providers.LLMResponse{Content: "fixture provider released"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (*pwHeldProvider) GetDefaultModel() string { return "parent-wake-fixture" }
func (p *pwHeldProvider) open()                 { p.once.Do(func() { close(p.release) }) }

func (p *pwHeldProvider) await(t *testing.T) []providers.Message {
	t.Helper()
	select {
	case messages := <-p.requests:
		return messages
	case <-time.After(5 * time.Second):
		t.Fatal("SETUP: real parent-wake execution did not reach its provider boundary")
		return nil
	}
}

func pwWireRuntime(t *testing.T, al *AgentLoop) *SteerUpwardDeliverer {
	t.Helper()
	if al.bootEpochFor() == 0 {
		mintGenuineBootEpochForLoop(t, al)
	}
	deliverer, ok := al.getUpwardDeliverer().(*SteerUpwardDeliverer)
	if !ok || deliverer == nil {
		deliverer = NewSteerUpwardDeliverer()
	}
	al.SetSteerAudienceDeps(NewSteerAudienceResolver(NewSteerRecordClassifier(
		al.GetSessionLifecycleStore(), al.GetSessionStore())), steer.NopBoundaryObserver{}, deliverer)
	al.SetSteerSessionLauncher(NewSteerLauncher(al))
	if te := GetTaskExecutor(al); te != nil {
		te.SetLifecycleStore(al.GetSessionLifecycleStore())
		te.SetSessionLauncher(al.getSteerSessionLauncher())
	}
	return deliverer
}

func newPWDeliverTestLoop(t *testing.T) (*AgentLoop, *session.LifecycleStore, *session.MessageInboxStore, *SteerUpwardDeliverer) {
	t.Helper()
	t.Setenv(config.EnvHome, t.TempDir())
	al, lifecycle, inbox, _ := newDeliverTestLoop(t)
	deliverer := pwWireRuntime(t, al)
	p := newPWHeldProvider(t) // release runs before the constructor's al.Close
	for _, id := range []string{"parent-agent", "worker"} {
		inst, ok := al.GetRegistry().GetAgent(id)
		if !ok {
			t.Fatalf("SETUP: parent-wake native agent %q is not registered", id)
		}
		inst.Provider = p
	}
	return al, lifecycle, inbox, deliverer
}

// Both endpoints must have metadata, not only lifecycle records. PW-D12 / §9.4
// refuses a missing parent; a missing child makes the real classifier refuse.
func pwSeedDeliverySessions(t *testing.T, al *AgentLoop, parentID, childID string) {
	t.Helper()
	lifecycle, store := al.GetSessionLifecycleStore(), al.GetSessionStore()
	seedParentAndChild(t, lifecycle, parentID, childID)
	seedUnifiedSession(t, al, parentID)
	if _, err := store.CreateSessionWithID(childID, parentID, session.SessionTypeDelegate, "", "worker"); err != nil {
		t.Fatalf("SETUP: create helper metadata: %v", err)
	}
	ws := testHarnessWorkspaceMembershipID
	for _, id := range []string{parentID, childID} {
		patch := session.MetaPatch{WorkspaceID: &ws}
		if id == childID {
			patch.ParentSessionID = &parentID
		}
		if err := store.SetMeta(id, patch); err != nil {
			t.Fatalf("SETUP: metadata for %s: %v", id, err)
		}
		if err := lifecycle.Mutate(id, func(rec *session.LifecycleRecord) error {
			rec.WorkspaceID = ws
			if id == childID {
				rec.ParentAgentID = "parent-agent"
				rec.SteeredBy.ReportingTarget.SessionID = parentID
			}
			return nil
		}); err != nil {
			t.Fatalf("SETUP: lifecycle for %s: %v", id, err)
		}
	}
	class, err := NewSteerRecordClassifier(lifecycle, store).Classify(t.Context(), childID)
	if err != nil || class != steer.ClassSteered {
		t.Fatalf("SETUP: helper classification=%q err=%v, want steered", class, err)
	}
}

func newPWWakeTestLoop(t *testing.T) (*AgentLoop, func()) {
	t.Helper()
	t.Setenv(config.EnvHome, t.TempDir())
	al, cleanup := newSteerAL(t)
	pwWireRuntime(t, al)
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: wake recipient's native agent is not registered")
	}
	inst.Provider = newPWHeldProvider(t)
	return al, cleanup
}

// Consume the actual published message through the production receiver, not a
// second fixture-created wake. Only the provider is held; join the receiver
// before closing its stores even when an acceptance assertion fails.
func pwDrivePublishedWake(t *testing.T, al *AgentLoop, sessionID string) {
	t.Helper()
	var msg bus.InboundMessage
	select {
	case msg = <-al.bus.InboundChan():
	case <-time.After(5 * time.Second):
		t.Fatal("WakeSession published no input for its real runtime receiver")
	}
	if msg.AsyncTranscriptSessionID != sessionID {
		t.Fatalf("wake addressed session %q, want %q", msg.AsyncTranscriptSessionID, sessionID)
	}
	inst, ok := al.GetRegistry().GetAgent(msg.AsyncOriginAgentID)
	if !ok {
		t.Fatalf("wake agent %q is not in the real roster", msg.AsyncOriginAgentID)
	}
	p, ok := inst.Provider.(*pwHeldProvider)
	if !ok {
		t.Fatal("SETUP: wake provider boundary is not held")
	}
	done := make(chan error, 1)
	go func() { _, err := al.processSystemMessage(context.Background(), msg); done <- err }()
	t.Cleanup(func() {
		p.open()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("real wake receiver: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("real wake receiver did not join before fixture teardown")
		}
	})
	p.await(t)
}

type pwBootRecoveryHarness struct {
	*bootRecoveryHarness
	al           *AgentLoop
	provider     *pwHeldProvider
	priorBootSeq uint64
}

func newPWBootRecoveryHarness(t *testing.T) *pwBootRecoveryHarness {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	cfg := &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{Home: filepath.Join(home, "workspace"),
			DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096, MaxToolIterations: 10},
		List: []config.AgentConfig{{ID: "agent-1", Home: filepath.Join(home, "agent-1")}},
	}}
	// Register Close before the provider's release cleanup (LIFO).
	p := &pwHeldProvider{requests: make(chan []providers.Message, 16), release: make(chan struct{})}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), p)
	t.Cleanup(al.Close)
	t.Cleanup(p.open)
	h := &bootRecoveryHarness{
		lifecycle: session.NewLifecycleStore(filepath.Join(home, "session_lifecycle")),
		sessions:  al.GetSessionStore(),
		inbox:     session.NewMessageInboxStore(filepath.Join(home, "session_messages")),
	}
	al.SetSessionMessagingStores(h.inbox, h.lifecycle)
	priorBoot := mintGenuineBootEpochForLoop(t, al)
	priorBootSeq := priorBoot.Current()
	// One Mint per process-lifetime instance (session.BootEpochStore).
	// Reopen the same persisted counter for the simulated gateway restart.
	h.writingBoot = session.NewBootEpochStore(filepath.Join(cfg.Agents.Defaults.Home, "boot_epoch"))
	if writing, err := h.writingBoot.Mint(); err != nil || writing <= priorBootSeq {
		t.Fatalf("SETUP: next writing boot=%d err=%v, previous=%d", writing, err, priorBootSeq)
	}
	al.SetBootEpochStore(h.writingBoot)
	pwWireRuntime(t, al)
	return &pwBootRecoveryHarness{bootRecoveryHarness: h, al: al, provider: p, priorBootSeq: priorBootSeq}
}

// Persist the abandoned execution using admission's own durable identity
// writer, as stampG5ExitedExecution does for an already-exited turn fixture.
// No prior goroutine survives this simulated process boundary.
func (h *pwBootRecoveryHarness) persist(t *testing.T, rec *session.LifecycleRecord) {
	t.Helper()
	h.bootRecoveryHarness.persist(t, rec)
	if rec.SteeredBy != nil && rec.ExecutionID == nil &&
		(rec.State == session.LifecycleRunning || rec.State == session.LifecycleQueued) {
		if err := stampAdmissionExecution(h.lifecycle, rec.SessionID, rec.Generation, freshRunID(), h.priorBootSeq, nil); err != nil {
			t.Fatalf("SETUP: persist abandoned prior-boot execution: %v", err)
		}
	}
}

func (h *pwBootRecoveryHarness) recovery() *SteerBootRecovery {
	r := h.bootRecoveryHarness.recovery()
	r.Deliverer = h.al.getUpwardDeliverer()
	return r
}

func (h *pwBootRecoveryHarness) newSession(t *testing.T, typ session.UnifiedSessionType, parentID string) string {
	t.Helper()
	id := h.bootRecoveryHarness.newSession(t, typ, parentID)
	ws := testHarnessWorkspaceMembershipID
	if err := h.sessions.SetMeta(id, session.MetaPatch{WorkspaceID: &ws}); err != nil {
		t.Fatalf("SETUP: boot session workspace: %v", err)
	}
	return id
}

func (h *pwBootRecoveryHarness) rootSession(t *testing.T) string {
	t.Helper()
	id := h.newSession(t, session.SessionTypeChat, "")
	h.persist(t, &session.LifecycleRecord{
		SessionID: id, Generation: 1, State: session.LifecycleCompleted,
		Origin: &session.Origin{Kind: session.OriginKindChat}, OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID: testHarnessWorkspaceMembershipID, AgentID: "agent-1",
	})
	return id
}

func (h *pwBootRecoveryHarness) steeredRecord(id, parent string, state session.LifecycleState) *session.LifecycleRecord {
	rec := h.bootRecoveryHarness.steeredRecord(id, parent, state)
	rec.WorkspaceID = testHarnessWorkspaceMembershipID
	rec.SteeredBy.ReportingTarget = session.ReportingTarget{Channel: "webchat", ChatID: parent, SessionID: parent}
	return rec
}

// The prior successful invocation is real saved model history, not a local
// counter or a successful row invented after boot. PW-D6/PW-D21 require resume
// to continue after this result; these existing acceptance tests pin admission.
func (h *pwBootRecoveryHarness) seedCompletedToolHistory(t *testing.T, childID string) {
	t.Helper()
	for _, msg := range []providers.Message{
		{Role: "user", Content: "Continue the delegated work from saved history."},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "call_0", Name: "read_file", Arguments: map[string]any{"path": "already-read.txt"}}}},
		{Role: "tool", ToolCallID: "call_0", Content: "historical successful result"},
	} {
		if _, err := appendWindowMsg(h.sessions, t.Context(), childID, msg); err != nil {
			t.Fatalf("SETUP: append saved helper history: %v", err)
		}
	}
	view, err := h.sessions.WindowView(t.Context(), childID)
	if err != nil {
		t.Fatalf("SETUP: read saved helper history: %v", err)
	}
	history := viewArchived(view)
	if len(history) < 3 {
		t.Fatalf("SETUP: saved history=%+v, missing the seeded user/call/result sequence", history)
	}
	tail := history[len(history)-3:]
	if tail[0].Role != "user" || tail[1].Role != "assistant" || len(tail[1].ToolCalls) != 1 || tail[1].ToolCalls[0].ID != "call_0" ||
		tail[2].Role != "tool" || tail[2].ToolCallID != "call_0" || tail[2].Content != "historical successful result" {
		t.Fatalf("SETUP: saved tail=%+v, want user, assistant call_0, successful tool result", tail)
	}
}

// TestParentWakeBootFixture_RealDispatchReachesHeldProvider tests the instrument
// independently. Acceptance tests NEVER dispatch a stopped helper themselves:
// only the production boot path is allowed to perform their required resume.
func TestParentWakeBootFixture_RealDispatchReachesHeldProvider(t *testing.T) {
	h := newPWBootRecoveryHarness(t)
	// The real launcher creates this parent's ordinary-root record. A completed
	// boot recipient cannot issue a new delegation without a fresh admission.
	parent := h.newSession(t, session.SessionTypeChat, "")
	launcher, ok := h.al.getSteerSessionLauncher().(*SteerLauncher)
	if !ok || launcher == nil || launcher.al != h.al {
		t.Fatal("SETUP: the real launcher is not installed on the boot runtime")
	}
	launched, err := launcher.Launch(t.Context(), steer.LaunchRequest{
		SteeringSessionID: parent, TargetAgentID: "agent-1", Task: "fixture dispatch control",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "fixture-dispatch"},
	})
	if err != nil {
		t.Fatalf("SETUP: real launcher: %v", err)
	}
	h.seedCompletedToolHistory(t, launched.SessionID)
	dispatched, err := launcher.Dispatch(t.Context(), launched.SessionID, launched.Generation)
	if err != nil || dispatched.State != steer.DispatchRunning {
		t.Fatalf("SETUP: real dispatch=%+v err=%v, want running", dispatched, err)
	}
	messages := h.provider.await(t)
	var successfulResults int
	for _, msg := range messages {
		if msg.Role == "tool" && msg.ToolCallID == "call_0" && msg.Content == "historical successful result" {
			successfulResults++
		}
	}
	if successfulResults != 1 {
		t.Fatalf("SETUP: provider history contains %d historical successful results, want 1", successfulResults)
	}
	if h.al.GetSessionStore() != h.sessions || h.al.GetSessionLifecycleStore() != h.lifecycle || h.al.GetMessageInboxStore() != h.inbox {
		t.Fatal("SETUP: boot and dispatch do not share the same real stores")
	}
}
