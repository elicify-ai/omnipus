// Omnipus — knowledge_read regressions for UAT 2026-09-13 (D-17, D-59, D-89,
// D-91). Fixture conventions follow knowledge_read_test.go.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func readUATVault(t *testing.T, root string) {
	t.Helper()
	write := func(rel, content string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o700))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
	}
	write(".omnipus-vault/records/project.yaml", "schema_version: 1\ntype: project\nproperties:\n"+
		"  prio: { type: enum, values: [low, high] }\n  status: { type: text }\n")
	// Renamed-away `priority` and removed `tags` still in the file (D-25's aftermath).
	write("Projects/Fleet Telemetry Rollout.md", "---\ntype: project\nid: PRJ-0001\nstatus: active\n"+
		"priority: low\ntags: [hospitality, design]\n---\n"+
		"## Tasks\n\n## Notes\n\nBody.\n\n## Tasks\n\n- second tasks section\n")
	write("Assets/whole.pdf", "%PDF-fake")
	write("Assets/chart.mmd", "graph TD; A-->B")
	write("Assets/pic.png", "fake")
	write("Dash.md", "![[Assets/whole.pdf]]\n\n![[Assets/whole.pdf#page=2]]\n\n![[Assets/chart.mmd]]\n\n![[Assets/pic.png]]\n\n![[Projects/Fleet Telemetry Rollout.md]]\n")
}

// D-17 — an extensionless note path resolves; a genuinely missing one names both spellings.
func TestUAT_D17_ReadExtensionlessPath(t *testing.T) {
	root := t.TempDir()
	readUATVault(t, root)
	ctx, deps := readCtxAndDeps(t, root)
	tool := NewReadTool(deps)

	res := tool.Execute(ctx, map[string]any{"path": "Projects/Fleet Telemetry Rollout"})
	require.False(t, res.IsError, resultText(res))
	require.True(t, strings.HasPrefix(resultText(res), "Projects/Fleet Telemetry Rollout.md — version "), resultText(res))

	missing := tool.Execute(ctx, map[string]any{"path": "Projects/Nope"})
	require.True(t, missing.IsError)
	require.Contains(t, resultText(missing), "no note at Projects/Nope (nor at Projects/Nope.md)")
}

// D-89 — undeclared keys on a record are marked, and counted in the header.
func TestUAT_D89_ReadMarksUndeclaredKeys(t *testing.T) {
	root := t.TempDir()
	readUATVault(t, root)
	ctx, deps := readCtxAndDeps(t, root)

	out := resultText(NewReadTool(deps).Execute(ctx, map[string]any{"path": "Projects/Fleet Telemetry Rollout.md", "include": []any{"frontmatter"}}))
	require.Contains(t, out, `FRONTMATTER (5, 2 not declared by record type "project"):`, out)
	require.Contains(t, out, "priority  low")
	require.Contains(t, out, "(NOT DECLARED by project")
	// Declared keys and the identity keys carry no marker.
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "status") || strings.HasPrefix(trimmed, "id ") || strings.HasPrefix(trimmed, "type ") {
			require.NotContains(t, line, "NOT DECLARED", line)
		}
	}
}

// D-59 — a duplicated heading is reported as ambiguous, not silently resolved.
func TestUAT_D59_ReadSectionAmbiguityIsReported(t *testing.T) {
	root := t.TempDir()
	readUATVault(t, root)
	ctx, deps := readCtxAndDeps(t, root)

	out := resultText(NewReadTool(deps).Execute(ctx, map[string]any{"path": "Projects/Fleet Telemetry Rollout.md", "section": "Tasks"}))
	require.Contains(t, out, `SECTION "Tasks" — AMBIGUOUS: this heading appears 2 times (lines 8, 14); showing the first`, out)

	single := resultText(NewReadTool(deps).Execute(ctx, map[string]any{"path": "Projects/Fleet Telemetry Rollout.md", "section": "Notes"}))
	require.NotContains(t, single, "AMBIGUOUS")
}

// D-91 — LINKS tells a mounting embed from one shown as a link.
func TestUAT_D91_ReadLinksDistinguishMountingEmbeds(t *testing.T) {
	root := t.TempDir()
	readUATVault(t, root)
	ctx, deps := readCtxAndDeps(t, root)

	out := resultText(NewReadTool(deps).Execute(ctx, map[string]any{"path": "Dash.md", "include": []any{"links"}}))
	require.Contains(t, out, "(embed, shown as a link: a whole-document PDF is shown as a link, not mounted — give page: N to mount one page) Assets/whole.pdf (line 1)", out)
	require.Contains(t, out, "(embed) Assets/whole.pdf #page=2", out)
	require.Contains(t, out, "(embed, shown as a link: a Mermaid diagram file", out)
	require.Contains(t, out, "(embed) Assets/pic.png", out)
	require.Contains(t, out, "(embed) Projects/Fleet Telemetry Rollout.md", out)
}
