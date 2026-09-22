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

// PublishOutbound wraps the text bus sink shared by text-producing boundaries.
func (r *OutboundRecorder) PublishOutbound(boundary steer.Boundary, sessionID, address, kind string) {
	r.Record(boundary, sessionID, address, kind)
}

// PublishOutboundMedia wraps the outbound-media bus sink.
func (r *OutboundRecorder) PublishOutboundMedia(sessionID, address, kind string) {
	r.Record(steer.BoundaryMedia, sessionID, address, kind)
}

// SendMedia wraps the direct channel media sink.
func (r *OutboundRecorder) SendMedia(sessionID, address, kind string) {
	r.Record(steer.BoundaryMedia, sessionID, address, kind)
}

// WriteExternalStream wraps an external-channel stream adapter's Write/Finalize sink.
func (r *OutboundRecorder) WriteExternalStream(sessionID, address, kind string) {
	r.Record(steer.BoundaryExternalChannelStreaming, sessionID, address, kind)
}

// SendWebSocket wraps the webchat WebSocket send sink.
func (r *OutboundRecorder) SendWebSocket(sessionID, address, kind string) {
	r.Record(steer.BoundaryWebchatStreaming, sessionID, address, kind)
}

// SendMessage wraps the message tool's outbound sink.
func (r *OutboundRecorder) SendMessage(sessionID, address, kind string) {
	r.Record(steer.BoundaryAgentRequestedMessage, sessionID, address, kind)
}

// NotifyTaskResult wraps task result notification.
func (r *OutboundRecorder) NotifyTaskResult(sessionID, address, kind string) {
	r.Record(steer.BoundaryTaskResultNotification, sessionID, address, kind)
}

// BroadcastQuestionCard wraps question-card broadcast.
func (r *OutboundRecorder) BroadcastQuestionCard(sessionID, address, kind string) {
	r.Record(steer.BoundaryQuestionCard, sessionID, address, kind)
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

// AssertBoundaryInvoked proves the boundary ran before its audience decision.
func (r *OutboundRecorder) AssertBoundaryInvoked(boundary steer.Boundary) {
	r.t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.boundaries[boundary]) == 0 {
		r.t.Fatalf("boundary %q was not invoked", boundary)
	}
}

// BoundaryInvocationCount returns how many real producer decisions reached boundary.
func (r *OutboundRecorder) BoundaryInvocationCount(boundary steer.Boundary) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.boundaries[boundary])
}
