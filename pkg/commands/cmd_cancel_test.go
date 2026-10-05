package commands

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestCancelDefinition_NotAliased preserves FR-5's zero-alias rule, as amended
// by frozen ADR-20260928 D9 and preserved by ADR-20261004: /stop and
// /stop-redirect are executable OWN commands, never aliases of /cancel.
// /abort and /kill remain forbidden.
func TestCancelDefinition_NotAliased(t *testing.T) {
	defs := BuiltinDefinitions()

	var cancelDef *Definition
	for i := range defs {
		if defs[i].Name == "cancel" {
			d := defs[i]
			cancelDef = &d
			break
		}
	}
	if cancelDef == nil {
		t.Fatal("cancel definition not found in BuiltinDefinitions()")
	}

	if len(cancelDef.Aliases) != 0 {
		t.Errorf("/cancel must have no aliases (FR-5), got: %v", cancelDef.Aliases)
	}

	// Required names are distinct definitions, not aliases (D9).
	for _, name := range []string{"stop", "stop-redirect"} {
		t.Run(name, func(t *testing.T) {
			count := 0
			for _, def := range defs {
				if def.Name == name {
					count++
					if def.Handler == nil || len(def.Aliases) != 0 {
						t.Errorf("/%s must be executable with zero aliases (D9), handler nil=%v aliases=%v", name, def.Handler == nil, def.Aliases)
					}
				}
			}
			if count != 1 {
				t.Fatalf("BLOCKED: /%s has %d own definitions, want exactly 1 — required by preserved ADR D9", name, count)
			}
		})
	}
	forbidden := []string{"abort", "kill"}
	for _, def := range defs {
		for _, alias := range def.Aliases {
			if alias == "stop" || alias == "stop-redirect" {
				t.Errorf("/%s must be an own command, never an alias of /%s (D9)", alias, def.Name)
			}
			for _, f := range forbidden {
				if alias == f {
					t.Errorf("found forbidden alias %q on definition %q (FR-5)", alias, def.Name)
				}
			}
		}
		for _, f := range forbidden {
			if def.Name == f {
				t.Errorf("found forbidden command name %q — must not be registered (FR-5)", def.Name)
			}
		}
	}
}

// stubAgentLoop is a minimal AgentLoopInterface implementation used in tests.
//
// ADR-057 FR-041/D8: AgentLoopInterface no longer declares InterruptSession
// (it was dead surface — see runtime.go's AgentLoopInterface doc comment).
// This stub keeps an unexported simulateInterrupt helper, renamed off the
// retired name, purely for its own internal test-glue reuse between the two
// exported behaviours (record the call, then let RequestCancelForSession
// answer from the recorded state) — it is not part of, and does not need to
// satisfy, any interface.
type stubAgentLoop struct {
	calledSessionID string
	calledHint      string
	callCount       int
	singleCalls     int
	scopedCalls     []scopedCancelCall
	returnErr       error // if non-nil, returned by the scoped tree capability
	returnFired     *bool // if non-nil, overrides the scoped fired result
	returnArmed     bool  // returned as the scoped armed result
}

func (s *stubAgentLoop) simulateInterrupt(sessionID, hint string) ([]string, error) {
	s.calledSessionID = sessionID
	s.calledHint = hint
	s.callCount++
	return nil, s.returnErr
}

func (s *stubAgentLoop) RequestCancelForSession(context.Context, string, string, string) (bool, bool, error) {
	s.singleCalls++
	return false, false, errUnscopedPathUsed
}

// RequestScopedCancelForSession is the tree capability specified by the
// existing D9 scope pack. The session-only method is a separate negative probe.
func (s *stubAgentLoop) RequestScopedCancelForSession(_ context.Context, sessionID, userID, channel, scope string) (bool, bool, error) {
	s.scopedCalls = append(s.scopedCalls, scopedCancelCall{sessionID: sessionID, userID: userID, channel: channel, scope: scope})
	hint := "cancel from " + userID + " via " + channel
	_, err := s.simulateInterrupt(sessionID, hint)
	if err != nil {
		return false, false, err
	}
	if s.returnFired != nil {
		return *s.returnFired, s.returnArmed, nil
	}
	return s.callCount > 0, s.returnArmed, nil
}

// TestCancelHandler_CallsInterruptSession verifies that the /cancel handler
// invokes the agent loop's cancel path with the correct session ID and a
// hint that contains the canceller identity (spec FR-27, FR-1). The name is
// kept (pre-ADR-057) even though the underlying stub method is now
// simulateInterrupt, not InterruptSession — see stubAgentLoop's doc comment.
func TestCancelHandler_CallsInterruptSession(t *testing.T) {
	stub := &stubAgentLoop{}

	rt := &Runtime{
		SessionID: func() string { return "session-abc" },
	}
	rt = rt.WithAgentLoop(stub)

	cancelDef := cancelCommand()
	if cancelDef.Handler == nil {
		t.Fatal("cancel handler must not be nil")
	}

	var reply string
	err := cancelDef.Handler(context.Background(), Request{
		Channel:  "telegram",
		SenderID: "@alice",
		Text:     "/cancel",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	}, rt)
	if err != nil {
		t.Fatalf("cancel handler returned unexpected error: %v", err)
	}

	if stub.singleCalls != 0 {
		t.Fatalf("session-only calls = %d, want 0 — /cancel requires tree scope (D9)", stub.singleCalls)
	}
	wantCall := scopedCancelCall{sessionID: "session-abc", userID: "@alice", channel: "telegram", scope: "tree"}
	if len(stub.scopedCalls) != 1 || stub.scopedCalls[0] != wantCall {
		t.Fatalf("tree calls = %+v, want exactly [%+v] (D9)", stub.scopedCalls, wantCall)
	}
	if stub.callCount != 1 {
		t.Fatalf("tree stop call count = %d, want 1", stub.callCount)
	}
	if stub.calledSessionID != "session-abc" {
		t.Errorf("simulateInterrupt sessionID = %q, want %q", stub.calledSessionID, "session-abc")
	}

	// The hint must contain the canceller identity (UserID and Channel).
	for _, want := range []string{"@alice", "telegram"} {
		found := false
		for i := 0; i+len(want) <= len(stub.calledHint); i++ {
			if stub.calledHint[i:i+len(want)] == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("simulateInterrupt hint %q must contain %q", stub.calledHint, want)
		}
	}

	if reply != "⏸ Canceling..." {
		t.Errorf("reply = %q, want %q", reply, "⏸ Canceling...")
	}
}

// TestCancelHandler_NilRuntimeRepliesUnavailable verifies that a nil Runtime
// causes the handler to reply with the standard unavailable message rather than
// panicking.
func TestCancelHandler_NilRuntimeRepliesUnavailable(t *testing.T) {
	cancelDef := cancelCommand()

	var reply string
	err := cancelDef.Handler(context.Background(), Request{
		Text: "/cancel",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply != unavailableMsg {
		t.Errorf("reply = %q, want %q", reply, unavailableMsg)
	}
}

// TestCancelHandler_NilAgentLoopRepliesNothingToCancel verifies that
// CancelActiveTurn with no agent loop wired returns ErrNoActiveTurn, causing
// the handler to reply "Nothing to cancel" (C-3 fix: was previously "⏸ Canceling...").
func TestCancelHandler_NilAgentLoopRepliesNothingToCancel(t *testing.T) {
	rt := &Runtime{
		SessionID: func() string { return "some-session" },
		// agentLoop intentionally left nil
	}

	cancelDef := cancelCommand()

	var reply string
	err := cancelDef.Handler(context.Background(), Request{
		Channel:  "web",
		SenderID: "user_123",
		Text:     "/cancel",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	}, rt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply != "Nothing to cancel" {
		t.Errorf("reply = %q, want %q", reply, "Nothing to cancel")
	}
}

// TestCancelActiveTurn_PropagatesRealError verifies that a genuine
// agent-loop cancel failure (e.g., fsync error) is not swallowed (C-3 fix).
func TestCancelActiveTurn_PropagatesRealError(t *testing.T) {
	stub := &stubAgentLoop{
		returnErr: errors.New("audit fsync failed"),
	}
	rt := &Runtime{
		SessionID: func() string { return "sess-x" },
	}
	rt = rt.WithAgentLoop(stub)

	err := rt.CancelActiveTurn(context.Background(), "sess-x", Canceller{UserID: "u", Channel: "c"})
	if err == nil {
		t.Fatal("expected non-nil error for real cancel failure, got nil")
	}
	if !errors.Is(err, stub.returnErr) {
		// The error must wrap the original.
		if err.Error() == "" || !contains(err.Error(), "audit fsync failed") {
			t.Errorf("error %q must contain %q", err.Error(), "audit fsync failed")
		}
	}
}

// TestCancelActiveTurn_NoActiveTurnReturnsSentinel verifies that when the agent
// loop reports no active turn, CancelActiveTurn returns ErrNoActiveTurn — and
// when the loop reports success (fired=true), CancelActiveTurn returns nil.
func TestCancelActiveTurn_NoActiveTurnReturnsSentinel(t *testing.T) {
	// Case 1: loop returns (fired=true, nil) → success (no ErrNoActiveTurn).
	stub := &stubAgentLoop{returnErr: nil}
	rt := &Runtime{SessionID: func() string { return "s" }}
	rt = rt.WithAgentLoop(stub)

	err := rt.CancelActiveTurn(context.Background(), "s", Canceller{UserID: "u", Channel: "c"})
	if err != nil {
		t.Errorf("fired=true must produce nil from CancelActiveTurn, got: %v", err)
	}

	// Case 2: loop returns (fired=false, nil) → ErrNoActiveTurn sentinel.
	// This is the correct path when no active turn exists for the session.
	f := false
	stub2 := &stubAgentLoop{returnFired: &f}
	rt2 := &Runtime{SessionID: func() string { return "s" }}
	rt2 = rt2.WithAgentLoop(stub2)

	err2 := rt2.CancelActiveTurn(context.Background(), "s", Canceller{UserID: "u", Channel: "c"})
	if !errors.Is(err2, ErrNoActiveTurn) {
		t.Errorf("fired=false must return ErrNoActiveTurn, got: %v", err2)
	}
}

// TestCancelActiveTurn_ArmedReturnsErrCancelArmed is the structural-fix
// regression test: when the agent loop reports (fired=false, armed=true) —
// no turn was registered yet, but a pre-registration cancel latch now stands
// in for the cancel — CancelActiveTurn MUST return ErrCancelArmed, distinct
// from BOTH the success path (nil) and the genuine no-op path
// (ErrNoActiveTurn). Before the widened RequestCancelForSession adapter, this
// case was structurally unreachable: the adapter flattened CancelOutcome to
// a bare (bool, error), discarding Armed, so CancelActiveTurn had no way to
// tell "armed" apart from "genuinely nothing to cancel" and always returned
// ErrNoActiveTurn for both — telling a user "nothing to cancel" for a Stop
// that WOULD still fire moments later.
func TestCancelActiveTurn_ArmedReturnsErrCancelArmed(t *testing.T) {
	fired := false
	stub := &stubAgentLoop{returnFired: &fired, returnArmed: true}
	rt := &Runtime{SessionID: func() string { return "s" }}
	rt = rt.WithAgentLoop(stub)

	err := rt.CancelActiveTurn(context.Background(), "s", Canceller{UserID: "u", Channel: "c"})
	if !errors.Is(err, ErrCancelArmed) {
		t.Errorf("fired=false, armed=true must return ErrCancelArmed, got: %v", err)
	}
	if errors.Is(err, ErrNoActiveTurn) {
		t.Error("an armed cancel must NOT also satisfy errors.Is(err, ErrNoActiveTurn) — the two outcomes must be distinguishable")
	}
}

// TestCancelHandler_ReplyMatchesErrorState verifies that the /cancel handler
// reply message matches the error state from CancelActiveTurn (C-3 fix).
func TestCancelHandler_ReplyMatchesErrorState(t *testing.T) {
	cancelDef := cancelCommand()

	fired := false

	cases := []struct {
		name        string
		loopErr     error
		returnFired *bool
		returnArmed bool
		wantReply   string
	}{
		{
			name:      "success",
			loopErr:   nil,
			wantReply: "⏸ Canceling...",
		},
		{
			// No active turn: loop returns (fired=false, nil).
			// CancelActiveTurn returns ErrNoActiveTurn → handler replies "Nothing to cancel".
			name:        "no_active_turn",
			returnFired: &fired,
			wantReply:   "Nothing to cancel",
		},
		{
			// No active turn YET, but a pre-registration cancel latch armed:
			// loop returns (fired=false, armed=true, nil). CancelActiveTurn
			// returns ErrCancelArmed → handler must NOT reply "Nothing to
			// cancel" (the exact bug this fix closes).
			name:        "armed_latch",
			returnFired: &fired,
			returnArmed: true,
			wantReply:   "⏸ Cancel acknowledged — nothing is running yet, but it will stop the instant it starts.",
		},
		{
			name:      "real_failure",
			loopErr:   errors.New("audit fsync failed: disk full"),
			wantReply: "Cancel request failed: audit fsync failed: disk full",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubAgentLoop{returnErr: tc.loopErr, returnFired: tc.returnFired, returnArmed: tc.returnArmed}
			rt := &Runtime{SessionID: func() string { return "sess-1" }}
			rt = rt.WithAgentLoop(stub)

			var reply string
			err := cancelDef.Handler(context.Background(), Request{
				Channel:  "web",
				SenderID: "user_x",
				Text:     "/cancel",
				Reply: func(text string) error {
					reply = text
					return nil
				},
			}, rt)
			if err != nil {
				t.Fatalf("handler returned unexpected error: %v", err)
			}
			wantCall := scopedCancelCall{sessionID: "sess-1", userID: "user_x", channel: "web", scope: "tree"}
			if stub.singleCalls != 0 || len(stub.scopedCalls) != 1 || stub.scopedCalls[0] != wantCall {
				t.Errorf("cancel routing: single=%d tree=%+v, want single=0 tree=[%+v]", stub.singleCalls, stub.scopedCalls, wantCall)
			}
			if reply != tc.wantReply {
				t.Errorf("reply = %q, want %q", reply, tc.wantReply)
			}
		})
	}
}

// singleOnlyCancelLoop deliberately lacks the tree capability. A successful
// session-only answer must not manufacture a successful Stop all (D7/D9).
type singleOnlyCancelLoop struct{ calls int }

func (s *singleOnlyCancelLoop) RequestCancelForSession(context.Context, string, string, string) (bool, bool, error) {
	s.calls++
	return true, false, nil
}

func TestCancelActiveTurn_MissingTreeCapabilityFailsVisiblyWithoutFallback(t *testing.T) {
	loop := &singleOnlyCancelLoop{}
	rt := (&Runtime{SessionID: func() string { return "root-tree" }}).WithAgentLoop(loop)
	err := rt.CancelActiveTurn(context.Background(), "root-tree", Canceller{UserID: "owner", Channel: "web"})
	if loop.calls != 0 {
		t.Errorf("session-only fallback calls = %d, want 0 — missing tree capability must be a visible error (D9)", loop.calls)
	}
	if err == nil {
		t.Fatal("BLOCKED: missing tree cancellation capability returned success — required by D7/D9, never session-only fallback")
	}
	if errors.Is(err, ErrNoActiveTurn) || errors.Is(err, ErrCancelArmed) || !strings.Contains(strings.ToLower(err.Error()), "tree") {
		t.Fatalf("missing-capability error = %v, want a visible tree-capability error, not an ordinary no-op/armed outcome", err)
	}
	var reply string
	handlerErr := cancelCommand().Handler(context.Background(), Request{
		Text: "/cancel", SenderID: "owner", Channel: "web",
		Reply: func(text string) error { reply = text; return nil },
	}, rt)
	if handlerErr != nil {
		t.Fatalf("cancel error reply transport: %v", handlerErr)
	}
	if want := "Cancel request failed: " + err.Error(); reply != want {
		t.Errorf("missing tree reply = %q, want %q", reply, want)
	}
	if loop.calls != 0 {
		t.Errorf("handler used session-only fallback %d times, want 0", loop.calls)
	}
}

// contains is a helper for string containment.
func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
