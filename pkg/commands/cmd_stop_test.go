package commands

// cmd_stop_test.go — RED wave, corrected transport (qa-lead,
// test/a-redirect-transport-red): the D9 chat commands /stop and
// /stop-redirect, re-based on the landed generated RedirectFrame.
//
// This pack CORRECTS the superseded QA2 pack (feat/stopall-qa2 @ 62eefb3d0),
// which pinned the guessed delivery-agent transport. Corrections per the
// architect's corrected ruling (stream-a-seams-assessment.md §3.1/§3.2/§4):
//   - /stop-redirect is AvailableWhileStreaming TRUE and DeliveryClient —
//     redirect stops first, then gives the new instruction (D9 founder model:
//     the command name encodes stop-first); mid-stream is the primary case.
//     The web path is CLIENT INTERCEPTION: the SPA intercepts the typed
//     command and sends the dedicated generated RedirectFrame (mirroring
//     /cancel's CancelFrame pattern) — it never touches message intake, so it
//     executes mid-stream. A delivery-agent path cannot (mid-turn text enters
//     the steering queue unparsed — pkg/agent/session_worker.go →
//     enqueueSteeringFromMessage).
//   - Root-chat refusal is fail-closed on helper identity (§3.2): unresolvable
//     identity ⇒ IsHelperSession false ⇒ root-style refusal, nothing sent.
//   - Sentinel outcomes: ErrNotHelperSession (root-style refusal w/ guidance)
//     and ErrNothingToRedirect (done/failed: "already finished — use
//     RESUME"). NO busy sentinel exists — mid-stream is the normal case. The
//     sentinel vars do not exist yet, so tests referencing them would not
//     compile; the unknown-error truthfulness leg is tested here with a proxy
//     error, and the two named mappings are specified to backend-lead as
//     GREEN-scope assertions (see REPORT.md).
//   - Every other decided assertion from the previous pack (receipts,
//     three-outcome truthfulness, root refusal, usage edges, goal aliases,
//     no /steer, /cancel untouched) is preserved verbatim.
//
// SPEC SOURCES (every expected value below derives from these, never from the
// implementation — oracle independence):
//   - ADR-20260928-sub-agent-control-plane.md @ cd20cf8b, D9 (founder F1011-Q1
//     + the founder model quote): /stop and /stop-redirect <instruction> are
//     THEIR OWN commands, not aliases of /cancel; /cancel keeps FR-5's "no
//     aliases"; no /steer alias (founder O4); /goal stop remains the /goal
//     clear alias; chat commands are conversation-scoped (#955) and authorized
//     for the signed-in owner; the root's own /stop still works.
//   - D9 input table: /stop (root OR helper) stops ONLY that session's current
//     turn — no cascade; /stop-redirect in a HELPER's chat = D2 redirect on
//     that helper only (subtree keeps working); in the ROOT's chat = refuse
//     with guidance to target a helper; bare/whitespace instruction → usage
//     reply, change nothing.
//   - D2 redirect: fence + stop_note cause "redirect_pause" under lock →
//     interrupt the live turn → same-generation resume with the instruction as
//     the newest message. Anchor symbols that exist:
//     pkg/session/lifecycle_edge.go::StopCauseRedirectPause,
//     pkg/agent/steer_cancel.go::StopTurns.
//   - cancel-cross-channel-spec.md decision 12 / FR-5 / review F-20: the
//     exact-set registration assertion, as amended by D9; FR-3a streaming
//     visibility (amended by D9 to name /cancel, /stop, /stop-redirect).
//   - Architect-endorsed interface shapes (§1.1/§1.3):
//     StopSessionTurn(ctx, sessionID, userID, channel string) (fired, armed
//     bool, err error); RedirectSessionTurn(ctx, sessionID, instruction,
//     userID, channel string) error.
//
// LIFECYCLE RECEIPTS BELOW THIS SEAM ARE EXPLICITLY OUT OF SCOPE HERE (seam
// ruling §6): stop_note cause redirect_pause, same-generation resume, the seq
// fence, "subtree keeps working", open-question retention and goal
// non-interference are production guarantees of StopSessionTurn /
// RedirectSessionTurn owned by Backend-CP — never cited as covered by
// command-stub greens. The fence/resume implementation is Backend-CP scope and
// MUST NOT be certified by these command mocks.

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// ─── Test fake: the agent-loop seam the handlers must call ──────────────────
//
// REPORTED INTERFACE SHAPES for backend-lead (the minimal injection this pack
// needs; primitive types only, per AgentLoopInterface's no-pkg/agent-import
// constraint; architect-endorsed §1.1/§1.3). Add to AgentLoopInterface in
// pkg/commands/runtime.go:
//
//	// StopSessionTurn stops ONLY the named session's current turn — D9 /stop
//	// (scope session, no cascade; D2 stop semantics: cause "stop",
//	// non-terminal, never ends a goal, keeps an open question open). Must
//	// delegate to the SAME StopTurns(subtree=false) chain the gateway Stop
//	// press uses — one shared stop implementation, never a divergent second
//	// path. Three-outcome contract identical to RequestCancelForSession:
//	//   (fired=true, armed=false, nil)            — stop fired.
//	//   (fired=false, armed=true, nil)            — pre-registration latch armed.
//	//   (fired=false, armed=false, nil)           — nothing to stop.
//	//   err non-nil                               — real failure, must surface.
//	StopSessionTurn(ctx context.Context, sessionID, userID, channel string) (fired, armed bool, err error)
//
//	// RedirectSessionTurn performs the D2 redirect on the named session:
//	// fence + stop_note cause redirect_pause under lock, interrupt the live
//	// turn (skipped when already stopped), same-generation resume with
//	// instruction as the newest message; subtree keeps working. Returns named
//	// sentinel errors for guidance outcomes — ErrNotHelperSession,
//	// ErrNothingToRedirect (already finished → "use RESUME") — so handlers
//	// reply truthfully instead of one opaque error (mirrors
//	// ErrNoActiveTurn/ErrCancelArmed).
//	RedirectSessionTurn(ctx context.Context, sessionID, instruction, userID, channel string) error
//
// And add to Runtime in pkg/commands/runtime.go (located reflectively by
// setHelperSession below until it exists):
//
//	// IsHelperSession reports whether the current chat targets a helper
//	// session. Fail-closed (seam ruling §3.2): unresolvable identity — no
//	// lifecycle record, unreadable store, blank session id — is FALSE (⇒
//	// /stop-redirect gets the root-style refusal). false ⇒ workspace root.
//	IsHelperSession func() bool
type stopPackFakeLoop struct {
	// configuration
	fired bool
	armed bool
	err   error

	// recordings — the observable receipts
	stopCalls           []stopPackStopCall
	redirectCalls       []stopPackRedirectCall
	requestCancelCalls  int // the /cancel (tree) path — /stop and /stop-redirect must NOT use it
	requestCancelTokill []string
}

type stopPackStopCall struct {
	sessionID string
	userID    string
	channel   string
}

type stopPackRedirectCall struct {
	sessionID   string
	instruction string
	userID      string
	channel     string
}

func (f *stopPackFakeLoop) RequestCancelForSession(ctx context.Context, sessionID, userID, channel string) (bool, bool, error) {
	f.requestCancelCalls++
	f.requestCancelTokill = append(f.requestCancelTokill, sessionID)
	return f.fired, f.armed, f.err
}

func (f *stopPackFakeLoop) StopSessionTurn(ctx context.Context, sessionID, userID, channel string) (bool, bool, error) {
	f.stopCalls = append(f.stopCalls, stopPackStopCall{sessionID: sessionID, userID: userID, channel: channel})
	return f.fired, f.armed, f.err
}

func (f *stopPackFakeLoop) RedirectSessionTurn(ctx context.Context, sessionID, instruction, userID, channel string) error {
	f.redirectCalls = append(f.redirectCalls, stopPackRedirectCall{sessionID: sessionID, instruction: instruction, userID: userID, channel: channel})
	return f.err
}

// findStopPackDef looks a command up in BuiltinDefinitions by its registered
// NAME (the amended F-20 exact-set seam). A missing definition is a BLOCKED
// failure naming the spec — never a skip (skipped tests are invisible).
func findStopPackDef(t *testing.T, name string) *Definition {
	t.Helper()
	defs := BuiltinDefinitions()
	for i := range defs {
		if defs[i].Name == name {
			return &defs[i]
		}
	}
	t.Fatalf("BLOCKED: /%s command not registered in BuiltinDefinitions() — required by ADR D9 (subagent control plane), which adds it as its OWN command", name)
	return nil
}

// setHelperSession reflectively wires Runtime.IsHelperSession (the reported
// root-vs-helper seam) so this pack compiles — and fails BLOCKED, not with a
// build error — before backend-lead adds the field. Fail-closed rule (§3.2):
// the field MUST be func() bool and MUST return false on unresolvable identity.
func setHelperSession(t *testing.T, rt *Runtime, isHelper bool) {
	t.Helper()
	field := reflect.ValueOf(rt).Elem().FieldByName("IsHelperSession")
	if !field.IsValid() {
		t.Fatalf("BLOCKED: commands.Runtime.IsHelperSession func field not implemented — required by ADR D9 (root chat must refuse /stop-redirect with guidance to target a helper) and seam ruling §3.2 (fail-closed on unresolvable identity)")
	}
	if field.Kind() != reflect.Func {
		t.Fatalf("BLOCKED: commands.Runtime.IsHelperSession exists but is %v, want func() bool", field.Kind())
	}
	field.Set(reflect.ValueOf(func() bool { return isHelper }))
}

// invokeStopPack runs a definition's handler with a standard web-origin
// request and captures the single reply.
func invokeStopPack(t *testing.T, def *Definition, rt *Runtime, text string) (string, error) {
	t.Helper()
	return invokeStopPackOn(t, def, rt, text, "webchat")
}

// invokeStopPackOn is invokeStopPack with an explicit channel — the CLI-mirror
// receipt test runs the same handler seam under Channel "cli" (§3.1 CLI row:
// mirrors /cancel's CLI delivery, same handler seam, same sentinel replies).
func invokeStopPackOn(t *testing.T, def *Definition, rt *Runtime, text, channel string) (string, error) {
	t.Helper()
	if def.Handler == nil {
		t.Fatalf("BLOCKED: /%s registered with nil Handler — D9 requires an executable command", def.Name)
	}
	var reply string
	err := def.Handler(context.Background(), Request{
		Channel:  channel,
		SenderID: "@alice",
		Text:     text,
		Reply: func(s string) error {
			reply = s
			return nil
		},
	}, rt)
	return reply, err
}

// ─── Registration (amended F-20 exact-set assertion) ────────────────────────

// TestStopAndStopRedirect_RegisteredExactSet is the D9 amendment of the
// cancel-cross-channel spec's exact-set registration assertion (review F-20 /
// T12a): /stop and /stop-redirect are registered as their OWN commands, while
// /cancel keeps FR-5's "no aliases" rule and no /steer alias exists anywhere.
func TestStopAndStopRedirect_RegisteredExactSet(t *testing.T) {
	stopDef := findStopPackDef(t, "stop")
	redirectDef := findStopPackDef(t, "stop-redirect")

	// Own commands, not aliases: zero aliases each (D9: "their own commands —
	// not aliases of /cancel").
	if len(stopDef.Aliases) != 0 {
		t.Errorf("/stop must be its own command with no aliases (D9), got %v", stopDef.Aliases)
	}
	if len(redirectDef.Aliases) != 0 {
		t.Errorf("/stop-redirect must be its own command with no aliases (D9), got %v", redirectDef.Aliases)
	}

	// /cancel still carries FR-5's no-aliases rule (D9 leaves it intact).
	cancelDef := findStopPackDef(t, "cancel")
	if len(cancelDef.Aliases) != 0 {
		t.Errorf("/cancel must keep zero aliases (FR-5 preserved by D9), got %v", cancelDef.Aliases)
	}

	// No /steer alias anywhere (founder O4 via D9), and abort/kill stay
	// forbidden as names and aliases (FR-5).
	defs := BuiltinDefinitions()
	for _, def := range defs {
		if def.Name == "steer" {
			t.Errorf("found command named %q — D9 (founder O4): no /steer alias may exist", def.Name)
		}
		if def.Name == "abort" || def.Name == "kill" {
			t.Errorf("found forbidden command name %q (FR-5)", def.Name)
		}
		for _, alias := range def.Aliases {
			switch alias {
			case "steer":
				t.Errorf("found forbidden alias %q on /%s (D9 founder O4)", alias, def.Name)
			case "abort", "kill":
				t.Errorf("found forbidden alias %q on /%s (FR-5)", alias, def.Name)
			case "cancel":
				if def.Name != "cancel" {
					t.Errorf("alias %q on /%s would blur the no-alias rule of the real /cancel (FR-5)", alias, def.Name)
				}
			}
		}
	}

	// /stop is available while streaming (D9 row 1 groups it with the Stop
	// button and Esc — inherently mid-stream controls) and on all three
	// surfaces, as for /cancel.
	if !stopDef.AvailableWhileStreaming {
		t.Error("/stop must set AvailableWhileStreaming — it must be usable mid-turn like the Stop button")
	}
	if len(stopDef.Surfaces) != 3 {
		t.Errorf("/stop surfaces = %v, want web+cli+channel (same as /cancel)", stopDef.Surfaces)
	}
	for _, s := range []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel} {
		found := false
		for _, has := range stopDef.Surfaces {
			if has == s {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("/stop missing surface %v (D9: surfaces as for /cancel)", s)
		}
	}
	// /stop rides the client path (§3.1: executes via cancelIfStreaming, the
	// Stop-button client path — same row as the Stop button and Esc).
	if stopDef.Delivery != DeliveryClient {
		t.Errorf("/stop Delivery = %q, want %q (client path, §3.1)", stopDef.Delivery, DeliveryClient)
	}

	// /stop-redirect — the CORRECTED transport decisions (§3.1; this pack's
	// reason to exist — the superseded pack pinned the opposite values):
	//   AvailableWhileStreaming TRUE: redirect stops first, then gives the new
	//   instruction (the command name encodes stop-first); mid-stream is the
	//   sequence's primary case, not an edge.
	//   DeliveryClient: the SPA intercepts and sends the dedicated generated
	//   RedirectFrame (the /cancel pattern) — never message intake, so it
	//   executes mid-stream. DeliveryAgent cannot work mid-turn (text enters
	//   the steering queue unparsed).
	if !redirectDef.AvailableWhileStreaming {
		t.Error("/stop-redirect must set AvailableWhileStreaming — redirect stops first then resumes mid-stream (D9 founder model; §3.1); the superseded false was a transport artifact")
	}
	if redirectDef.Delivery != DeliveryClient {
		t.Errorf("/stop-redirect Delivery = %q, want %q (client interception via the generated RedirectFrame; §3.1)", redirectDef.Delivery, DeliveryClient)
	}

	// /stop-redirect takes an instruction argument (D9: "/stop-redirect
	// <instruction>"); its usage line must say so.
	if !strings.Contains(redirectDef.Usage, "instruction") {
		t.Errorf("/stop-redirect Usage = %q, want it to name the <instruction> argument (D9)", redirectDef.Usage)
	}
	// Surfaced on web+cli+channel, as for /cancel (§3.1 web/channel/CLI rows).
	for _, s := range []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel} {
		found := false
		for _, has := range redirectDef.Surfaces {
			if has == s {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("/stop-redirect missing surface %v (§3.1: web + CLI mirror + channel interception)", s)
		}
	}
}

// ─── Compile-valid RED seams: the interface the GREEN must add ──────────────

// TestRedirectTransport_AgentLoopInterfaceMethodSet: the two loop primitives
// the D9 commands hang on must exist on AgentLoopInterface with the
// architect-endorsed primitive-typed signatures (§1.1/§1.3). Asserted by
// reflection so this file compiles — and fails BLOCKED, not with a build
// error — before backend-lead adds the methods (a compile error is not
// behavioural RED; this is).
func TestRedirectTransport_AgentLoopInterfaceMethodSet(t *testing.T) {
	iface := reflect.TypeOf((*AgentLoopInterface)(nil)).Elem()

	stop, ok := iface.MethodByName("StopSessionTurn")
	if !ok {
		t.Fatalf("BLOCKED: AgentLoopInterface.StopSessionTurn not implemented — required by ADR D9 /stop (seam ruling §1.1, architect-endorsed signature: (ctx, sessionID, userID, channel string) (fired, armed bool, err error))")
	}
	var wantStop func(context.Context, string, string, string) (bool, bool, error)
	if stop.Type != reflect.TypeOf(wantStop) {
		t.Errorf("StopSessionTurn signature = %v, want %v (architect-endorsed §1.1 — mirrors RequestCancelForSession's three-outcome contract)", stop.Type, reflect.TypeOf(wantStop))
	}

	redirect, ok := iface.MethodByName("RedirectSessionTurn")
	if !ok {
		t.Fatalf("BLOCKED: AgentLoopInterface.RedirectSessionTurn not implemented — required by ADR D9 row 2 / D2 redirect (seam ruling §1.3, architect-endorsed signature: (ctx, sessionID, instruction, userID, channel string) error)")
	}
	var wantRedirect func(context.Context, string, string, string, string) error
	if redirect.Type != reflect.TypeOf(wantRedirect) {
		t.Errorf("RedirectSessionTurn signature = %v, want %v (architect-endorsed §1.3; must return named sentinels ErrNotHelperSession / ErrNothingToRedirect for truthful guidance replies)", redirect.Type, reflect.TypeOf(wantRedirect))
	}
}

// ─── /stop — scope session, no cascade, conversation-scoped ─────────────────

// TestStopHandler_HelperChat_StopsOnlyThatSession: /stop typed in a helper's
// chat stops exactly that session via the session-scoped seam — never the
// /cancel (tree) path, never a redirect (D9 table row 1; no cascade).
func TestStopHandler_HelperChat_StopsOnlyThatSession(t *testing.T) {
	fake := &stopPackFakeLoop{fired: true}
	rt := &Runtime{SessionID: func() string { return "helper-1" }}
	rt = rt.WithAgentLoop(fake)
	setHelperSession(t, rt, true)

	def := findStopPackDef(t, "stop")
	reply, err := invokeStopPack(t, def, rt, "/stop")
	if err != nil {
		t.Fatalf("/stop handler returned error: %v", err)
	}
	if reply == "" {
		t.Error("/stop must reply (acknowledgment), got empty reply")
	}
	if len(fake.stopCalls) != 1 {
		t.Fatalf("StopSessionTurn call count = %d, want 1", len(fake.stopCalls))
	}
	call := fake.stopCalls[0]
	if call.sessionID != "helper-1" {
		t.Errorf("StopSessionTurn sessionID = %q, want %q (conversation-scoped, #955)", call.sessionID, "helper-1")
	}
	if call.userID != "@alice" || call.channel != "webchat" {
		t.Errorf("StopSessionTurn canceller = (%q, %q), want (@alice, webchat) for audit attribution", call.userID, call.channel)
	}
	if fake.requestCancelCalls != 0 {
		t.Errorf("RequestCancelForSession (the /cancel path) called %d times — /stop must not reuse the cancel/tree path (D9: scope session, no cascade); touched %v", fake.requestCancelCalls, fake.requestCancelTokill)
	}
	if len(fake.redirectCalls) != 0 {
		t.Errorf("/stop must not redirect, got %d redirect calls", len(fake.redirectCalls))
	}
}

// TestStopHandler_RootChat_ConversationScopedStillWorks: the root's own /stop
// works — same single-session receipt, no refusal (D9: "the root's own /stop
// still works").
func TestStopHandler_RootChat_ConversationScopedStillWorks(t *testing.T) {
	fake := &stopPackFakeLoop{fired: true}
	rt := &Runtime{SessionID: func() string { return "root-1" }}
	rt = rt.WithAgentLoop(fake)
	setHelperSession(t, rt, false)

	def := findStopPackDef(t, "stop")
	reply, err := invokeStopPack(t, def, rt, "/stop")
	if err != nil {
		t.Fatalf("/stop handler returned error in root chat: %v", err)
	}
	if reply == "" {
		t.Error("/stop in root chat must still acknowledge, got empty reply")
	}
	if len(fake.stopCalls) != 1 || fake.stopCalls[0].sessionID != "root-1" {
		t.Fatalf("StopSessionTurn calls = %+v, want exactly one for root-1 (conversation-scoped)", fake.stopCalls)
	}
	if fake.requestCancelCalls != 0 {
		t.Errorf("/stop in root chat must not cascade via the /cancel path (D9 row 1: no cascade), got %d calls", fake.requestCancelCalls)
	}
}

// TestStopHandler_CLISurface_SameReceipt: the CLI surface mirrors the web
// receipt — same handler seam, same truthful replies (§3.1 CLI row: "mirrors
// whatever /cancel's CLI delivery does today — same handler seam, same
// sentinel replies").
func TestStopHandler_CLISurface_SameReceipt(t *testing.T) {
	fake := &stopPackFakeLoop{fired: true}
	rt := &Runtime{SessionID: func() string { return "cli-session-1" }}
	rt = rt.WithAgentLoop(fake)
	setHelperSession(t, rt, true)

	def := findStopPackDef(t, "stop")
	reply, err := invokeStopPackOn(t, def, rt, "/stop", "cli")
	if err != nil {
		t.Fatalf("/stop handler returned error on the cli channel: %v", err)
	}
	if reply == "" {
		t.Error("/stop on cli must acknowledge, got empty reply")
	}
	if len(fake.stopCalls) != 1 {
		t.Fatalf("StopSessionTurn call count = %d, want 1", len(fake.stopCalls))
	}
	call := fake.stopCalls[0]
	if call.sessionID != "cli-session-1" || call.channel != "cli" || call.userID != "@alice" {
		t.Errorf("StopSessionTurn receipt = (%q, %q, %q), want (cli-session-1, cli, @alice) — CLI mirrors the web receipt", call.sessionID, call.channel, call.userID)
	}
	if fake.requestCancelCalls != 0 {
		t.Errorf("/stop on cli must not reuse the /cancel (tree) path, got %d calls", fake.requestCancelCalls)
	}
}

// TestStopHandler_NothingRunning_TruthfulReply: when nothing runs the reply
// must truthfully say so — distinguishable from the fired acknowledgment
// (task: "truthful replies when nothing runs"; the package's documented
// three-outcome truthfulness contract, runtime.go).
func TestStopHandler_NothingRunning_TruthfulReply(t *testing.T) {
	// Fired case — the acknowledgment baseline.
	firedFake := &stopPackFakeLoop{fired: true}
	rtFired := &Runtime{SessionID: func() string { return "s" }}
	rtFired = rtFired.WithAgentLoop(firedFake)
	setHelperSession(t, rtFired, true)
	firedReply, err := invokeStopPack(t, findStopPackDef(t, "stop"), rtFired, "/stop")
	if err != nil {
		t.Fatalf("fired case returned error: %v", err)
	}

	// Nothing-running case.
	noneFake := &stopPackFakeLoop{} // fired=false, armed=false
	rtNone := &Runtime{SessionID: func() string { return "s" }}
	rtNone = rtNone.WithAgentLoop(noneFake)
	setHelperSession(t, rtNone, true)
	noneReply, err := invokeStopPack(t, findStopPackDef(t, "stop"), rtNone, "/stop")
	if err != nil {
		t.Fatalf("nothing-running case returned error: %v (a no-op stop is informational, not a failure)", err)
	}
	if noneReply == "" {
		t.Error("nothing-running reply is empty — the user must be told the truth (nothing was running)")
	}
	if noneReply == firedReply {
		t.Errorf("nothing-running reply %q must be distinguishable from the fired acknowledgment %q — claiming a stop that did not happen is the exact untruthfulness this test pins", noneReply, firedReply)
	}
}

// TestStopHandler_ArmedLatch_DistinctTruthfulReply: the armed outcome (no turn
// registered yet, latch standing in) must get its own reply — not the fired
// acknowledgment and not "nothing to stop" (runtime.go's ErrCancelArmed
// contract; D9 gives /stop the Stop button's server behaviour, which arms
// latches too).
func TestStopHandler_ArmedLatch_DistinctTruthfulReply(t *testing.T) {
	firedFake := &stopPackFakeLoop{fired: true}
	rtFired := &Runtime{SessionID: func() string { return "s" }}
	rtFired = rtFired.WithAgentLoop(firedFake)
	setHelperSession(t, rtFired, true)
	firedReply, err := invokeStopPack(t, findStopPackDef(t, "stop"), rtFired, "/stop")
	if err != nil {
		t.Fatalf("fired case returned error: %v", err)
	}

	noneFake := &stopPackFakeLoop{}
	rtNone := &Runtime{SessionID: func() string { return "s" }}
	rtNone = rtNone.WithAgentLoop(noneFake)
	setHelperSession(t, rtNone, true)
	noneReply, err := invokeStopPack(t, findStopPackDef(t, "stop"), rtNone, "/stop")
	if err != nil {
		t.Fatalf("nothing-running case returned error: %v", err)
	}

	armedFake := &stopPackFakeLoop{armed: true}
	rtArmed := &Runtime{SessionID: func() string { return "s" }}
	rtArmed = rtArmed.WithAgentLoop(armedFake)
	setHelperSession(t, rtArmed, true)
	armedReply, err := invokeStopPack(t, findStopPackDef(t, "stop"), rtArmed, "/stop")
	if err != nil {
		t.Fatalf("armed case returned error: %v (an armed latch is acknowledged-and-pending, not a failure)", err)
	}
	if armedReply == "" {
		t.Error("armed-latch reply is empty — the user must be told the stop will fire on registration")
	}
	if armedReply == firedReply {
		t.Error("armed-latch reply must not equal the fired acknowledgment — nothing stopped yet")
	}
	if armedReply == noneReply {
		t.Error("armed-latch reply must not equal the nothing-running reply — reporting 'nothing to stop' while a latch stands in is the exact bug class ErrCancelArmed's contract forbids")
	}
}

// TestStopHandler_RealFailure_SurfacesError: a real failure from the stop
// must not be swallowed — the reply names it (truthfulness; mirrors the
// /cancel handler's failure mapping).
func TestStopHandler_RealFailure_SurfacesError(t *testing.T) {
	fake := &stopPackFakeLoop{err: errors.New("lifecycle fsync failed")}
	rt := &Runtime{SessionID: func() string { return "s" }}
	rt = rt.WithAgentLoop(fake)
	setHelperSession(t, rt, true)

	reply, err := invokeStopPack(t, findStopPackDef(t, "stop"), rt, "/stop")
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if !strings.Contains(reply, "lifecycle fsync failed") {
		t.Errorf("reply %q must surface the underlying failure %q — a silent failure would read as success", reply, "lifecycle fsync failed")
	}
}

// ─── /stop-redirect — helper redirect, root refusal, usage ──────────────────

// TestStopRedirectHandler_HelperChat_RedirectReceipt: in a HELPER's chat,
// /stop-redirect <instruction> issues the D2 redirect on exactly that helper
// with the instruction text (trimmed) and the canceller identity — the receipt
// the atomic stop + same-generation resume (stop_note cause redirect_pause)
// hangs on (D9 row 2; D2 redirect steps 1..3).
func TestStopRedirectHandler_HelperChat_RedirectReceipt(t *testing.T) {
	fake := &stopPackFakeLoop{}
	rt := &Runtime{SessionID: func() string { return "helper-7" }}
	rt = rt.WithAgentLoop(fake)
	setHelperSession(t, rt, true)

	def := findStopPackDef(t, "stop-redirect")
	reply, err := invokeStopPack(t, def, rt, "/stop-redirect focus on the failing tests")
	if err != nil {
		t.Fatalf("/stop-redirect handler returned error: %v", err)
	}
	if reply == "" {
		t.Error("/stop-redirect must acknowledge the redirect, got empty reply")
	}
	if len(fake.redirectCalls) != 1 {
		t.Fatalf("RedirectSessionTurn call count = %d, want 1", len(fake.redirectCalls))
	}
	call := fake.redirectCalls[0]
	if call.sessionID != "helper-7" {
		t.Errorf("RedirectSessionTurn sessionID = %q, want %q (the helper's own chat, conversation-scoped)", call.sessionID, "helper-7")
	}
	if call.instruction != "focus on the failing tests" {
		t.Errorf("RedirectSessionTurn instruction = %q, want %q (trimmed argument text)", call.instruction, "focus on the failing tests")
	}
	if call.userID != "@alice" || call.channel != "webchat" {
		t.Errorf("RedirectSessionTurn canceller = (%q, %q), want (@alice, webchat)", call.userID, call.channel)
	}
	if fake.requestCancelCalls != 0 {
		t.Errorf("redirect must not reuse the /cancel (tree) path, got %d calls", fake.requestCancelCalls)
	}
	if len(fake.stopCalls) != 0 {
		t.Errorf("redirect must go through the redirect seam (atomic stop+resume), not a separate plain stop, got %d stop calls", len(fake.stopCalls))
	}
}

// TestStopRedirectHandler_RootChat_RefusesWithHelperGuidance: in the ROOT's
// chat the command is refused with guidance to target a helper, and NOTHING
// is issued — no redirect, no stop (D9 row 3; fail-closed on helper identity
// per §3.2).
func TestStopRedirectHandler_RootChat_RefusesWithHelperGuidance(t *testing.T) {
	fake := &stopPackFakeLoop{}
	rt := &Runtime{SessionID: func() string { return "root-1" }}
	rt = rt.WithAgentLoop(fake)
	setHelperSession(t, rt, false)

	def := findStopPackDef(t, "stop-redirect")
	reply, err := invokeStopPack(t, def, rt, "/stop-redirect do the other thing")
	if err != nil {
		t.Fatalf("refusal must be a normal reply, not an error: %v", err)
	}
	if !strings.Contains(strings.ToLower(reply), "helper") {
		t.Errorf("root-chat refusal reply %q must guide the user to target a helper session (D9)", reply)
	}
	if len(fake.redirectCalls) != 0 {
		t.Errorf("root chat refusal must issue no redirect, got %+v", fake.redirectCalls)
	}
	if len(fake.stopCalls) != 0 {
		t.Errorf("root chat refusal must stop nothing, got %+v", fake.stopCalls)
	}
}

// TestStopRedirectHandler_BareInstruction_RepliesUsageChangesNothing: an
// empty instruction replies with usage and changes nothing (dispatch brief;
// D9's command shape is "/stop-redirect <instruction>").
func TestStopRedirectHandler_BareInstruction_RepliesUsageChangesNothing(t *testing.T) {
	fake := &stopPackFakeLoop{}
	rt := &Runtime{SessionID: func() string { return "helper-3" }}
	rt = rt.WithAgentLoop(fake)
	setHelperSession(t, rt, true)

	def := findStopPackDef(t, "stop-redirect")
	reply, err := invokeStopPack(t, def, rt, "/stop-redirect")
	if err != nil {
		t.Fatalf("bare /stop-redirect must reply with usage, not error: %v", err)
	}
	if !strings.Contains(reply, "/stop-redirect") {
		t.Errorf("usage reply %q must name the command", reply)
	}
	if len(fake.redirectCalls) != 0 || len(fake.stopCalls) != 0 || fake.requestCancelCalls != 0 {
		t.Errorf("bare instruction must change nothing, got redirect=%+v stop=%+v cancel=%d", fake.redirectCalls, fake.stopCalls, fake.requestCancelCalls)
	}
}

// TestStopRedirectHandler_WhitespaceInstruction_RepliesUsageChangesNothing:
// whitespace-only is still an empty instruction (boundary of "empty").
func TestStopRedirectHandler_WhitespaceInstruction_RepliesUsageChangesNothing(t *testing.T) {
	fake := &stopPackFakeLoop{}
	rt := &Runtime{SessionID: func() string { return "helper-3" }}
	rt = rt.WithAgentLoop(fake)
	setHelperSession(t, rt, true)

	def := findStopPackDef(t, "stop-redirect")
	reply, err := invokeStopPack(t, def, rt, "/stop-redirect \u00a0\u2003 ")
	if err != nil {
		t.Fatalf("whitespace-only instruction must reply with usage, not error: %v", err)
	}
	if !strings.Contains(reply, "/stop-redirect") {
		t.Errorf("usage reply %q must name the command", reply)
	}
	if len(fake.redirectCalls) != 0 {
		t.Errorf("whitespace-only instruction must not redirect, got %+v", fake.redirectCalls)
	}
}

// TestStopRedirectHandler_UnicodeWhitespaceInstruction_RepliesUsageChangesNothing:
// UNICODE whitespace-only (NBSP U+00A0, em space U+2003) is still an empty
// instruction — the boundary of "empty" is Unicode-aware, not ASCII-only
// (dispatch brief: "empty/Unicode-whitespace instruction returns usage with no
// control/message side effects"). An ASCII-only trim would let this through as
// a real instruction — exactly the bug class this test pins.
func TestStopRedirectHandler_UnicodeWhitespaceInstruction_RepliesUsageChangesNothing(t *testing.T) {
	fake := &stopPackFakeLoop{}
	rt := &Runtime{SessionID: func() string { return "helper-3" }}
	rt = rt.WithAgentLoop(fake)
	setHelperSession(t, rt, true)

	def := findStopPackDef(t, "stop-redirect")
	reply, err := invokeStopPack(t, def, rt, "/stop-redirect    ")
	if err != nil {
		t.Fatalf("Unicode-whitespace-only instruction must reply with usage, not error: %v", err)
	}
	if !strings.Contains(reply, "/stop-redirect") {
		t.Errorf("usage reply %q must name the command", reply)
	}
	if len(fake.redirectCalls) != 0 || len(fake.stopCalls) != 0 || fake.requestCancelCalls != 0 {
		t.Errorf("Unicode-whitespace-only instruction must change nothing, got redirect=%+v stop=%+v cancel=%d", fake.redirectCalls, fake.stopCalls, fake.requestCancelCalls)
	}
}

// TestStopRedirectHandler_LoopError_SurfacesErrorTruthfully: a real (unknown)
// failure from the redirect primitive must not be swallowed — the reply names
// it (truthfulness; mirrors the /stop failure mapping). NOTE: this pins the
// UNKNOWN-error leg with a proxy error. The two NAMED sentinel mappings —
// ErrNotHelperSession → root-style refusal guidance, ErrNothingToRedirect →
// "already finished — use RESUME" — need errors.Is identity against the
// sentinel vars, which do not exist yet; they are specified to backend-lead as
// GREEN-scope assertions in REPORT.md (a test referencing nonexistent vars
// would not compile, and a compile error is not behavioural RED).
func TestStopRedirectHandler_LoopError_SurfacesErrorTruthfully(t *testing.T) {
	fake := &stopPackFakeLoop{err: errors.New("redirect ledger write failed")}
	rt := &Runtime{SessionID: func() string { return "helper-11" }}
	rt = rt.WithAgentLoop(fake)
	setHelperSession(t, rt, true)

	reply, err := invokeStopPack(t, findStopPackDef(t, "stop-redirect"), rt, "/stop-redirect export the report")
	if err != nil {
		t.Fatalf("handler returned error: %v (the failure must surface as a truthful reply, not a transport error)", err)
	}
	if !strings.Contains(reply, "redirect ledger write failed") {
		t.Errorf("reply %q must surface the underlying failure %q — a silent failure would read as success", reply, "redirect ledger write failed")
	}
	if len(fake.redirectCalls) != 1 {
		t.Errorf("the redirect seam must have been exercised exactly once, got %+v", fake.redirectCalls)
	}
}

// ─── /goal stop alias untouched (D9) ────────────────────────────────────────

// TestGoalClearAliases_UnchangedPerD9: D9 pins "/goal stop remains the /goal
// clear alias and must not change" — the verb set is exactly what it was
// before /stop became a chat command, so adding /stop must not have touched
// goal vocabulary.
func TestGoalClearAliases_UnchangedPerD9(t *testing.T) {
	want := []string{"clear", "stop", "off", "reset", "cancel", "none"}
	got := GoalClearAliases()
	if len(got) != len(want) {
		t.Fatalf("GoalClearAliases() = %v, want exactly %v (D9: must not change)", got, want)
	}
	for _, w := range want {
		found := false
		for _, g := range got {
			if g == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("GoalClearAliases() lost verb %q — D9 pins the set unchanged: %v", w, want)
		}
	}
}
