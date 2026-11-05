// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// fieldselect.go — the one field-selection helper every read tool uses
// (tool-parity spec r17 §7.13, P-1, P-2; scenarios 20.1–20.11). A read tool
// declares its selectable object with a FieldSpec; the registry checks the
// `fields` argument with ValidateFieldsArg before generic schema validation,
// and the tool returns Project's result.

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// FieldSpec describes one read tool's selectable object.
type FieldSpec struct {
	// Object names the object in the error texts: "agent", "agent preview",
	// "task", "plan", "goal".
	Object string
	// Identity keys are always returned and never selectable.
	Identity []string
	// Valid holds the selectable names, in schema order (schema properties −
	// Identity − Metadata).
	Valid []string
}

// Metadata keys a result may carry; never selectable, never in Valid (P-2).
var Metadata = []string{"absent_fields", "truncated_fields"}

// FieldSelecting is implemented by every read tool. Registry.Execute calls
// ValidateFieldsArg with FieldSpecFor(args) before generic schema validation,
// so the shared error is the one the agent sees (§7.13 validation boundary).
type FieldSelecting interface {
	FieldSpecFor(args map[string]any) FieldSpec
}

const (
	// ResultBoundChars is the most JSON-encoded characters a read result
	// may carry.
	ResultBoundChars = 48000
	// CutChars is the length a long text value is cut to.
	CutChars = 8000
)

// AgentPreviewObject is the FieldSpec.Object of get_agent's new-agent
// preview. A name that is valid for an agent but not for a preview gets the
// preview's own refusal text (§7.13 error table, row 4).
const AgentPreviewObject = "agent preview"

// AgentFieldNames are the 29 selectable names of AgentDetail (§7.12.1), in
// schema order: get_agent's Valid list.
var AgentFieldNames = []string{
	"name", "description", "type", "locked", "figure", "role", "color", "voice",
	"model", "provider", "fallback_models", "model_chain", "model_params",
	"max_tool_iterations", "context_window_override", "memory_enabled",
	"soul", "agent_md", "skills", "stale_skill_grants", "tool_permissions", "stored_overrides",
	"connectors", "executor", "runnable", "program_warning",
	"activation_status", "editable_fields", "has_main_chat",
}

// AgentPreviewFieldNames are the names a new-agent preview accepts.
var AgentPreviewFieldNames = []string{"tool_permissions", "connectors"}

const fieldsAll = "all"

// ValidateFieldsArg checks args["fields"] and returns the selected names (all
// Valid names, in schema order, for ["all"]) or the shared §7.13 error.
func ValidateFieldsArg(args map[string]any, spec FieldSpec) ([]string, *ToolResult) {
	names, ok := fieldNames(args["fields"])
	if !ok {
		return nil, ErrorResult(fmt.Sprintf(`fields is required for %s: pass ["all"] or one or more of: %s`,
			spec.Object, strings.Join(spec.Valid, ", ")))
	}
	if slices.Contains(names, fieldsAll) {
		if len(names) > 1 {
			return nil, ErrorResult(`"all" cannot be combined with other field names`)
		}
		return append([]string{}, spec.Valid...), nil
	}
	for _, name := range names {
		if slices.Contains(spec.Valid, name) {
			continue
		}
		if spec.Object == AgentPreviewObject && slices.Contains(AgentFieldNames, name) {
			return nil, ErrorResult(`fields for a new-agent preview: pass ["all"] or one or more of: ` +
				strings.Join(spec.Valid, ", "))
		}
		return nil, ErrorResult(fmt.Sprintf(`unknown field %q for %s. Valid: all, %s`,
			name, spec.Object, strings.Join(spec.Valid, ", ")))
	}
	return names, nil
}

// fieldNames reads a non-empty array of strings; anything else is not a
// fields argument.
func fieldNames(raw any) ([]string, bool) {
	var names []string
	switch v := raw.(type) {
	case []string:
		names = append(names, v...)
	case []any:
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			names = append(names, s)
		}
	default:
		return nil, false
	}
	if len(names) == 0 {
		return nil, false
	}
	return names, true
}

// FieldsParameter is the `fields` parameter schema, identical in every read
// tool: an array of unique names from ["all"] + valid, at least one, no
// default.
func FieldsParameter(valid []string) map[string]any {
	enum := make([]any, 0, len(valid)+1)
	enum = append(enum, fieldsAll)
	for _, name := range valid {
		enum = append(enum, name)
	}
	return map[string]any{
		"type":        "array",
		"minItems":    1,
		"uniqueItems": true,
		"items":       map[string]any{"type": "string", "enum": enum},
		"description": `Required. ["all"] or one or more property names.`,
	}
}

// Project marshals the full generated object once, keeps Identity plus the
// selected keys the full object carries (values unchanged), records the others
// in absent_fields, and applies the size bound. It never alters full.
func Project(full any, spec FieldSpec, selected []string) (map[string]any, *ToolResult) {
	raw, err := json.Marshal(full)
	if err != nil {
		return nil, ErrorResult(fmt.Sprintf("read %s failed: could not encode it: %v", spec.Object, err))
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, ErrorResult(fmt.Sprintf("read %s failed: it is not an object: %v", spec.Object, err))
	}
	// The capacity comes from the spec, never from the caller's selection: a
	// selection may repeat a name, so its length does not bound the keys.
	out := make(map[string]any, len(spec.Identity)+len(spec.Valid)+len(Metadata))
	for _, id := range spec.Identity {
		if v, ok := object[id]; ok {
			out[id] = v
		}
	}
	var absent []string
	for _, name := range selected {
		if v, ok := object[name]; ok {
			out[name] = v
		} else {
			absent = append(absent, name)
		}
	}
	if len(absent) > 0 {
		out["absent_fields"] = absent
	}
	return boundResult(out, spec, selected)
}

// boundResult applies the 48,000-character bound: over it, selected
// non-identity top-level string values longer than CutChars are cut, longest
// first, until the result fits; still over, the call fails with no object.
func boundResult(out map[string]any, spec FieldSpec, selected []string) (map[string]any, *ToolResult) {
	size, errRes := encodedCharsOrError(out, spec)
	if errRes != nil {
		return nil, errRes
	}
	if size <= ResultBoundChars {
		return out, nil
	}
	type candidate struct {
		name   string
		text   string
		length int
	}
	var candidates []candidate
	for _, name := range selected {
		if slices.Contains(spec.Identity, name) {
			continue
		}
		if s, ok := out[name].(string); ok && utf8.RuneCountInString(s) > CutChars {
			candidates = append(candidates, candidate{name, s, utf8.RuneCountInString(s)})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].length > candidates[j].length })
	var truncated []string
	for _, c := range candidates {
		if size <= ResultBoundChars {
			break
		}
		out[c.name] = cutText(c.text, c.length)
		truncated = append(truncated, c.name)
		out["truncated_fields"] = truncated
		if size, errRes = encodedCharsOrError(out, spec); errRes != nil {
			return nil, errRes
		}
	}
	if size <= ResultBoundChars {
		return out, nil
	}
	largest, largestChars, errRes := largestField(out, spec)
	if errRes != nil {
		return nil, errRes
	}
	return nil, ErrorResult(fmt.Sprintf("result too large for %s: %d characters after cutting long text (limit 48,000); "+
		"largest field: %s (%d characters). Request fewer fields.", spec.Object, size, largest, largestChars))
}

// cutText keeps the first CutChars characters and appends the fixed marker
// naming the full length.
func cutText(s string, length int) string {
	runes := []rune(s)
	return string(runes[:CutChars]) + fmt.Sprintf("…[cut: %s of %s characters]",
		thousands(CutChars), thousands(length))
}

// largestField names the non-identity, non-metadata key with the longest
// JSON encoding.
func largestField(out map[string]any, spec FieldSpec) (string, int, *ToolResult) {
	keys := make([]string, 0, len(out))
	for k := range out {
		if !slices.Contains(spec.Identity, k) && !slices.Contains(Metadata, k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	name, best := "", -1
	for _, k := range keys {
		n, errRes := encodedCharsOrError(out[k], spec)
		if errRes != nil {
			return "", 0, errRes
		}
		if n > best {
			name, best = k, n
		}
	}
	return name, best, nil
}

// encodedCharsOrError is the JSON-encoded length in characters (not bytes).
// The values come from decoding JSON, so encoding them again cannot normally
// fail; if it does, the read fails rather than guessing a size.
func encodedCharsOrError(v any, spec FieldSpec) (int, *ToolResult) {
	raw, err := json.Marshal(v)
	if err != nil {
		return 0, ErrorResult(fmt.Sprintf("read %s failed: could not measure the result: %v", spec.Object, err))
	}
	return utf8.RuneCount(raw), nil
}

// thousands formats n with comma thousands separators.
func thousands(n int) string {
	s := strconv.Itoa(n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}
