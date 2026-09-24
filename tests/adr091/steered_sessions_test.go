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
}

var _ steer.UpwardDeliverer = (*recordingUpwardDeliverer)(nil)

func (d *recordingUpwardDeliverer) Deliver(ctx context.Context, event steer.UpwardEvent) (steer.Delivery, error) {
	delivery, err := d.inner.Deliver(ctx, event)
	if err == nil {
		d.mu.Lock()
		d.seen = append(d.seen, event.ChildSessionID)
		d.mu.Unlock()
	}
	return delivery, err
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
	if err := os.WriteFile(filepath.Join(workspaceDir, "adr091-fixture-workspace.json"), workspaceRecord, 0o600); err != nil {
		t.Fatalf("write fixture workspace: %v", err)
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

	for {
		select {
		case outbound := <-h.msgBus.OutboundChan():
			h.recorder.Record(steer.BoundaryFinalReply, outbound.SessionID, "human", "leak")
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
			h.recorder.Record(steer.BoundaryMedia, outboundMedia.SessionID, "human", "media-leak")
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
// (true = the terminal shape).
//
// The founder's decision of 2026-09-24 retired the single shape this test used
// to assert exclusively. A Stop marker is an INSTRUCTION ("do not run this
// generation"); landing the terminal state IS that instruction being carried
// out, so the marker is spent and cleared. Who stopped it and when live in the
// event log, not on the record. pkg/session/lifecycle.go's write choke point
// enforces the same rule from the other side — it REJECTS a terminal record
// that still carries a current-generation marker, because the pair says
// "finished" and "still waiting to be stopped" at once. So:
//
//   - NOT terminal -> the instruction is still outstanding and the
//     current-generation marker MUST be on the record. Unchanged by the
//     decision; this is the shape a session with a live turn sits in until the
//     turn unwinds.
//   - terminal -> the instruction has been carried out. The marker is gone and
//     `cancelled` is the fingerprint the Stop left in its place.
//
// Every other shape is a LOST Stop and fails: `running`/`queued` with no
// marker means the cascade's durable write vanished, and any terminal state
// other than `cancelled` (notably `completed`) means the child finished on its
// own terms and the Stop never took effect at all. Neither is weakened to a
// nil-check — both limbs still have to name the exact generation.
func assertStopLandedOn(t *testing.T, rec *session.LifecycleRecord, when string) bool {
	t.Helper()
	live := rec.Stop != nil && rec.Stop.Generation == rec.Generation
	switch {
	case rec.Terminal() && live:
		t.Fatalf("B %s is terminal (%s) AND still carries a current-generation stop marker %+v — "+
			"a spent instruction must be cleared when it is carried out (and lifecycle.go's write "+
			"choke point rejects this pair outright)", when, rec.State, rec.Stop)
	case rec.Terminal() && rec.State != session.LifecycleCancelled:
		t.Fatalf("B %s is terminal at %q, want %q — a session the cascade REACHED and terminalised "+
			"was stopped, not finished; any other terminal state means the Stop never took effect "+
			"(record=%+v)", when, rec.State, session.LifecycleCancelled, rec)
	case rec.Terminal():
		// Which of the two legal shapes a run lands in depends on whether B
		// still had a live turn when the cascade reached it, so record it:
		// `-v` output is the only thing that distinguishes them afterwards.
		t.Logf("B %s: terminal shape — state=%q generation=%d, spent stop marker cleared (older marker: %+v)",
			when, rec.State, rec.Generation, rec.Stop)
		return true
	case live:
		t.Logf("B %s: outstanding shape — state=%q generation=%d, live stop marker %+v",
			when, rec.State, rec.Generation, rec.Stop)
		return false
	default:
		t.Fatalf("B %s is %q at generation %d with stop marker %+v — a session the cascade REACHED "+
			"must either still carry its current-generation marker or have landed %q; this record "+
			"carries neither, so the Stop was LOST",
			when, rec.State, rec.Generation, rec.Stop, session.LifecycleCancelled)
	}
	return false
}

func TestE2E_StopSurvivesRestart(t *testing.T) {
	h := newE2EHarness(t)
	report, err := h.tree.Stop(h.tree.Root.SessionID)
	if err != nil {
		t.Fatalf("Stop(root): %v", err)
	}
	// Everything below is about what a REACHED session's durable record looks
	// like across a restart, so "the cascade reached B at all" is a premise,
	// not an assumption: a B that was skipped as already-terminal would make
	// every assertion after this vacuous.
	if !slices.Contains(report.Reached, h.tree.B.SessionID) {
		t.Fatalf("Stop(root) did not reach B (%s): reached=%v skipped_terminal=%v unreachable=%+v",
			h.tree.B.SessionID, report.Reached, report.SkippedTerminal, report.Unreachable)
	}
	stopped, err := h.lifecycle.Load(h.tree.B.SessionID)
	if err != nil {
		t.Fatalf("load B after Stop: %v", err)
	}
	wasTerminal := assertStopLandedOn(t, stopped, "immediately after Stop")
	oldGeneration := stopped.Generation
	if err := h.tree.Crash(); err != nil {
		t.Fatalf("Crash: %v", err)
	}
	if err := h.tree.Reboot(context.Background()); err != nil {
		t.Fatalf("Reboot: %v", err)
	}
	reopened, err := h.tree.Deps().LifecycleStore.Load(h.tree.B.SessionID)
	if err != nil {
		t.Fatalf("load B after reboot: %v", err)
	}
	// THE property this test is named for: the crash/reboot changes nothing.
	// Whichever of the two shapes the Stop left, the reopened store must show
	// the SAME one, at the same generation, with the same marker (or the same
	// absence of one) — a Stop that evaporated on restart is the regression.
	if reopened.Generation != oldGeneration {
		t.Fatalf("B generation moved across the restart: %d -> %d", oldGeneration, reopened.Generation)
	}
	if got := assertStopLandedOn(t, reopened, "after crash+reboot"); got != wasTerminal {
		t.Fatalf("B's durable shape changed across the restart: terminal=%v before, terminal=%v after "+
			"(before=%+v after=%+v)", wasTerminal, got, stopped, reopened)
	}
	switch {
	case stopped.Stop == nil && reopened.Stop != nil:
		t.Fatalf("B carried no stop marker before the restart but has %+v after", reopened.Stop)
	case stopped.Stop != nil && reopened.Stop == nil:
		t.Fatalf("B's stop marker %+v did not survive the restart", stopped.Stop)
	case stopped.Stop != nil:
		// time.Time is compared with Equal, never ==: the two values reach
		// here through different decodes and == would also compare the
		// monotonic reading and the *Location pointer.
		if !reopened.Stop.At.Equal(stopped.Stop.At) ||
			reopened.Stop.Generation != stopped.Stop.Generation ||
			reopened.Stop.By != stopped.Stop.By {
			t.Fatalf("B's stop marker changed across the restart: before=%+v after=%+v", stopped.Stop, reopened.Stop)
		}
	}
	newGeneration, err := h.tree.Revive(h.tree.B.SessionID)
	if err != nil {
		t.Fatalf("Revive(B): %v", err)
	}
	if newGeneration <= oldGeneration {
		t.Fatalf("Revive(B) generation = %d, want > %d", newGeneration, oldGeneration)
	}
	revived, err := h.tree.Deps().LifecycleStore.Load(h.tree.B.SessionID)
	if err != nil {
		t.Fatalf("load B after Revive: %v", err)
	}
	if revived.Generation != newGeneration {
		t.Fatalf("B generation after Revive = %d, want the minted %d", revived.Generation, newGeneration)
	}
	// A revived session is not stopped: the only marker it may still carry is
	// an OLDER one, which I-6 deliberately keeps as inert history
	// (LifecycleRecord.Stopped()'s doc comment names all four shapes).
	if revived.Stopped() {
		t.Fatalf("B is still live-stopped after Revive: marker %+v at generation %d", revived.Stop, revived.Generation)
	}
	if reopened.Stop != nil {
		if revived.Stop == nil || revived.Stop.Generation != oldGeneration {
			t.Fatalf("Revive discarded the older stop marker: before=%+v after=%+v — an earlier "+
				"generation's marker is inert history a revival retains", reopened.Stop, revived.Stop)
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
	if err := h.tree.Crash(); err != nil {
		t.Fatalf("Crash: %v", err)
	}
	if err := h.tree.Reboot(context.Background()); err != nil {
		t.Fatalf("Reboot: %v", err)
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
