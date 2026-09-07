//go:build records_no_sqlite || mipsle || netbsd || (freebsd && arm)

// Omnipus — ADR-068 D16.2a / FR-020h: the refusal, executed.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run this half on any host with the forcing tag:
//
//	go test -tags goolm,stdjson,records_no_sqlite -run TestStubBuild ./pkg/records/
//
// It is the same code path linux/mipsle gets; the tag only decides which of the
// two PropertyIndexAvailable constants compiles.

package records

import (
	"errors"
	"strings"
	"testing"
)

// TestStubBuild_PropertyIndexIsUnavailable asserts the constant this whole file
// set turns on. If this fails, the build constraints have drifted apart and one
// of the two halves is compiling on the wrong target.
func TestStubBuild_PropertyIndexIsUnavailable(t *testing.T) {
	if PropertyIndexAvailable {
		t.Fatal("propindex_stub_available.go compiled under the SQLite-less build " +
			"constraints — the two //go:build lines are no longer complements")
	}
}

// TestStubBuild_EveryCapabilityRefusesByNameAndNeverReturnsNil is the exit
// criterion in ADR-068 W1 and spec SC-023: on a build without the properties
// index, every typed capability refuses by name, and zero of them return an
// empty success.
func TestStubBuild_EveryCapabilityRefusesByNameAndNeverReturnsNil(t *testing.T) {
	for _, c := range PropertyIndexCapabilities {
		err := RequirePropertyIndex(c)
		if err == nil {
			t.Fatalf("%s: RequirePropertyIndex returned nil on a SQLite-less build — "+
				"the caller would proceed and produce the empty result FR-020h forbids", c)
		}
		if !errors.Is(err, ErrPropertyIndexUnavailable) {
			t.Errorf("%s: refusal does not match the sentinel: %v", c, err)
		}

		var typed *PropertyIndexUnavailableError
		if !errors.As(err, &typed) {
			t.Fatalf("%s: refusal is not a *PropertyIndexUnavailableError: %v", c, err)
		}
		if typed.Capability != c {
			t.Errorf("%s: refusal names the wrong capability %q", c, typed.Capability)
		}
		if typed.Platform != PropertyIndexPlatform() {
			t.Errorf("%s: refusal names platform %q, want this host's %q",
				c, typed.Platform, PropertyIndexPlatform())
		}
		if !strings.Contains(err.Error(), PropertyIndexPlatform()) {
			t.Errorf("%s: refusal text does not name the platform: %s", c, err)
		}
	}
}

// TestStubBuild_TypedFilterQueryRefusesByName is the narrow case the exit
// criterion is written against: a typed filter — the query capability — must
// come back as a named refusal, not as "no records matched".
func TestStubBuild_TypedFilterQueryRefusesByName(t *testing.T) {
	err := RequirePropertyIndex(CapabilityTypedFilter)
	if err == nil {
		t.Fatal("a typed filter succeeded on a build with no properties index")
	}
	got := err.Error()
	want := "typed filters are unavailable on " + PropertyIndexPlatform() +
		": this build has no properties index. Plain-word search and knowledge_read still work"
	if got != want {
		t.Errorf("typed-filter refusal drifted\n got: %s\nwant: %s", got, want)
	}
	t.Logf("typed-filter refusal on this build: %s", got)
}
