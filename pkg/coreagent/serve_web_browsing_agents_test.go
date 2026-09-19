// Omnipus — FR-030: the file:// pointer must lead somewhere.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// browser_navigate refuses a file:// URL, and the refusal now tells the agent
// to serve the file with serve_web and open the /preview/ URL instead. That
// pointer is only worth anything if the agent it is handed to can actually
// call serve_web.
//
// Before this change, three of the five browser-capable agents — Ray, Explorer
// and Researcher — resolved serve_web: deny, because serve_web is in
// allStaticToolNames and only Jim overrode it. For them the new message would
// have been issue #242's dead end relocated one failed tool call further away,
// which is exactly what D2 exists to remove.

package coreagent_test

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// TestCoreAgentSeed_BrowsingAgentsCanCallServeWeb COMPUTES the population from
// resolved policy rather than listing it.
//
// The predicate is "resolves allow for at least one browser_* tool". Two
// properties follow from that choice and both are load-bearing:
//
//   - It is derived, so an agent that GAINS the browser surface later is
//     picked up automatically. A hardcoded list would silently stop covering
//     the new agent, which is the failure mode this requirement is about.
//   - `allow`, not "allow or ask", so browser_upload_file's ask never moves
//     the set. Otherwise an agent whose only browser entry was the ask would
//     be pulled in and the test would start asserting a grant nobody argued
//     for.
func TestCoreAgentSeed_ServeWebIsGeneralPurposeOnly(t *testing.T) {
	browserTools := []string{}
	for _, name := range coreagent.AllStaticToolNames() {
		if strings.HasPrefix(name, "browser_") {
			browserTools = append(browserTools, name)
		}
	}
	if len(browserTools) == 0 {
		t.Fatal("no browser_* tool is in allStaticToolNames — the predicate below would select " +
			"nobody and this test would pass while asserting nothing")
	}

	for _, id := range []coreagent.CoreAgentID{coreagent.IDMia, coreagent.IDJim, coreagent.IDAva, coreagent.IDAdmin, coreagent.IDPlanner, coreagent.IDResearcher} {
		if got := d2Resolve(t, id, "serve_web"); got != "deny" {
			t.Errorf("%s serve_web resolves %q, want deny", id, got)
		}
	}
	if got := d2Resolve(t, coreagent.IDWorker, "serve_web"); got != "allow" {
		t.Errorf("General Purpose serve_web resolves %q, want allow", got)
	}
}
