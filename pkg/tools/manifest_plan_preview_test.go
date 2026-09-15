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

// manifestBullet is the prefix BuildCompressedManifest writes for one
// previewed tool: "  - <name> — ".
func manifestBullet(name string) string { return "  - " + name + " — " }

// manifestLineFor returns the rendered description text of name's bullet in
// note, or "" when note has no bullet for name.
func manifestLineFor(note, name string) string {
	for _, line := range strings.Split(note, "\n") {
		if rest, ok := strings.CutPrefix(line, manifestBullet(name)); ok {
			return rest
		}
	}
	return ""
}

func TestManifest_PlanToolPreviewFollowsToolPolicy(t *testing.T) {
	if previewAllLazy.Load() {
		t.Fatal("precondition: the PreviewAllLazy revert is on, so every lazy tool previews and " +
			"this test could not tell the previewed tier apart from the revert")
	}

	catalog := []Tool{
		&PlanCreateTool{},
		&PlanExecuteTool{},
		// Control: a previewed tool no case below ever denies. Without it an
		// "absent" assertion would also pass on a block that rendered nothing.
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
				note := BuildCompressedManifest(filtered, nil)

				if manifestLineFor(note, "create_task") == "" {
					t.Fatalf("control: create_task must render in every case; block was:\n%s", note)
				}
				if got := manifestLineFor(note, "create_plan") != ""; got != tc.wantCreate {
					t.Errorf("create_plan preview line present = %v, want %v (verdicts %v); block was:\n%s",
						got, tc.wantCreate, verdicts, note)
				}
				if got := manifestLineFor(note, "execute_plan") != ""; got != tc.wantExecute {
					t.Errorf("execute_plan preview line present = %v, want %v (verdicts %v); block was:\n%s",
						got, tc.wantExecute, verdicts, note)
				}
			})
		}
	}
}

func TestManifest_PlanToolPreviewLinesSayWhenToUse(t *testing.T) {
	note := BuildCompressedManifest([]Tool{&PlanCreateTool{}, &PlanExecuteTool{}}, nil)

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
			line := manifestLineFor(note, tc.name)
			if line == "" {
				t.Fatalf("%s has no preview line; block was:\n%s", tc.name, note)
			}
			first, _, _ := strings.Cut(tc.tool.Description(), "\n")
			if want := strings.TrimSpace(first); line != want {
				t.Errorf("%s preview line is not its description's whole first line — it was cut or "+
					"the description lost its line break:\n got: %q\nwant: %q", tc.name, line, want)
			}
			for _, phrase := range tc.mustSay {
				if !strings.Contains(line, phrase) {
					t.Errorf("%s preview line must say %q so the model knows when to use it; got %q",
						tc.name, phrase, line)
				}
			}
		})
	}
}
