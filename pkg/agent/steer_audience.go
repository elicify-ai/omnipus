// Owner: WP-B (landing order §7: "the WP-A lane writes the compiled no-op
// bodies for the interfaces WP-B and WP-D later implement, in files named
// for their owners ... ownership of those files passes to WP-B and WP-D at
// CP-0"). WP-A (this lane) writes this file's CP-0 stub bodies only; WP-B
// replaces them with the real I-5 rule and Deliver path at CP-2. Do not
// add production logic here after CP-0 — that is WP-B's.

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package agent

import (
	"context"
	"errors"

	"github.com/elicify-ai/omnipus/pkg/steer"
)

// errSteerUpwardDelivererNotWired is returned by every SteerUpwardDeliverer
// method until WP-B's real I-5 Deliver path replaces this stub. Nothing
// calls it yet at CP-0.
var errSteerUpwardDelivererNotWired = errors.New("agent: steer: UpwardDeliverer not wired (ADR-091 CP-0 stub — WP-B lands the real body)")

// SteerAudienceResolver is the CP-0 compiled stub for
// steer.AudienceResolver, owned by WP-B from CP-0 onward. It returns
// today's behaviour — every session's audience is the human user — until
// WP-B lands I-5's real rule (ordinary_root -> user, steered -> steering
// session, everything else -> none).
type SteerAudienceResolver struct {
	// Classifier supplies the Class half of the answer (I-8, already real
	// as of CP-0 — see steer_classify.go); only the Audience half is a
	// placeholder here.
	Classifier steer.RecordClassifier
}

var _ steer.AudienceResolver = (*SteerAudienceResolver)(nil)

// NewSteerAudienceResolver returns the CP-0 stub AudienceResolver.
func NewSteerAudienceResolver(classifier steer.RecordClassifier) *SteerAudienceResolver {
	return &SteerAudienceResolver{Classifier: classifier}
}

// Audience implements steer.AudienceResolver (I-5, real body): a steered
// session's audience is always its steering session; an ordinary root's is
// the user; every other class, an unreadable record, or a classifier error
// answers none — "any doubt" never falls back to the user (D3).
func (r *SteerAudienceResolver) Audience(ctx context.Context, sessionID string) (steer.Audience, steer.Class, error) {
	if r.Classifier == nil {
		return steer.AudienceNone, steer.ClassUnreadable, errors.New("steer: audience: no classifier configured")
	}
	class, err := r.Classifier.Classify(ctx, sessionID)
	if err != nil {
		return steer.AudienceNone, class, err
	}
	switch class {
	case steer.ClassOrdinaryRoot:
		return steer.AudienceUser, class, nil
	case steer.ClassSteered:
		return steer.AudienceSteeringSession, class, nil
	default:
		// damaged_child, legacy_delegate, unreadable, invalid_edge: no
		// audience at all (D3's "answers none on any doubt").
		return steer.AudienceNone, class, nil
	}
}

// SteerUpwardDeliverer is the CP-0 compiled stub for steer.UpwardDeliverer,
// owned by WP-B from CP-0 onward. Nothing calls it yet.
type SteerUpwardDeliverer struct{}

var _ steer.UpwardDeliverer = (*SteerUpwardDeliverer)(nil)

// NewSteerUpwardDeliverer returns the CP-0 stub UpwardDeliverer.
func NewSteerUpwardDeliverer() *SteerUpwardDeliverer { return &SteerUpwardDeliverer{} }

// Deliver implements steer.UpwardDeliverer. CP-0 stub: always refuses.
func (*SteerUpwardDeliverer) Deliver(context.Context, steer.UpwardEvent) (steer.Delivery, error) {
	return steer.Delivery{}, errSteerUpwardDelivererNotWired
}
