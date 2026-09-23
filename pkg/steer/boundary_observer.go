// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package steer

// NopBoundaryObserver is the production BoundaryObserver — every
// publication boundary calls Observe before acting, and in
// production that call does nothing. Every OTHER implementation is a test
// recorder used to distinguish "boundary exercised and blocked" from "never
// exercised"; there are several, not one — see BoundaryObserver's own doc
// comment in steer.go for where they live.
type NopBoundaryObserver struct{}

var _ BoundaryObserver = NopBoundaryObserver{}

// Observe implements BoundaryObserver. Deliberately a no-op.
func (NopBoundaryObserver) Observe(Boundary, string, Audience) {}
