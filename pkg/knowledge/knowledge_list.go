// Omnipus — KB-2a (defect-list-knowledge-base-ux-2026-09-08.md, founder-
// ratified 2026-09-08): knowledge_list, so an agent can discover which
// knowledge bases it can reach WITHOUT already knowing one.
//
// # The gap this closes
//
// The only existing route to "which knowledge bases exist here" was
// knowledge_describe's "COLLECTIONS in scope" line — a side effect of
// describing ONE knowledge base, so an agent had to already know a name to
// discover the others. This tool answers the question directly, with no
// collection argument to guess.
//
// # Why this reuses indexFreshness rather than recomputing it
//
// knowledge_describe already renders a freshness line ("index holds N
// notes" / "NOT INDEXED yet" / "INDEXING, N of M notes" / …) from the exact
// same IndexProgress + manifest state this tool needs. indexFreshness
// (knowledge_describe.go) is the single place that wording is decided;
// calling it here — the same function, not a second copy of its wording —
// is what keeps the two responses from disagreeing about what "fresh" means
// for a collection whose index state changed between one call and the next.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ListTool is knowledge_list.
type ListTool struct {
	tools.BaseTool
	deps ToolDeps
}

// NewListTool builds the tool.
func NewListTool(deps ToolDeps) *ListTool {
	if deps.RateLimiter == nil {
		deps.RateLimiter = NewRetrievalRateLimiter(RetrievalRateLimitConfig{})
	}
	return &ListTool{deps: deps}
}

// Name is the registered tool name.
func (t *ListTool) Name() string { return "knowledge_list" }

// Description is what the model reads.
func (t *ListTool) Description() string {
	return "List every knowledge base your workspace can reach — by name, so you can pass one " +
		"as 'collection' to the other knowledge_* tools — with each one's index freshness. Call " +
		"this before knowledge_describe when you do not already know a knowledge base's name; " +
		"it needs none. Reads only."
}

// Scope classifies the tool for per-agent visibility filtering.
func (t *ListTool) Scope() tools.ToolScope { return tools.ScopeGeneral }

// Category groups the tool in the picker UI.
func (t *ListTool) Category() tools.ToolCategory { return tools.CategoryMemory }

// Parameters is the JSON schema the model fills in. knowledge_list takes no
// arguments — it answers from the calling agent's own workspace scope, the
// same way FR-052/FR-053 resolve every other knowledge tool's addressable
// set, never from anything the model supplies.
func (t *ListTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

// Execute lists every knowledge base in the caller's scope.
func (t *ListTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	if unknown := unknownArgs(args, nil); len(unknown) > 0 {
		return tools.ErrorResult(fmt.Sprintf(
			"knowledge_list: unknown argument(s) %s; knowledge_list takes no arguments",
			strings.Join(unknown, ", ")))
	}
	if res := checkRetrievalRate(t.deps.RateLimiter, t.Name(), tools.ToolAgentID(ctx)); res != nil {
		return res
	}

	scope, _ := ResolveTurnScope(ctx, t.deps.Home)
	collections := scope.Collections()
	if len(collections) == 0 {
		return tools.NewToolResult("No knowledge base is available in this workspace.\n")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "KNOWLEDGE BASES in scope (%d):\n", len(collections))
	for _, col := range collections {
		fmt.Fprintf(&b, "- %s — %s\n", col.Name, listCollectionFreshness(t.deps, col.Root))
	}
	if scope.Truncated() {
		b.WriteString("(list truncated — more knowledge bases may exist below the scanned depth)\n")
	}
	return tools.NewToolResult(b.String())
}

// listCollectionFreshness renders one collection's index-freshness line by
// projecting into the SAME DescribeData shape knowledge_describe's own
// renderIndexAndCollections feeds indexFreshness, and calling that function
// — never a second wording of what "fresh" means.
//
// A manifest read failure (an unreadable index directory, a corrupt
// manifest) is not surfaced as an error here: it degrades to "not indexed
// yet" rather than failing the whole listing, matching this package's usual
// per-entry tolerance for a state it cannot establish (see
// pkg/gateway/rest_library.go's annotateKnowledgeBaseEntries for the same
// principle applied to the REST listing).
func listCollectionFreshness(deps ToolDeps, collectionRoot string) string {
	data := DescribeData{IndexProgress: resolveIndexProgress(deps, collectionRoot)}

	dir, err := IndexDirFor(deps.Home, collectionRoot)
	if err != nil {
		return indexFreshness(data)
	}
	manifestPath := filepath.Join(dir, ManifestFileName)
	exists, statErr := ManifestExists(manifestPath)
	if statErr != nil || !exists {
		return indexFreshness(data)
	}
	if m, merr := LoadManifest(manifestPath, collectionRoot); merr == nil {
		data.ManifestCount, data.ManifestKnown = m.Len(), true
	}
	return indexFreshness(data)
}
