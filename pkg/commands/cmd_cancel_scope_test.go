package commands

import (
	"context"
	"errors"
	"testing"
)

// Tests in this file pin ADR-20260928 D9 + MAJ-002 for the /cancel command:
// /cancel is a Stop-all surface — it must request a TREE-scoped stop through
// the agent loop's scoped cancel entry, never through the legacy single-turn
// entry (RequestCancelForSession), because only the scoped entry cascades
// down the delegation subtree. CancelFrame.scope on the wire is the enum
// "session"|"tree" (contracts, MAJ-002); /cancel always sends "tree".
//
// Oracle: ADR-20260928 D9 surface table + common.md decided behaviour
// ("/cancel = scope tree ... themselves the confirmation"), and the truthful
// reply vocabulary of cmd_cancel.go. No expected value was derived from
// running the implementation.

// scopedCancelScopeTree is the scope value /cancel must pass: the CancelFrame
// wire enum value "tree" (MAJ-002).
const scopedCancelScopeTree = "tree"

// errUnscopedPathUsed is answered by scopedCancelFake.RequestCancelForSession.
// After D9, /cancel must not use the legacy single-turn cancel path at all,
// so any call there is itself the failure the test reports.
var errUnscopedPathUsed = errors.New("unscoped single-turn cancel path used by /cancel — D9 requires the scoped tree path")

// scopedCancelCall records one cancel request seen by the fake.
type scopedCancelCall struct {
	sessionID string
	userID    string
	channel   string
	scope     string
}

// scopedCancelFake implements the existing AgentLoopInterface plus the scoped
// cancel entry the /cancel handler must use.
//
// Interface shape reported to backend-lead (minimal addition to
// commands.AgentLoopInterface; scope carries the CancelFrame wire values):
//
//	RequestScopedCancelForSession(ctx context.Context, sessionID, userID, channel, scope string) (fired bool, armed bool, err error)
//
// fired/armed/err keep the same meaning as RequestCancelForSession, evaluated
// across the scoped behaviour: fired — the stop claimed the named session's
// live turn or stopped its subtree; armed — the pre-registration cancel latch
// was armed for the named session.
type scopedCancelFake struct {
	singleCalls []scopedCancelCall
	scopedCalls []scopedCancelCall

	scopedFired bool
	scopedArmed bool
	scopedErr   error
}

// RequestCancelForSession implements the existing AgentLoopInterface method.
// It records the call and returns a distinctive error: after D9 this path must
// stay silent for /cancel, so reaching it is a loud, self-describing failure.
func (f *scopedCancelFake) RequestCancelForSession(ctx context.Context, sessionID, userID, channel string) (bool, bool, error) {
	f.singleCalls = append(f.singleCalls, scopedCancelCall{sessionID: sessionID, userID: userID, channel: channel})
	return false, false, errUnscopedPathUsed
}

// RequestScopedCancelForSession is the proposed scoped entry /cancel must
// call with scope "tree" (ADR-20260928 D9, MAJ-002).
func (f *scopedCancelFake) RequestScopedCancelForSession(ctx context.Context, sessionID, userID, channel, scope string) (bool, bool, error) {
	f.scopedCalls = append(f.scopedCalls, scopedCancelCall{sessionID: sessionID, userID: userID, channel: channel, scope: scope})
	return f.scopedFired, f.scopedArmed, f.scopedErr
}

// runCancelCommand drives the real /cancel handler the way the executor does.
func runCancelCommand(t *testing.T, rt *Runtime, fake *scopedCancelFake) string {
	t.Helper()
	cancelDef := cancelCommand()
	if cancelDef.Handler == nil {
		t.Fatal("cancel handler must not be nil")
	}
	var reply string
	err := cancelDef.Handler(context.Background(), Request{
		Channel:  "web",
		SenderID: "user-tree-1",
		Text:     "/cancel",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	}, rt)
	if err != nil {
		t.Fatalf("cancel handler returned unexpected error: %v", err)
	}
	return reply
}

// TestCancelCommand_TreeScope_RoutesThroughScopedStopAll asserts D9's decided
// behaviour: /cancel requests a tree-scoped stop for its own session, carrying
// the canceller identity, and never touches the unscoped single-turn cancel
// path (which cannot cascade — MAJ-002: only scope "tree" may cascade).
func TestCancelCommand_TreeScope_RoutesThroughScopedStopAll(t *testing.T) {
	fake := &scopedCancelFake{scopedFired: true}
	rt := &Runtime{SessionID: func() string { return "root-session-1" }}
	rt = rt.WithAgentLoop(fake)

	reply := runCancelCommand(t, rt, fake)

	if len(fake.scopedCalls) != 1 {
		t.Fatalf("scoped cancel calls = %d, want exactly 1 (calls: %+v)", len(fake.scopedCalls), fake.scopedCalls)
	}
	got := fake.scopedCalls[0]
	if got.sessionID != "root-session-1" {
		t.Errorf("scoped call sessionID = %q, want %q", got.sessionID, "root-session-1")
	}
	if got.scope != scopedCancelScopeTree {
		t.Errorf("scoped call scope = %q, want %q (D9: /cancel is a Stop-all surface)", got.scope, scopedCancelScopeTree)
	}
	if got.userID != "user-tree-1" {
		t.Errorf("scoped call userID = %q, want %q (canceller identity must reach the stop)", got.userID, "user-tree-1")
	}
	if got.channel != "web" {
		t.Errorf("scoped call channel = %q, want %q", got.channel, "web")
	}
	if len(fake.singleCalls) != 0 {
		t.Errorf("unscoped RequestCancelForSession calls = %d, want 0 — /cancel must not use the single-turn path (%v)", len(fake.singleCalls), errUnscopedPathUsed)
	}
	if reply != "⏸ Canceling..." {
		t.Errorf("reply = %q, want %q (truthful: the stop fired)", reply, "⏸ Canceling...")
	}
}

// TestCancelCommand_TreeScope_ReplyMatchesOutcome pins the truthful reply
// vocabulary for every outcome of the scoped tree stop. The texts are the
// decided ones already shipped for /cancel (cmd_cancel.go); D9 changes the
// scope of the stop, not the honesty of the reply.
func TestCancelCommand_TreeScope_ReplyMatchesOutcome(t *testing.T) {
	fired := false
	cases := []struct {
		name        string
		scopedFired *bool
		scopedArmed bool
		scopedErr   error
		wantReply   string
	}{
		{
			name:        "stop_fired",
			scopedFired: boolPtr(true),
			wantReply:   "⏸ Canceling...",
		},
		{
			name:        "nothing_running_yet_latch_armed",
			scopedFired: &fired,
			scopedArmed: true,
			wantReply:   "⏸ Cancel acknowledged — nothing is running yet, but it will stop the instant it starts.",
		},
		{
			name:        "nothing_to_stop",
			scopedFired: &fired,
			wantReply:   "Nothing to stop.",
		},
		{
			name:      "real_failure_reported",
			scopedErr: errors.New("audit fsync failed"),
			wantReply: "Cancel request failed: audit fsync failed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &scopedCancelFake{scopedFired: boolDeref(tc.scopedFired), scopedArmed: tc.scopedArmed, scopedErr: tc.scopedErr}
			rt := &Runtime{SessionID: func() string { return "sess-scope-1" }}
			rt = rt.WithAgentLoop(fake)

			reply := runCancelCommand(t, rt, fake)

			if len(fake.scopedCalls) != 1 {
				t.Fatalf("scoped cancel calls = %d, want 1 — replies must come from the scoped tree stop", len(fake.scopedCalls))
			}
			if reply != tc.wantReply {
				t.Errorf("reply = %q, want %q", reply, tc.wantReply)
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }

func boolDeref(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
}
