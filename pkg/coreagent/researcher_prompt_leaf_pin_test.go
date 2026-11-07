package coreagent_test

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResearcherPrompt_NoLeafWording_DelegateListedAsTool is RED by design on
// the current tree (issue #1661).
//
// Founder decision 2026-11-07 04:00Z ("Researcher B"): Researcher keeps the
// tools documented in docs/tools.md — the Worker core, whose "Delegate and
// inspect work" row carries delegate Allow, applied to Researcher by
// "Planner, Researcher and General Purpose use the Worker core". The
// Researcher prompt's leaf wording is to be fixed by a developer. This test
// pins the corrected prompt:
//
//  1. It must NOT claim "You are a leaf": Researcher holds delegate Allow
//     and seeds a self-edge (pkg/coreagent/seed.go::coreAgentDelegation
//     default arm), so the leaf claim contradicts both the granted policy
//     and the shipped docs.
//  2. It must list delegate among its tools with the compiled-prompt tool
//     token convention `tool:delegate` — the convention every other prompt
//     uses for every held tool (Jim and Planner both list tool:delegate).
//     The bare word "delegate" is NOT a valid oracle here: today's prompt
//     already contains "delegation-only" prose, which a substring match
//     would satisfy while the tool list stays wrong.
//
// Oracle sources: the founder decision above and docs/tools.md — never the
// prompt's current text (the assertion that would merely mirror today's
// wording is exactly the one this RED test refuses to write).
func TestResearcherPrompt_NoLeafWording_DelegateListedAsTool(t *testing.T) {
	prompt := coreagent.GetPrompt("researcher")
	require.NotEmpty(t, prompt, "GetPrompt(researcher) must return the compiled prompt (pkg/coreagent/prompts_adr090.go)")

	// Both assertions use assert (not require) so ONE red run shows BOTH
	// failures — the leaf claim and the missing delegate entry are two
	// separate corrections to the same prompt.
	assert.NotContains(t, prompt, "You are a leaf",
		"founder decision 2026-11-07 04:00Z (Researcher B, #1661): the prompt must stop claiming Researcher is a leaf — "+
			"Researcher holds the Worker core's delegate Allow (docs/tools.md) and seeds a self-edge")
	assert.Contains(t, prompt, "tool:delegate",
		"the prompt's tool list must include tool:delegate (the tool:<name> convention) now that Researcher keeps delegate: allow — "+
			"a bare 'delegate' substring is not accepted as evidence, it would match the existing 'delegation-only' prose")

	// The whole-prompt match above also hits the graph-permission sentence
	// ("Use tool:delegate only when ..."), so it cannot prove list membership.
	// Parse the "Standard tools include ..." sentence itself and check its items.
	const marker = "Standard tools include "
	start := strings.Index(prompt, marker)
	require.GreaterOrEqual(t, start, 0, "the prompt must carry a %q sentence listing its tools", marker)
	sentence := prompt[start+len(marker):]
	end := strings.Index(sentence, ".")
	require.GreaterOrEqual(t, end, 0, "the Standard tools sentence must end with a period")
	var items []string
	for _, item := range strings.Split(sentence[:end], ",") {
		items = append(items, strings.TrimPrefix(strings.TrimSpace(item), "and "))
	}
	assert.Contains(t, items, "tool:delegate",
		"tool:delegate must be an item of the Standard tools list itself, independent of the graph-permission sentence (founder 2026-11-07 04:00Z)")
	assert.Contains(t, items, "tool:message_parent",
		"control: the parse yields real list items (Researcher returns its bundle through tool:message_parent)")
	assert.NotContains(t, items, "tool:bash",
		"control: Researcher holds no bash (notool:bash)")
}
