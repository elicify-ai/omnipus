// Omnipus — regression coverage for the SECOND symptom Finding 2
// (docs/internal/uat/uat-findings-knowledge-tools-2026-09-01-run2.md-shaped
// regression, reported directly rather than filed) names: knowledge_describe
// naming a remedy that does not exist ("re-index to reconcile" — no agent
// tool, CLI verb or REST endpoint performs that) after one knowledge_edit
// write lands on a collection nobody has ever swept.
//
// HISTORY: this file used to document the symptom as still open ("THIS FILE
// DELIBERATELY DOES NOT FIX ANYTHING" — knowledge_describe.go was outside an
// earlier task's file ownership). It is now the fix and its regression guard:
// indexFreshness's drift branch (knowledge_describe.go) no longer says "the
// two disagree; re-index to reconcile". It states the coverage it actually
// knows (how many of the notes on disk the index currently holds) and names
// no remedy, in either direction — the same reasoning
// pkg/vaultprops/find_tool.go's Populated() doc comment gives for treating a
// one-entry, instant-write-only manifest as "not (yet) meaningfully
// searched" rather than as evidence of drift.
//
// WHY THIS IS NOT "NOT INDEXED yet" EITHER: that branch fires only when no
// manifest exists at all (ManifestKnown == false). One knowledge_edit create
// on a never-swept collection DOES write a one-entry manifest
// (Index.UpdatePath, docs/internal/design/knowledge-index-freshness.md), so
// ManifestKnown is true here — collapsing this case into "NOT INDEXED yet"
// would require pretending that write never happened, which is exactly the
// kind of overcorrection the coverage message avoids by stating a number
// instead of a category.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestF9DescribeSymptom' ./pkg/knowledge/
package knowledge

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestF9DescribeSymptom_CoverageMessageNamesNoNonexistentRemedy is the
// regression guard for the fix: after exactly one knowledge_edit create on a
// collection nobody has ever swept, knowledge_describe's index section must
// state the true, partial coverage — and must not claim a "disagreement"
// needing reconciliation, and must not name "re-index" (or any other action)
// as the way to fix it, because no such action exists on any agent tool, CLI
// verb or REST endpoint.
func TestF9DescribeSymptom_CoverageMessageNamesNoNonexistentRemedy(t *testing.T) {
	home := b5Home(t)
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")
	b5Note(t, vault, "one.md", "# One\n\nsomething.\n")
	b5Note(t, vault, "two.md", "# Two\n\nsomething else.\n")
	b5Note(t, vault, "three.md", "# Three\n\nyet more.\n")
	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", vault)
	assertManifestAbsent(t, home, vault)

	before := mnbDescribe(t, home, "mia", ws)
	require.Contains(t, before, "NOT INDEXED yet",
		"precondition: before any write, the collection must report NOT INDEXED yet")

	editDeps := AuthoringDeps{
		Home:  home,
		Audit: AuthorAuditFunc(func(AuthorAuditRecord) {}),
	}
	editTool := NewEditTool(editDeps)
	res := editTool.Execute(b5Ctx("mia", ws), map[string]any{
		"collection": "notes", "op": "create", "path": "New.md",
		"body": "# New\n\na brand new note.\n",
	})
	require.False(t, res.IsError, "create refused: %s", res.ForLLM)

	after := mnbDescribe(t, home, "mia", ws)

	// THE DEFECT: naming a remedy nobody can perform.
	require.NotContains(t, after, "re-index",
		"knowledge_describe must never tell an agent to re-index — no agent tool, CLI verb "+
			"or REST endpoint performs that action\ngot: %s", after)
	// THE FRAMING: "disagree" casts a still-mostly-unindexed collection (one
	// instant write against several never-touched notes) as a conflict that
	// needs resolving, which overstates what is actually known.
	require.NotContains(t, after, "disagree",
		"knowledge_describe must not frame partial coverage from instant-only writes as a "+
			"disagreement needing reconciliation\ngot: %s", after)
	// THE HONEST REPLACEMENT: the response states the real coverage — the
	// manifest holds exactly the one note the create touched, out of the four
	// now on disk (three fixture notes plus New.md) — so a reader can judge
	// for themselves how much of the collection is actually searchable.
	require.Contains(t, after, "1 of 4 notes on disk",
		"knowledge_describe should state the true coverage instead of a drift verdict\ngot: %s", after)
	t.Logf("confirmed fixed coverage message: %s", after)
}
