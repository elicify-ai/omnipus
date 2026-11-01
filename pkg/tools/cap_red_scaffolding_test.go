package tools

// RED scaffolding: compatibility/injection only. This file does not implement
// observations, admission, message rendering, queueing or any other CAP policy.
// PlanExecuteTool has neither injection seam at the pinned base. The implementing
// owner may replace these assertions with direct calls to the chosen real seams.

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

type capRedSnapshot struct {
	active, max    int
	reliable       bool
	observed, tick time.Time
}

func (s capRedSnapshot) CapSnapshot() (int, int, bool, time.Time, time.Time) {
	return s.active, s.max, s.reliable, s.observed, s.tick
}

func capRedWireObservation(t *testing.T, tool *PlanExecuteTool, source JobCapSnapshotSource, now time.Time) {
	t.Helper()
	// Match the existing ListJobsTool injection shape, not a fake PlanExecuteTool.
	if setter, ok := any(tool).(interface {
		SetCapSnapshotSource(func() JobCapSnapshotSource)
	}); ok {
		setter.SetCapSnapshotSource(func() JobCapSnapshotSource { return source })
	} else {
		t.Error("CAP-A1 RED: execute_plan has no snapshot-source injection seam; real Execute still asserted below")
	}
	if clock, ok := any(tool).(interface{ SetNow(func() time.Time) }); ok {
		clock.SetNow(func() time.Time { return now })
	} else {
		t.Error("CAP-A1 RED: execute_plan has no deterministic clock seam; age must be derived at read time")
	}
}

func capRedJSON(t *testing.T, result *ToolResult) map[string]any {
	t.Helper()
	if result == nil || result.IsError {
		t.Fatalf("expected successful real tool result, got %+v", result)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result.ForLLM), &got); err != nil {
		t.Fatalf("decode real tool JSON %q: %v", result.ForLLM, err)
	}
	return got
}
func capRedEqual(t *testing.T, label string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %#v; want %#v (cap spec r6)", label, got, want)
	}
}
