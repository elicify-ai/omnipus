// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// argumentNamesRefuser is an opt-in, no-write refusal before an argument map
// reaches logging, generic validation, approval or persistence. Implementations
// receive parameter names only and must not echo them or inspect values.
type argumentNamesRefuser interface {
	RefuseArgumentNames(names []string) *ToolResult
}

// ToolArgumentRefusal shares the same value-free preflight between native turn
// admission and direct registry dispatch. Only the three integration setup tools
// implement it; every other tool keeps its existing validation/execution path.
func ToolArgumentRefusal(tool Tool, args map[string]any) *ToolResult {
	refuser, enabled := tool.(argumentNamesRefuser)
	if !enabled {
		return nil
	}
	names := make([]string, 0, len(args))
	for name := range args {
		names = append(names, name)
	}
	refusal := refuser.RefuseArgumentNames(names)
	if refusal != nil {
		if _, change := tool.(ChangeTool); change {
			return FinalizeChangeResult(tool.Name(), refusal)
		}
	}
	return refusal
}
