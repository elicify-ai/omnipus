// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package steer

import "errors"

// Typed errors published from pkg/steer (I-2, I-3, I-6). Every
// SessionLauncher/Canceller implementation returns one of these — never a
// package-private error a caller outside pkg/agent cannot compare against
// — so a caller in pkg/tools, pkg/gateway or a test can branch on the
// specific failure with errors.Is.
var (
	// ErrTitleRequired is returned when a launch supplies neither Label nor
	// Task text (Q15, founder decision round 5) — refused before any write.
	ErrTitleRequired = errors.New("steer: launch: title required (label and task text both empty)")

	// ErrDepthExceeded is returned when a launch would exceed the
	// effective delegation depth (global ceiling or a tighter per-edge
	// value).
	ErrDepthExceeded = errors.New("steer: launch: delegation depth exceeded")

	// ErrInvalidEdge is returned for a cycle, an unknown steering session,
	// or an invalid ancestor chain.
	ErrInvalidEdge = errors.New("steer: launch: invalid edge (cycle, unknown steering session, or invalid ancestor)")

	// ErrAgentUnknown is returned when LaunchRequest.TargetAgentID names no
	// known agent profile.
	ErrAgentUnknown = errors.New("steer: launch: unknown target agent")

	// ErrStoreWrite is returned when a mandatory write fails; the launch
	// leaves no session and no lifecycle record.
	ErrStoreWrite = errors.New("steer: launch: mandatory write failed")

	// ErrDispatchCancelled is returned by Dispatch when the record's Stop
	// marker names the record's current generation.
	ErrDispatchCancelled = errors.New("steer: dispatch: cancelled (stop marker for current generation)")

	// ErrStaleGeneration is returned by Dispatch, or by reconstruction for
	// a wake, whose generation is older than the record's current one.
	ErrStaleGeneration = errors.New("steer: dispatch: stale generation")

	// ErrTerminal is returned by Dispatch of a terminal session with no
	// follow-up.
	ErrTerminal = errors.New("steer: dispatch: session is terminal")
)
