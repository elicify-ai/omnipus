// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Owner: WP-A (this lane, phase 2). CP-0 publishes this file compiled, with
// a body that reports "not wired" — nothing calls it yet (landing order §4
// CP-0: "Stubs are replaced by real bodies at CP-2 / CP-3; no alias
// period, no second path"). The real body — task_executor.go's
// createTaskSessionSync + mintTaskLifecycleRecord, extracted and
// generalised with the edge (I-1/I-2) — lands after CP-0 is merged; see
// the WP-A lane brief §"Do NOT start the launcher body ... yet — that is
// your phase 2."
package agent

import (
	"context"
	"errors"

	"github.com/elicify-ai/omnipus/pkg/steer"
)

// errSteerLauncherNotWired is returned by every SteerLauncher method until
// WP-A's phase 2 replaces this stub body. Deliberately NOT one of
// pkg/steer's typed errors (ErrTitleRequired, ...): those name real launch
// refusals a caller may branch on; this one names "nothing is wired to
// this yet" and must never be mistaken for a real verdict.
var errSteerLauncherNotWired = errors.New("agent: steer: SessionLauncher not wired (ADR-091 CP-0 stub — WP-A phase 2 lands the real body)")

// SteerLauncher is the CP-0 compiled stub for steer.SessionLauncher, owned
// by WP-A. Nothing calls it yet.
type SteerLauncher struct{}

var _ steer.SessionLauncher = (*SteerLauncher)(nil)

// NewSteerLauncher returns the CP-0 stub SessionLauncher.
func NewSteerLauncher() *SteerLauncher { return &SteerLauncher{} }

// Launch implements steer.SessionLauncher. CP-0 stub: always refuses.
func (*SteerLauncher) Launch(context.Context, steer.LaunchRequest) (steer.LaunchResult, error) {
	return steer.LaunchResult{}, errSteerLauncherNotWired
}

// Dispatch implements steer.SessionLauncher. CP-0 stub: always refuses.
func (*SteerLauncher) Dispatch(context.Context, string, int) (steer.DispatchResult, error) {
	return steer.DispatchResult{}, errSteerLauncherNotWired
}
