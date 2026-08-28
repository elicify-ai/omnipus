// Omnipus — tests for ADR-068 D16.2a / FR-020h, the platform posture.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// These tests compile on BOTH halves of the gate. The half-specific behaviour
// (does RequirePropertyIndex refuse, or not) is asserted in
// propindex_stub_unavailable_test.go and propindex_stub_available_test.go, which
// carry the same build constraints as the files they cover.

package records

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

// TestPropertyIndexRefusal_EveryCapabilityHasItsOwnMessage is the guard on the
// vocabulary: a capability added to PropertyIndexCapabilities without a refusal
// message would silently fall through to the generic text, which names the
// capability but not what still works for it.
func TestPropertyIndexRefusal_EveryCapabilityHasItsOwnMessage(t *testing.T) {
	if len(PropertyIndexCapabilities) == 0 {
		t.Fatal("PropertyIndexCapabilities is empty — the platform gate covers nothing")
	}
	seen := make(map[PropertyIndexCapability]bool, len(PropertyIndexCapabilities))
	for _, c := range PropertyIndexCapabilities {
		if seen[c] {
			t.Errorf("duplicate capability %q in PropertyIndexCapabilities", c)
		}
		seen[c] = true
		if _, ok := propertyIndexRefusals[c]; !ok {
			t.Errorf("capability %q has no refusal message in propertyIndexRefusals", c)
		}
	}
	for c := range propertyIndexRefusals {
		if !seen[c] {
			t.Errorf("propertyIndexRefusals has a message for %q, which is not in "+
				"PropertyIndexCapabilities — an unreachable refusal", c)
		}
	}
}

// TestPropertyIndexRefusal_NamesThePlatformAndWhatStillWorks is the core FR-020h
// assertion about the message itself: every refusal names the platform, and none
// of them is a bare "unavailable" that leaves the operator with nowhere to go.
func TestPropertyIndexRefusal_NamesThePlatformAndWhatStillWorks(t *testing.T) {
	const platform = "linux/mipsle"
	for _, c := range PropertyIndexCapabilities {
		err := &PropertyIndexUnavailableError{Capability: c, Platform: platform}
		msg := err.Error()

		if !strings.Contains(msg, platform) {
			t.Errorf("%s: refusal does not name the platform %q: %s", c, platform, msg)
		}
		if !strings.Contains(msg, "properties index") {
			t.Errorf("%s: refusal does not name the missing component: %s", c, msg)
		}
		// Every refusal must tell the reader what survives — either the
		// bleve-backed half (vault_read / plain-word search), or, for the two
		// capabilities where that would be misleading, the specific narrower
		// consequence the spec spells out.
		switch c {
		case CapabilityIntegrityCheck:
			if !strings.Contains(msg, "wikilink and orphan checks still run") {
				t.Errorf("%s: refusal must say which checks still run: %s", c, msg)
			}
		case CapabilityRecordTypeDeclaration:
			if !strings.Contains(msg, "never enforced") {
				t.Errorf("%s: refusal must say why declaring anyway is worse than "+
					"refusing: %s", c, msg)
			}
		default:
			if !strings.Contains(msg, "vault_read still work") {
				t.Errorf("%s: refusal must name what still works: %s", c, msg)
			}
		}
	}
}

// TestPropertyIndexRefusal_ExactSpecWording pins the three messages the
// implementing spec writes out verbatim. Paraphrasing them here would put the
// code and the spec quietly out of step, which is the failure mode ADR-068 spent
// eight revisions on.
func TestPropertyIndexRefusal_ExactSpecWording(t *testing.T) {
	cases := []struct {
		capability PropertyIndexCapability
		want       string
		specLine   string
	}{
		{
			capability: CapabilityTypedFilter,
			want: "typed filters are unavailable on linux/mipsle: this build has no properties index. " +
				"Plain-word search and vault_read still work",
			specLine: "vault-records-spec-2026-08-25.md:1377",
		},
		{
			capability: CapabilityIntegrityCheck,
			want: "typed integrity checks are unavailable on linux/mipsle: this build has no properties index. " +
				"Duplicate identifiers, relation targets and orphan rows cannot be checked here; " +
				"wikilink and orphan checks still run",
			specLine: "vault-records-spec-2026-08-25.md:1293",
		},
		{
			capability: CapabilityRecordTypeDeclaration,
			want: "record types cannot be declared on linux/mipsle: this build has no properties index. " +
				"The schema file would be written and never enforced",
			specLine: "vault-records-spec-2026-08-25.md:1622",
		},
	}
	for _, tc := range cases {
		err := &PropertyIndexUnavailableError{Capability: tc.capability, Platform: "linux/mipsle"}
		if got := err.Error(); got != tc.want {
			t.Errorf("%s: refusal drifted from %s\n got: %s\nwant: %s",
				tc.capability, tc.specLine, got, tc.want)
		}
	}
}

// TestPropertyIndexRefusal_UnwrapsToSentinel lets callers branch on the class
// without matching message text.
func TestPropertyIndexRefusal_UnwrapsToSentinel(t *testing.T) {
	var err error = &PropertyIndexUnavailableError{Capability: CapabilityGrouping}
	if !errors.Is(err, ErrPropertyIndexUnavailable) {
		t.Fatal("a platform refusal does not match ErrPropertyIndexUnavailable")
	}
	var typed *PropertyIndexUnavailableError
	if !errors.As(err, &typed) {
		t.Fatal("a platform refusal does not unwrap to *PropertyIndexUnavailableError")
	}
	if typed.Capability != CapabilityGrouping {
		t.Errorf("capability lost through errors.As: got %q", typed.Capability)
	}
}

// TestPropertyIndexRefusal_UnknownCapabilityStillRefusesByName covers the
// fallback branch: an unregistered capability must still produce a refusal that
// names both itself and the platform. Degrading to silence here would reopen the
// empty-result hole through a typo.
func TestPropertyIndexRefusal_UnknownCapabilityStillRefusesByName(t *testing.T) {
	err := &PropertyIndexUnavailableError{Capability: "made_up_thing", Platform: "netbsd/amd64"}
	msg := err.Error()
	if !strings.Contains(msg, "made_up_thing") || !strings.Contains(msg, "netbsd/amd64") {
		t.Errorf("unknown-capability refusal must name the capability and the platform: %s", msg)
	}

	// And an empty capability must not render as an empty subject.
	blank := (&PropertyIndexUnavailableError{Platform: "netbsd/amd64"}).Error()
	if !strings.HasPrefix(blank, "this record-layer operation is unavailable") {
		t.Errorf("blank-capability refusal has no subject: %s", blank)
	}
}

// TestPropertyIndexRefusal_DefaultsPlatformToThisHost — a refusal constructed
// without an explicit platform still names one, so no code path can produce
// "unavailable on : ...".
func TestPropertyIndexRefusal_DefaultsPlatformToThisHost(t *testing.T) {
	want := runtime.GOOS + "/" + runtime.GOARCH
	if got := PropertyIndexPlatform(); got != want {
		t.Fatalf("PropertyIndexPlatform() = %q, want %q", got, want)
	}
	msg := (&PropertyIndexUnavailableError{Capability: CapabilityTypedFilter}).Error()
	if !strings.Contains(msg, want) {
		t.Errorf("refusal with no explicit platform does not name this host %q: %s", want, msg)
	}
}

// TestRequirePropertyIndex_AgreesWithTheBuildConstant ties the runtime behaviour
// to the compile-time constant on whichever half is being built, so neither can
// be changed without the other.
func TestRequirePropertyIndex_AgreesWithTheBuildConstant(t *testing.T) {
	for _, c := range PropertyIndexCapabilities {
		err := RequirePropertyIndex(c)
		if PropertyIndexAvailable {
			if err != nil {
				t.Errorf("%s: PropertyIndexAvailable is true but RequirePropertyIndex "+
					"refused: %v", c, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: PropertyIndexAvailable is false but RequirePropertyIndex "+
				"returned nil — this is the empty-result hole FR-020h closes", c)
			continue
		}
		if !errors.Is(err, ErrPropertyIndexUnavailable) {
			t.Errorf("%s: refusal is not an ErrPropertyIndexUnavailable: %v", c, err)
		}
	}
}
