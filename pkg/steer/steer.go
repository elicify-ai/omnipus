// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package steer holds every interface and type ADR-091 publishes across
// package boundaries: "a sub-agent is a session steered
// by another session." pkg/agent implements these interfaces; the gateway
// wires the implementations at boot (pkg/gateway/gateway_boot.go); every
// other consumer (pkg/tools, pkg/channels, pkg/gateway,
// pkg/agent/testutil) imports pkg/steer and nothing else of the agent's —
// audience resolution, cancellation, revival, classification and the
// fixture hooks are all consumable across package boundaries through this
// one neutral package.
//
// Import direction: pkg/steer may import pkg/session types, and I-9 has
// pkg/session's index return a steer.IndexReport — read together those two
// sentences are a cycle. Resolved here one way, stated once: every type
// PERSISTED on session.LifecycleRecord (Origin, SteeredBy, ReportingTarget,
// Authorization, Limits, Stop, Principal) and I-9's IndexReport are defined
// in pkg/session (see pkg/session/lifecycle_edge.go and
// pkg/session/lifecycle_index.go), and pkg/steer re-exports each one with a
// Go type alias below so callers still resolve
// `steer.Origin`, `steer.IndexReport`, etc. pkg/steer imports
// pkg/session (for exactly those aliases and the two store types Deps
// carries) and pkg/api/generated (for the SessionMessage wire type
// UpwardEvent carries) — nothing from pkg/agent, pkg/tools, pkg/channels
// or pkg/gateway (FR-A-016).
package steer

import "context"

// SessionLauncher creates a steered session (I-2 Launch) and decides
// admission for its first or next turn (I-2 Dispatch). Implemented in
// pkg/agent (pkg/agent/steer_launcher.go); injected at gateway
// boot into the delegate tool, the task executor and the I-7 fixture.
type SessionLauncher interface {
	Launch(ctx context.Context, req LaunchRequest) (LaunchResult, error)
	Dispatch(ctx context.Context, sessionID string, gen int) (DispatchResult, error)
}

// AudienceResolver answers "who is this session's audience?" at every
// publication boundary: a human user, its steering session, or
// nobody. Implemented in pkg/agent on top of I-8 (pkg/agent/steer_audience.go);
// injected into every package that hosts a boundary.
type AudienceResolver interface {
	Audience(ctx context.Context, sessionID string) (Audience, Class, error)
}

// UpwardDeliverer delivers one event from a child session to its steering
// session (I-5) — the durable-inbox-then-wake path `message_parent`
// already runs, made the only upward path. Implemented in pkg/agent
// (pkg/agent/steer_audience.go::SteerUpwardDeliverer); injected into every
// package that can report upward, by
// pkg/agent/steer_boundary.go::SetSteerAudienceDeps.
//
// It REPLACED the former tools.MessageParentWaker, which no longer exists:
// do not look for that interface, and do not wire a second upward path
// beside this one.
type UpwardDeliverer interface {
	Deliver(ctx context.Context, event UpwardEvent) (Delivery, error)
}

// Canceller stops a subtree (I-6 CancelSubtree) and revives a stopped or
// terminal session for a newer instruction (I-6 Revive). Implemented in
// pkg/agent (pkg/agent/steer_cancel.go); published for the
// gateway, the tools and the I-7 fixture — the reservation half
// (reserveDispatch) is package-internal to pkg/agent, not part of this
// interface.
type Canceller interface {
	CancelSubtree(ctx context.Context, sessionID string, by Principal) (CancelReport, error)
	Revive(ctx context.Context, sessionID string, by Principal) (int, error)
}

// RecordClassifier answers I-8's question for one session: which of the
// six classes does its lifecycle record (plus its own session metadata)
// belong to. Implemented in pkg/agent (pkg/agent/steer_classify.go);
// consumed by SteerAudienceResolver and boot recovery.
type RecordClassifier interface {
	Classify(ctx context.Context, sessionID string) (Class, error)
}

// BoundaryObserver is called at every publication boundary, BEFORE the
// audience decision is acted on. The one production implementation
// (NopBoundaryObserver, this package) is a no-op.
//
// Test implementations record instead, so a test can tell "boundary
// exercised and blocked" apart from "never exercised". The shared I-7
// fixture is pkg/agent/testutil: the type is OutboundRecorder and the
// constructor is RecordingOutbound, so the assertion reads
// `testutil.RecordingOutbound(t).AssertBoundaryInvoked(...)`. It is not the
// only one — pkg/agent, pkg/askuser, pkg/channels, pkg/gateway, pkg/tools
// and this package's own consumer test each define a local recorder, so a
// change to this interface breaks seven implementations, not two.
type BoundaryObserver interface {
	Observe(boundary Boundary, sessionID string, audience Audience)
}
