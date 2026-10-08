// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// One rule for an invalid performance.max_delegation_depth, at every consumer:
// log ERROR with config_key and fail closed — never the backstop default.

func TestConfiguredDelegationDepth_InvalidLogsErrorWithKeyAndSite(t *testing.T) {
	lines := dwLogLines(t)
	if _, valid := configuredDelegationDepth(config.PerformanceConfig{MaxDelegationDepth: -2}, "unit-site"); valid {
		t.Fatal("negative limit must be reported invalid")
	}
	found := false
	for _, l := range lines() {
		if strings.Contains(l, `"config_key":"performance.max_delegation_depth"`) && strings.Contains(l, `"site":"unit-site"`) {
			found = true
			if !strings.Contains(l, `"level":"error"`) {
				t.Fatalf("logged below error: %s", l)
			}
		}
	}
	if !found {
		t.Fatalf("no ERROR naming config_key and site: %v", lines())
	}
	for _, ok := range []int{0, 4} {
		if got, valid := configuredDelegationDepth(config.PerformanceConfig{MaxDelegationDepth: ok}, "s"); !valid || got != ok {
			t.Errorf("limit %d: got (%d,%v)", ok, got, valid)
		}
	}
}

// The ownership-walk and task-recursion setters take the tightest positive
// bound on an invalid limit, not the backstop default (3).
func TestDelegationDepthBound_InvalidFailsClosedValidResolves(t *testing.T) {
	if got := delegationDepthBound(config.PerformanceConfig{MaxDelegationDepth: -1}, "s"); got != failClosedBoundedDepth {
		t.Errorf("invalid limit: bound = %d, want %d", got, failClosedBoundedDepth)
	}
	if got := delegationDepthBound(config.PerformanceConfig{}, "s"); got != defaultMaxSubTurnDepth {
		t.Errorf("unset limit: bound = %d, want backstop %d", got, defaultMaxSubTurnDepth)
	}
	if got := delegationDepthBound(config.PerformanceConfig{MaxDelegationDepth: 5}, "s"); got != 5 {
		t.Errorf("explicit limit: bound = %d, want 5", got)
	}
}

// The launch budget is 0 on an invalid limit (no delegation budget, matching the
// gate's denial) and positive on a valid one.
func TestStartingRemainingDepth_InvalidLimitIsZeroBudget(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	l := NewSteerLauncher(al)
	rec := &session.LifecycleRecord{}

	if got := l.startingRemainingDepth(rec, "worker", 0); got <= 0 {
		t.Fatalf("control: valid default limit must leave a positive budget, got %d", got)
	}
	al.GetConfig().Performance.MaxDelegationDepth = -1
	if got := l.startingRemainingDepth(rec, "worker", 0); got != 0 {
		t.Fatalf("invalid limit must leave no budget, got %d", got)
	}
}
