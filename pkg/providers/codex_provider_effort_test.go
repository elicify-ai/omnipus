// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/thinking-reasoning-spec.md — D32 adapter coverage
// matrix (ChatGPT subscription / Codex API row) and Section 16 test 42.
//
// The Codex API adapter maps llmOpts["reasoning_effort"] (WP-G's C5 output,
// pkg/agent/loop_run_turn.go::prepareLLMRequest) onto the OpenAI Responses
// request's reasoning object exactly: a named level sets reasoning.effort to
// that level; the unset token "default", an absent option, an empty string,
// and a stale level no longer routable all leave the WHOLE reasoning request
// object absent (D9 — absence is the send-nothing signal, never an
// empty-object or empty-string placeholder; T1/SC-007 — a stale level
// degrades to the provider default instead of blocking the turn).
//
// Expected RED: buildCodexParams does not read options["reasoning_effort"]
// yet, so every named-level case fails with the reasoning object absent.
package providers

import (
	"encoding/json"
	"testing"

	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

// codexWireMap marshals params and decodes the result, so assertions pin the
// wire bytes the OpenAI Responses API would receive — the same level the
// existing TestBuildCodexParams_ToolImageInCorrelatedOutput asserts at.
func codexWireMap(t *testing.T, params responses.ResponseNewParams) map[string]any {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshaling ResponseNewParams: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("decoding marshaled ResponseNewParams: %v", err)
	}
	return wire
}

// TestBuildCodexParams_ReasoningEffortRequestShape is spec test 42: D32 routes
// a selected level and omits Default exactly.
func TestBuildCodexParams_ReasoningEffortRequestShape(t *testing.T) {
	cases := []struct {
		name       string
		options    map[string]any
		wantEffort string // "" = the whole reasoning object must be absent
	}{
		{
			name:    "absent option leaves the reasoning object absent",
			options: map[string]any{},
		},
		{
			name:       "empty option leaves the reasoning object absent",
			options:    map[string]any{"reasoning_effort": ""},
		},
		{
			name:       "Default token leaves the reasoning object absent",
			options:    map[string]any{"reasoning_effort": "default"},
		},
		{
			name:       "high sets reasoning.effort to high",
			options:    map[string]any{"reasoning_effort": "high"},
			wantEffort: "high",
		},
		{
			// "ultra" is the spec's own stale-level dataset example (Section
			// 16 dataset, row 3): offered levels are low/medium/high, the
			// stored level no longer is. T1: degrade to the provider default
			// — no reasoning object sent — never block the turn.
			name:       "stale level degrades to no reasoning object",
			options:    map[string]any{"reasoning_effort": "ultra"},
			wantEffort: "",
		},
		{
			// The options map is map[string]any; a wrong-typed value must
			// degrade the same way prompt_cache_key's type assertion does —
			// ignored, no panic, nothing sent.
			name:       "non-string option sends no reasoning object and does not panic",
			options:    map[string]any{"reasoning_effort": 7},
			wantEffort: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := buildCodexParams(
				[]Message{{Role: "user", Content: "hi"}},
				nil,
				"gpt-5",
				tc.options,
				false,
			)

			if tc.wantEffort == "" {
				if params.Reasoning != (shared.ReasoningParam{}) {
					t.Fatalf("Reasoning = %+v, want the zero shared.ReasoningParam — D9: the whole object stays absent, never a placeholder", params.Reasoning)
				}
				wire := codexWireMap(t, params)
				if _, present := wire["reasoning"]; present {
					t.Fatalf("wire request carries a reasoning key (%v); spec test 42: Default/absent/stale leaves the reasoning request object absent", wire["reasoning"])
				}
				return
			}

			// Whole-struct equality: the spec maps reasoning.effort=<level>
			// and nothing else — no summary, no generate_summary rides along.
			want := shared.ReasoningParam{Effort: shared.ReasoningEffort(tc.wantEffort)}
			if params.Reasoning != want {
				t.Fatalf("Reasoning = %+v, want %+v (reasoning.effort=%q only)", params.Reasoning, want, tc.wantEffort)
			}
			wire := codexWireMap(t, params)
			got, present := wire["reasoning"]
			if !present {
				t.Fatalf("wire request has no reasoning key; want {\"effort\":%q}", tc.wantEffort)
			}
			gotObj, ok := got.(map[string]any)
			if !ok {
				t.Fatalf("wire reasoning is %T, want a JSON object", got)
			}
			if len(gotObj) != 1 || gotObj["effort"] != tc.wantEffort {
				t.Fatalf("wire reasoning = %v, want exactly {\"effort\":%q}", got, tc.wantEffort)
			}
		})
	}
}
