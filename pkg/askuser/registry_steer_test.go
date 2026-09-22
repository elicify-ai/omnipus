// Omnipus — AskUserQuestion registry tests (ADR-091 WP-B boundary 12)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package askuser

import (
	"context"
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/steer"
)

// fakeSteerAudience is a minimal steer.AudienceResolver double, keyed by
// session id.
type fakeSteerAudience struct {
	audience map[string]steer.Audience
}

var _ steer.AudienceResolver = (*fakeSteerAudience)(nil)

func (f *fakeSteerAudience) Audience(_ context.Context, sessionID string) (steer.Audience, steer.Class, error) {
	a, ok := f.audience[sessionID]
	if !ok {
		return steer.AudienceUser, steer.ClassOrdinaryRoot, nil
	}
	return a, steer.ClassSteered, nil
}

// recordingObserver is a minimal steer.BoundaryObserver double that records
// every call.
type recordingObserver struct {
	calls []steer.Boundary
}

var _ steer.BoundaryObserver = (*recordingObserver)(nil)

func (r *recordingObserver) Observe(b steer.Boundary, _ string, _ steer.Audience) {
	r.calls = append(r.calls, b)
}

// TestCreatePending_SteeredSession_ObservedAndRejected proves ADR-091
// boundary 12 (landing order §6, FR-B-012, FR-B-014): CreatePending
// consults the injected steer.AudienceResolver, calls
// steer.BoundaryObserver.Observe BEFORE acting, and refuses a steered
// session even when its durable ParentSessionID metadata is (for whatever
// reason) not yet in step with the edge.
func TestCreatePending_SteeredSession_ObservedAndRejected(t *testing.T) {
	store := newTestStore(t)
	sid := newOwnerSession(t, store)
	reg := NewRegistry(store, &fakeResume{}, Options{})
	t.Cleanup(reg.Quiesce)

	obs := &recordingObserver{}
	reg.SetSteerAudienceResolver(&fakeSteerAudience{
		audience: map[string]steer.Audience{sid: steer.AudienceSteeringSession},
	}, obs)

	err := reg.CreatePending(testSet(sid))
	if !errors.Is(err, ErrDelegatedChild) {
		t.Fatalf("want ErrDelegatedChild (edge-based rejection), got %v", err)
	}
	if len(obs.calls) != 1 || obs.calls[0] != steer.BoundaryQuestionCard {
		t.Fatalf("expected exactly one Observe(question_card, ...) call, got %+v", obs.calls)
	}
}

// TestCreatePending_OrdinaryRootSession_ObservedAndAllowed proves the
// resolver's "user" answer does not interfere with a legitimate owner
// session's card.
func TestCreatePending_OrdinaryRootSession_ObservedAndAllowed(t *testing.T) {
	store := newTestStore(t)
	sid := newOwnerSession(t, store)
	reg := NewRegistry(store, &fakeResume{}, Options{})
	t.Cleanup(reg.Quiesce)

	obs := &recordingObserver{}
	reg.SetSteerAudienceResolver(&fakeSteerAudience{audience: map[string]steer.Audience{}}, obs)

	if err := reg.CreatePending(testSet(sid)); err != nil {
		t.Fatalf("expected an ordinary root session's card to be created, got: %v", err)
	}
	if len(obs.calls) != 1 || obs.calls[0] != steer.BoundaryQuestionCard {
		t.Fatalf("expected exactly one Observe(question_card, ...) call, got %+v", obs.calls)
	}
}
