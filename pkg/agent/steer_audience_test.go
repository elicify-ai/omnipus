// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 WP-B — I-5's real steer.AudienceResolver body (landing order I-5,
// "ordinary_root -> user; steered -> steering session; every other class, a
// missing record for a session that should have one, or an error -> none").
// TDD plan test 1, TestAudience_ByClass.

package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// TestAudience_ByClass exercises every one of I-8's classes through the real
// AudienceResolver sitting on top of SteerRecordClassifier, proving each
// class maps onto the correct audience per landing order I-5.
func TestAudience_ByClass(t *testing.T) {
	lifecycle, unified := newTestClassifierStores(t)
	classifier := NewSteerRecordClassifier(lifecycle, unified)
	resolver := NewSteerAudienceResolver(classifier)
	ctx := context.Background()

	t.Run("ordinary_root_is_user", func(t *testing.T) {
		id := newMetaSession(t, unified, session.SessionTypeChat, "")
		audience, class, err := resolver.Audience(ctx, id)
		if err != nil {
			t.Fatalf("Audience: %v", err)
		}
		if class != steer.ClassOrdinaryRoot {
			t.Fatalf("class = %q, want ordinary_root", class)
		}
		if audience != steer.AudienceUser {
			t.Fatalf("Audience(ordinary_root) = %q, want user", audience)
		}
	})

	t.Run("steered_is_steering_session", func(t *testing.T) {
		parentID := newMetaSession(t, unified, session.SessionTypeChat, "")
		childID := newMetaSession(t, unified, session.SessionTypeDelegate, parentID)
		if err := lifecycle.Persist(&session.LifecycleRecord{
			SessionID: childID, State: session.LifecycleRunning,
			OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: parentID,
			WorkspaceID: "ws-1", AgentID: "worker",
			Origin: &session.Origin{Kind: session.OriginKindDelegate},
			SteeredBy: &session.SteeredBy{
				SteeringSessionID: parentID,
				RootSessionID:     parentID,
			},
		}); err != nil {
			t.Fatalf("Persist: %v", err)
		}
		audience, class, err := resolver.Audience(ctx, childID)
		if err != nil {
			t.Fatalf("Audience: %v", err)
		}
		if class != steer.ClassSteered {
			t.Fatalf("class = %q, want steered", class)
		}
		if audience != steer.AudienceSteeringSession {
			t.Fatalf("Audience(steered) = %q, want steering_session", audience)
		}
	})

	for _, tc := range []struct {
		name  string
		class steer.Class
	}{
		{"damaged_child", steer.ClassDamagedChild},
		{"legacy_delegate", steer.ClassLegacyDelegate},
		{"unreadable", steer.ClassUnreadable},
		{"invalid_edge", steer.ClassInvalidEdge},
	} {
		t.Run(tc.name+"_is_none", func(t *testing.T) {
			fake := &fakeAgentClassifier{class: tc.class}
			r := NewSteerAudienceResolver(fake)
			audience, class, err := r.Audience(ctx, "any-session")
			if err != nil {
				t.Fatalf("Audience: %v", err)
			}
			if class != tc.class {
				t.Fatalf("class = %q, want %q", class, tc.class)
			}
			if audience != steer.AudienceNone {
				t.Fatalf("Audience(%s) = %q, want none", tc.class, audience)
			}
		})
	}

	t.Run("classifier_error_is_none", func(t *testing.T) {
		fake := &fakeAgentClassifier{err: errors.New("boom")}
		r := NewSteerAudienceResolver(fake)
		audience, _, err := r.Audience(ctx, "any-session")
		if err == nil {
			t.Fatal("expected the classifier's error to propagate")
		}
		if audience != steer.AudienceNone {
			t.Fatalf("Audience(classifier error) = %q, want none", audience)
		}
	})
}

// fakeAgentClassifier is a minimal steer.RecordClassifier double for testing
// AudienceResolver in isolation from the real classifier's store I/O.
type fakeAgentClassifier struct {
	class steer.Class
	err   error
}

var _ steer.RecordClassifier = (*fakeAgentClassifier)(nil)

func (f *fakeAgentClassifier) Classify(context.Context, string) (steer.Class, error) {
	if f.err != nil {
		return steer.ClassUnreadable, f.err
	}
	return f.class, nil
}
