// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 WP-B — the shared per-boundary helper every landing-order §6 site
// calls: resolve audience through the injected steer.AudienceResolver, then
// call steer.BoundaryObserver.Observe before the caller acts (FR-B-001,
// FR-B-014).

package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// recordingBoundaryObserver is a minimal steer.BoundaryObserver double that
// records every Observe call, for asserting "boundary invoked" in isolation
// from WP-G's shared I-7 fixture (which this package cannot import — I-7
// lives in pkg/agent/testutil, a downstream consumer of this package).
type recordingBoundaryObserver struct {
	calls []struct {
		boundary steer.Boundary
		session  string
		audience steer.Audience
	}
}

var _ steer.BoundaryObserver = (*recordingBoundaryObserver)(nil)

func (r *recordingBoundaryObserver) Observe(b steer.Boundary, sessionID string, a steer.Audience) {
	r.calls = append(r.calls, struct {
		boundary steer.Boundary
		session  string
		audience steer.Audience
	}{b, sessionID, a})
}

func newSteerBoundaryTestLoop(t *testing.T) *AgentLoop {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &toolCallProvider{finalResp: "ok"})
	t.Cleanup(al.Close)
	return al
}

// TestAudienceFor_UnwiredResolver_AnswersUser proves a bare AgentLoop that
// never called SetSteerAudienceDeps keeps today's unrestricted behaviour
// (AudienceUser) rather than breaking every test that never wires steering.
func TestAudienceFor_UnwiredResolver_AnswersUser(t *testing.T) {
	al := newSteerBoundaryTestLoop(t)
	obs := &recordingBoundaryObserver{}
	al.steerDepsMu.Lock()
	al.boundaryObserver = obs
	al.steerDepsMu.Unlock()

	got := al.audienceFor(context.Background(), steer.BoundaryFinalReply, "some-session")
	if got != steer.AudienceUser {
		t.Fatalf("audienceFor(unwired) = %q, want user", got)
	}
	if len(obs.calls) != 1 || obs.calls[0].boundary != steer.BoundaryFinalReply || obs.calls[0].audience != steer.AudienceUser {
		t.Fatalf("expected exactly one Observe(final_reply, ..., user) call, got %+v", obs.calls)
	}
}

// TestAudienceFor_WiredResolver_DelegatesAndObserves proves a wired resolver
// is consulted and its answer (not a hardcoded default) reaches Observe.
func TestAudienceFor_WiredResolver_DelegatesAndObserves(t *testing.T) {
	al := newSteerBoundaryTestLoop(t)
	obs := &recordingBoundaryObserver{}
	fake := &fakeAgentClassifier{class: steer.ClassSteered}
	al.SetSteerAudienceDeps(NewSteerAudienceResolver(fake), obs, NewSteerUpwardDeliverer())

	got := al.audienceFor(context.Background(), steer.BoundaryMedia, "child-1")
	if got != steer.AudienceSteeringSession {
		t.Fatalf("audienceFor(steered) = %q, want steering_session", got)
	}
	if len(obs.calls) != 1 || obs.calls[0].boundary != steer.BoundaryMedia || obs.calls[0].audience != steer.AudienceSteeringSession {
		t.Fatalf("expected exactly one Observe(media, ..., steering_session) call, got %+v", obs.calls)
	}
}

// TestAudienceFor_ResolverError_AnswersNone proves a resolver error never
// falls back to the user (D3 "answers none on any doubt").
func TestAudienceFor_ResolverError_AnswersNone(t *testing.T) {
	al := newSteerBoundaryTestLoop(t)
	obs := &recordingBoundaryObserver{}
	fake := &fakeAgentClassifier{err: context.DeadlineExceeded}
	al.SetSteerAudienceDeps(NewSteerAudienceResolver(fake), obs, NewSteerUpwardDeliverer())

	got := al.audienceFor(context.Background(), steer.BoundarySyncToolText, "child-1")
	if got != steer.AudienceNone {
		t.Fatalf("audienceFor(resolver error) = %q, want none", got)
	}
	if len(obs.calls) != 1 || obs.calls[0].audience != steer.AudienceNone {
		t.Fatalf("expected exactly one Observe(..., none) call, got %+v", obs.calls)
	}
}
