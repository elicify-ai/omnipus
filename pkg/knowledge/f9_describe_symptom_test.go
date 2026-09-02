// Omnipus — diagnostic coverage for the SECOND symptom Finding 2
// (docs/internal/uat/uat-findings-knowledge-tools-2026-09-01-run2.md-shaped
// regression, reported directly rather than filed) names: knowledge_describe
// flipping from "NOT INDEXED yet" to a false DRIFT report ("index holds 1
// notes, N on disk — re-index to reconcile") after one knowledge_edit write
// to a never-swept collection.
//
// THIS FILE DELIBERATELY DOES NOT FIX ANYTHING. knowledge_describe.go is
// outside this change's file ownership (author.go and
// pkg/vaultprops/find_tool.go, plus new *_test.go files) — this test exists
// to answer, with evidence, whether fixing pkg/vaultprops/find_tool.go's
// Populated() (the knowledge_find-side fix for the reopened F-9) also fixes
// this knowledge_describe-side symptom, since both read the SAME manifest
// file author.go's refreshIndexesForNote writes.
//
// ANSWER, recorded here so it does not need re-deriving: NO. knowledge_find's
// fix works by comparing the manifest's entry count against a fresh
// knowledge.Scan of the collection (found in find_tool.go); knowledge_describe's
// indexFreshness (knowledge_describe.go) does that SAME comparison already
// (DescribeData.NotesOnDisk vs ManifestCount) but ONLY when check_integrity
// is requested — its DEFAULT, cheap path still treats "manifest exists" as
// "index holds ManifestCount notes" per se, which after one instant write is
// exactly the "index holds 1 notes, N on disk" false-drift message the
// finding describes. Fixing that is a knowledge_describe.go change, which
// this task's file ownership does not include; it is reported here as an
// open, related defect rather than silently left unverified.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestF9DescribeSymptom' ./pkg/knowledge/
package knowledge

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestF9DescribeSymptom_StillMisreportsAfterOneInstantWrite documents the
// CURRENT (post knowledge_find fix) behaviour of knowledge_describe's
// default index section after exactly one knowledge_edit create lands on a
// collection nobody has ever swept. It is written to PASS on today's code —
// it is not a fix's regression guard, it is the evidence for this task's
// "does the describe symptom also resolve" question. If a future change to
// knowledge_describe.go makes this collection correctly report "NOT INDEXED
// yet" again, this test's second assertion will fail and should be updated
// (that would be describe.go's own fix landing, not a regression).
func TestF9DescribeSymptom_StillMisreportsAfterOneInstantWrite(t *testing.T) {
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

	// This is the CURRENT, still-open symptom: knowledge_describe's default
	// (non check_integrity) path answers from ManifestKnown/ManifestCount
	// alone, so one instant write flips it from "NOT INDEXED yet" to a false
	// drift report naming a remedy ("re-index") the code has no way to
	// perform. The knowledge_find-side fix (pkg/vaultprops/find_tool.go's
	// Populated()) does not touch this path, because knowledge_describe never
	// calls it — describe reads the manifest directly.
	if strings.Contains(after, "NOT INDEXED yet") {
		t.Fatalf("knowledge_describe's default index section now correctly reports NOT INDEXED yet "+
			"after one instant write on a never-swept collection — the describe-message symptom has "+
			"been independently fixed (in knowledge_describe.go, outside this task's file ownership); "+
			"update this test's expectation.\ngot: %s", after)
	}
	t.Logf("confirmed still-open, out-of-ownership-scope symptom: %s", after)
}
