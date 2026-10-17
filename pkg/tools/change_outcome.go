// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"encoding/json"
	"log/slog"
	"strings"
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
// changed_fields, …) stay unchanged beside them; changed_fields lists the
// durable parts.
const (
	OutcomeWorking            = "working"
	OutcomeSavedNotWorking    = "saved but not working"
	OutcomePartlySaved        = "partly saved"
	OutcomeFailedNothingSaved = "failed: nothing saved"
)

// ChangeTool marks a change tool: every result it returns carries the FR-030
// verdict. The registry relies on it to give its own argument-schema refusal —
// returned before Execute runs, so nothing was saved — the same
// "failed: nothing saved" envelope the tool gives its own refusals.
type ChangeTool interface {
	Tool
	ReportsChangeOutcome()
}

// ChangeOutcome is one FR-030 verdict.
type ChangeOutcome struct {
	Outcome      string
	Reason       string
	CheckScope   string
	NotTested    []string
	MissingParts []string
}

// ChangeWorking is the verdict for a saved change whose check passed.
func ChangeWorking(checkScope string, notTested ...string) ChangeOutcome {
	return ChangeOutcome{Outcome: OutcomeWorking, CheckScope: checkScope, NotTested: notTested}
}

// ChangeSavedNotWorking is the verdict for a saved change whose check failed
// or could not run.
func ChangeSavedNotWorking(reason, checkScope string, notTested ...string) ChangeOutcome {
	return ChangeOutcome{Outcome: OutcomeSavedNotWorking, Reason: reason, CheckScope: checkScope, NotTested: notTested}
}

// ChangePartlySaved is the verdict for a change of which some parts are
// durable and others (missing) were not saved or not verified.
func ChangePartlySaved(reason, checkScope string, missing []string, notTested ...string) ChangeOutcome {
	return ChangeOutcome{Outcome: OutcomePartlySaved, Reason: reason, CheckScope: checkScope,
		MissingParts: missing, NotTested: notTested}
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Into writes the verdict's fields into a result map and returns it.
func (o ChangeOutcome) Into(m map[string]any) map[string]any {
	m["outcome"] = o.Outcome
	if o.Reason != "" {
		m["reason"] = o.Reason
	}
	m["check_scope"] = o.CheckScope
	m["not_tested"] = nonNilStrings(o.NotTested)
	m["missing_parts"] = nonNilStrings(o.MissingParts)
	return m
}

// marshalResult renders m as indented JSON (a map of plain values cannot fail
// to encode; a failure is reported in the text rather than dropped).
func marshalResult(m map[string]any) string {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return `{"success":false,"error":{"code":"ENCODE_FAILED","message":"the tool result could not be encoded"}}`
	}
	return string(b)
}

// ChangeSuccessResult renders a non-error change-tool result carrying its
// verdict. "partly saved" and "saved but not working" are still non-error
// results: the change is durable and the caller must read the fields.
func ChangeSuccessResult(fields map[string]any, o ChangeOutcome) *ToolResult {
	return NewToolResult(marshalResult(o.Into(fields)))
}

// ChangeErrorResult renders an error result (IsError) carrying a verdict
// other than "failed: nothing saved" — a failure AFTER a side effect.
func ChangeErrorResult(fields map[string]any, o ChangeOutcome) *ToolResult {
	return ErrorResult(marshalResult(o.Into(fields)))
}

// FinalizeChangeResult guarantees the FR-030 fields on a change tool's
// result, and changed_fields: [] on every "failed: nothing saved". A result that already carries an outcome is returned unchanged. A
// refusal without one gets "failed: nothing saved" with the refusal text as
// reason — so every path that reaches here without an explicit verdict MUST
// be one on which no side effect happened; a failure after a side effect sets
// its own verdict (ChangeErrorResult). A success without an outcome is a
// programming error: it is reported as "partly saved" (never "working") so it
// cannot overclaim, and logged. Other result properties (Silent, Async,
// media) are kept.
func FinalizeChangeResult(tool string, res *ToolResult) *ToolResult {
	if res == nil {
		return res
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(res.ForLLM), &m); err != nil || m == nil {
		// Normalize the plain text into the structured result object.
		return finalizePlainText(tool, res)
	}
	if dry, _ := m["dry_run"].(bool); dry {
		// A dry run changes nothing and states its own check scope.
		return res
	}
	if o, has := m["outcome"]; has {
		if o == OutcomeFailedNothingSaved && m["changed_fields"] == nil {
			m["changed_fields"] = []string{}
			res.ForLLM = marshalResult(m)
		}
		return res
	}
	var o ChangeOutcome
	if res.IsError {
		o = ChangeOutcome{Outcome: OutcomeFailedNothingSaved, Reason: changeErrorMessage(m)}
		// Ruling 2026-10-14T18:40Z: the durable parts of a change that saved
		// nothing are an empty list, never null or omitted.
		if m["changed_fields"] == nil {
			m["changed_fields"] = []string{}
		}
	} else {
		slog.Error("tools: change tool returned success without an FR-030 outcome", "tool", tool)
		o = ChangePartlySaved("the tool did not report what it checked; read the affected resource before relying on it",
			"", []string{"verification"})
	}
	res.ForLLM = marshalResult(o.Into(m))
	return res
}

// finalizePlainText turns a plain-text change result into the structured
// FR-030 object: the text becomes error.message (a refusal) or message, so
// every change-tool result is one JSON shape (RED pack, WU-3).
func finalizePlainText(tool string, res *ToolResult) *ToolResult {
	text := strings.TrimSpace(res.ForLLM)
	var o ChangeOutcome
	fields := map[string]any{}
	if res.IsError {
		o = ChangeOutcome{Outcome: OutcomeFailedNothingSaved, Reason: text}
		fields["success"] = false
		fields["error"] = map[string]any{"message": text}
		fields["changed_fields"] = []string{}
	} else {
		slog.Error("tools: change tool returned success without an FR-030 outcome", "tool", tool)
		fields["message"] = text
		o = ChangePartlySaved("the tool did not report what it checked; read the affected resource before relying on it",
			"", []string{"verification"})
	}
	res.ForLLM = marshalResult(o.Into(fields))
	return res
}

// changeErrorMessage extracts the refusal text from an error-shaped map.
func changeErrorMessage(m map[string]any) string {
	if e, ok := m["error"].(map[string]any); ok {
		if msg, ok := e["message"].(string); ok && msg != "" {
			return msg
		}
		if code, ok := e["code"].(string); ok {
			return code
		}
	}
	if e, ok := m["error"].(string); ok && e != "" {
		return e
	}
	if msg, ok := m["message"].(string); ok {
		return msg
	}
	return ""
}

// The two change tools that live in this package.
var (
	_ ChangeTool = (*InstallSkillTool)(nil)
	_ ChangeTool = (*EnvironmentSetupTool)(nil)
)

// ReportsChangeOutcome marks install_skill as a change tool (ChangeTool).
func (t *InstallSkillTool) ReportsChangeOutcome() {}

// ReportsChangeOutcome marks environment_setup as a change tool (ChangeTool).
func (t *EnvironmentSetupTool) ReportsChangeOutcome() {}
