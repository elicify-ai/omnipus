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

	// ErrTaskIDRequired is returned when a launch's Origin.Kind is task but
	// Origin.TaskID is empty — refused at launch, before any write, rather
	// than failing later at dispatch (task_executor.go::dispatchLaunchedTask
	// cannot load a task without its id). The missing field is named in the
	// message so the caller can report it precisely.
	ErrTaskIDRequired = errors.New("steer: launch: origin kind \"task\" requires Origin.TaskID")

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

	// ErrSteeringStopped is ADR-093 D2's launch-time backstop: a launch whose
	// steering conversation's own lifecycle record is terminal — or carries a
	// Stop marker for its current generation — is refused before anything is
	// created (no child record, no unified session, no goal). Callers map it
	// through IsSteeringUnavailable to ADR-093 D5's plain sentence instead of
	// surfacing store machinery text.
	ErrSteeringStopped = errors.New("steer: launch: steering conversation is not active (its record is terminal, or stopped for its current generation)")
)

// IsSteeringUnavailable reports whether err is one of the refusal sentinels
// that mean "this conversation cannot take the delegation right now because
// it is not active" (ADR-093 D5): the launch-time backstop
// (ErrSteeringStopped) or a dispatch refusal (ErrDispatchCancelled,
// ErrTerminal, ErrStaleGeneration). Callers map these to the D5 sentence
// instead of surfacing store machinery text.
func IsSteeringUnavailable(err error) bool {
	return errors.Is(err, ErrSteeringStopped) ||
		errors.Is(err, ErrDispatchCancelled) ||
		errors.Is(err, ErrTerminal) ||
		errors.Is(err, ErrStaleGeneration)
}
