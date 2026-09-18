// Omnipus — the plan tools' preview lines (ADR-071 amendment 2026-09-14)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-071 amendment 2026-09-14 (founder decision): create_plan and
// execute_plan moved from the search-only tier to the previewed tier, so every
// agent whose policy allows them sees one line each in the "More tools" block.
// These tests pin the two properties that decision depends on:
//
//  1. A preview line is not a policy bypass. The agent loop hands
//     BuildCompressedManifest the output of FilterToolsByPolicy
//     (pkg/agent/loop.go -> buildToolManifestNote), so an agent denied a plan
//     tool must never see its line — at either policy layer.
//  2. The line the model sees says WHEN to reach for the tool, in full, rather
//     than a truncated fragment of what it does.

package tools

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestManifest_PlanToolUpfrontDefinitionsFollowToolPolicy(t *testing.T) {
	catalog := []Tool{
		&PlanCreateTool{},
		&PlanExecuteTool{},
		// Control: another upfront tool no case below ever denies.
		&fakeManifestTool{name: "create_task", desc: "Create a task.", cat: CategoryTasks},
	}
	ceilingAllow := map[string]config.ToolPolicy{
		"create_plan":  config.ToolPolicyAllow,
		"execute_plan": config.ToolPolicyAllow,
		"create_task":  config.ToolPolicyAllow,
	}

	cases := []struct {
		name        string
		global      map[string]config.ToolPolicy
		agent       map[string]config.ToolPolicy
		wantCreate  bool
		wantExecute bool
	}{
		{
			name:       "ceiling allows, no per-agent entry",
			global:     ceilingAllow,
			wantCreate: true, wantExecute: true,
		},
		{
			name:   "per-agent deny on both",
			global: ceilingAllow,
			agent: map[string]config.ToolPolicy{
				"create_plan": config.ToolPolicyDeny, "execute_plan": config.ToolPolicyDeny,
			},
		},
		{
			name: "global deny beats a per-agent allow",
			global: map[string]config.ToolPolicy{
				"create_plan": config.ToolPolicyDeny, "execute_plan": config.ToolPolicyDeny,
				"create_task": config.ToolPolicyAllow,
			},
			agent: map[string]config.ToolPolicy{
				"create_plan": config.ToolPolicyAllow, "execute_plan": config.ToolPolicyAllow,
			},
		},
		{
			// "ask" is not a denial: the tool stays callable behind an approval
			// prompt, which is the seeded posture for every core agent except Jim.
			name:   "per-agent ask keeps both lines",
			global: ceilingAllow,
			agent: map[string]config.ToolPolicy{
				"create_plan": config.ToolPolicyAsk, "execute_plan": config.ToolPolicyAsk,
			},
			wantCreate: true, wantExecute: true,
		},
		{
			name:        "deny on create_plan alone leaves execute_plan",
			global:      ceilingAllow,
			agent:       map[string]config.ToolPolicy{"create_plan": config.ToolPolicyDeny},
			wantExecute: true,
		},
	}

	// "core" passes the ScopeCore gate; "custom" fails it and falls through to
	// the policy merge, which is the path a user-defined agent takes.
	for _, agentType := range []string{"core", "custom"} {
		for _, tc := range cases {
			t.Run(agentType+"/"+tc.name, func(t *testing.T) {
				filtered, verdicts := FilterToolsByPolicy(catalog, agentType,
					&ToolPolicyCfg{Policies: tc.agent, GlobalPolicies: tc.global})
				kept := make(map[string]bool, len(filtered))
				for _, tool := range filtered {
					kept[tool.Name()] = true
				}
				if !kept["create_task"] {
					t.Fatalf("control: create_task must remain offered; verdicts %v", verdicts)
				}
				if got := kept["create_plan"]; got != tc.wantCreate {
					t.Errorf("create_plan offered = %v, want %v (verdicts %v)", got, tc.wantCreate, verdicts)
				}
				if got := kept["execute_plan"]; got != tc.wantExecute {
					t.Errorf("execute_plan offered = %v, want %v (verdicts %v)", got, tc.wantExecute, verdicts)
				}
			})
		}
	}
}

func TestManifest_PlanToolUpfrontDescriptionsSayWhenToUse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tool    Tool
		mustSay []string
	}{
		{
			name: "create_plan",
			tool: &PlanCreateTool{},
			mustSay: []string{
				"instead of several parallel delegate calls",
				"when a goal has two or more independent parts",
				"run in parallel",
			},
		},
		{
			name:    "execute_plan",
			tool:    &PlanExecuteTool{},
			mustSay: []string{"draft plan", "once its member tasks are attached"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ToolManifestTier(tc.name); got != ManifestFull {
				t.Fatalf("ToolManifestTier(%q) = %v, want ManifestFull", tc.name, got)
			}
			description := tc.tool.Description()
			for _, phrase := range tc.mustSay {
				if !strings.Contains(description, phrase) {
					t.Errorf("%s upfront description must say %q so the model knows when to use it; got %q",
						tc.name, phrase, description)
				}
			}
		})
	}
}
