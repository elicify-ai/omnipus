// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"fmt"
	"strings"
)

// RedactedValue replaces every masked argument value in a record copy.
const RedactedValue = "[redacted]"

// EnvOverridesArgument is the agent-writer parameter whose values never leave
// execution in clear (r17 SR-1).
const EnvOverridesArgument = "env_overrides"

// argumentRedactor is the opt-in record-copy step (r17 SR-3), beside
// argumentNamesRefuser. RedactArguments returns a new map for every record,
// log, event and frame; the input map is the execution copy and must not be
// changed. An error means no safe record copy exists: the caller refuses the
// call before anything is written (fail closed).
type argumentRedactor interface {
	RedactArguments(args map[string]any) (map[string]any, error)
}

// RecordArguments returns the copy of args that every record may carry.
// Tools without the redaction step get args back unchanged (no copy); the
// agent writers get a copy with each env_overrides value masked.
func RecordArguments(tool Tool, args map[string]any) (map[string]any, error) {
	redactor, enabled := tool.(argumentRedactor)
	if !enabled {
		return args, nil
	}
	return redactor.RedactArguments(args)
}

// RedactionRefusal is the model-facing result of a call whose arguments could
// not be made safe to record. It names no value.
func RedactionRefusal(tool Tool, err error) *ToolResult {
	refusal := ErrorResult(fmt.Sprintf("%s refused: %v", tool.Name(), err)).WithError(err)
	if _, change := tool.(ChangeTool); change {
		return FinalizeChangeResult(tool.Name(), refusal)
	}
	return refusal
}

// RedactEnvOverrides is the shared redaction of the agent writers. Every
// object stored under a key named env_overrides (any letter case, at any
// depth, inside objects and arrays) keeps its names and has every value
// replaced by RedactedValue, whatever its length (SR-2: independent of
// FilterSensitiveData). Every other key, value and the structure stay as
// they are, so unknown arguments remain visible (17.3); maps and slices on
// the path to a masked value are copied and args itself is never changed.
// Args with nothing to mask come back unchanged. The real top-level
// env_overrides parameter must be an object (or null): any other value
// cannot be masked by name, so it is an error. A nested or case-variant
// env_overrides that is not an object is replaced whole.
func RedactEnvOverrides(args map[string]any) (map[string]any, error) {
	if raw, present := args[EnvOverridesArgument]; present {
		if _, err := maskedEnvValues(raw); err != nil {
			return nil, err
		}
	}
	return redactEnvTree(args), nil
}

// redactEnvTree returns args with every env_overrides container masked; see
// RedactEnvOverrides. It never fails: a container that is not an object is
// replaced whole.
func redactEnvTree(args map[string]any) map[string]any {
	masked, _ := redactEnvInMap(args)
	return masked
}

func isEnvOverridesKey(key string) bool {
	return strings.EqualFold(key, EnvOverridesArgument)
}

// redactEnvInMap reports whether anything was masked; when nothing was, the
// input map itself is returned (copy on write).
func redactEnvInMap(in map[string]any) (map[string]any, bool) {
	var out map[string]any
	for key, value := range in {
		replacement, changed := redactEnvEntry(key, value)
		if !changed {
			continue
		}
		if out == nil {
			out = make(map[string]any, len(in))
			for k, v := range in {
				out[k] = v
			}
		}
		out[key] = replacement
	}
	if out == nil {
		return in, false
	}
	return out, true
}

func redactEnvEntry(key string, value any) (any, bool) {
	if !isEnvOverridesKey(key) {
		return redactEnvInValue(value)
	}
	if value == nil {
		return nil, false // an explicit null carries no value to mask
	}
	masked, err := maskedEnvValues(value)
	if err != nil {
		return RedactedValue, true
	}
	return masked, true
}

func redactEnvInValue(value any) (any, bool) {
	switch v := value.(type) {
	case map[string]any:
		return redactEnvInMap(v)
	case []any:
		return redactEnvInSlice(v)
	default:
		return value, false
	}
}

func redactEnvInSlice(in []any) ([]any, bool) {
	var out []any
	for i, item := range in {
		replacement, changed := redactEnvInValue(item)
		if !changed {
			continue
		}
		if out == nil {
			out = make([]any, len(in))
			copy(out, in)
		}
		out[i] = replacement
	}
	if out == nil {
		return in, false
	}
	return out, true
}

func maskedEnvValues(raw any) (any, error) {
	switch env := raw.(type) {
	case nil:
		return raw, nil // an explicit null carries no value to mask
	case map[string]any:
		out := make(map[string]any, len(env))
		for name := range env {
			out[name] = RedactedValue
		}
		return out, nil
	case map[string]string:
		out := make(map[string]any, len(env))
		for name := range env {
			out[name] = RedactedValue
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s must be an object of names to values", EnvOverridesArgument)
	}
}

// ApprovalDisplayArguments is the approval-frame copy of a pending call's
// arguments, live and on reconnect (r17 SR-1, 10.4). The gateway holds no tool
// instance for a pending entry, so it masks by parameter name: every
// env_overrides object, at any depth, has every value replaced, and an
// env_overrides value that is not an object is replaced whole (also at the
// top level, where the record copy would refuse it). The entry's own
// arguments are never changed.
func ApprovalDisplayArguments(args map[string]any) map[string]any {
	if args == nil {
		return map[string]any{}
	}
	return redactEnvTree(args)
}
