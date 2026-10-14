// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import "github.com/elicify-ai/omnipus/pkg/tools"

// Package-local short names for the shared FR-030 change-tool verdict
// (pkg/tools/change_outcome.go) — every system change tool uses them.
type changeOutcome = tools.ChangeOutcome

const outcomeWorking = tools.OutcomeWorking

func working(checkScope string, notTested ...string) changeOutcome {
	return tools.ChangeWorking(checkScope, notTested...)
}

func savedNotWorking(reason, checkScope string, notTested ...string) changeOutcome {
	return tools.ChangeSavedNotWorking(reason, checkScope, notTested...)
}

func partlySaved(reason, checkScope string, missing []string, notTested ...string) changeOutcome {
	return tools.ChangePartlySaved(reason, checkScope, missing, notTested...)
}

func changeSuccess(fields map[string]any, o changeOutcome) *tools.ToolResult {
	return tools.ChangeSuccessResult(fields, o)
}

func changeErrorWithOutcome(fields map[string]any, o changeOutcome) *tools.ToolResult {
	return tools.ChangeErrorResult(fields, o)
}

func finalizeChangeResult(tool string, res *tools.ToolResult) *tools.ToolResult {
	return tools.FinalizeChangeResult(tool, res)
}
