// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"errors"
	"maps"
	"slices"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
)

type mcpSetupError struct {
	code    string
	message string
}

func (e *mcpSetupError) Error() string { return e.message }

// readReferenceMap rejects malformed members rather than silently dropping them.
// Only reference names are accepted; existence and consent are checked later.
func readReferenceMap(value any) (map[string]string, error) {
	refs := make(map[string]string)
	switch supplied := value.(type) {
	case map[string]any:
		for key, raw := range supplied {
			ref, valid := raw.(string)
			if !valid || key == "" || ref == "" {
				return nil, errors.New("env_refs must contain non-empty reference names for each environment key")
			}
			refs[key] = ref
		}
	case map[string]string:
		for key, ref := range supplied {
			if key == "" || ref == "" {
				return nil, errors.New("env_refs must contain non-empty reference names for each environment key")
			}
			refs[key] = ref
		}
	default:
		return nil, errors.New("env_refs must be an object of environment keys and stored reference names")
	}
	return refs, nil
}

func mcpStringArgument(args map[string]any, key string) (string, error) {
	raw, supplied := args[key]
	if !supplied {
		return "", nil
	}
	value, valid := raw.(string)
	if !valid {
		return "", &mcpSetupError{code: "INVALID_INPUT", message: key + " must be a string"}
	}
	return value, nil
}

func mcpArguments(value any) ([]string, error) {
	switch supplied := value.(type) {
	case []string:
		return slices.Clone(supplied), nil
	case []any:
		arguments := make([]string, 0, len(supplied))
		for _, raw := range supplied {
			argument, valid := raw.(string)
			if !valid {
				return nil, &mcpSetupError{code: "INVALID_INPUT", message: "args must contain only strings"}
			}
			arguments = append(arguments, argument)
		}
		return arguments, nil
	default:
		return nil, &mcpSetupError{code: "INVALID_INPUT", message: "args must be an array of strings"}
	}
}

func (mat *mcpAddToolExecute) createEntry() (config.MCPServerConfig, error) {
	entry := config.MCPServerConfig{Enabled: true, Type: "stdio"}
	var err error
	if transport, supplied := mat.args["transport"]; supplied {
		var valid bool
		entry.Type, valid = transport.(string)
		if !valid {
			return entry, &mcpSetupError{code: "INVALID_INPUT", message: "transport must be stdio, sse or http"}
		}
	}
	if entry.Command, err = mcpStringArgument(mat.args, "command"); err != nil {
		return entry, err
	}
	if entry.URL, err = mcpStringArgument(mat.args, "url"); err != nil {
		return entry, err
	}
	if raw, supplied := mat.args["args"]; supplied {
		if entry.Args, err = mcpArguments(raw); err != nil {
			return entry, err
		}
	}
	switch entry.Type {
	case "stdio":
		if entry.Command == "" {
			return entry, &mcpSetupError{code: "INVALID_INPUT", message: "command is required for stdio transport"}
		}
	case "sse", "http":
		if entry.URL == "" || !mcpURLSchemeValid(entry.URL) {
			return entry, &mcpSetupError{code: "INVALID_INPUT", message: "url must use https, or http for loopback addresses only"}
		}
	default:
		return entry, &mcpSetupError{code: "INVALID_INPUT", message: "transport must be stdio, sse or http"}
	}
	return entry, nil
}

// checkAttachmentDescriptor treats omission as unchanged, not a create default.
func (mat *mcpAddToolExecute) checkAttachmentDescriptor(entry config.MCPServerConfig) error {
	for key, expected := range map[string]string{"transport": mcpEffectiveTransport(entry), "command": entry.Command, "url": entry.URL} {
		if _, supplied := mat.args[key]; !supplied {
			continue
		}
		actual, err := mcpStringArgument(mat.args, key)
		if err != nil {
			return err
		}
		if actual != expected {
			return &mcpSetupError{code: "INVALID_INPUT", message: "reference attachment cannot change the saved MCP recipient descriptor"}
		}
	}
	if raw, supplied := mat.args["args"]; supplied {
		arguments, err := mcpArguments(raw)
		if err != nil {
			return err
		}
		if !slices.Equal(arguments, entry.Args) {
			return &mcpSetupError{code: "INVALID_INPUT", message: "reference attachment cannot change the saved MCP recipient arguments"}
		}
	}
	return nil
}

// boundEntry checks the complete candidate before changing any shared map.
func (mat *mcpAddToolExecute) boundEntry(entry config.MCPServerConfig) (config.MCPServerConfig, error) {
	candidate := &config.Config{}
	candidate.Tools.MCP.Servers = map[string]config.MCPServerConfig{mat.name: entry}
	for _, key := range sortedStringKeys(mat.envRefs) {
		recipients, _, _, err := secretrequest.ResolveRecipient(candidate, secretrequest.Input{Server: mat.name, EnvKey: key})
		if err != nil || len(recipients) != 1 {
			return entry, &referenceSelectionError{err: errors.New("MCP reference recipient could not be resolved; request a secret for the saved stdio server and environment key")}
		}
		if err := checkSecretReference(mat.t.deps.CredStore, mat.envRefs[key], recipients[0]); err != nil {
			return entry, err
		}
	}
	entry.EnvRefs = maps.Clone(entry.EnvRefs)
	if len(mat.envRefs) > 0 && entry.EnvRefs == nil {
		entry.EnvRefs = make(map[string]string, len(mat.envRefs))
	}
	for key, ref := range mat.envRefs {
		entry.EnvRefs[key] = ref
	}
	return entry, nil
}
