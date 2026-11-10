// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package coreagent

// prompt_omnipus_test.go — RED test for the Omnipus prompt text (tool-parity
// spec r17 §7.14.3; §13.1 test 45; scenario 21.10).
//
// ORACLE: §7.14.3's exact proposed sentences. The prompt today still carries
// the OLD sentence ("Run the confirmed apply batch in at most one helper per
// workspace") and lacks the routing reconciliation, so every pin below fails
// in RED. Proves presence only; behaviour is UAT row U-21. The marker lint
// ("every tool: marker is a tool Omnipus holds") is §7.14.2 change 5 and is
// covered by the existing catalog lint once the seed lands (test 44).

import (
	"strings"
	"testing"
)

func TestPrompt_OmnipusDelegatesRealWork(t *testing.T) {
	prompt := GetPrompt("omnipus")

	// The standing rules paragraph of §7.14.3 — pinned phrases from the
	// exact proposed text.
	pins := []struct {
		phrase string
		why    string
	}{
		{"Do real work in helpers, not yourself", "the opening rule of §7.14.3"},
		{"Start each helper with tool:delegate, using your own agent id as agent_id",
			"self-delegation is the default mechanism"},
		{"it cannot see this chat", "helpers get the full brief"},
		{"tool:list_jobs shows the helpers still running", "the parallel-helpers visibility rule"},
		{"Send changes to the same workspace or the same agent to one helper",
			"the conflicting-writes rule"},
		{"Answer quick questions and single look-ups yourself", "the quick-question boundary (21.5)"},
		{"If you are a helper yourself (you were started by tool:delegate and cannot delegate further), do the work directly",
			"the helper self-rule"},
		{"Two requests need no workflow skill", "the r5 routing reconciliation is present"},
		{"create a task assigned to yourself with tool:create_task and a trigger",
			"the scheduling route through create_task (U-21)"},
		{"read it back with tool:get_task and report the next run time",
			"the read-back-and-report rule"},
	}
	for _, p := range pins {
		if !strings.Contains(prompt, p.phrase) {
			t.Errorf("omnipus prompt must carry the §7.14.3 sentence (%s); missing: %q", p.why, p.phrase)
		}
	}

	// The replaced no-fit rule: the old wording must be gone and the new
	// wording present.
	if strings.Contains(prompt, "nothing fits, answer the question and call no change tool.") {
		t.Errorf("omnipus prompt still carries the OLD no-fit rule that §7.14.3 replaces")
	}
	if !strings.Contains(prompt, "nothing fits and it is neither of those two requests, answer the question and call no change tool.") {
		t.Errorf("omnipus prompt must carry the REPLACED no-fit rule of §7.14.3")
	}

	// The replaced one-helper sentence must be gone.
	if strings.Contains(prompt, "in at most one helper per workspace") {
		t.Errorf("omnipus prompt still carries the replaced one-helper-per-workspace sentence")
	}

	// No helper-count number (founder Q25 = B): the prompt must not carry a
	// helper cap ("normally no more than four at once" removed in r5.1).
	if strings.Contains(prompt, "no more than four") {
		t.Errorf("omnipus prompt must not carry a helper-count cap (founder Q25 = B)")
	}
}
