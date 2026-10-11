package agent

import (
	"context"
	"testing"
)

type nopStreamerForGoal struct{}

func (nopStreamerForGoal) Update(context.Context, string) error   { return nil }
func (nopStreamerForGoal) Finalize(context.Context, string) error { return nil }
func (nopStreamerForGoal) Cancel(context.Context)                 {}

type goalRecordingStreamer struct {
	nopStreamerForGoal
	got string
}

func (g *goalRecordingStreamer) SetGoalID(id string) { g.got = id }

// Final-gate M11: the goal id a turn was dispatched under reaches a streamer that
// implements the named capability, and only that capability.
func TestStampStreamerGoalID_ReachesAGoalIDStreamer(t *testing.T) {
	ts := &turnState{goalID: "goal-7"}
	rec := &goalRecordingStreamer{}
	var _ GoalIDStreamer = rec
	ts.stampStreamerGoalID(rec)
	if rec.got != "goal-7" {
		t.Fatalf("the streamer got goal id %q, want goal-7", rec.got)
	}
	ts.stampStreamerGoalID(&nopStreamerForGoal{}) // a streamer without the capability is untouched
}
