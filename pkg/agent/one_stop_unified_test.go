// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// one_stop_unified_test.go pins the agent side of the founder's one-stop
// decision (2026-10-05, coordination/LANE-A-ONE-STOP-DECISION-20261005.md):
//
//	"use the same mechanism for humans as for agents, remove the 5 seconds and
//	 make 3 seconds everywhere ... there should be only one stop mechanism that
//	 works for sessions, only it can be triggered by agents and humans".
//
// Contract pinned here (team-lead rulings):
//   - the polite stop is requested immediately; the forced stop fires 3 s
//     after it; nothing in the stop path waits 5 s;
//   - an agent's delegate stop_all and a human's /cancel, /stop and the stop
//     half of redirect produce the SAME observable sequence;
//   - only the owning execution lands `stopped`, after its running work shut
//     down: the fence stays until then, the note is kept, the goal is not
//     ended.
//
// Oracle: the decision file and rulings above -- not the implementation. The
// instrument is a real admitted helper whose provider ignores cancellation
// until released (the "uncooperative paid request" of
// delegate_grace_original_positive_test.go), so the polite stage and the
// forced stage are separately observable on the real immutable turn handle.

package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Timeline bounds, derived from the founder's "3 seconds everywhere".
const (
	oneStopPoliteWithin = 1 * time.Second
	oneStopNotBefore    = 2700 * time.Millisecond // forced stop must not fire earlier than ~3s
	oneStopNotAfter     = 4200 * time.Millisecond // ...and must fire by ~3s plus scheduling slack (never 5s)
)

// oneStopTimeline records when each stage of a stop became visible on the
// live turn: polite (graceful interrupt requested) and forced (hard abort
// requested). A stage that never happened inside `within` reports time.Hour.
func oneStopTimeline(ts *turnState, since time.Time, within time.Duration) (polite, forced time.Duration) {
	polite, forced = time.Hour, time.Hour
	deadline := since.Add(within)
	for time.Now().Before(deadline) {
		if polite == time.Hour {
			if ok, _ := ts.gracefulInterruptRequested(); ok {
				polite = time.Since(since)
			}
		}
		if forced == time.Hour && ts.hardAbortRequested() {
			forced = time.Since(since)
		}
		if polite != time.Hour && forced != time.Hour {
			return polite, forced
		}
		time.Sleep(10 * time.Millisecond)
	}
	return polite, forced
}

// requireOneStopTimeline asserts the unified timeline for `entry`.
func requireOneStopTimeline(t *testing.T, entry string, ts *turnState, since time.Time) {
	t.Helper()
	polite, forced := oneStopTimeline(ts, since, 6*time.Second)
	if polite > oneStopPoliteWithin {
		t.Errorf("%s: polite stop became visible after %v, want immediately (<= %v)", entry, polite, oneStopPoliteWithin)
	}
	if forced == time.Hour {
		t.Fatalf("%s: the forced stop never fired within 6s; the unified timeline forces at 3s", entry)
	}
	if forced < oneStopNotBefore {
		t.Errorf("%s: forced stop fired at %v, before the 3s mark -- the polite stop must get its 3s", entry, forced)
	}
	if forced > oneStopNotAfter {
		t.Errorf("%s: forced stop fired at %v; the unified timeline is 3s (never the retired 5s grace)", entry, forced)
	}
}

// oneStopUncooperativeChild launches one real running helper under a parent
// whose provider ignores cancellation until the returned release is called.
func oneStopUncooperativeChild(t *testing.T, tag string) (al *AgentLoop, parentID string, child *session.LifecycleRecord, handle *turnState, provider *originalGraceProvider) {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ = newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	provider = &originalGraceProvider{
		entered: make(chan struct{}), softCancelled: make(chan struct{}), release: make(chan struct{}),
	}
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: registered agent missing")
	}
	inst.Provider = provider
	t.Cleanup(provider.open)
	parentID = newTestSteeringSession(t, al, "ws-one-stop-"+tag)
	child = launchGoalBearingChild(t, al, parentID, "call-one-stop-"+tag, goalChildLaunchOptions{live: true})
	select {
	case <-provider.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("SETUP: the helper never reached its (uncooperative) provider")
	}
	handle = al.getActiveTurnState(child.SessionID)
	if handle == nil || !handle.IsAlive() {
		t.Fatal("SETUP: the helper has no live immutable turn handle")
	}
	if ok, _ := handle.gracefulInterruptRequested(); ok || handle.hardAbortRequested() {
		t.Fatal("SETUP: the turn must start with no stop stage requested")
	}
	return al, parentID, child, handle, provider
}

// TestOneStop_EveryEntryRunsTheSameTimeline drives the SAME helper tree through
// each entry and asserts the identical observable sequence, then the shared
// landing rules. The agent entry is the real wired delegate tool; the human
// entries are the real command seams (/cancel = tree scope, /stop = session
// scope) that web, CLI and channels all call.
func TestOneStop_EveryEntryRunsTheSameTimeline(t *testing.T) {
	entries := []struct {
		name string
		stop func(t *testing.T, al *AgentLoop, parentID, childID string) steer.Principal
	}{
		{"agent delegate stop_all", func(t *testing.T, al *AgentLoop, parentID, childID string) steer.Principal {
			res := delegateToolFor(t, al).Execute(tools.WithTranscriptSessionID(context.Background(), parentID),
				map[string]any{"action": "stop_all", "session_id": childID})
			if res == nil || res.IsError {
				t.Fatalf("stop_all: %+v", res)
			}
			if want := "Stop requested for session " + childID + " and its helpers; " +
				"they will show as stopped once their running work has shut down."; res.ForLLM != want {
				t.Errorf("stop_all reply = %q, want exactly %q", res.ForLLM, want)
			}
			return steer.Principal{Kind: steer.PrincipalKindAgent, ID: parentID}
		}},
		{"human /cancel (tree scope)", func(t *testing.T, al *AgentLoop, _, childID string) steer.Principal {
			if _, _, err := al.RequestScopedCancelForSession(context.Background(), childID, "human-one-stop", "web", "tree"); err != nil {
				t.Fatalf("/cancel: %v", err)
			}
			return steer.Principal{Kind: steer.PrincipalKindHuman, ID: "human-one-stop"}
		}},
		{"human /stop (session scope)", func(t *testing.T, al *AgentLoop, _, childID string) steer.Principal {
			if _, _, err := al.StopSessionTurn(context.Background(), childID, "human-one-stop", "web"); err != nil {
				t.Fatalf("/stop: %v", err)
			}
			return steer.Principal{Kind: steer.PrincipalKindHuman, ID: "human-one-stop"}
		}},
	}
	for i, entry := range entries {
		t.Run(entry.name, func(t *testing.T) {
			al, parentID, child, handle, provider := oneStopUncooperativeChild(t, string(rune('a'+i)))
			goalBefore := mustGoalRecord(t, child.GoalRef)

			since := time.Now()
			by := entry.stop(t, al, parentID, child.SessionID)
			requireOneStopTimeline(t, entry.name, handle, since)

			// The provider has seen the polite cancellation but cannot return, so
			// the owning execution's tail has NOT settled: the fence is still
			// in flight and nothing has landed (D2/D4: only the owner lands it,
			// after its tail).
			select {
			case <-provider.softCancelled:
			case <-time.After(2 * time.Second):
				t.Fatalf("%s: the polite stop never reached the provider-cancel boundary", entry.name)
			}
			held := rootReopenedRecord(t, al, child.SessionID)
			if held.State != session.LifecycleRunning || held.Stop == nil || held.StopNote == nil {
				t.Errorf("%s: before the owner's tail retired the record must still be running with the in-flight fence and note, got state=%q stop=%+v note=%+v",
					entry.name, held.State, held.Stop, held.StopNote)
			}

			provider.open()
			joinGoalFixtureRuns(t, al)
			landed := rootReopenedRecord(t, al, child.SessionID)
			if landed.State != session.LifecycleStopped || landed.Terminal() {
				t.Fatalf("%s: after the owning tail the record must land stopped (non-terminal), got %q", entry.name, landed.State)
			}
			if landed.Stop != nil || landed.StopNote == nil {
				t.Errorf("%s: landing must clear the fence and keep the note in one mutation: stop=%+v note=%+v", entry.name, landed.Stop, landed.StopNote)
			} else {
				if landed.StopNote.Cause != session.StopCauseStop {
					t.Errorf("%s: StopNote.Cause = %q, want %q", entry.name, landed.StopNote.Cause, session.StopCauseStop)
				}
				if want := session.StopActorFromPrincipal(by); landed.StopNote.By != want {
					t.Errorf("%s: StopNote.By = %q, want %q -- the triggering principal is recorded as today", entry.name, landed.StopNote.By, want)
				}
			}
			if got := mustGoalRecord(t, child.GoalRef); got.State != generated.GoalStateActive || got.State != goalBefore.State {
				t.Errorf("%s: a stop must never end a goal: before=%q after=%q", entry.name, goalBefore.State, got.State)
			}
		})
	}
}

// oneStopRedirectProvider is uncooperative on its FIRST request only (the
// stopped turn), and parks cooperatively on later ones (the replacement turn
// a redirect starts), so a redirect test leaves no failing second turn behind.
type oneStopRedirectProvider struct {
	calls         atomic.Int32
	entered       chan struct{}
	softCancelled chan struct{}
	release       chan struct{}
	once          sync.Once
}

func (p *oneStopRedirectProvider) open()                   { p.once.Do(func() { close(p.release) }) }
func (p *oneStopRedirectProvider) GetDefaultModel() string { return "one-stop-redirect" }
func (p *oneStopRedirectProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	if p.calls.Add(1) == 1 {
		close(p.entered)
		<-ctx.Done()
		close(p.softCancelled)
		<-p.release
		return &providers.LLMResponse{Content: "late answer of the stopped turn"}, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.release:
		return &providers.LLMResponse{Content: "replacement done"}, nil
	}
}

// TestOneStop_RedirectStopHalfUsesTheSameThreeSecondTimeline: the stop half of
// delegate redirect is the one stop method (session scope), so its forced stop
// fires at 3 s -- not the retired 5 s redirectEscalationDelay.
func TestOneStop_RedirectStopHalfUsesTheSameThreeSecondTimeline(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	provider := &oneStopRedirectProvider{
		entered: make(chan struct{}), softCancelled: make(chan struct{}), release: make(chan struct{}),
	}
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: registered agent missing")
	}
	inst.Provider = provider
	t.Cleanup(provider.open)
	parentID := newTestSteeringSession(t, al, "ws-one-stop-redirect")
	child := launchGoalBearingChild(t, al, parentID, "call-one-stop-redirect", goalChildLaunchOptions{live: true})
	select {
	case <-provider.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("SETUP: the helper never reached its provider")
	}
	handle := al.getActiveTurnState(child.SessionID)
	if handle == nil || !handle.IsAlive() {
		t.Fatal("SETUP: no live turn handle")
	}

	since := time.Now()
	res := delegateToolFor(t, al).Execute(tools.WithTranscriptSessionID(context.Background(), parentID),
		map[string]any{"action": "redirect", "session_id": child.SessionID, "text": "do the other thing instead"})
	if res == nil || res.IsError {
		t.Fatalf("delegate redirect: %+v", res)
	}
	requireOneStopTimeline(t, "delegate redirect (stop half)", handle, since)

	// Let the stopped turn's tail retire so the redirect's resume half can
	// run and the fixture drains cleanly.
	provider.open()
	joinGoalFixtureRuns(t, al)
}
