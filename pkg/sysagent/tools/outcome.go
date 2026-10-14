// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"encoding/json"
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Truthful change-tool results (omnipus-system-agent FR-030, ADR OSA-4; field
// shape ruled in WU3-TOOL-SHAPE-RULINGS-20261014 Q2). Every change tool's
// result — success or refusal — carries:
//
//	outcome        one of the four literal phrases below
//	reason         why it is not "working" (required for "saved but not
//	               working" and "partly saved"; the refusal text for
//	               "failed: nothing saved")
//	check_scope    what the tool's own check actually proved
//	not_tested     what the check did not prove (never null)
//	missing_parts  what was not saved or not verified (never null)
//
// Existing structured fields (persistence_status, activation_status,
// changed_fields, …) are kept unchanged beside them; changed_fields is the
// list of durable parts.
const (
	outcomeWorking            = "working"
	outcomeSavedNotWorking    = "saved but not working"
	outcomePartlySaved        = "partly saved"
	outcomeFailedNothingSaved = "failed: nothing saved"
)

// changeOutcome is one FR-030 verdict.
type changeOutcome struct {
	Outcome      string
	Reason       string
	CheckScope   string
	NotTested    []string
	MissingParts []string
}

// working is the verdict for a saved change whose check passed.
func working(checkScope string, notTested ...string) changeOutcome {
	return changeOutcome{Outcome: outcomeWorking, CheckScope: checkScope, NotTested: notTested}
}

// savedNotWorking is the verdict for a saved change whose check failed.
func savedNotWorking(reason, checkScope string, notTested ...string) changeOutcome {
	return changeOutcome{Outcome: outcomeSavedNotWorking, Reason: reason, CheckScope: checkScope, NotTested: notTested}
}

// partlySaved is the verdict for a change of which some parts are durable and
// others were not saved or not verified (missing lists the latter).
func partlySaved(reason, checkScope string, missing []string, notTested ...string) changeOutcome {
	return changeOutcome{Outcome: outcomePartlySaved, Reason: reason, CheckScope: checkScope,
		MissingParts: missing, NotTested: notTested}
}

// nonNil returns s, or an empty slice for nil (the fields are never null).
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// into writes the verdict's fields into a result map and returns it.
func (o changeOutcome) into(m map[string]any) map[string]any {
	m["outcome"] = o.Outcome
	if o.Reason != "" {
		m["reason"] = o.Reason
	}
	m["check_scope"] = o.CheckScope
	m["not_tested"] = nonNil(o.NotTested)
	m["missing_parts"] = nonNil(o.MissingParts)
	return m
}

// changeSuccess renders a successful change-tool result carrying its verdict.
// A "partly saved" or "saved but not working" verdict is still a non-error
// result: the change is durable and the caller must read the fields.
func changeSuccess(fields map[string]any, o changeOutcome) *tools.ToolResult {
	return tools.NewToolResult(successJSON(o.into(fields)))
}

// changeErrorWithOutcome renders an error result (IsError) that carries a
// verdict other than "failed: nothing saved" — a failure AFTER a side effect.
func changeErrorWithOutcome(fields map[string]any, o changeOutcome) *tools.ToolResult {
	return tools.ErrorResult(successJSON(o.into(fields)))
}

// finalizeChangeResult guarantees the FR-030 fields on a change tool's result.
// A result that already carries an outcome is returned unchanged. A refusal
// without one gets "failed: nothing saved" with the refusal message as reason
// — so every path that reaches here without an explicit verdict MUST be a
// path on which no side effect happened; a failure after a side effect sets
// its own verdict (changeErrorWithOutcome). A success without an outcome is a
// programming error: it is reported as "partly saved" (never "working") so it
// cannot overclaim, and logged.
func finalizeChangeResult(tool string, res *tools.ToolResult) *tools.ToolResult {
	if res == nil {
		return res
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(res.ForLLM), &m); err != nil || m == nil {
		m = map[string]any{"message": res.ForLLM}
	}
	if _, has := m["outcome"]; has {
		return res
	}
	var o changeOutcome
	if res.IsError {
		o = changeOutcome{Outcome: outcomeFailedNothingSaved, Reason: errorMessageOf(m)}
	} else {
		slog.Error("sysagent: change tool returned success without an FR-030 outcome", "tool", tool)
		o = partlySaved("the tool did not report what it checked; read the affected resource before relying on it",
			"", []string{"verification"})
	}
	res.ForLLM = successJSON(o.into(m))
	return res
}

// errorMessageOf extracts error.message from an errorJSON-shaped map.
func errorMessageOf(m map[string]any) string {
	if e, ok := m["error"].(map[string]any); ok {
		if msg, ok := e["message"].(string); ok && msg != "" {
			return msg
		}
		if code, ok := e["code"].(string); ok {
			return code
		}
	}
	if msg, ok := m["message"].(string); ok {
		return msg
	}
	return ""
}
