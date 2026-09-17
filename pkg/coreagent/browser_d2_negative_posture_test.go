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

// Non-browser roles retain explicit exclusions despite the global browser ceiling.
func TestCoreAgentSeed_UploadIsDenyForNonBrowsingAgents(t *testing.T) {
	for _, agent := range zeroBrowserAgents {
		for _, tool := range append([]string{"browser_upload_file"}, d2BrowserVerbs...) {
			if got := d2Resolve(t, agent, tool); got != "deny" {
				t.Errorf("(%s, %s) RESOLVES %q, want \"deny\". This agent holds no browser tool, "+
					"and the global ceiling is the only thing that could have granted it — so a "+
					"non-deny here means the seed has lost its explicit "+
					"agent-level deny for catalog names an agent does not override, and every "+
					"zero-browser agent has quietly gained the whole browser surface",
					agent, tool, got)
			}
		}
	}
}

// ADR-090 General Purpose has no browser capability by default.
func TestCoreAgentSeed_WorkerExplicitlyDeniesBrowserSurface(t *testing.T) {
	for _, tool := range d2BrowserVerbs {
		if got := d2Resolve(t, coreagent.IDWorker, tool); got != "deny" {
			t.Errorf("(Worker, %s) resolves %q, want deny under ADR-090", tool, got)
		}
	}
	if got := d2Resolve(t, coreagent.IDWorker, "browser_upload_file"); got != "deny" {
		t.Errorf("(Worker, browser_upload_file) resolves %q, want deny", got)
	}
}
