// #1431 gate T4 / silent-failure finding 2: a failed final-round commit
// cannot terminate work or leave the live goal on judging. The chief's (b)
// supplies the visibility oracle; ADR-084 section 13 supplies accounting.
package agent

import (
	"os"
	"reflect"
	"strings"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestGoalAdjudication_FinalRoundPersistFailureStaysActiveAndRecovers(t *testing.T) {
	for _, child := range []bool{false, true} {
		name := "interactive"
		if child {
			name = "goal-bearing-child"
		}
		t.Run(name, func(t *testing.T) {
			g := newGap1431Chat(t)
			var parent string
			if child {
				parent = g.sid
				g.al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), session.NewLifecycleStore(t.TempDir()))
				wireSteerCompletionDeps(t, g.al)
				lc := stampG5ExitedExecution(t, g.al, launchGoalBearingChild947(t, g.al, parent, "gap1431-final-write"))
				ts, err := g.al.reconstructSteeredTurn(lc, nil)
				if err != nil {
					t.Fatalf("reconstruct child turn: %v", err)
				}
				g.sid, g.gid, g.opts = lc.SessionID, lc.GoalRef, ts.opts
			}
			gs := resolveGoalRecordStore()
			if _, err := gs.Update(g.gid, func(cur *goal.Goal) error {
				cur.Round = cur.MaxRounds - 1
				return nil
			}); err != nil {
				t.Fatalf("arrange last permissible round: %v", err)
			}
			judge := &gap1431Judge{met: false, reason: "the report still lacks the requested acceptance"}
			g.judge.Provider = judge
			dispatches := recordGoalDispatches(g.al)
			work := g.emptyClaim(t, "gap1431-final-unmet", "The report is ready to be checked.")
			before := mustGoalRecord(t, g.gid)
			// This existing fault helper proves EISDIR on the actual goal ID,
			// while proving reads still work; it also bites under CI's root uid.
			failGoalRecordWrites(t, g.gid)
			events, stop := recordAgentEvents(t, g.al)
			g.al.dispatchDeferredGoalAdjudication(work)
			stop() // drains all events; no sleeps or last-frame polling

			if n := len(judge.snapshot()); n != 1 {
				t.Fatalf("Judge requests = %d, want one completed final-round verdict", n)
			}
			after := mustGoalRecord(t, g.gid)
			if after.State != generated.GoalStateActive || after.Round != before.Round {
				t.Errorf("failed final persist changed durable state/round to %q/%d; want active/%d", after.State, after.Round, before.Round)
			}
			if !reflect.DeepEqual(after.LatestVerdict, before.LatestVerdict) || after.TerminalReason != before.TerminalReason {
				t.Errorf("failed final persist changed durable verdict/reason: before=%+v/%q after=%+v/%q", before.LatestVerdict, before.TerminalReason, after.LatestVerdict, after.TerminalReason)
			}
			if n := countGoalOutcomeEntries(t, g.store, g.sid); n != 0 {
				t.Errorf("terminal outcome entries after failed persist = %d, want zero", n)
			}
			for _, event := range dispatches.all() {
				// A child's verdict may notify its parent. That is not a
				// re-dispatch of this goal's worker; assert the target session.
				if event.TranscriptSessionID == g.sid {
					t.Errorf("failed final persist re-dispatched its worker: %+v", event)
				}
			}
			requireGap1431StorageFailureVisible(t, events, g.sid, before.Round)
			entries, err := g.store.ReadTranscript(g.sid)
			if err != nil {
				t.Fatalf("read storage-error transcript: %v", err)
			}
			foundStorageNote := false
			for _, e := range entries {
				if e.Type == session.EntryTypeSystem && strings.Contains(e.Content, "storage") && strings.Contains(e.Content, "retry") {
					foundStorageNote = true
				}
			}
			if !foundStorageNote {
				t.Error("failed final persist needs a durable system note naming storage and retry")
			}
			if child {
				lc, lerr := g.al.GetSessionLifecycleStore().Load(g.sid)
				if lerr != nil {
					t.Fatalf("read child lifecycle: %v", lerr)
				}
				if lc.State != session.LifecycleRunning || lc.Terminal() {
					t.Errorf("failed final persist completed child: state=%q terminal=%t, want running/false", lc.State, lc.Terminal())
				}
				messages, ierr := g.al.GetMessageInboxStore().Entries(parent)
				if ierr != nil {
					t.Fatalf("read parent inbox: %v", ierr)
				}
				for _, entry := range messages {
					if entry.Message == nil {
						continue
					}
					class, cerr := session.ClassifySessionMessage(*entry.Message)
					if cerr != nil {
						t.Fatalf("classify parent message: %v", cerr)
					}
					if class.Kind == "handback" || class.Kind == "error" {
						t.Errorf("failed final persist published premature %s to parent", class.Kind)
					}
				}
			}
			// Writable-store control: a subsequent real claim may finish, with
			// exactly the same last round; the failed write spent none of it.
			if err := os.Remove(goalRecordLockPath(g.gid)); err != nil {
				t.Fatalf("repair goal-store lock path: %v", err)
			}
			if _, err := gs.Update(g.gid, func(*goal.Goal) error { return nil }); err != nil {
				t.Fatalf("control: repaired store still refuses writes: %v", err)
			}
			g.al.dispatchDeferredGoalAdjudication(g.emptyClaim(t, "gap1431-repaired-claim", "Please check the report again."))
			final := mustGoalRecord(t, g.gid)
			if final.State != generated.GoalStateExhausted || final.Round != before.MaxRounds {
				t.Errorf("after storage repair state/round=%q/%d, want exhausted/%d", final.State, final.Round, before.MaxRounds)
			}
			if n := len(judge.snapshot()); n != 2 {
				t.Errorf("Judge requests after recovery = %d, want one failed-save adjudication and one retry", n)
			}
			out := requireOneGoalOutcome(t, g.store, g.sid, g.gid)
			if out.GoalOutcome.Ending != generated.GoalOutcomeEndingRoundsExhausted {
				t.Errorf("recovered final ending=%q, want rounds_exhausted", out.GoalOutcome.Ending)
			}
		})
	}
}

func requireGap1431StorageFailureVisible(t *testing.T, events *eventCollector, sid string, round int) {
	t.Helper()
	frames := goalStatusPayloadsFor(events, sid)
	if len(frames) < 2 || frames[0].State != goalPillJudging {
		t.Errorf("goal frames = %+v, want judging followed by a visible failure correction", frames)
	}
	if len(frames) > 0 {
		last := frames[len(frames)-1]
		if last.State != goalPillActive || last.Round != round {
			t.Errorf("last goal frame state/round=%q/%d, want active/%d, never stuck on judging", last.State, last.Round, round)
		}
		if !strings.Contains(strings.ToLower(last.LatestReason), "retry") {
			t.Errorf("live failure reason=%q, want a recovery instruction", last.LatestReason)
		}
	}
	notices := 0
	events.mu.Lock()
	defer events.mu.Unlock()
	for _, e := range events.events {
		if e.Kind != EventKindError {
			continue
		}
		p, ok := e.Payload.(ErrorPayload)
		if ok && p.SessionID == sid && p.Code == string(CodeJudgeConclusionFailed) {
			notices++
			if p.Message != UserMessageForCode(CodeJudgeConclusionFailed) || !strings.Contains(p.Detail, "directory") {
				t.Errorf("storage failure notice = %+v, want catalog message and actual EISDIR cause", p)
			}
		}
	}
	if notices != 1 {
		t.Errorf("visible storage-failure notices=%d, want exactly one judge_conclusion_failed notice", notices)
	}
}
