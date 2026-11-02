// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import "fmt"

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

// RedactEnvOverrides is the shared redaction of the agent writers: a shallow
// copy of args whose env_overrides object keeps its names and has every value
// replaced by RedactedValue, whatever its length (SR-2: independent of
// FilterSensitiveData). Absent env_overrides needs no masking. A present value
// that is not an object cannot be masked by name, so it is an error.
func RedactEnvOverrides(args map[string]any) (map[string]any, error) {
	raw, present := args[EnvOverridesArgument]
	if !present {
		return args, nil
	}
	masked, err := maskedEnvValues(raw)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(args))
	for key, value := range args {
		out[key] = value
	}
	out[EnvOverridesArgument] = masked
	return out, nil
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
// instance for a pending entry, so it masks by parameter name: any
// env_overrides object has every value replaced. A value that is not an
// object is replaced whole. The entry's own arguments are never changed.
func ApprovalDisplayArguments(args map[string]any) map[string]any {
	if args == nil {
		return map[string]any{}
	}
	masked, err := RedactEnvOverrides(args)
	if err == nil {
		return masked
	}
	out := make(map[string]any, len(args))
	for key, value := range args {
		out[key] = value
	}
	out[EnvOverridesArgument] = RedactedValue
	return out
}
