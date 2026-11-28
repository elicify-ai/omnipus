// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build s1seams

package channels

import "sort"

// RegisteredPatternsForTest returns a sorted copy of every pattern currently
// registered on the shared HTTP router (the dynamicServeMux every gateway
// route ends in: health endpoints, channel webhook/health handlers and every
// route added through RegisterHTTPHandler). It is nil before
// SetupHTTPServer. Read-only; compiled only in the s1seams test build
// (#1960 S1, seam H46), so a normal build has no such method.
func (m *Manager) RegisteredPatternsForTest() []string {
	if m == nil || m.mux == nil {
		return nil
	}
	return m.mux.patterns()
}

// patterns is a sorted copy of the registered patterns.
func (dm *dynamicServeMux) patterns() []string {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	out := make([]string, 0, len(dm.handlers))
	for p := range dm.handlers {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
