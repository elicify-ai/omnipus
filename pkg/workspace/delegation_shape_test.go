// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package workspace

import "testing"

// shapeCase is one ValidateShape expectation. wantErr "" means the edge must be
// ACCEPTED; any other value is the exact error message the edge must be
// rejected with. Exact-message matching is deliberate: ValidateShape returns
// inline errors.New/fmt.Errorf values (no sentinels), so the message is the
// only way to prove WHICH invariant fired — a bare err != nil assertion would
// pass even if a different check rejected the edge.
type shapeCase struct {
	name    string
	edge    DelegationEdge
	wantErr string
}

// runShapeCases asserts every case against DelegationEdge.ValidateShape.
func runShapeCases(t *testing.T, cases []shapeCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.edge.ValidateShape()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateShape(%+v) = %q, want accepted (nil error)", tc.edge, err.Error())
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateShape(%+v) = nil error, want %q", tc.edge, tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("ValidateShape(%+v) error = %q, want exactly %q", tc.edge, err.Error(), tc.wantErr)
			}
		})
	}
}

// TestDelegationEdge_ValidateShape_EndpointsNonEmpty proves invariant 1: both
// endpoints must be non-empty AFTER TRIMMING — whitespace-only is empty. Either
// endpoint being empty yields the same message (the check is a single OR).
func TestDelegationEdge_ValidateShape_EndpointsNonEmpty(t *testing.T) {
	const emptyErr = "delegation edge from_agent and to_agent must not be empty"
	runShapeCases(t, []shapeCase{
		{name: "reject: empty from_agent", edge: DelegationEdge{FromAgent: "", ToAgent: "jim"}, wantErr: emptyErr},
		{name: "reject: empty to_agent", edge: DelegationEdge{FromAgent: "mia", ToAgent: ""}, wantErr: emptyErr},
		{name: "reject: both endpoints empty", edge: DelegationEdge{}, wantErr: emptyErr},
		{name: "reject: whitespace-only from_agent", edge: DelegationEdge{FromAgent: " \t", ToAgent: "jim"}, wantErr: emptyErr},
		{name: "reject: whitespace-only to_agent", edge: DelegationEdge{FromAgent: "mia", ToAgent: "   "}, wantErr: emptyErr},
		{name: "accept: both endpoints non-empty", edge: DelegationEdge{FromAgent: "mia", ToAgent: "jim"}},
	})
}

// TestDelegationEdge_ValidateShape_NoSelfEdge proves invariant 2: an edge from
// an agent to itself is rejected. The comparison runs on the TRIMMED values (so
// "mia" → " mia " is still a self-edge) and the message interpolates the
// trimmed agent name.
func TestDelegationEdge_ValidateShape_NoSelfEdge(t *testing.T) {
	selfEdgeErr := func(agent string) string {
		return "delegation edge cannot be a self-edge (from_agent == to_agent: " + agent + ")"
	}
	runShapeCases(t, []shapeCase{
		{name: "reject: identical endpoints", edge: DelegationEdge{FromAgent: "mia", ToAgent: "mia"}, wantErr: selfEdgeErr("mia")},
		{
			name:    "reject: self-edge only visible after trimming",
			edge:    DelegationEdge{FromAgent: "mia", ToAgent: " mia "},
			wantErr: selfEdgeErr("mia"),
		},
		{name: "accept: distinct endpoints", edge: DelegationEdge{FromAgent: "mia", ToAgent: "jim"}},
	})
}

// TestDelegationEdge_ValidateShape_ModesClosedSet proves invariant 3: when
// Modes is non-empty, EVERY entry must be one of the two closed-set values
// direct|task. Legacy "await"/"background" strings are INVALID here on purpose:
// the legacy→direct migration lives only in UnmarshalJSON, so a hand-built
// struct (not decoded from JSON) carrying a legacy mode must be rejected by
// shape validation. Nil and empty Modes are both accepted (all modes allowed).
func TestDelegationEdge_ValidateShape_ModesClosedSet(t *testing.T) {
	modeErr := func(mode string) string {
		return "delegation edge mode " + mode + " is invalid (valid: direct, task)"
	}
	base := func(modes []DelegationMode) DelegationEdge {
		return DelegationEdge{FromAgent: "mia", ToAgent: "jim", Modes: modes}
	}
	runShapeCases(t, []shapeCase{
		{name: "reject: unknown mode", edge: base([]DelegationMode{"banana"}), wantErr: modeErr("banana")},
		{name: "reject: legacy mode await", edge: base([]DelegationMode{"await"}), wantErr: modeErr("await")},
		{name: "reject: legacy mode background", edge: base([]DelegationMode{"background"}), wantErr: modeErr("background")},
		// The wantErr has two consecutive spaces: the empty mode name interpolated.
		{name: "reject: empty-string mode", edge: base([]DelegationMode{""}), wantErr: modeErr("")},
		{
			name:    "reject: invalid entry after a valid one (every entry is scanned)",
			edge:    base([]DelegationMode{ModeDirect, "banana"}),
			wantErr: modeErr("banana"),
		},
		{
			name:    "reject: first invalid entry in slice order is the one named",
			edge:    base([]DelegationMode{"banana", ModeTask}),
			wantErr: modeErr("banana"),
		},
		{name: "accept: nil modes (all modes allowed)", edge: base(nil)},
		{name: "accept: empty modes slice", edge: base([]DelegationMode{})},
		{name: "accept: direct only", edge: base([]DelegationMode{ModeDirect})},
		{name: "accept: task only", edge: base([]DelegationMode{ModeTask})},
		{name: "accept: direct and task", edge: base([]DelegationMode{ModeDirect, ModeTask})},
	})
}

// TestDelegationEdge_ValidateShape_DepthNonNegative proves invariant 4: a
// non-nil Depth must be >= 0. Nil means "inherit the cap" and 0 is a legal
// value (per the DEPTH INVARIANT it grants NO onward delegation — strictest
// bound, still a well-formed edge). ValidateShape deliberately does NOT check
// an upper ceiling: that is Validate's job, against the live config.
func TestDelegationEdge_ValidateShape_DepthNonNegative(t *testing.T) {
	const depthErr = "delegation edge depth must be >= 0"
	base := func(depth *int) DelegationEdge {
		return DelegationEdge{FromAgent: "mia", ToAgent: "jim", Depth: depth}
	}
	runShapeCases(t, []shapeCase{
		{name: "reject: depth -1", edge: base(new(-1)), wantErr: depthErr},
		{name: "reject: depth -100", edge: base(new(-100)), wantErr: depthErr},
		{name: "accept: nil depth (inherit the cap)", edge: base(nil)},
		{name: "accept: depth 0 (grants no onward delegation, still well-formed)", edge: base(new(0))},
		{name: "accept: depth 1", edge: base(new(1))},
		{name: "accept: depth 99", edge: base(new(99))},
	})
}

// TestDelegationEdge_ValidateShape_ZeroValue: the zero edge violates exactly
// one invariant — both endpoints empty (Modes and Depth are nil, both legal) —
// so it must be rejected by the first check with the empty-endpoints message.
func TestDelegationEdge_ValidateShape_ZeroValue(t *testing.T) {
	err := DelegationEdge{}.ValidateShape()
	want := "delegation edge from_agent and to_agent must not be empty"
	if err == nil || err.Error() != want {
		t.Fatalf("zero-value edge: ValidateShape() = %v, want %q", err, want)
	}
}

// TestDelegationEdge_ValidateShape_FirstViolationWins pins the observable
// check ORDER for edges violating several invariants at once: empty endpoints
// first, then self-edge, then modes, then depth. The returned message is the
// only witness to which check fired, so this ordering is behavior the store's
// WARN logs (load) and refusal error (save) both surface.
func TestDelegationEdge_ValidateShape_FirstViolationWins(t *testing.T) {
	runShapeCases(t, []shapeCase{
		{
			name:    "empty endpoints beat self-edge, mode and depth (both-empty is also a self-edge)",
			edge:    DelegationEdge{FromAgent: "", ToAgent: "", Modes: []DelegationMode{"banana"}, Depth: new(-1)},
			wantErr: "delegation edge from_agent and to_agent must not be empty",
		},
		{
			name:    "self-edge beats invalid mode",
			edge:    DelegationEdge{FromAgent: "mia", ToAgent: "mia", Modes: []DelegationMode{"banana"}},
			wantErr: "delegation edge cannot be a self-edge (from_agent == to_agent: mia)",
		},
		{
			name:    "self-edge beats negative depth",
			edge:    DelegationEdge{FromAgent: "mia", ToAgent: "mia", Depth: new(-1)},
			wantErr: "delegation edge cannot be a self-edge (from_agent == to_agent: mia)",
		},
		{
			name:    "invalid mode beats negative depth",
			edge:    DelegationEdge{FromAgent: "mia", ToAgent: "jim", Modes: []DelegationMode{"banana"}, Depth: new(-1)},
			wantErr: "delegation edge mode banana is invalid (valid: direct, task)",
		},
		{
			name: "every field populated and valid is accepted",
			edge: DelegationEdge{FromAgent: "mia", ToAgent: "jim", Modes: []DelegationMode{ModeDirect, ModeTask}, Depth: new(0)},
		},
	})
}
