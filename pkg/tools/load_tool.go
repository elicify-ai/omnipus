// Omnipus — load_tool: on-demand tool schema fetcher (v0.1.0 manifest optimization)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// LoadTool makes lazy tools directly callable by fetching their full parameter
// schemas on demand. It is the companion to the compressed-manifest optimization
// (cfg.Tools.Manifest.Compressed): when the manifest is active, the agent calls
// load_tool with one or more tool names from the "More tools" manifest block,
// and on the next turn those tools appear as normal callable defs.
//
// The tool requires a resolver wired by the agent loop via SetResolver before
// Execute is called. The resolver pair isolates pkg/tools from pkg/agent (no
// import cycle): the loop supplies canLoad/markLoaded closures that have full
// access to the per-agent registry and session state.
type LoadTool struct {
	BaseTool
	canLoad    func(ctx context.Context, name string) bool
	markLoaded func(ctx context.Context, names []string) (loaded map[string]any, rejected []string)
}

// NewLoadTool constructs a LoadTool. SetResolver must be called before Execute
// when this instance is used for real execution (not just metadata lookup).
func NewLoadTool() *LoadTool {
	return &LoadTool{}
}

// SetResolver wires the loop's per-agent, per-session callbacks into the tool.
//
//   - canLoad(ctx, name) reports whether name is a real, policy-allowed lazy tool
//     for the calling agent. Returns false for unknown or policy-denied names.
//     The ctx carries the calling agent ID and transcript session ID so the
//     resolver can read the correct agent's registry and session state without
//     relying on a captured per-turn closure (avoids data races across concurrent
//     turns on the same shared LoadTool instance).
//   - markLoaded(ctx, names) records the names in the session's loaded-set, returns
//     each loaded tool's full JSON schema (as a map[string]any) keyed by tool name,
//     plus any names that were rejected (unknown/denied) with reasons.
//
// The callbacks are set once per agent-loop initialization and are safe to call
// concurrently (the loop is responsible for any necessary locking).
func (t *LoadTool) SetResolver(
	canLoad func(ctx context.Context, name string) bool,
	markLoaded func(ctx context.Context, names []string) (loaded map[string]any, rejected []string),
) {
	t.canLoad = canLoad
	t.markLoaded = markLoaded
}

func (t *LoadTool) Name() string           { return "load_tool" }
func (t *LoadTool) Scope() ToolScope       { return ScopeGeneral }
func (t *LoadTool) Category() ToolCategory { return CategoryToolDiscovery }

func (t *LoadTool) Description() string {
	return "Fetch the full parameters for one or more tools listed in the 'More tools' manifest so you can call them. " +
		"Pass the exact tool name(s). After loading, the tool becomes directly callable."
}

func (t *LoadTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"names": map[string]any{
				"type":        "array",
				"description": "The exact tool name(s) to load from the manifest.",
				"items": map[string]any{
					"type": "string",
				},
				"minItems": 1,
			},
		},
		"required": []string{"names"},
	}
}

// Execute loads the requested tool names.
//
// It validates each name against the agent's policy-filtered registry (via
// canLoad), collects the accepted names, calls markLoaded to record them and
// retrieve their schemas, and returns a structured JSON result:
//
//	{
//	  "loaded":   ["name1", "name2"],
//	  "schemas":  {"name1": <full-schema>, "name2": <full-schema>},
//	  "rejected": ["name3 — reason"]
//	}
//
// If all names are rejected, the result is an ErrorResult. If markLoaded/canLoad
// is nil (metadata-only instance not wired by the loop), the result is an error.
func (t *LoadTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.canLoad == nil || t.markLoaded == nil {
		return ErrorResult("load_tool not wired: resolver not set (internal configuration error)")
	}

	// Parse names array.
	raw, ok := args["names"]
	if !ok {
		return ErrorResult("load_tool: missing required parameter 'names'")
	}
	var names []string
	switch v := raw.(type) {
	case []string:
		names = v
	case []any:
		names = make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return ErrorResult(fmt.Sprintf("load_tool: 'names' must be an array of strings, got element of type %T", item))
			}
			names = append(names, s)
		}
	default:
		return ErrorResult(fmt.Sprintf("load_tool: 'names' must be an array of strings, got %T", raw))
	}

	if len(names) == 0 {
		return ErrorResult("load_tool: 'names' must have at least 1 element")
	}

	// Validate each name against the agent's available tools.
	accepted := make([]string, 0, len(names))
	preRejected := make([]string, 0)
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			preRejected = append(preRejected, "(empty string) — invalid name")
			continue
		}
		if !t.canLoad(ctx, n) {
			preRejected = append(preRejected, n+" — unknown or not available for this agent")
			continue
		}
		accepted = append(accepted, n)
	}

	if len(accepted) == 0 {
		msg := fmt.Sprintf("load_tool: no valid tools to load. Rejected: %s", strings.Join(preRejected, "; "))
		return ErrorResult(msg)
	}

	// Mark the accepted names as loaded and retrieve their schemas.
	schemas, postRejected := t.markLoaded(ctx, accepted)

	// Merge all rejected names into a single list for the result.
	allRejected := append(preRejected, postRejected...)

	// Compute the final loaded set (accepted minus any markLoaded rejections).
	loadedNames := make([]string, 0, len(schemas))
	for n := range schemas {
		loadedNames = append(loadedNames, n)
	}

	// If markLoaded rejected everything that canLoad accepted, treat as full failure.
	if len(loadedNames) == 0 {
		msg := fmt.Sprintf("load_tool: failed to load requested tools. Rejected: %s", strings.Join(allRejected, "; "))
		return ErrorResult(msg)
	}

	// Sort for determinism.
	sort.Strings(loadedNames)
	sort.Strings(allRejected)

	result := map[string]any{
		"loaded":   loadedNames,
		"schemas":  schemas,
		"rejected": allRejected,
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return ErrorResult(fmt.Sprintf("load_tool: failed to encode result: %v", err))
	}

	return SilentResult(string(encoded))
}
