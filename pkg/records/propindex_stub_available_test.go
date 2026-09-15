//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

// Omnipus — ADR-068 D16.2a: the SQLite-capable half costs nothing.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import "testing"

// TestCapableBuild_PropertyIndexIsAvailable asserts the constant, so that a
// mistake in either //go:build line — which would otherwise show up only as a
// mysterious refusal on a normal machine — fails here instead.
func TestCapableBuild_PropertyIndexIsAvailable(t *testing.T) {
	if !PropertyIndexAvailable {
		t.Fatal("propindex_stub_unavailable.go compiled on a SQLite-capable target — " +
			"the record layer would refuse every typed query on an ordinary build")
	}
}

// TestCapableBuild_NoCapabilityIsRefused guards against the gate being applied
// where it does not belong. Every capability must pass through untouched on a
// build that has SQLite; a refusal here is a false negative, and a false
// negative is how "graceful degradation" becomes "the feature is broken".
func TestCapableBuild_NoCapabilityIsRefused(t *testing.T) {
	for _, c := range PropertyIndexCapabilities {
		if err := RequirePropertyIndex(c); err != nil {
			t.Errorf("%s: refused on a SQLite-capable build: %v", c, err)
		}
	}
}
