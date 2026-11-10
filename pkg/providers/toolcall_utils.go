// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"encoding/json"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// NormalizeToolCall normalizes a ToolCall to ensure all fields are properly populated.
// It handles cases where Name/Arguments might be in different locations (top-level vs Function)
// and ensures both are populated consistently.
func NormalizeToolCall(tc ToolCall) ToolCall {
	normalized := tc

	// Ensure Name is populated from Function if not set
	if normalized.Name == "" && normalized.Function != nil {
		normalized.Name = normalized.Function.Name
	}

	// Ensure Arguments is not nil
	if normalized.Arguments == nil {
		normalized.Arguments = map[string]any{}
	}

	// Parse Arguments from Function.Arguments if not already set.
	//
	// This is the last decode before dispatch, and it used to be the quietest:
	// the unmarshal error was discarded with `err == nil &&`, so an
	// undecodable payload left Arguments as the empty map while
	// Function.Arguments kept the fragment. The tool was then dispatched with
	// no parameters and failed downstream on a schema complaint that named the
	// wrong cause.
	//
	// The decode now runs through the one canonical decoder and REPORTS. A
	// failure here cannot return an error — NormalizeToolCall's signature is
	// fixed by its callers in the agent loop — so it logs at Error and leaves
	// Arguments empty, which is the same conservative value as before but no
	// longer silent. In practice the providers refuse such a call before it
	// ever reaches normalisation; this path firing at all means a decode site
	// was added that bypasses common.DecodeToolCallArguments.
	if len(normalized.Arguments) == 0 && normalized.Function != nil && normalized.Function.Arguments != "" {
		parsed, err := common.DecodeToolCallArguments(
			json.RawMessage(normalized.Function.Arguments), normalized.Name,
		)
		if err != nil {
			logger.ErrorCF(
				"providers",
				"tool call arguments reached normalisation undecodable; "+
					"dispatching with no arguments (a provider decode site is bypassing the canonical decoder)",
				map[string]any{"tool": normalized.Name, "error": err.Error()},
			)
		} else if parsed != nil {
			normalized.Arguments = parsed
		}
	}

	// Ensure Function is populated with consistent values
	argsJSON, err := json.Marshal(normalized.Arguments)
	if err != nil {
		logger.WarnCF(
			"providers",
			"failed to marshal normalized tool arguments",
			map[string]any{"tool": normalized.Name, "error": err.Error()},
		)
		argsJSON = []byte("{}")
	}
	if normalized.Function == nil {
		normalized.Function = &FunctionCall{
			Name:      normalized.Name,
			Arguments: string(argsJSON),
		}
	} else {
		if normalized.Function.Name == "" {
			normalized.Function.Name = normalized.Name
		}
		if normalized.Name == "" {
			normalized.Name = normalized.Function.Name
		}
		if normalized.Function.Arguments == "" {
			normalized.Function.Arguments = string(argsJSON)
		}
	}

	return normalized
}
