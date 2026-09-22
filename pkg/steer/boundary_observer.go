// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package steer

// NopBoundaryObserver is the production BoundaryObserver — every boundary
// in the landing order §6 inventory calls Observe before acting, and in
// production that call does nothing. WP-G's I-7 fixture supplies the only
// other implementation (RecordingOutbound), used in tests to distinguish
// "boundary exercised and blocked" from "never exercised".
type NopBoundaryObserver struct{}

var _ BoundaryObserver = NopBoundaryObserver{}

// Observe implements BoundaryObserver. Deliberately a no-op.
func (NopBoundaryObserver) Observe(Boundary, string, Audience) {}
