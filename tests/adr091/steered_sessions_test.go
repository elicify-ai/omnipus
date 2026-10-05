package adr091_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const e2eProbeToolName = "adr091_boundary_probe"

// e2eBoundaryProvider's chatCalls (Gap 4, ADR-091 fix-lane 7) is the
// distinguishing signal TestE2E_ThreeLevelDelegation_NoLeak uses to prove
// the chain did real work end to end, not merely stayed contained: this
// provider's response is IDENTICAL regardless of trigger (a fixed canned
// string), so content alone cannot tell "A was genuinely re-entered and
// processed its child's completion" apart from "A's stale pre-delegation
// answer was reused" (lane 1's depth-two result-loss finding: a steered
// child's empty reporting channel makes WakeParentAlways refuse the live
// wake, so completeWaitingAncestors falls back to the parent's OWN last
// answer instead of a real re-entry). A naive, un-re-entered chain makes
// exactly 2 Chat calls per level (the tool call, then its own finalize) —
// 6 total for three levels. Any additional call proves at least one level
// was actually re-entered.
type e2eBoundaryProvider struct {
	chatCalls atomic.Int32
}

func (p *e2eBoundaryProvider) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.chatCalls.Add(1)
	for _, message := range messages {
		if message.Role == "tool" {
			return &providers.LLMResponse{Content: "child turn completed after the boundary probe"}, nil
		}
	}
	return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
		ID:       "adr091-probe-call",
		Function: &providers.FunctionCall{Name: e2eProbeToolName, Arguments: `{}`},
	}}}, nil
}

func (*e2eBoundaryProvider) GetDefaultModel() string { return "scripted-model" }

type e2eIdleProvider struct{}

func (*e2eIdleProvider) Chat(
	context.Context,
	[]providers.Message,
	[]providers.ToolDefinition,
	string,
	map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: "idle child"}, nil
}

func (*e2eIdleProvider) GetDefaultModel() string { return "scripted-model" }

type e2eBlockingProvider struct {
	release <-chan struct{}
}

func (p *e2eBlockingProvider) Chat(
	ctx context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	select {
	case <-p.release:
		return &providers.LLMResponse{Content: "released child"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (*e2eBlockingProvider) GetDefaultModel() string { return "scripted-model" }

type e2eBoundaryProbe struct {
	tools.BaseTool
	calls    atomic.Int32
	recorder *testutil.OutboundRecorder
}

func (*e2eBoundaryProbe) Name() string        { return e2eProbeToolName }
func (*e2eBoundaryProbe) Description() string { return "exercise a real child tool boundary" }
func (*e2eBoundaryProbe) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (*e2eBoundaryProbe) Scope() tools.ToolScope { return tools.ScopeCore }
func (p *e2eBoundaryProbe) Execute(ctx context.Context, _ map[string]any) *tools.ToolResult {
	p.calls.Add(1)
	sessionID := tools.ToolTranscriptSessionID(ctx)
	p.recorder.Record(steer.BoundarySyncToolText, sessionID, sessionID, "tool_error")
	result := tools.ErrorResult("intentional boundary probe failure")
	// Gap 2 (ADR-091 fix-lane 7): also carry media on the SAME probe call so
	// the real production media gate (boundary 4, loop_run_turn_tools.go —
	// "UNGATED before ADR-091") is exercised by this suite, not just the
	// text boundary. Previously this probe never returned Media at all, so
	// AssertBoundaryInvoked(steer.BoundaryMedia) could never have passed
	// here, and the e2e drain never touched the outbound MEDIA channel.
	result.Media = []string{"adr091-e2e-media-ref.png"}
	return result
}

// recordingUpwardDeliverer wraps the real deliverer and records every upward
// event as it is delivered. Asserting on the root's RESIDUAL inbox is unsound
// now that the harness runs AgentLoop.Run: Run is what consumes the root's
// inbox entry and re-enters the root, so a passing delegation necessarily
// empties the very queue the assertion read. Recording at delivery time is
// race-free and strictly stronger -- it proves the event was delivered, not
// merely that nothing has consumed it yet.
type recordingUpwardDeliverer struct {
	inner steer.UpwardDeliverer
	mu    sync.Mutex
	seen  []string // ChildSessionID of every delivered event, in order
	// outcomes records, per ChildSessionID, the steer.Outcome carried by
	// every event delivered for it, in delivery order. TestE2E_StopSurvivesRestart
	// (fix/1074-stop-survives-terminal-20260929) uses this as the INDEPENDENT
	// signal for which of the two legal terminal shapes a Stop-reached
	// session actually landed in: steer_completion.go::deliverSteeredCompletion
	// calls Deliver with this Outcome BEFORE its own terminal-state write
	// ("Delivery is deliberately first"), so reading it back here is a
	// SEPARATE observation from re-reading the lifecycle record's own State —
	// not a re-derivation of the same fact from itself.
	outcomes map[string][]steer.Outcome
}

var _ steer.UpwardDeliverer = (*recordingUpwardDeliverer)(nil)

func (d *recordingUpwardDeliverer) Deliver(ctx context.Context, event steer.UpwardEvent) (steer.Delivery, error) {
	delivery, err := d.inner.Deliver(ctx, event)
	if err == nil {
		d.mu.Lock()
		d.seen = append(d.seen, event.ChildSessionID)
		if d.outcomes == nil {
			d.outcomes = make(map[string][]steer.Outcome)
		}
		d.outcomes[event.ChildSessionID] = append(d.outcomes[event.ChildSessionID], event.Outcome)
		d.mu.Unlock()
	}
	return delivery, err
}

// outcomesFor returns every steer.Outcome delivered for childSessionID's OWN
// completion, in delivery order — a copy, so a caller cannot mutate the
// recorder's internal slice.
func (d *recordingUpwardDeliverer) outcomesFor(childSessionID string) []steer.Outcome {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]steer.Outcome(nil), d.outcomes[childSessionID]...)
}

// countFor reports how many upward events were delivered for childSessionID
// — "exactly one owner" needs a count, not a boolean.
func (d *recordingUpwardDeliverer) countFor(childSessionID string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	count := 0
	for _, id := range d.seen {
		if id == childSessionID {
			count++
		}
	}
	return count
}

func (d *recordingUpwardDeliverer) deliveredFor(childSessionID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, id := range d.seen {
		if id == childSessionID {
			return true
		}
	}
	return false
}

type e2eHarness struct {
	al         *agent.AgentLoop
	tree       *testutil.Tree
	sessions   *session.UnifiedStore
	lifecycle  *session.LifecycleStore
	inbox      *session.MessageInboxStore
	audience   steer.AudienceResolver
	deliverer  steer.UpwardDeliverer
	canceller  steer.Canceller
	classifier steer.RecordClassifier
	launcher   steer.SessionLauncher
	recorder   *testutil.OutboundRecorder
	probe      *e2eBoundaryProbe
	msgBus     *bus.MessageBus
	// boundaryProvider is set only by newE2EHarness (the e2eBoundaryProvider
	// variant); nil for harnesses built with a different provider
	// (newE2EHarnessWithProvider's other callers).
	boundaryProvider *e2eBoundaryProvider
	// upward records upward deliveries as they happen (see recordingUpwardDeliverer).
	upward *recordingUpwardDeliverer
}

func newE2EHarness(t *testing.T) *e2eHarness {
	t.Helper()
	provider := &e2eBoundaryProvider{}
	h := newE2EHarnessWithProvider(t, provider, testutil.RecordingOutbound(t), true)
	h.boundaryProvider = provider
	return h
}

// newE2EHarnessWithProvider builds the harness with the REAL production
// steer.AudienceResolver (agent.NewSteerAudienceResolver). Delegates to
// newE2EHarnessCustom, which a test needing a different resolver (Gap 3's
// leak-detection control) calls directly.
func newE2EHarnessWithProvider(
	t *testing.T,
	provider providers.LLMProvider,
	recorder *testutil.OutboundRecorder,
	registerProbe bool,
) *e2eHarness {
	t.Helper()
	return newE2EHarnessCustom(t, provider, recorder, registerProbe, nil)
}

// newE2EHarnessCustom is newE2EHarnessWithProvider's full body, plus an
// audienceOverride hook: nil means "use the real production resolver"
// (agent.NewSteerAudienceResolver over the real classifier); non-nil
// replaces it outright — used by
// TestE2E_ThreeLevelDelegation_LeakIsDetected to prove the leak suite's
// AssertNothingTo has teeth against a resolver that answers AudienceUser
// for everyone, WITHOUT touching any production code.
func newE2EHarnessCustom(
	t *testing.T,
	provider providers.LLMProvider,
	recorder *testutil.OutboundRecorder,
	registerProbe bool,
	audienceOverride steer.AudienceResolver,
) *e2eHarness {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	workspaceDir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(workspaceDir, 0o700); err != nil {
		t.Fatalf("create fixture workspace directory: %v", err)
	}
	workspaceRecord, err := json.Marshal(map[string]any{
		"id": "adr091-fixture-workspace",
		"core_team": []string{
			"adr091-fixture-agent-root", "adr091-fixture-agent-a", "adr091-fixture-agent-b",
			"adr091-fixture-agent-c", "mia", "adr091-fixture-task-agent",
		},
	})
	if err != nil {
		t.Fatalf("marshal fixture workspace: %v", err)
	}
	if writeErr := os.WriteFile(filepath.Join(workspaceDir, "adr091-fixture-workspace.json"), workspaceRecord, 0o600); writeErr != nil {
		t.Fatalf("write fixture workspace: %v", writeErr)
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	cfg := &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{
			Home: filepath.Join(home, "agents"), DefaultModel: config.DefaultModel{Model: "scripted-model"}, MaxTokens: 4096,
		},
		List: []config.AgentConfig{
			{ID: "adr091-fixture-agent-root", Name: "ADR-091 fixture root", Type: config.AgentTypeCustom, Home: filepath.Join(home, "agents", "root")},
			{ID: "adr091-fixture-agent-a", Name: "ADR-091 fixture A", Type: config.AgentTypeCustom, Home: filepath.Join(home, "agents", "a")},
			{ID: "adr091-fixture-agent-b", Name: "ADR-091 fixture B", Type: config.AgentTypeCustom, Home: filepath.Join(home, "agents", "b")},
			{ID: "adr091-fixture-agent-c", Name: "ADR-091 fixture C", Type: config.AgentTypeCustom, Home: filepath.Join(home, "agents", "c")},
			{ID: "mia", Name: "ADR-091 fixture agent", Type: config.AgentTypeCustom, Home: filepath.Join(home, "agents", "mia")},
			{ID: "adr091-fixture-task-agent", Name: "ADR-091 task fixture agent", Type: config.AgentTypeCustom, Home: filepath.Join(home, "agents", "task")},
		},
	}}
	al, err := agent.NewAgentLoop(cfg, msgBus, provider)
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	t.Cleanup(al.Close)
	// Mint before the tree's real dispatches; never stamp fixture identities.
	boot := session.NewBootEpochStore(home)
	epoch, err := boot.Mint()
	if err != nil {
		t.Fatalf("SETUP Mint genuine boot epoch: %v", err)
	}
	if epoch == 0 || boot.Current() != epoch {
		t.Fatalf("SETUP minted/current boot epoch = %d/%d, require one genuine nonzero epoch", epoch, boot.Current())
	}
	al.SetBootEpochStore(boot)
	sessions := al.GetSessionStore()
	if sessions == nil {
		t.Fatal("NewAgentLoop did not construct the shared session store")
	}
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "lifecycle"))
	inbox := session.NewMessageInboxStore(filepath.Join(home, "inbox"))
	al.SetSessionMessagingStores(inbox, lifecycle)
	classifier := agent.NewSteerRecordClassifier(lifecycle, sessions)
	var audience steer.AudienceResolver = agent.NewSteerAudienceResolver(classifier)
	if audienceOverride != nil {
		audience = audienceOverride
	}
	concreteDeliverer := agent.NewSteerUpwardDeliverer()
	recordingDeliverer := &recordingUpwardDeliverer{inner: concreteDeliverer}
	var deliverer steer.UpwardDeliverer = recordingDeliverer
	// SetSteerAudienceDeps back-wires the deliverer's *AgentLoop dependency
	// ONLY when handed the concrete *SteerUpwardDeliverer (steer_boundary.go's
	// type assertion). A wrapper hides that type, and every upward delivery
	// then fails with "UpwardDeliverer not wired". Wire the concrete instance
	// first; the second call installs the recording wrapper and the inner
	// instance keeps the back-wiring from the first.
	al.SetSteerAudienceDeps(audience, recorder, concreteDeliverer)
	al.SetSteerAudienceDeps(audience, recorder, deliverer)
	launcher := agent.NewSteerLauncher(al)
	var probe *e2eBoundaryProbe
	if registerProbe {
		probe = &e2eBoundaryProbe{recorder: recorder}
		al.RegisterTool(probe)
		for _, agentID := range al.GetRegistry().ListAgentIDs() {
			if instance, ok := al.GetRegistry().GetAgent(agentID); ok {
				instance.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{e2eProbeToolName: "allow"}})
			}
		}
	}
	canceller := agent.NewSteerCanceller(lifecycle, al.SteerGenerationCancel)
	deps := steer.Deps{
		Canceller: canceller, Deliverer: deliverer, Classifier: classifier,
		LifecycleStore: lifecycle, SessionStore: sessions,
		Launcher: launcher,
		BootHook: func(context.Context) error { return nil },
	}
	// The delegation chain's upward wake is published onto the inbound bus
	// (async_notifier.go), and AgentLoop.Run is the only thing that drains
	// it — in production it is always running. Without it a completing
	// grandchild's follow-up sat in the queue for ever and its ancestors
	// never re-entered, so every level above the deepest stayed `running`.
	runCtx, stopLoop := context.WithCancel(context.Background())
	loopDone := make(chan struct{})
	go func() { defer close(loopDone); _ = al.Run(runCtx) }()
	// Cancel AND WAIT. t.Cleanup runs BEFORE t.TempDir's own removal, so a
	// loop still draining its inbox keeps writing into a directory Go is
	// deleting: "TempDir RemoveAll cleanup: directory not empty" fails the
	// test for a reason unrelated to anything it asserts.
	t.Cleanup(func() {
		stopLoop()
		<-loopDone
	})

	harness := &e2eHarness{
		al:   al,
		tree: testutil.DelegationTree(t, deps, 3), sessions: sessions,
		lifecycle: lifecycle, inbox: inbox, audience: audience, deliverer: deliverer,
		canceller: canceller, classifier: classifier, launcher: launcher,
		recorder: recorder, probe: probe, msgBus: msgBus, upward: recordingDeliverer,
	}
	// [ADR-091 fix/adr091-stop-survives-restart-race, Finding 3] Canceller and
	// Classifier were built once above, wired to the PRE-CRASH `lifecycle`
	// variable. Tree.Reboot installs a brand-new *session.LifecycleStore into
	// deps.LifecycleStore (a fresh instance, own lock pool — lifecycle_lock.go)
	// but has no way to reach into this closure and rebuild Canceller/Classifier
	// itself, so without this hook they would keep serializing through the
	// orphaned pre-crash store's lock pool forever after a Reboot, while
	// deps.LifecycleStore (and this harness's own diagnostic Loads through it)
	// use a completely independent one. Rebuilding both here mirrors what a real
	// restart does — boot constructs a fresh SteerCanceller/RecordClassifier
	// wired to the freshly-opened store, from the composition root
	// (gateway.wireSteerDeps), not by reusing pre-crash instances.
	harness.tree.RebuildAfterReboot = func(lifecycle *session.LifecycleStore, sessions *session.UnifiedStore) (steer.Canceller, steer.RecordClassifier) {
		return agent.NewSteerCanceller(lifecycle, al.SteerGenerationCancel), agent.NewSteerRecordClassifier(lifecycle, sessions)
	}
	t.Cleanup(func() { harness.waitForTreeTurns() })
	return harness
}

func (h *e2eHarness) waitForTreeTurns() {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		settled := true
		for _, child := range []testutil.TreeNode{h.tree.A, h.tree.B, h.tree.C} {
			record, err := h.lifecycle.Load(child.SessionID)
			if err == nil && !record.Terminal() {
				settled = false
				break
			}
		}
		if settled {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestE2E_ThreeLevelDelegation_NoLeak(t *testing.T) {
	h := newE2EHarness(t)

	deadline := time.Now().Add(10 * time.Second)
	for h.probe.calls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := h.probe.calls.Load(); got != 3 {
		t.Fatalf("real child tool calls = %d, want 3", got)
	}

	for time.Now().Before(deadline) {
		allTerminal := true
		for _, child := range []testutil.TreeNode{h.tree.A, h.tree.B, h.tree.C} {
			record, err := h.lifecycle.Load(child.SessionID)
			if err != nil || !record.Terminal() {
				allTerminal = false
				break
			}
		}
		if allTerminal {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Gap 4 (ADR-091 fix-lane 7): the polling loop above used to break out
	// unconditionally at the deadline WITHOUT checking whether it actually
	// found every level terminal — a delegation that never finishes (the
	// depth-two result-loss defect) silently fell through to the containment
	// assertions below and still reported PASS. Delegation must actually
	// WORK, not merely stay contained.
	for _, child := range []testutil.TreeNode{h.tree.A, h.tree.B, h.tree.C} {
		record, err := h.lifecycle.Load(child.SessionID)
		if err != nil || !record.Terminal() {
			t.Fatalf("%s did not reach a terminal lifecycle state within the deadline (record=%+v, err=%v) — "+
				"a three-level delegation must complete end to end, not merely stay contained",
				child.Name, record, err)
		}
	}
	for h.recorder.BoundaryInvocationCount(steer.BoundaryFinalReply) < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	// Only a STEERED CHILD reaching the human is a leak. The ROOT is the
	// human's own chat, so its own publications are the correct behaviour,
	// not a breach — and the root does publish here: the hand-back arrives
	// through processSystemMessage, the root runs a further turn, and
	// e2eBoundaryProbe.Execute returns Media on EVERY call including the
	// root's. Recording every outbound message as a leak therefore made
	// both drains race the root's hand-back turn: green when the drain won,
	// red with `forbidden address "human"` when the root's turn published
	// first. It failed exactly that way on release 72bb9646e (CI job
	// 107839025045) while passing locally, which reads like a containment
	// regression and is not one.
	//
	// The assertion below stays strict — the defect was that it could not
	// tell a child from the root, never that it demanded too much.
	steeredChildren := map[string]struct{}{
		h.tree.A.SessionID: {},
		h.tree.B.SessionID: {},
		h.tree.C.SessionID: {},
	}
	// Give the root's hand-back turn time to publish BEFORE draining, so
	// every run exercises the interleaving that used to fail rather than
	// reaching the assertion only when the drain happens to win the race.
	// A leak from a child would be caught in this window too.
	settle := time.Now().Add(2 * time.Second)
	for time.Now().Before(settle) {
		time.Sleep(20 * time.Millisecond)
	}

	for {
		select {
		case outbound := <-h.msgBus.OutboundChan():
			if _, isChild := steeredChildren[outbound.SessionID]; isChild {
				h.recorder.Record(steer.BoundaryFinalReply, outbound.SessionID, "human", "leak")
			}
		default:
			goto textDrained
		}
	}
textDrained:
	// Gap 2 (ADR-091 fix-lane 7): the leak suite drained the TEXT channel
	// but never the MEDIA channel — "the end-to-end suite could not catch a
	// media leak even if it tried". e2eBoundaryProbe now returns Media on
	// every call (see its Execute), so this drain has something real to
	// catch if BoundaryMedia's containment ever regresses.
	for {
		select {
		case outboundMedia := <-h.msgBus.OutboundMediaChan():
			// Same child-only rule as the text drain above: the root's own
			// media is legitimate, a steered child's is the leak this
			// catches.
			if _, isChild := steeredChildren[outboundMedia.SessionID]; isChild {
				h.recorder.Record(steer.BoundaryMedia, outboundMedia.SessionID, "human", "media-leak")
			}
		default:
			goto mediaDrained
		}
	}
mediaDrained:
	// ADR-091 fix lane RX-TESTS: these three assertions used to name only a
	// boundary — "was this boundary ever reached by anyone?". That is true of
	// a correctly contained system AND of a completely broken one, because a
	// broken gate still RUNS the boundary; it just answers the wrong
	// audience. Proof: mutating the production resolver
	// (steer_audience.go::SteerAudienceResolver.Audience) so ClassSteered
	// resolves AudienceNone instead of AudienceSteeringSession — every
	// steered child silently losing the audience the ADR assigns it — left
	// this whole test green, because nothing leaks to the user under that
	// mutation either, so AssertNothingTo cannot see it.
	//
	// Each assertion now pins the SESSION the boundary ran for and the
	// AUDIENCE its decision resolved to, on every one of the three levels:
	// a steered child is addressed to its steering session at every
	// boundary, never to the user and never to nobody.
	for _, child := range []testutil.TreeNode{h.tree.A, h.tree.B, h.tree.C} {
		scope := testutil.ForSession(child.SessionID, steer.AudienceSteeringSession)
		h.recorder.AssertBoundaryInvoked(steer.BoundarySyncToolText, scope)
		h.recorder.AssertBoundaryInvoked(steer.BoundaryFinalReply, scope)
		h.recorder.AssertBoundaryInvoked(steer.BoundaryMedia, scope)
	}
	h.recorder.AssertReceived(h.tree.C.SessionID, "tool_error")
	h.recorder.AssertNothingTo("human")

	// Gap 4 (ADR-091 fix-lane 7): NoLeak's headline claim was containment
	// only — nothing asserted that the chain's result actually reaches the
	// root. Observe the real production upward-delivery path
	// (steer_completion.go::completeSteeredTurn's Deliver, which since fix
	// lane 1 is the ONLY path to an ancestor) rather than re-deriving
	// content from the tree fixture.
	if !h.upward.deliveredFor(h.tree.A.SessionID) {
		t.Fatal("the root received NOTHING from its three-level delegation chain even though every " +
			"level went terminal: no upward event for A (the root's own child) was ever delivered")
	}
	// "The root got a message" alone is not enough: A's OWN completion
	// always delivers upward regardless of whether it ever heard from B/C —
	// lane 1's depth-two result-loss finding is precisely that a steered
	// ancestor's stale pre-delegation answer can propagate upward via
	// completeWaitingAncestors's fallback WITHOUT the ancestor ever being
	// genuinely re-entered to process its child's completion. A naive,
	// never-re-entered chain makes exactly 2 scripted Chat calls per level
	// (the tool call, then its own finalize) — 6 total for three levels.
	// This asserts at least one extra call happened, i.e. at least one
	// level was actually woken and re-run, not just fallen back on.
	if calls := h.boundaryProvider.chatCalls.Load(); calls <= 6 {
		t.Fatalf("provider Chat() was called %d times — want > 6 (2 per level x 3 levels is the "+
			"never-re-entered baseline); no ancestor was genuinely re-entered by its child's "+
			"completion, which is exactly lane 1's depth-two result-loss defect", calls)
	}
}

type recordingFailureT struct {
	mu       sync.Mutex
	failures []string
}

func (*recordingFailureT) Helper() {}
func (t *recordingFailureT) Fatalf(format string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failures = append(t.failures, fmt.Sprintf(format, args...))
}

func TestE2E_ThreeLevelDelegation_ZeroToolControlFails(t *testing.T) {
	failureT := &recordingFailureT{}
	recorder := testutil.RecordingOutbound(failureT)
	h := newE2EHarnessWithProvider(t, &e2eIdleProvider{}, recorder, false)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		record, err := h.lifecycle.Load(h.tree.A.SessionID)
		if err == nil && record.Terminal() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	recorder.AssertReceived(h.tree.A.SessionID, "tool_error")
	failureT.mu.Lock()
	defer failureT.mu.Unlock()
	if len(failureT.failures) != 1 || !strings.Contains(failureT.failures[0], "no outbound control received") {
		t.Fatalf("zero-tool control failures = %v, want the connected recorder assertion to fail", failureT.failures)
	}
}

// alwaysUserAudienceResolver is an injected TEST STUB (never production
// code) that answers AudienceUser for every session, regardless of its real
// class. It exists solely to prove Gap 3: that AssertNothingTo("human") is
// backed by a REAL production decision point (audienceFor's
// finalReplyAudience == steer.AudienceUser gate, loop.go) and would
// genuinely detect a leak if that gate were ever broken — not merely a
// probe tool that manually calls recorder.Record on itself.
type alwaysUserAudienceResolver struct{}

func (alwaysUserAudienceResolver) Audience(context.Context, string) (steer.Audience, steer.Class, error) {
	return steer.AudienceUser, steer.ClassOrdinaryRoot, nil
}

// TestE2E_ThreeLevelDelegation_LeakIsDetected is Gap 3 (ADR-091 fix-lane 7):
// TestE2E_ThreeLevelDelegation_NoLeak's AssertNothingTo("human") scans a
// ledger that, before this test, nothing had ever proven would actually
// catch a leak — the existing control (TestE2E_ThreeLevelDelegation_
// ZeroToolControlFails) only proves the probe tool RAN. This test wires the
// REAL audience-resolution call site (audienceFor, loop.go's finalReplyAudience
// gate) to an injected stub that answers AudienceUser for every session —
// simulating exactly the regression "a steered child's final reply is
// treated as reaching the user" — and proves the SAME harness, drain and
// assertion used by NoLeak now correctly DETECTS the leak and FAILS.
// Without this, nobody knows the leak suite's safety claim has teeth.
func TestE2E_ThreeLevelDelegation_LeakIsDetected(t *testing.T) {
	failureT := &recordingFailureT{}
	recorder := testutil.RecordingOutbound(failureT)
	h := newE2EHarnessCustom(t, &e2eBoundaryProvider{}, recorder, true, alwaysUserAudienceResolver{})

	deadline := time.Now().Add(10 * time.Second)
	for h.probe.calls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	for time.Now().Before(deadline) {
		allTerminal := true
		for _, child := range []testutil.TreeNode{h.tree.A, h.tree.B, h.tree.C} {
			record, err := h.lifecycle.Load(child.SessionID)
			if err != nil || !record.Terminal() {
				allTerminal = false
				break
			}
		}
		if allTerminal {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for h.recorder.BoundaryInvocationCount(steer.BoundaryFinalReply) < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	// With every session's audience stubbed to AudienceUser, the REAL
	// production gate at loop.go's finalReplyAudience == steer.AudienceUser
	// now actually publishes each steered child's final reply to the
	// outbound bus — the same bus NoLeak drains and records under "human".
	for {
		select {
		case outbound := <-h.msgBus.OutboundChan():
			recorder.Record(steer.BoundaryFinalReply, outbound.SessionID, "human", "leak")
		default:
			goto drained
		}
	}
drained:
	recorder.AssertNothingTo("human")

	failureT.mu.Lock()
	defer failureT.mu.Unlock()
	if len(failureT.failures) == 0 {
		t.Fatal("AssertNothingTo(\"human\") did not fail against a stubbed AudienceUser resolver — " +
			"the leak suite's safety claim has NO teeth: it would never catch a real leak either")
	}
	found := false
	for _, failure := range failureT.failures {
		if strings.Contains(failure, "forbidden address") {
			found = true
		}
	}
	if !found {
		t.Fatalf("AssertNothingTo(\"human\") failed for an unexpected reason: %v", failureT.failures)
	}
}

func TestE2E_StopReachesReenteredChild(t *testing.T) {
	h := newE2EHarness(t)
	if class, err := h.classifier.Classify(context.Background(), h.tree.C.SessionID); err != nil || class != steer.ClassSteered {
		t.Fatalf("re-entered child classification = %q, %v; want steered", class, err)
	}
	report, err := h.tree.Stop(h.tree.Root.SessionID)
	if err != nil {
		t.Fatalf("Stop(root): %v", err)
	}
	for _, node := range h.tree.Nodes {
		if !slices.Contains(report.Reached, node.SessionID) {
			t.Fatalf("Stop(root) reached %v, want re-entered node %s (%s)", report.Reached, node.Name, node.SessionID)
		}
	}
}

// assertStopLandedOn checks rec against the TWO durable shapes a Stop cascade
// may legally leave on a session it REACHED, and reports which one it found
// (true = the settled shape — the Stop's effect has landed somewhere
// durable).
//
// The founder's decision of 2026-09-24 retired the single shape this test used
// to assert exclusively. A Stop marker is an INSTRUCTION ("do not run this
// generation"); landing the settled state IS that instruction being carried
// out, so the marker is spent and cleared. Who stopped it and when live in the
// event log, not on the record. pkg/session/lifecycle.go's write choke point
// enforces the same rule from the other side — it REJECTS a `stopped` (or
// terminal) record that still carries a current-generation marker, because
// the pair says "finished" and "still waiting to be stopped" at once. So:
//
//   - NOT settled -> the instruction is still outstanding and the
//     current-generation marker MUST be on the record. Unchanged by the
//     decision; this is the shape a session with a live turn sits in until the
//     turn unwinds.
//   - settled -> the instruction has been carried out. The marker is gone,
//     and the state left in its place is EITHER `stopped`, carrying the
//     persisted session.StopNote (the cascade's own cancellation is what
//     ended the turn — D2/D6, ADR-20260928-sub-agent-control-plane D8
//     ~L412-414: "withdraw the...separate terminal `failed(interrupted)`
//     state...use ordinary D2/D6 stopped-session machinery"; `stopped` is
//     explicitly NON-terminal — session.IsTerminalLifecycleState — so
//     rec.Terminal() alone no longer detects this shape, unlike the retired
//     terminal `cancelled` it replaces) OR `completed` (the founder's
//     2026-09-29 ruling for fix/1074-stop-survives-terminal: Stop's cancel is
//     ASYNCHRONOUS — turn.go::requestCancelForGeneration only fires the
//     turn's context cancel and returns, so a turn that had ALREADY produced
//     its final answer before the cascade's cancellation could take effect
//     legitimately finishes and lands `completed`; the Stop arrived too late
//     to change an outcome already decided, and that session still counts as
//     done). This function does NOT decide which of the two is the RIGHT one
//     for a given run — it only pins the LEGAL set; the caller must
//     independently confirm which one actually occurred and that the record
//     matches it (assertStopOutcomeMatchesState, below) rather than accepting
//     either unconditionally, which Hard Constraint #7 forbids as a
//     "known flaky, don't block" non-fix.
//
// Every other shape is a LOST Stop and fails: `running`/`queued` with no
// marker means the cascade's durable write vanished, `stopped` with no
// stop_note means D2/CRIT-001's persisted-note invariant was not honored, and
// any other terminal state (i.e. `failed`) means something else entirely went
// wrong with the turn — none of this Stop's two legal endings. Neither limb
// is weakened to a nil-check — both still have to name the exact generation.
func assertStopLandedOn(t *testing.T, rec *session.LifecycleRecord, when string) bool {
	t.Helper()
	live := rec.Stop != nil && rec.Stop.Generation == rec.Generation
	// settled: the Stop's effect has landed somewhere durable. rec.Terminal()
	// alone is no longer sufficient — `stopped` replaced the retired terminal
	// `cancelled` and is explicitly NON-terminal (ADR D8 ~L412-414: "use
	// ordinary D2/D6 stopped-session machinery", not a separate terminal
	// state).
	settled := rec.Terminal() || rec.State == session.LifecycleStopped
	legalLanded := rec.State == session.LifecycleCompleted ||
		(rec.State == session.LifecycleStopped && rec.StopNote != nil) // ADR D8 ~L412-414 / D2 CRIT-001: a landed stopped record requires a persisted stop_note
	switch {
	case settled && live:
		t.Fatalf("B %s landed at %q AND still carries a current-generation stop marker %+v — "+
			"a spent instruction must be cleared when it is carried out (and lifecycle.go's write "+
			"choke point rejects this pair outright)", when, rec.State, rec.Stop)
	case settled && !legalLanded:
		t.Fatalf("B %s landed at %q, want %q (with a stop_note) or %q — a session the cascade REACHED "+
			"and settled was either stopped or finished on its own terms before the Stop could take "+
			"effect; any OTHER settled state means neither of those happened (record=%+v)",
			when, rec.State, session.LifecycleStopped, session.LifecycleCompleted, rec)
	case settled:
		// Which of the two legal shapes a run lands in depends on whether B
		// still had a live turn when the cascade reached it, so record it:
		// `-v` output is the only thing that distinguishes them afterwards.
		// This does NOT yet confirm the shape is the RIGHT one for this run
		// — assertStopOutcomeMatchesState does that independently.
		t.Logf("B %s: settled shape — state=%q generation=%d, stop_note=%+v, spent stop marker cleared (older marker: %+v)",
			when, rec.State, rec.Generation, rec.StopNote, rec.Stop)
		return true
	case live:
		t.Logf("B %s: outstanding shape — state=%q generation=%d, live stop marker %+v",
			when, rec.State, rec.Generation, rec.Stop)
		return false
	default:
		t.Fatalf("B %s is %q at generation %d with stop marker %+v — a session the cascade REACHED "+
			"must either still carry its current-generation marker or have landed %q (with a stop_note) "+
			"or %q; this record carries neither, so the Stop was LOST",
			when, rec.State, rec.Generation, rec.Stop, session.LifecycleStopped, session.LifecycleCompleted)
	}
	return false
}

// terminalOutcomeForState is the ONE steer.Outcome
// steer_completion.go::completionDisposition (and the FinalAnswer branch
// completeSteeredTurnDurably falls into when it returns "") ever pairs with
// each of the two settled states assertStopLandedOn now treats as legal for
// a Stop-reached session (D2/D6, ADR-20260928-sub-agent-control-plane D8
// ~L412-414 — `stopped` replaces the retired terminal `cancelled` and stays
// non-terminal):
//
//   - session.LifecycleCompleted only ever follows steer.OutcomeFinalAnswer
//     (completeSteeredTurnDurably: `outcome = steer.OutcomeFinalAnswer;
//     nextState = session.LifecycleCompleted`).
//   - session.LifecycleStopped only ever follows steer.OutcomeInterrupted in
//     THIS harness's Stop-cascade scenario (completionDisposition: `case
//     errors.Is(runErr, context.Canceled), result.status ==
//     TurnEndStatusAborted: return steer.OutcomeInterrupted,
//     session.LifecycleStopped, ...`) — the cascade's own cancellation
//     stamps cause "stop"/"cascade" (steer_cancel.go::stampStop), never
//     "timeout" or "restart", neither of which this Stop-cascade E2E
//     harness exercises.
//
// This is the production mapping restated for the test, not a fresh guess —
// see assertStopOutcomeMatchesState for why restating it here is not circular.
func terminalOutcomeForState(state session.LifecycleState) (steer.Outcome, bool) {
	switch state {
	case session.LifecycleCompleted:
		return steer.OutcomeFinalAnswer, true
	case session.LifecycleStopped:
		return steer.OutcomeInterrupted, true
	default:
		return "", false
	}
}

// assertStopOutcomeMatchesState is the check Hard Constraint #7 requires on
// top of assertStopLandedOn: it independently determines WHICH of the two
// legal settled shapes actually occurred (D2/D6, ADR D8 ~L412-414: `stopped`
// replaces the retired terminal `cancelled`), rather than accepting
// completed-or-stopped as an unchecked either/or ("either is fine, don't
// care which" is a loosened, non-deterministic assertion — the exact
// "known flaky, don't block" closure path Hard Constraint #7 forbids).
//
// The independent signal is sessionID's OWN upward-delivery outcome, recorded
// by upward (recordingUpwardDeliverer) at Deliver time. This is NOT a
// re-derivation of rec.State from itself: steer_completion.go's own comment
// places that Deliver call strictly BEFORE the terminal-state write
// ("Delivery is deliberately first; boot recovery can repair a
// delivered-but-not-terminal record, while terminal-first could lose the
// only copy of the child's result") — the same ordering
// reportSteeredSessionTerminalUpward mirrors for the "never ran" cancel path
// (steer_cancel.go). So the outcome recorded here is a SEPARATE artifact,
// captured at a different point in the code than the lifecycle record read,
// and a regression that writes the wrong terminal State for what was
// actually decided and delivered (e.g. a Mutate-closure variable mix-up
// between outcome and nextState) changes State WITHOUT changing the
// already-recorded Outcome — exactly what this catches.
func assertStopOutcomeMatchesState(t *testing.T, upward *recordingUpwardDeliverer, sessionID string, state session.LifecycleState, when string) {
	t.Helper()
	wantOutcome, ok := terminalOutcomeForState(state)
	if !ok {
		// ADR D8 ~L412-414: `stopped` replaces the retired terminal `cancelled`.
		t.Fatalf("%s: %q is not one of the two legal Stop-reached settled states (%q/%q) — "+
			"assertStopLandedOn should already have fataled on this",
			when, state, session.LifecycleStopped, session.LifecycleCompleted)
	}
	delivered := upward.outcomesFor(sessionID)
	if len(delivered) == 0 {
		t.Fatalf("%s: B landed terminal at %q but its own upward delivery recorded NO outcome at all — "+
			"the lifecycle record and the delivered message disagree about what happened to B's turn "+
			"(steer_completion.go's own ordering delivers before it writes terminal, so an empty record "+
			"here means the write and the delivery came from two different, inconsistent decisions)",
			when, state)
	}
	got := delivered[len(delivered)-1]
	if got != wantOutcome {
		t.Fatalf("%s: B landed terminal at %q, which only ever follows upward outcome %q, but the LAST "+
			"outcome its own upward delivery actually carried was %q (full history: %v) — the lifecycle "+
			"record and the delivered message disagree about what happened to B's turn",
			when, state, wantOutcome, got, delivered)
	}
}

// sameStopMarker reports whether two decodes of a lifecycle record carry the
// identical Stop marker, treating "no marker" as a value so that a marker
// appearing out of nowhere and one vanishing are both mismatches rather than
// silently-skipped nil cases.
//
// time.Time is compared with Equal, never ==: the two values reach here
// through different decodes, and == would also compare the monotonic reading
// and the *Location pointer.
func sameStopMarker(before, after *session.Stop) bool {
	if before == nil || after == nil {
		return before == nil && after == nil
	}
	return before.At.Equal(after.At) &&
		before.Generation == after.Generation &&
		before.By == after.By
}

// waitForLifecycleTerminalOrDeadline polls sessionID's lifecycle record until
// it reaches a terminal state, lands LifecycleStopped (rec.Stopped()), or
// timeout elapses, returning the LAST successful read either way.
//
// Why this closes the race a bare Load cannot: a terminal read is a STABLE
// baseline. lifecycle.go's write choke point (persistLocked) REJECTS any
// further write to a terminal record's own generation ("a resume
// must mint generation N+1 via resumed_from") — so once this loop observes
// Terminal(), nothing else can touch that generation's record again before a
// caller's very next read. Two SEPARATE lock acquisitions (this Load, then a
// later Revive) racing a concurrent in-flight unwind is exactly what produced
// "Revive discarded the older stop marker" in CI (run 36263187959): a bare
// re-read immediately before Revive only NARROWS that window, it cannot
// CLOSE it, because Load-then-Revive are still two sequential calls with an
// unavoidable gap between them regardless of how close together they sit in
// source. Waiting for Terminal() first removes the gap's only remaining
// degree of freedom: once terminal, the two reads are guaranteed to agree.
//
// A record that has LANDED LifecycleStopped (rec.Stopped() true via
// ADR-20260928-sub-agent-control-plane.md line ~636's widened predicate) is
// the OTHER legal "settled" shape assertStopLandedOn documents — the
// current-generation fence is already cleared by TransitionSession the
// instant it lands, so there is no further in-flight unwind left to race
// against on THIS generation either; polling past that point only spends the
// budget for no benefit. This loop therefore also returns promptly on it,
// rather than spinning to the deadline (the bug this file's own RED pack,
// wait_for_lifecycle_terminal_landed_stopped_test.go, was written to catch).
//
// Bounded (matches this file's own 10s-deadline/10ms-poll convention, e.g.
// e2eHarness.waitForTreeTurns): a session the cascade left in the OTHER legal
// "outstanding" shape (assertStopLandedOn) — a current-generation marker with
// no live turn left to ever unwind it — never reaches Terminal() or lands
// LifecycleStopped, and this loop falls through at the deadline with
// whatever it last read, no worse than the unconditional single Load it
// replaces. The CI failure this fixes ran in 0.09s end to end, so a real
// unwind is expected to land many orders of magnitude inside this budget.
func waitForLifecycleTerminalOrDeadline(t *testing.T, store *session.LifecycleStore, sessionID string, timeout time.Duration) *session.LifecycleRecord {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		rec, err := store.Load(sessionID)
		if err != nil {
			t.Fatalf("load %s while waiting for a stable pre-Revive snapshot: %v", sessionID, err)
		}
		if rec.Terminal() || rec.Stopped() || time.Now().After(deadline) {
			return rec
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestE2E_StopSurvivesRestart(t *testing.T) {
	h := newE2EHarness(t)
	// Frozen D2/D7: "The cascade lands every reached session in stopped ...
	// same generation, resumable". D6 replaces interrupted-final publication
	// with the direct parent's durable notice; the completion-winner oracle
	// remains unchanged. This harness reopens the real stores on Reboot.
	report, err := h.tree.Stop(h.tree.Root.SessionID)
	if err != nil {
		t.Fatalf("Stop(root): %v", err)
	}
	if !slices.Contains(report.Reached, h.tree.B.SessionID) {
		t.Fatalf("Stop(root) did not reach B (%s): reached=%v skipped_terminal=%v unreachable=%+v",
			h.tree.B.SessionID, report.Reached, report.SkippedTerminal, report.Unreachable)
	}
	stopped, err := h.lifecycle.Load(h.tree.B.SessionID)
	if err != nil {
		t.Fatalf("load B after Stop: %v", err)
	}
	wasSettled := assertStopLandedOn(t, stopped, "immediately after Stop")
	if wasSettled {
		g7AssertSettledStopPublication(t, h, stopped, "immediately after Stop")
	}
	oldGeneration := stopped.Generation
	if crashErr := h.tree.Crash(); crashErr != nil {
		t.Fatalf("Crash: %v", crashErr)
	}
	if rebootErr := h.tree.Reboot(context.Background()); rebootErr != nil {
		t.Fatalf("Reboot: %v", rebootErr)
	}
	reopened, err := h.tree.Deps().LifecycleStore.Load(h.tree.B.SessionID)
	if err != nil {
		t.Fatalf("load B after reboot: %v", err)
	}
	if reopened.Generation != oldGeneration {
		t.Fatalf("B generation moved across the restart: %d -> %d", oldGeneration, reopened.Generation)
	}
	isSettled := assertStopLandedOn(t, reopened, "after crash+reboot")
	if isSettled {
		g7AssertSettledStopPublication(t, h, reopened, "after crash+reboot")
	}
	if wasSettled && !isSettled {
		t.Fatalf("B was settled before the crash and is %q at generation %d after it — the restart resurrected it (before=%+v after=%+v)",
			reopened.State, reopened.Generation, stopped, reopened)
	}
	switch {
	case !isSettled:
		if stopped.Stop == nil || reopened.Stop == nil {
			t.Fatalf("B has an outstanding Stop on both sides but a marker is missing: before=%+v after=%+v", stopped.Stop, reopened.Stop)
		}
		if !sameStopMarker(stopped.Stop, reopened.Stop) {
			t.Fatalf("B's outstanding Stop marker changed across reopen: before=%+v after=%+v", stopped.Stop, reopened.Stop)
		}
	case wasSettled:
		if !sameStopMarker(stopped.Stop, reopened.Stop) {
			t.Fatalf("B was settled on both sides, but its stop marker changed: before=%+v after=%+v", stopped.Stop, reopened.Stop)
		}
		if stopped.State == session.LifecycleStopped && !g7SameStopNote(stopped.StopNote, reopened.StopNote) {
			t.Fatalf("D8: restart rewrote an already-landed Stop note: before=%+v after=%+v", stopped.StopNote, reopened.StopNote)
		}
	default:
		t.Logf("B's Stop settled inside the restart window: %q -> %q at generation %d (marker %+v spent)",
			stopped.State, reopened.State, reopened.Generation, stopped.Stop)
	}
	preRevive := g7AwaitOwnerSettled(t, h.tree.Deps().LifecycleStore, h.tree.B.SessionID)
	newGeneration, err := h.tree.Revive(h.tree.B.SessionID)
	if err != nil {
		t.Fatalf("Revive(B): %v", err)
	}
	// Frozen D2 CRIT-001: "resumes a stopped child on the same generation
	// ... only done/failed mints a next generation". The old unconditional
	// generation bump and retention of a spent Stop fence are superseded.
	wantGeneration := oldGeneration + 1
	if preRevive.State == session.LifecycleStopped {
		wantGeneration = oldGeneration
	}
	if newGeneration != wantGeneration {
		t.Fatalf("Revive(B) generation = %d, want %d for prior state %q (D2)", newGeneration, wantGeneration, preRevive.State)
	}
	revived, err := h.tree.Deps().LifecycleStore.Load(h.tree.B.SessionID)
	if err != nil {
		t.Fatalf("load B after Revive: %v", err)
	}
	if revived.Generation != newGeneration {
		t.Fatalf("B generation after Revive = %d, want the resumed %d", revived.Generation, newGeneration)
	}
	if revived.Stopped() {
		t.Fatalf("B is still stopped after Revive: state=%q marker=%+v at generation %d", revived.State, revived.Stop, revived.Generation)
	}
	if preRevive.State == session.LifecycleStopped {
		if revived.State != session.LifecycleQueued || revived.Stop != nil || revived.StopNote != nil || revived.StopEffect != nil || revived.ExecutionID != nil {
			t.Fatalf("D2: stopped resume must atomically queue a fresh admission and clear the old fence/note/effect/identity: %+v", revived)
		}
	} else if preRevive.Stop != nil {
		if revived.Stop == nil || revived.Stop.Generation != oldGeneration {
			t.Fatalf("Revive discarded an older terminal-generation stop marker: before=%+v after=%+v", preRevive.Stop, revived.Stop)
		}
	}
}

func TestE2E_CompletionWakesPerChild(t *testing.T) {
	release := make(chan struct{})
	h := newE2EHarnessWithProvider(t, &e2eBlockingProvider{release: release}, testutil.RecordingOutbound(t), false)
	defer close(release)
	for _, parent := range []testutil.TreeNode{h.tree.A, h.tree.B} {
		delivery, err := h.tree.Reenter(parent.SessionID)
		if err != nil {
			t.Fatalf("completion handback to %s: %v", parent.Name, err)
		}
		if delivery.MessageID == "" {
			t.Fatalf("completion handback to %s has empty message id", parent.Name)
		}
		if delivery.Outcome != steer.DeliveryWoke && delivery.Outcome != steer.DeliveryQueuedIntoLiveTurn {
			t.Fatalf("completion handback to %s outcome = %q, want woke or queued_into_live_turn", parent.Name, delivery.Outcome)
		}
	}
}

func TestE2E_Restart_ParentToldOnce(t *testing.T) {
	h := newE2EHarness(t)
	first, err := h.tree.QueueWake(h.tree.B.SessionID, h.tree.B.Generation)
	if err != nil {
		t.Fatalf("persist terminal handback before crash: %v", err)
	}
	if first.MessageID == "" {
		t.Fatal("first terminal delivery has empty message id")
	}
	if crashErr := h.tree.Crash(); crashErr != nil {
		t.Fatalf("Crash: %v", crashErr)
	}
	if rebootErr := h.tree.Reboot(context.Background()); rebootErr != nil {
		t.Fatalf("Reboot: %v", rebootErr)
	}
	second, err := h.tree.QueueWake(h.tree.B.SessionID, h.tree.B.Generation)
	if err != nil {
		t.Fatalf("repair terminal handback after reboot: %v", err)
	}
	if second.MessageID != first.MessageID {
		t.Fatalf("terminal delivery id changed across restart: before=%q after=%q", first.MessageID, second.MessageID)
	}
}

func TestE2E_TaskChildInSidePanel(t *testing.T) {
	h := newE2EHarness(t)
	taskChild := seedTaskChild(t, h, h.tree.Root)
	message := progressMessage(t, taskChild, h.tree.Root.SessionID)
	delivery, err := h.deliverer.Deliver(context.Background(), steer.UpwardEvent{
		ChildSessionID: taskChild.SessionID, Outcome: steer.OutcomeProgress, Message: message,
	})
	if err != nil {
		t.Fatalf("deliver task-child progress: %v", err)
	}
	if delivery.MessageID == "" {
		t.Fatal("task-child progress delivery has empty message id")
	}

	entries, err := h.sessions.ReadTranscript(h.tree.Root.SessionID)
	if err != nil {
		t.Fatalf("read parent transcript: %v", err)
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal parent transcript: %v", err)
	}
	for _, frameType := range []string{"subagent_start", "subagent_state", "subagent_message"} {
		if !strings.Contains(string(raw), frameType) {
			t.Fatalf("parent transcript lacks persisted %s event for task child; replay cannot restore the side-panel row", frameType)
		}
	}
}

func seedTaskChild(t *testing.T, h *e2eHarness, parent testutil.TreeNode) testutil.TreeNode {
	t.Helper()
	const agentID = "adr091-fixture-task-agent"
	result, err := h.launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parent.SessionID,
		TargetAgentID:     agentID,
		Label:             "ADR-091 task child",
		Task:              "Exercise the task-origin side-panel path",
		Origin:            steer.Origin{Kind: steer.OriginKindTask, CallID: "create-task-call", TaskID: "task-record"},
	})
	if err != nil {
		t.Fatalf("launch task child: %v", err)
	}
	node := testutil.TreeNode{
		Name: "task", SessionID: result.SessionID, AgentID: agentID,
		WorkspaceID: parent.WorkspaceID, Generation: result.Generation,
	}
	return node
}

func progressMessage(t *testing.T, child testutil.TreeNode, parentID string) generated.SessionMessage {
	t.Helper()
	gen := child.Generation
	var message generated.SessionMessage
	if err := message.FromSessionMessageProgress(generated.SessionMessageProgress{
		MessageId: fmt.Sprintf("%s:%d:progress", child.SessionID, child.Generation),
		SessionId: child.SessionID, ParentSessionId: &parentID,
		CreatedAt: time.Now().UTC(), Generation: &gen, Depth: 1,
		Direction:      generated.SessionMessageProgressDirection("child_to_parent"),
		SenderIdentity: child.AgentID, Text: "task child is working", UntrustedOrigin: true,
	}); err != nil {
		t.Fatalf("encode task child progress: %v", err)
	}
	return message
}
