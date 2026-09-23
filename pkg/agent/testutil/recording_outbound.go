package testutil

import (
	"sync"

	"github.com/elicify-ai/omnipus/pkg/steer"
)

type assertionT interface {
	Helper()
	Fatalf(format string, args ...any)
}

// OutboundDelivery is one invocation of a user-visible sink.
type OutboundDelivery struct {
	Boundary  steer.Boundary
	SessionID string
	Address   string
	Kind      string
}

type boundaryInvocation struct {
	SessionID string
	Audience  steer.Audience
}

// OutboundRecorder records both pre-decision boundary probes and sink calls.
type OutboundRecorder struct {
	t assertionT

	mu         sync.Mutex
	boundaries map[steer.Boundary][]boundaryInvocation
	deliveries []OutboundDelivery
}

var _ steer.BoundaryObserver = (*OutboundRecorder)(nil)

// RecordingOutbound returns a concurrency-safe BoundaryObserver and sink recorder.
func RecordingOutbound(t assertionT) *OutboundRecorder {
	t.Helper()
	return &OutboundRecorder{t: t, boundaries: make(map[steer.Boundary][]boundaryInvocation)}
}

// Observe implements steer.BoundaryObserver.
func (r *OutboundRecorder) Observe(boundary steer.Boundary, sessionID string, audience steer.Audience) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.boundaries[boundary] = append(r.boundaries[boundary], boundaryInvocation{
		SessionID: sessionID, Audience: audience,
	})
}

// Record captures one sink invocation.
func (r *OutboundRecorder) Record(boundary steer.Boundary, sessionID, address, kind string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deliveries = append(r.deliveries, OutboundDelivery{
		Boundary: boundary, SessionID: sessionID, Address: address, Kind: kind,
	})
}

// SendMessage wraps the message tool's outbound sink.
func (r *OutboundRecorder) SendMessage(sessionID, address, kind string) {
	r.Record(steer.BoundaryAgentRequestedMessage, sessionID, address, kind)
}

// Deliveries returns an independent snapshot of recorded sink calls.
func (r *OutboundRecorder) Deliveries() []OutboundDelivery {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]OutboundDelivery(nil), r.deliveries...)
}

// AssertNothingTo fails if any sink sent to address.
func (r *OutboundRecorder) AssertNothingTo(address string) {
	r.t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, delivery := range r.deliveries {
		if delivery.Address == address {
			r.t.Fatalf("outbound delivery reached forbidden address %q at boundary %q from session %q", address, delivery.Boundary, delivery.SessionID)
		}
	}
}

// AssertReceived is the non-vacuity control: the named session must have reached a sink with kind.
func (r *OutboundRecorder) AssertReceived(sessionID, kind string) {
	r.t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, delivery := range r.deliveries {
		if delivery.SessionID == sessionID && delivery.Kind == kind {
			return
		}
	}
	r.t.Fatalf("no outbound control received for session %q with kind %q", sessionID, kind)
}

// BoundaryScope pins ONE expected audience decision at a boundary: the
// session the boundary ran for, and the audience that decision resolved to.
//
// Why this type exists (ADR-091 fix lane RX-TESTS): the unscoped
// AssertBoundaryInvoked below only ever proved "SOMETHING reached this
// boundary" — true of a correctly contained system AND of a broken one,
// because a broken gate still RUNS the boundary, it just answers the wrong
// audience.
//
// Measured, not assumed. Mutating the production resolver
// (pkg/agent/steer_audience.go::SteerAudienceResolver.Audience) so
// ClassSteered resolves AudienceNone instead of AudienceSteeringSession —
// every steered child silently losing the audience the ADR assigns it — left
// tests/adr091::TestE2E_ThreeLevelDelegation_NoLeak GREEN, because nothing
// leaks to the user under that mutation either, so AssertNothingTo cannot
// see it. With the scope below the same mutation fails the test.
//
// (For the record: the neighbouring mutation "ClassSteered resolves
// AudienceUser" — full containment loss — IS caught today, by
// AssertNothingTo. Scoping is about the decisions that never reach the bus.)
type BoundaryScope struct {
	SessionID string
	Audience  steer.Audience
}

// ForSession builds the BoundaryScope "this boundary ran for sessionID and
// resolved audience".
func ForSession(sessionID string, audience steer.Audience) BoundaryScope {
	return BoundaryScope{SessionID: sessionID, Audience: audience}
}

// AssertBoundaryInvoked proves the boundary ran before its audience decision.
//
// Pass one or more BoundaryScope values to make the assertion mean something:
// each scope requires (a) at least one recorded invocation of boundary for
// that session which resolved EXACTLY the named audience, and (b) NO
// invocation of boundary for that session which resolved any other audience.
// Half (b) is the half that catches a broken containment gate: a boundary
// that still runs but hands the decision to the wrong audience.
//
// Called with no scope it degrades to the original "was this boundary ever
// reached at all?" check. That form is only sound when the caller pairs it
// with a real containment assertion of its own (pkg/agent's boundary
// containment pairs drain the outbound bus either side of it); on its own it
// is vacuous, and new call sites should always pass a scope.
func (r *OutboundRecorder) AssertBoundaryInvoked(boundary steer.Boundary, scopes ...BoundaryScope) {
	r.t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	invocations := r.boundaries[boundary]
	if len(invocations) == 0 {
		r.t.Fatalf("boundary %q was not invoked", boundary)
		return
	}
	for _, scope := range scopes {
		matched := 0
		var wrong []steer.Audience
		for _, invocation := range invocations {
			switch {
			case invocation.SessionID != scope.SessionID:
			case invocation.Audience == scope.Audience:
				matched++
			default:
				wrong = append(wrong, invocation.Audience)
			}
		}
		if len(wrong) > 0 {
			r.t.Fatalf("boundary %q resolved audience %v for session %q, want %q on every invocation — "+
				"a boundary that still runs but answers the WRONG audience is the containment regression "+
				"ADR-091 exists to prevent (recorded: %+v)",
				boundary, wrong, scope.SessionID, scope.Audience, invocations)
			continue
		}
		if matched == 0 {
			r.t.Fatalf("boundary %q was never invoked for session %q with audience %q (recorded: %+v)",
				boundary, scope.SessionID, scope.Audience, invocations)
		}
	}
}

// BoundaryInvocationCount returns how many real producer decisions reached boundary.
func (r *OutboundRecorder) BoundaryInvocationCount(boundary steer.Boundary) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.boundaries[boundary])
}
