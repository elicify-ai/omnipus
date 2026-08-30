package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/skills"
)

// FindSkillsTool allows the LLM agent to search for installable skills from registries.
type FindSkillsTool struct {
	BaseTool
	registryMgr *skills.RegistryManager
	cache       *skills.SearchCache
}

// NewFindSkillsTool creates a new FindSkillsTool.
// registryMgr is the shared registry manager (built from config in createToolRegistry).
// cache is the search cache for deduplicating similar queries.
func NewFindSkillsTool(registryMgr *skills.RegistryManager, cache *skills.SearchCache) *FindSkillsTool {
	return &FindSkillsTool{
		registryMgr: registryMgr,
		cache:       cache,
	}
}

func (t *FindSkillsTool) Name() string {
	return "find_skills"
}

func (t *FindSkillsTool) Description() string {
	return "Search for installable skills from skill registries. Returns skill slugs, descriptions, versions, and relevance scores. Use this to discover skills before installing them with install_skill. " +
		"Results are cached per query (re-sliced to the current call's limit on a cache hit); if a registry is unreachable, results may be partial and the response notes it."
}

func (t *FindSkillsTool) Scope() ToolScope       { return ScopeGeneral }
func (t *FindSkillsTool) Category() ToolCategory { return CategorySkills }

func (t *FindSkillsTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query describing the desired skill capability (e.g., 'github integration', 'database management')",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of results to return (1-20, default 5)",
				"minimum":     1.0,
				"maximum":     20.0,
			},
		},
		"required": []string{"query"},
	}
}

func (t *FindSkillsTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	query, ok := args["query"].(string)
	query = strings.ToLower(strings.TrimSpace(query))
	if !ok || query == "" {
		return ErrorResult("query is required and must be a non-empty string")
	}

	limit := 5
	if raw, exists := args["limit"]; exists {
		l, ok := raw.(float64)
		if !ok {
			return ErrorResult("limit must be a number")
		}
		li := int(l)
		// House style (see shell.go's resolveTimeoutSeconds): an out-of-range
		// value is REJECTED, never silently dropped to the default.
		if li < 1 || li > 20 {
			return ErrorResult(fmt.Sprintf("limit must be between 1 and 20 (got %d)", li))
		}
		limit = li
	}

	// Check cache first. The cached slice was captured under whatever limit
	// was in effect when it was populated, so it must still be re-sliced to
	// THIS call's limit — otherwise a cache hit silently ignores a larger
	// limit requested on a later call.
	if t.cache != nil {
		if cached, hit := t.cache.Get(query); hit {
			sliced := cached
			if len(sliced) > limit {
				sliced = sliced[:limit]
			}
			return SilentResult(formatSearchResults(query, sliced, true, nil))
		}
	}

	// Search all registries.
	results, err := t.registryMgr.SearchAll(ctx, query, limit)
	var partialErr *skills.PartialSearchError
	switch {
	case err == nil:
		// full success
	case errors.As(err, &partialErr):
		// partial: proceed but note incomplete results in output below
	default:
		return ErrorResult(fmt.Sprintf("skill search failed: %v", err))
	}

	// Cache the results.
	if t.cache != nil && len(results) > 0 {
		t.cache.Put(query, results)
	}

	return SilentResult(formatSearchResults(query, results, false, partialErr))
}

func formatSearchResults(
	query string,
	results []skills.SearchResult,
	cached bool,
	partial *skills.PartialSearchError,
) string {
	if len(results) == 0 {
		return fmt.Sprintf("No skills found for query: %q", query)
	}

	var sb strings.Builder
	source := ""
	if cached {
		source = " (cached)"
	}
	fmt.Fprintf(&sb, "Found %d skills for %q%s:\n\n", len(results), query, source)
	if partial != nil {
		fmt.Fprintf(&sb, "⚠️  Note: results may be incomplete — one or more registries failed: %v\n\n", partial.Cause)
	}

	for i, r := range results {
		fmt.Fprintf(&sb, "%d. **%s**", i+1, r.Slug)
		if r.Version != "" {
			fmt.Fprintf(&sb, " v%s", r.Version)
		}
		fmt.Fprintf(&sb, "  (score: %.3f, registry: %s)\n", r.Score, r.RegistryName)
		if r.DisplayName != "" && r.DisplayName != r.Slug {
			fmt.Fprintf(&sb, "   Name: %s\n", r.DisplayName)
		}
		if r.Summary != "" {
			fmt.Fprintf(&sb, "   %s\n", r.Summary)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Use install_skill with the slug to install a skill.")
	return sb.String()
}
