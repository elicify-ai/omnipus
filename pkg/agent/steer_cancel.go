// Owner: WP-D (landing order §7: "the WP-A lane writes the compiled no-op
// bodies for the interfaces WP-B and WP-D later implement, in files named
// for their owners ... ownership of those files passes to WP-B and WP-D at
// CP-0"). WP-A (this lane) writes this file's CP-0 stub bodies only; WP-D
// replaces them with the real I-6 cascade, reservation and revival at CP-3.
// Do not add production logic here after CP-0 — that is WP-D's.

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package agent

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// SteerCanceller is the CP-0 compiled stub for steer.Canceller, owned by
// WP-D from CP-0 onward. CancelSubtree reaches nothing; Revive returns the
// current generation unchanged — placeholders until WP-D's I-6 cascade
// lands at CP-3.
type SteerCanceller struct {
	Lifecycle *session.LifecycleStore
}

var _ steer.Canceller = (*SteerCanceller)(nil)

// NewSteerCanceller returns the CP-0 stub Canceller.
func NewSteerCanceller(lifecycle *session.LifecycleStore) *SteerCanceller {
	return &SteerCanceller{Lifecycle: lifecycle}
}

// CancelSubtree implements steer.Canceller. CP-0 stub: reaches nothing —
// every candidate is reported unreachable rather than silently dropped, so
// a caller driving this stub before CP-3 sees an honest empty cascade, not
// a false "reached" report.
func (*SteerCanceller) CancelSubtree(_ context.Context, sessionID string, _ steer.Principal) (steer.CancelReport, error) {
	return steer.CancelReport{
		Unreachable: []steer.UnreachableSession{{
			ID:     sessionID,
			Reason: "steer: Canceller not wired (ADR-091 CP-0 stub — WP-D lands the real cascade at CP-3)",
		}},
	}, nil
}

// Revive implements steer.Canceller. CP-0 stub: returns the record's
// current generation unchanged (no revival takes place).
func (c *SteerCanceller) Revive(_ context.Context, sessionID string, _ steer.Principal) (int, error) {
	if c.Lifecycle == nil {
		return 0, nil
	}
	rec, err := c.Lifecycle.Load(sessionID)
	if err != nil {
		return 0, err
	}
	return rec.Generation, nil
}

// reserveDispatch is I-6's package-internal reservation primitive
// (landing order I-6: "internal to pkg/agent, called by Dispatch"). The
// CP-0 stub admits everything — WP-D's phase-3 body refuses a Stop marker
// for the record's current generation (ErrDispatchCancelled), a stale
// generation (ErrStaleGeneration), or a terminal record with no follow-up
// (ErrTerminal). Nothing calls this yet: SteerLauncher.Dispatch is itself a
// CP-0 stub (steer_launcher.go).
func reserveDispatch(rec *session.LifecycleRecord, gen int) (ok bool, reason string) {
	return true, ""
}
