// Shared serving surfaces obey the Library listing loading rules (#1017).
// Oracle: regular files only, never a link, at most 256 KiB per file.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultprops

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
)

const servingRulesSchema = "schema_version: 1\ntype: invoice\nproperties:\n  client: { type: text }\n"

func servingRulesViewBody(name string) string {
	return "name: " + name + "\ntype: invoice\nproperties: [client]\n"
}

// servingRulesKb builds a knowledge base inside a workspace with one record type and three
// view files: "ok" (a regular file), a YAML-named link to a regular file elsewhere in
// the knowledge base whose view is named "aliased", and "over", a view file of
// 256 KiB plus one byte.
func servingRulesKb(t *testing.T) (home, ws, root string) {
	t.Helper()
	home = f9Home(t)
	ws = f9Workspace(t, home)
	col, err := knowledge.CreateInWorkspace(home, ws, "kb", knowledge.Marker{DisplayName: "KB"})
	require.NoError(t, err)
	root = col.Root()
	f9Note(t, root, ".omnipus-vault/records/invoice.yaml", servingRulesSchema)
	f9Note(t, root, ".omnipus-vault/views/ok.yaml", servingRulesViewBody("ok"))
	f9Note(t, root, "notes/linked-target.yaml", servingRulesViewBody("aliased"))
	require.NoError(t, os.Symlink("../../notes/linked-target.yaml", filepath.Join(root, ".omnipus-vault", "views", "linked.yaml")))
	header := servingRulesViewBody("over")
	size := 256<<10 + 1
	f9Note(t, root, ".omnipus-vault/views/over.yaml", header+"#"+strings.Repeat("x", size-len(header)-2)+"\n")

	// Both serving tests use a resolvable relative link: the root itself accepts
	// it, so only the loader's regular-file rule can exclude its valid view.
	anchored, err := os.OpenRoot(root)
	require.NoError(t, err)
	file, err := anchored.Open(".omnipus-vault/views/linked.yaml")
	require.NoError(t, err, "instrument: the relative link stays inside the knowledge base")
	body, err := io.ReadAll(file)
	require.NoError(t, err)
	require.Equal(t, servingRulesViewBody("aliased"), string(body))
	require.NoError(t, file.Close())
	require.NoError(t, anchored.Close())
	info, err := os.Stat(filepath.Join(root, ".omnipus-vault", "views", "over.yaml"))
	require.NoError(t, err)
	require.Equal(t, int64(256<<10+1), info.Size(), "instrument: the rejected file is exactly one byte over the contract limit")
	return home, ws, root
}

// OpenFindEnv serves only what the listing serves.
func TestSavedViewServing_OpenFindEnvServesOnlyWhatTheListingServes(t *testing.T) {
	home, ws, root := servingRulesKb(t)

	// Instrument check: the link resolves, and the oversize file is really too large.
	linked, err := os.ReadFile(filepath.Join(root, ".omnipus-vault", "views", "linked.yaml"))
	require.NoError(t, err, "fixture check: the link must resolve")
	require.Contains(t, string(linked), "name: aliased")
	info, err := os.Stat(filepath.Join(root, ".omnipus-vault", "views", "over.yaml"))
	require.NoError(t, err)
	require.Equal(t, int64(256<<10+1), info.Size())

	env, closeEnv, err := OpenFindEnv(f9Ctx("mia", ws), home, knowledge.ScopedCollection{Name: "KB", Root: root})
	defer closeEnv()
	require.NoError(t, err)
	require.Equal(t, []string{"ok"}, env.Views.Names(),
		"a view the listing does not show (a link, or a file over 256 KiB) must not be served by open view, vault search or knowledge_find")
	require.NotNil(t, env.ViewReport)
	rejected := map[string]bool{}
	for _, rej := range env.ViewReport.Rejections {
		for _, p := range rej.Paths {
			rejected[filepath.Base(p)] = true
		}
	}
	require.True(t, rejected["linked.yaml"], "the link is reported as a view that could not be loaded")
	require.True(t, rejected["over.yaml"], "the oversize file is reported as a view that could not be loaded")
}

// knowledge_describe describes only what the listing serves.
func TestSavedViewServing_DescribeShowsOnlyWhatTheListingServes(t *testing.T) {
	home, ws, _ := servingRulesKb(t)

	res := knowledge.NewDescribeTool(knowledge.ToolDeps{Home: home}, Open).Execute(f9Ctx("mia", ws), map[string]any{})
	require.NotNil(t, res)
	require.False(t, res.IsError, res.ForLLM)
	text := res.ForLLM
	require.Contains(t, text, "VIEWS (1)", "exactly one view is served:\n%s", text)
	require.Contains(t, text, "  ok  type ", "the regular view is described:\n%s", text)
	require.NotContains(t, text, "aliased", "a view reached only through a link must not be described:\n%s", text)
	require.NotContains(t, text, "  over  type ", "a view file over 256 KiB must not be described:\n%s", text)
}
