// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// file: pkg/agent/reasoning_effort_resolve_test.go
// Copyright (c) 2026 Omnipus contributors

// reasoning_effort_resolve_test.go is the RED contract for C5's resolution
// function `resolveReasoningEffort` (spec
// docs/internal/specs/thinking-reasoning-spec.md — Section 1 C5 resolution
// order steps (3)-(5); D9, D10, T1; Section 16 test 32; dataset "effort value
// handling" rows 1-10; BDD scenarios at lines ~1093-1118). WP-G's RED author
// pins the contract; backend-lead implements to it and reports deviations
// rather than adapting silently.
//
// THE CONTRACT THIS FILE PINS (backend-lead implements to this):
//
//	Signature (flat values only, so WP-H can add its two higher-precedence
//	override parameters — the web per-message effort and the non-web session
//	effort — ahead of these without rewriting the walk):
//
//	  func resolveReasoningEffort(
//	      servingCandidateEffort string, // step 3: the serving fallback candidate's OWN effort; "" when the primary serves
//	      primaryInherited     bool,    // step 4 gate: the agent's primary is the instance default model (no own model)
//	      defaultModelEffort   string,  // step 4: config.DefaultModel.ReasoningEffort
//	      agentEffort          string,  // step 5: the agent's own configured effort
//	  ) (value string, ok bool)
//
//	Semantics — walk 3 -> 4 -> 5; FIRST non-empty, non-"default" value wins;
//	none → ("", false). The caller maps ok=false to "set NO llmOpts key"
//	(D9: absence is the send-nothing signal — never an empty-string value).
//	"default" (exact, lowercase — the spec's literal token) means unset.
//	Effort is a plain string (T1): the function SELECTS, it never validates,
//	normalizes or trims — any other string passes through byte-exactly.
//
//	TWO INTERPRETATION FLAGS (spec-ambiguous points, pinned here so a ruling
//	is a one-row change; flagged to squad-lead in the WP-G RED report):
//
//	  FLAG 1 (walk vs exclusive branches): the dispatch's "if none of these
//	  produce a non-empty, non-"default" value, resolve to nothing" is pinned
//	  as a linear walk — a serving candidate with NO own effort falls through
//	  to steps 4/5 (DefaultModel, then the agent's own). The alternative
//	  exclusive reading (a serving fallback consults ONLY its own effort;
//	  empty → nothing) would change exactly the two FLAG 1 rows below.
//
//	  FLAG 2 (sentinel exactness): "default" is matched exactly; "Default"
//	  (any other casing) is NOT the sentinel and passes through as a plain
//	  string. The spec writes the sentinel lowercase ("\"default\"" = send
//	  nothing). If the squad rules case-insensitive, the two FLAG 2 rows
//	  change.
package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// resolveReasoningEffortCase is one row of the resolution table. Every
// "want" derives from the spec sources named in wantSource — never from any
// implementation output.
type resolveReasoningEffortCase struct {
	name string
	// inputs, in walk order
	servingCandidateEffort string
	primaryInherited       bool
	defaultModelEffort     string
	agentEffort            string
	// expected
	wantValue        string
	wantOK           bool
	wantSource       string // the spec line the expectation derives from
	interpretationFL string // non-empty = an interpretation flag this row pins
}

func TestResolveReasoningEffort_ResolutionWalk(t *testing.T) {
	t.Parallel()

	cases := []resolveReasoningEffortCase{
		{
			name:                   "fallback candidate's own effort wins over everything inherited",
			servingCandidateEffort: "low", primaryInherited: true,
			defaultModelEffort: "medium", agentEffort: "high",
			wantValue: "low", wantOK: true,
			wantSource: "C5 step (3); BDD 'A serving fallback candidate uses its own effort' (primary high, candidate low → request carries low); D10",
		},
		{
			name:                   "serving candidate with no own effort falls through to the inherited default model",
			servingCandidateEffort: "", primaryInherited: true,
			defaultModelEffort: "medium", agentEffort: "high",
			wantValue: "medium", wantOK: true,
			wantSource:       "C5 steps (3)-(5) walk; dispatch 'if none of these produce a non-empty, non-default value'",
			interpretationFL: "FLAG 1",
		},
		{
			name:                   "serving candidate at the default sentinel falls through to the inherited default model",
			servingCandidateEffort: "default", primaryInherited: true,
			defaultModelEffort: "medium", agentEffort: "",
			wantValue: "medium", wantOK: true,
			wantSource:       "'default' means send nothing → the surface is unset; C5 walk",
			interpretationFL: "FLAG 1",
		},
		{
			name:                   "inherited primary consults DefaultModel.reasoning_effort",
			servingCandidateEffort: "", primaryInherited: true,
			defaultModelEffort: "medium", agentEffort: "",
			wantValue: "medium", wantOK: true,
			wantSource: "C5 step (4); BDD 'An agent riding the instance default model uses the default model's effort'; dataset row 9",
		},
		{
			name:                   "DefaultModel effort outranks the agent's own when the primary is inherited",
			servingCandidateEffort: "", primaryInherited: true,
			defaultModelEffort: "medium", agentEffort: "high",
			wantValue: "medium", wantOK: true,
			wantSource: "C5 order: step (4) precedes step (5)",
		},
		{
			name:                   "inherited primary with unset default model falls through to the agent's own effort",
			servingCandidateEffort: "", primaryInherited: true,
			defaultModelEffort: "", agentEffort: "high",
			wantValue: "high", wantOK: true,
			wantSource: "C5 steps (4)->(5): empty at step 4 → the agent's own applies",
		},
		{
			name:                   "non-inherited primary uses the agent's own effort",
			servingCandidateEffort: "", primaryInherited: false,
			defaultModelEffort: "", agentEffort: "high",
			wantValue: "high", wantOK: true,
			wantSource: "C5 step (5); dataset row 2 ('high' → request carries high)",
		},
		{
			name:                   "non-inherited primary ignores the default model's effort",
			servingCandidateEffort: "", primaryInherited: false,
			defaultModelEffort: "medium", agentEffort: "",
			wantValue: "", wantOK: false,
			wantSource: "C5 step (4) is gated on the primary being INHERITED — a non-inherited primary never consults DefaultModel",
		},
		{
			name:                   "no effort anywhere resolves to nothing",
			servingCandidateEffort: "", primaryInherited: false,
			defaultModelEffort: "", agentEffort: "",
			wantValue: "", wantOK: false,
			wantSource: "C5 step (5-else); dataset rows 1/5 (unset → request carries nothing); D9",
		},
		{
			name:                   "empty string is unset, never a value",
			servingCandidateEffort: "", primaryInherited: false,
			defaultModelEffort: "", agentEffort: "",
			wantValue: "", wantOK: false,
			wantSource: "dataset row 6 ('' → treated as unset, sends nothing)",
		},
		{
			name:                   "explicit default sentinel resolves to nothing",
			servingCandidateEffort: "", primaryInherited: false,
			defaultModelEffort: "", agentEffort: "default",
			wantValue: "", wantOK: false,
			wantSource: "dataset row 7 (explicit 'default' → treated as unset, sends nothing)",
		},
		{
			name:                   "default sentinel on the default model falls through to the agent's own effort",
			servingCandidateEffort: "", primaryInherited: true,
			defaultModelEffort: "default", agentEffort: "high",
			wantValue: "high", wantOK: true,
			wantSource: "'default' = unset at that surface → the walk continues",
		},
		{
			name:                   "default sentinel everywhere resolves to nothing",
			servingCandidateEffort: "default", primaryInherited: true,
			defaultModelEffort: "default", agentEffort: "default",
			wantValue: "", wantOK: false,
			wantSource: "'default' = unset at every surface → C5 (5-else): nothing sent",
		},
		{
			name:                   "sentinel is exact lowercase — any other casing is a plain value",
			servingCandidateEffort: "", primaryInherited: false,
			defaultModelEffort: "", agentEffort: "Default",
			wantValue: "Default", wantOK: true,
			wantSource:       "the spec's sentinel is the literal lowercase \"default\"; T1: plain string, unvalidated",
			interpretationFL: "FLAG 2",
		},
		{
			name:                   "whitespace is a plain value, never trimmed (T1: select, never validate)",
			servingCandidateEffort: " high ", primaryInherited: false,
			defaultModelEffort: "", agentEffort: "",
			wantValue: " high ", wantOK: true,
			wantSource: "T1: reasoning_effort is a plain string, not validated — the resolver selects, never normalizes",
		},
		{
			name:                   "single-variant value passes through unchanged",
			servingCandidateEffort: "", primaryInherited: false,
			defaultModelEffort: "", agentEffort: "minimal",
			wantValue: "minimal", wantOK: true,
			wantSource: "dataset row 4 ('minimal' → request carries minimal)",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotValue, gotOK := resolveReasoningEffort(
				tc.servingCandidateEffort,
				tc.primaryInherited,
				tc.defaultModelEffort,
				tc.agentEffort,
			)

			assert.Equal(t, tc.wantOK, gotOK,
				"resolution ok-flag mismatch (source: %s)", tc.wantSource)
			assert.Equal(t, tc.wantValue, gotValue,
				"resolved value mismatch (source: %s)", tc.wantSource)
			if !tc.wantOK {
				assert.Equal(t, "", gotValue,
					"D9: a nothing-resolution must return the zero string — the caller maps ok=false to setting NO llmOpts key at all, so a non-empty value here would leak")
			}
		})
	}
}
