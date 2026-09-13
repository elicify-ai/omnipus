// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package fspolicy — ADR-084 JUDGE-FR-060 / FR-060b: the read-confinement
// posture's polarity and its blast radius.
//
// The requirement this file exists for is the FR's "what does NOT change"
// clause, which is the half an implementer is most likely to skip:
//
//	"every non-System agent's FSOpRead reach is exactly as ADR-063 FR-2.2
//	 left it — open outside the secret set, independent of Scope. This MUST
//	 be stated in the code and asserted by a named test, because the change
//	 lives in a function every filesystem tool for every agent goes through."
//
// This file carries NO build tag on purpose. It is a security posture test
// for a function on every filesystem tool's path; excluding it from a build
// configuration would mean the posture is unverified in that configuration.
package fspolicy

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// readConfinedFixture returns a realpath'd ($OMNIPUS_HOME, agentHome) pair.
// Both directories are materialised because EffectiveFSPolicy realpaths them
// and fails closed if it cannot.
func readConfinedFixture(t *testing.T) (home, agentHome string) {
	t.Helper()
	raw := t.TempDir()
	ah := filepath.Join(raw, "agents", "mia")
	if err := os.MkdirAll(ah, 0o755); err != nil {
		t.Fatalf("mkdir agentHome: %v", err)
	}
	resolve := func(p string) string {
		t.Helper()
		r, err := filepath.EvalSymlinks(p)
		if err != nil {
			t.Fatalf("EvalSymlinks(%q): %v", p, err)
		}
		return r
	}
	return resolve(raw), resolve(ah)
}

// TestEffectiveFSPolicy_ReadConfinedOnlyForSystemAgents is JUDGE-FR-060b's
// polarity oracle.
//
// pkg/fspolicy has no System-Agent concept at all and deliberately gains
// none: it is a stdlib-only leaf (see the package comment) and cannot import
// the packages that know what a System Agent is. "Only for System agents" is
// therefore expressed here as the property that actually makes it true —
// **the posture arrives ONLY when a caller names it**. Every other caller in
// the product, present and future, gets the unconfined policy it got before
// ADR-084, whatever agent id it passes.
//
// The three assertions, and what each would catch:
//
//   - The seven-parameter EffectiveFSPolicy never returns a confined policy,
//     for any agent id including ones that look like System Agents. Catches
//     an implementation that tried to infer the posture from the agent id
//     string — a guess that would confine an operator's agent named
//     "omnipus-judge" and would miss a renamed real one.
//   - readConfined=false is byte-identical to EffectiveFSPolicy. Catches a
//     delegating wrapper that quietly changed some other field.
//   - readConfined=true differs in EXACTLY one field. Catches the confined
//     posture bleeding into Scope, CarveOuts or AllowedRoots — which would
//     change enforcement for writes and exec, neither of which FR-060 touches.
func TestEffectiveFSPolicy_ReadConfinedOnlyForSystemAgents(t *testing.T) {
	home, agentHome := readConfinedFixture(t)
	ctx := context.Background()

	// Agent ids chosen adversarially: two that a name-sniffing
	// implementation would most plausibly treat as "System".
	agentIDs := []string{"", "mia", "omnipus-judge", "judge", "system", "PlanSupervisor"}

	t.Run("the default constructor is never confined, for any agent id or scope", func(t *testing.T) {
		for _, agentID := range agentIDs {
			for _, restrict := range []bool{false, true} {
				got, err := EffectiveFSPolicy(ctx, agentHome, "", restrict, home, agentID, "")
				if err != nil {
					t.Fatalf("EffectiveFSPolicy(agentID=%q, restrict=%v): %v", agentID, restrict, err)
				}
				if got.ReadConfined {
					t.Fatalf(
						"EffectiveFSPolicy(agentID=%q, restrict=%v).ReadConfined = true; "+
							"JUDGE-FR-060b requires unset to mean NOT confined and forbids inferring the posture from an agent id",
						agentID, restrict)
				}
			}
		}
	})

	t.Run("readConfined=false is byte-identical to the default constructor", func(t *testing.T) {
		for _, restrict := range []bool{false, true} {
			base, err := EffectiveFSPolicy(ctx, agentHome, "", restrict, home, "mia", "w1")
			if err != nil {
				t.Fatalf("EffectiveFSPolicy: %v", err)
			}
			explicit, err := EffectiveFSPolicyWithReadConfined(ctx, agentHome, "", restrict, home, "mia", "w1", false)
			if err != nil {
				t.Fatalf("EffectiveFSPolicyWithReadConfined: %v", err)
			}
			if !reflect.DeepEqual(base, explicit) {
				t.Fatalf("restrict=%v: an explicit readConfined=false policy differs from the default one\n base = %+v\n explicit = %+v",
					restrict, base, explicit)
			}
		}
	})

	t.Run("readConfined=true changes ReadConfined and nothing else", func(t *testing.T) {
		for _, restrict := range []bool{false, true} {
			base, err := EffectiveFSPolicy(ctx, agentHome, "", restrict, home, "judge", "w1")
			if err != nil {
				t.Fatalf("EffectiveFSPolicy: %v", err)
			}
			confined, err := EffectiveFSPolicyWithReadConfined(ctx, agentHome, "", restrict, home, "judge", "w1", true)
			if err != nil {
				t.Fatalf("EffectiveFSPolicyWithReadConfined: %v", err)
			}
			if !confined.ReadConfined {
				t.Fatalf("restrict=%v: readConfined=true did not reach the policy", restrict)
			}
			// Normalise the one field that is supposed to differ, then
			// require everything else to be equal. Written this way rather
			// than field-by-field so a field ADDED to FSPolicy later is
			// covered on the day it is added.
			normalised := confined
			normalised.ReadConfined = false
			if !reflect.DeepEqual(base, normalised) {
				t.Fatalf("restrict=%v: the confined policy differs from the unconfined one in more than ReadConfined\n base = %+v\n confined = %+v",
					restrict, base, confined)
			}
		}
	})
}

// TestFSPolicy_ReadConfinedZeroValueIsUnconfined pins the polarity at the
// type level, independently of any constructor.
//
// A hand-built FSPolicy{} literal — the shape several resolver-level unit
// tests use, and the shape any future caller will reach for first — must be
// UNCONFINED. If the field's meaning were ever inverted (say, renamed to
// ReadOpen with the same bool), every such literal would silently flip
// posture and this test is what would notice.
func TestFSPolicy_ReadConfinedZeroValueIsUnconfined(t *testing.T) {
	var zero FSPolicy
	if zero.ReadConfined {
		t.Fatal("the zero-value FSPolicy is read-confined; JUDGE-FR-060b requires the zero value to mean NOT confined")
	}
}

// TestFSPolicy_ReadConfinedValidates guards the one invariant a new field on
// this struct could quietly break: Validate must keep accepting a policy
// regardless of the posture, because ResolvePath calls Validate BEFORE it
// reaches any access decision. A Validate that rejected confined policies
// would turn every confined read into ErrPathInvalid — which looks like a
// malformed path to the caller and hides the real refusal.
func TestFSPolicy_ReadConfinedValidates(t *testing.T) {
	home, agentHome := readConfinedFixture(t)
	for _, confined := range []bool{false, true} {
		p, err := EffectiveFSPolicyWithReadConfined(
			context.Background(), agentHome, "", true, home, "judge", "", confined,
		)
		if err != nil {
			t.Fatalf("confined=%v: EffectiveFSPolicyWithReadConfined: %v", confined, err)
		}
		if valErr := p.Validate(); valErr != nil {
			t.Fatalf("confined=%v: Validate() = %v, want nil", confined, valErr)
		}
	}
}
