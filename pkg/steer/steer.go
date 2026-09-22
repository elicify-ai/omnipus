// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package steer holds every interface and type ADR-091 publishes across
// package boundaries (landing order §2): "a sub-agent is a session steered
// by another session." pkg/agent implements these interfaces; the gateway
// wires the implementations at boot (pkg/gateway/gateway_boot.go); every
// other consumer (pkg/tools, pkg/channels, pkg/gateway,
// pkg/agent/testutil) imports pkg/steer and nothing else of the agent's —
// audience resolution, cancellation, revival, classification and the
// fixture hooks are all consumable across package boundaries through this
// one neutral package.
//
// Owner: WP-A, published complete at CP-0 (landing order §4). Stub bodies
// for WP-B's and WP-D's implementations are compiled here too, in files
// named for their eventual owners (pkg/agent/steer_audience.go,
// pkg/agent/steer_cancel.go) — see landing order §7 "CP-0 publication".
//
// Import direction (the landing order's own §2 states pkg/steer may import
// pkg/session types, and I-9 has pkg/session's index return a
// steer.IndexReport — read together those two sentences are a cycle).
// Resolved here one way, stated once: every type PERSISTED on
// session.LifecycleRecord (Origin, SteeredBy, ReportingTarget,
// Authorization, Limits, Stop, Principal) and I-9's IndexReport are defined
// in pkg/session (see pkg/session/lifecycle_edge.go and
// pkg/session/lifecycle_index.go), and pkg/steer re-exports each one with a
// Go type alias below so the landing order's binding names still resolve
// as `steer.Origin`, `steer.IndexReport`, etc. pkg/steer imports
// pkg/session (for exactly those aliases and the two store types Deps
// carries) and pkg/api/generated (for the SessionMessage wire type
// UpwardEvent carries) — nothing from pkg/agent, pkg/tools, pkg/channels
// or pkg/gateway (landing order §2, FR-A-016).
package steer

import "context"

// SessionLauncher creates a steered session (I-2 Launch) and decides
// admission for its first or next turn (I-2 Dispatch). Implemented in
// pkg/agent (pkg/agent/steer_launcher.go, owner WP-A); injected at gateway
// boot into the delegate tool, the task executor and the I-7 fixture.
type SessionLauncher interface {
	Launch(ctx context.Context, req LaunchRequest) (LaunchResult, error)
	Dispatch(ctx context.Context, sessionID string, gen int) (DispatchResult, error)
}

// AudienceResolver answers "who is this session's audience?" at every
// boundary (landing order §6): a human user, its steering session, or
// nobody. Implemented in pkg/agent on top of I-8 (pkg/agent/steer_audience.go,
// owner WP-B); injected into every package that hosts a boundary.
type AudienceResolver interface {
	Audience(ctx context.Context, sessionID string) (Audience, Class, error)
}

// UpwardDeliverer delivers one event from a child session to its steering
// session (I-5) — the durable-inbox-then-wake path `message_parent`
// already runs, made the only upward path. Implemented in pkg/agent
// (pkg/agent/steer_audience.go, owner WP-B); injected wherever
// tools.MessageParentWaker is wired today (it replaces that interface).
type UpwardDeliverer interface {
	Deliver(ctx context.Context, event UpwardEvent) (Delivery, error)
}

// Canceller stops a subtree (I-6 CancelSubtree) and revives a stopped or
// terminal session for a newer instruction (I-6 Revive). Implemented in
// pkg/agent (pkg/agent/steer_cancel.go, owner WP-D); published for the
// gateway, the tools and the I-7 fixture — the reservation half
// (reserveDispatch) is package-internal to pkg/agent, not part of this
// interface.
type Canceller interface {
	CancelSubtree(ctx context.Context, sessionID string, by Principal) (CancelReport, error)
	Revive(ctx context.Context, sessionID string, by Principal) (int, error)
}

// RecordClassifier answers I-8's question for one session: which of the
// six classes does its lifecycle record (plus its own session metadata)
// belong to. Implemented in pkg/agent (pkg/agent/steer_classify.go, owner
// WP-A); consumed by WP-B (AudienceResolver) and WP-D (boot recovery).
type RecordClassifier interface {
	Classify(ctx context.Context, sessionID string) (Class, error)
}

// BoundaryObserver is called at every boundary in the landing order §6
// inventory, BEFORE the audience decision is acted on. The production
// implementation (NopBoundaryObserver, this package) is a no-op; WP-G's I-7
// fixture supplies a recording implementation
// (RecordingOutbound.AssertBoundaryInvoked) that tells "exercised and
// blocked" apart from "never exercised".
type BoundaryObserver interface {
	Observe(boundary Boundary, sessionID string, audience Audience)
}
