// Omnipus — the D2 seed's NEGATIVE half (capability spec §11 site 6a, FR-021).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// A separate file from browser_d2_seed_test.go on purpose. That file is edited
// by more than one stream as tools land, and these two assertions were lost
// once already to a concurrent overwrite in a shared worktree. They are the
// half that says who must NOT have the surface, which is the half nothing else
// in the suite covers.
//
// It reuses browser_d2_seed_test.go's d2Resolve / zeroBrowserAgents rather than
// building a parallel harness: two harnesses for one question is how the two
// halves drift into disagreeing.

package coreagent_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// TestCoreAgentSeed_UploadIsDenyForNonBrowsingAgents is the assertion revision
// 2 of the spec got WRONG, and the reason it got it wrong is worth keeping in
// front of whoever edits this next.
//
// It reasoned from COVERAGE — which is OR-based, so a global entry "covers"
// every agent — and concluded that the global `ask` on browser_upload_file
// reaches Mia and Ava. It does not. Coverage asks whether an entry exists;
// RESOLUTION asks which value applies, and it is most-restrictive-wins.
// denyAllThenOverride writes an explicit agent-level deny for every catalog
// name an agent does not override, and Mia and Ava name no browser tool at
// all, so agent-deny beats global-ask.
//
// That is why §11 site 6a is "NO EDIT" rather than an omission: the two
// zero-browser agents get their least-privilege posture for free, and this
// test is what proves the mechanism still delivers it.
func TestCoreAgentSeed_UploadIsDenyForNonBrowsingAgents(t *testing.T) {
	for _, agent := range zeroBrowserAgents {
		for _, tool := range append([]string{"browser_upload_file"}, d2BrowserVerbs...) {
			if got := d2Resolve(t, agent, tool); got != "deny" {
				t.Errorf("(%s, %s) RESOLVES %q, want \"deny\". This agent holds no browser tool, "+
					"and the global ceiling is the only thing that could have granted it — so a "+
					"non-deny here means denyAllThenOverride has stopped stamping an explicit "+
					"agent-level deny for catalog names an agent does not override, and every "+
					"zero-browser agent has quietly gained the whole browser surface",
					agent, tool, got)
			}
		}
	}
}

// TestCoreAgentSeed_WorkerInheritsGlobalCeiling is the only assertion on the
// GLOBAL ceiling's VALUES, and it works precisely because IDWorker is the one
// seeded agent whose map is sparse.
//
// tightenGlobalCeiling names no browser tool, so whatever Worker resolves IS
// the global ceiling, merged through the real compositor.
//
// BE PRECISE ABOUT WHAT THIS ADDS, because overstating it is how a test earns
// unearned trust. A ceiling wrong in the RESTRICTIVE direction (a global
// `deny`) is visible to every agent, because most-restrictive-wins drags their
// per-agent `allow` down with it — the other posture tests catch that. What
// they CANNOT see is a ceiling wrong in the PERMISSIVE direction: a global
// `allow` where `ask` was intended is completely masked by every agent that
// carries its own entry. Worker carries none, so it is the only agent whose
// resolved value is the ceiling itself, in both directions. The coverage test
// sees neither: it asks whether an entry exists, never what it says.
//
// Worker is also the delegation-tier agent the `ask` on browser_upload_file
// exists for, so this is a posture assertion twice over.
func TestCoreAgentSeed_WorkerInheritsGlobalCeiling(t *testing.T) {
	for _, tool := range d2BrowserVerbs {
		if got := d2Resolve(t, coreagent.IDWorker, tool); got != "allow" {
			t.Errorf("(Worker, %s) resolves %q, want \"allow\". Worker's own map is SPARSE and "+
				"names no browser tool, so this value is the global sandbox.tool_policies ceiling "+
				"speaking directly, in both directions — including the permissive one that every "+
				"agent carrying its own entry would mask", tool, got)
		}
	}
	if got := d2Resolve(t, coreagent.IDWorker, "browser_upload_file"); got != "ask" {
		t.Errorf("(Worker, browser_upload_file) resolves %q, want \"ask\". Worker inherits this "+
			"straight from the global ceiling, and it is the delegation-tier agent the consent "+
			"gate exists for: an unattended worker attaching the operator's files to a page on "+
			"their signed-in session is the exact case FR-021 is about", got)
	}
}
