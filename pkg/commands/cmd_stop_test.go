package commands

// cmd_stop_test.go — RED wave 2 (qa-lead, feat/stopall-qa2): the D9 chat
// commands /stop and /stop-redirect.
//
// SPEC SOURCES (every expected value below derives from these, never from the
// implementation — oracle independence):
//   - ADR-20260928-sub-agent-control-plane.md D9 (founder F1011-Q1): /stop and
//     /stop-redirect <instruction> are THEIR OWN commands, not aliases of
//     /cancel; /cancel keeps FR-5's "no aliases"; no /steer alias (founder O4);
//     /goal stop remains the /goal clear alias and must not change.
//   - D9 input table: /stop (root OR helper) stops ONLY that session's current
//     turn — no cascade, no root refusal; /stop-redirect in a HELPER's chat =
//     D2 redirect on that helper only; /stop-redirect in the ROOT's chat =
//     refuse with guidance to target a helper; chat commands are
//     conversation-scoped (#955) and authorized for the signed-in owner.
//   - D2 redirect: atomic stop (stop_note cause "redirect_pause") + resume the
//     SAME generation with the instruction as the newest message; the subtree
//     keeps working; later steers are delivered after the instruction
//     (sequence fence, steps 1..3). Anchor symbols that already exist:
//     pkg/session/lifecycle_edge.go::StopCauseRedirectPause and
//     pkg/agent/steer_cancel.go::StopTurns.
//   - cancel-cross-channel-spec.md decision 12 / FR-5 / review F-20: the
//     exact-set registration assertion, as amended by D9.
//
// LIFECYCLE RECEIPTS BELOW THIS SEAM ARE EXPLICITLY OUT OF SCOPE HERE (see the
// report): stop_note cause redirect_pause, same-generation resume, the seq
// fence, "subtree keeps working", open-question retention and goal
// non-interference are production guarantees of StopSessionTurn /
// RedirectSessionTurn, not command-layer observables. They are reported to
// backend-lead as required behaviours with the anchors above.

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// ─── Test fake: the agent-loop seam the handlers must call ──────────────────
//
// REPORTED INTERFACE SHAPE for backend-lead (the minimal injection this pack
// needs; primitive types only, per AgentLoopInterface's no-pkg/agent-import
// constraint). Add to AgentLoopInterface in pkg/commands/runtime.go:
//
//	// StopSessionTurn stops ONLY the named session's current turn — D9 /stop
//	// (scope session, no cascade, D2 stop semantics: cause "stop",
//	// non-terminal, never ends a goal, keeps an open question open).
//	// Three-outcome contract identical to RequestCancelForSession:
//	//   (fired=true, armed=false, nil)            — stop fired.
//	//   (fired=false, armed=true, nil)            — pre-registration latch armed.
//	//   (fired=false, armed=false, nil)           — nothing to stop.
//	//   err non-nil                               — real failure, must surface.
//	StopSessionTurn(ctx context.Context, sessionID, userID, channel string) (fired, armed bool, err error)
//
//	// RedirectSessionTurn performs the D2 redirect on the named session:
//	// atomic stop (stop_note cause redirect_pause) + same-generation resume
//	// with instruction as the newest message; subtree keeps working; later
//	// steers queue behind the instruction (seq fence).
//	RedirectSessionTurn(ctx context.Context, sessionID, instruction, userID, channel string) error
//
// And add to Runtime in pkg/commands/runtime.go (located reflectively by
// setHelperSession below until it exists):
//
//	// IsHelperSession reports whether the current chat targets a helper
//	// session (false/nil ⇒ workspace root — /stop-redirect must refuse).
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
// build error — before backend-lead adds the field.
func setHelperSession(t *testing.T, rt *Runtime, isHelper bool) {
	t.Helper()
	field := reflect.ValueOf(rt).Elem().FieldByName("IsHelperSession")
	if !field.IsValid() {
		t.Fatalf("BLOCKED: commands.Runtime.IsHelperSession func field not implemented — required by ADR D9 (root chat must refuse /stop-redirect with guidance to target a helper)")
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
	if def.Handler == nil {
		t.Fatalf("BLOCKED: /%s registered with nil Handler — D9 requires an executable command", def.Name)
	}
	var reply string
	err := def.Handler(context.Background(), Request{
		Channel:  "webchat",
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

	// /stop is available while streaming (dispatch brief: "make /stop
	// available while streaming") and on all three surfaces, as for /cancel.
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

	// /stop-redirect takes an instruction argument (D9: "/stop-redirect
	// <instruction>"); its usage line must say so.
	if !strings.Contains(redirectDef.Usage, "instruction") {
		t.Errorf("/stop-redirect Usage = %q, want it to name the <instruction> argument (D9)", redirectDef.Usage)
	}
	found := false
	for _, s := range []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel} {
		for _, has := range redirectDef.Surfaces {
			if has == s {
				found = true
			}
		}
	}
	if !found {
		t.Error("/stop-redirect must be surfaced on web+cli+channel, as for /cancel")
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
// is issued — no redirect, no stop (D9 row 3).
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
	reply, err := invokeStopPack(t, def, rt, "/stop-redirect    ")
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
