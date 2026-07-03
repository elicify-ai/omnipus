//go:build !cgo

package gateway

import (
	"fmt"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// rest_agent_executor.go — mapping helpers for the sub-agent Executor wire field.
//
// The Executor (kind/cli/cli_path/env_overrides/cli_args) is part of the
// AgentCreateRequest / AgentUpdateRequest / Agent generated contract types.
// W3 (agent-form-requirements) extended this surface with cli_path,
// env_overrides, and cli_args; this file is the single source-of-truth for
// mapping between the wire shape and config.AgentConfig.Subagents.Executor.

// validExecutorCLIs is the set of external CLI names accepted for
// kind="external-cli". It matches the gen.ExternalCliTool enum and
// runner.SupportedCLIs(). Kept here (not imported from runner) so this gateway
// validation has no dependency on the runner package's internal map.
var validExecutorCLIs = map[string]bool{
	"claude-code": true,
	"codex":       true,
	"opencode":    true,
}

// executorCliStr dereferences an optional generated CLI enum pointer to its string
// value. Returns "" when the pointer is nil. Agent.Executor.Cli,
// AgentCreateRequest.Executor.Cli, and AgentUpdateRequest.Executor.Cli all
// resolve to the SAME shared gen.ExternalCliTool type (contracts/components/
// schemas/ExternalCli.yaml, $ref'd — see ExecutorConfig.yaml#/properties/cli),
// so this generic only exists to also accept the locally-declared mirror
// struct's *gen.ExternalCliTool field below without a second helper.
func executorCliStr[T ~string](cli *T) string {
	if cli == nil {
		return ""
	}
	return string(*cli)
}

// executorKindStr dereferences an optional generated ExecutorKind pointer to its
// string value. Returns "" when the pointer is nil. Used by the create/update
// handlers where Executor.Kind is *ExecutorKind (the schema marks kind as
// optional + derived, so the pointer may be nil on the wire).
func executorKindStr[T ~string](kind *T) string {
	if kind == nil {
		return ""
	}
	return string(*kind)
}

// executorKindPtr returns a pointer to the given gen.AgentExecutorKind. Used
// when populating the response-side gen.Agent.Executor struct, where Kind is
// `*AgentExecutorKind` because the field is derived and may be omitted when
// empty.
func executorKindPtr(k gen.ExecutorConfigKind) *gen.AgentExecutorKind {
	v := gen.AgentExecutorKind(k)
	return &v
}

// executorConfigFromRequest validates a request executor (kind + cli) and converts
// it to a *config.ExecutorConfig.
//
// Returns:
//   - (nil, "")            when the request resolves to native with no CLI (the
//     default — the caller should clear/omit Subagents.Executor).
//   - (*ExecutorConfig, "") for a valid non-native executor.
//   - (nil, "<errMsg>")     for an invalid kind or a missing/invalid cli on
//     kind="external-cli". The caller returns 400 with errMsg.
//
// Validation rules:
//   - kind must be one of "native" / "external-cli" / "remote-a2a" (the schema
//     enum). "remote-a2a" is accepted-but-reserved: it is persisted so the contract
//     round-trips, but dispatch rejects it (runner.ResolveDispatch).
//   - kind="external-cli" REQUIRES a cli in {claude-code, codex, opencode}.
//   - cli is ignored (and dropped) for non-external kinds.
func executorConfigFromRequest(kind, cli string) (*config.ExecutorConfig, string) {
	switch config.ExecutorKind(kind) {
	case "", config.ExecutorKindNative:
		// Native (explicit or defaulted): no executor config to persist. The cli
		// field, if any, is ignored for native.
		return nil, ""
	case config.ExecutorKindExternalCLI:
		if cli == "" {
			return nil, "executor.cli is required when executor.kind is \"external-cli\" (one of: claude-code, codex, opencode)"
		}
		if !validExecutorCLIs[cli] {
			return nil, fmt.Sprintf("executor.cli %q is not supported (valid: claude-code, codex, opencode)", cli)
		}
		return &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: cli}, ""
	case config.ExecutorKindRemoteA2A:
		// Reserved: accepted in the schema for forward-compatibility, rejected at
		// dispatch. Persist it so the contract round-trips; cli is ignored.
		return &config.ExecutorConfig{Kind: config.ExecutorKindRemoteA2A}, ""
	default:
		return nil, fmt.Sprintf("executor.kind %q is invalid (valid: native, external-cli, remote-a2a)", kind)
	}
}

// executorConfigToMap serializes a *config.ExecutorConfig into the JSON-map shape
// written under agents.list[*].subagents.executor by the safeUpdateConfigJSON
// writers. cli is omitted when empty (matches the `omitempty` on the Go struct).
// cli_path, env_overrides, cli_args are similarly omitted when empty.
func executorConfigToMap(ec *config.ExecutorConfig) map[string]any {
	if ec == nil {
		return nil
	}
	m := map[string]any{"kind": string(ec.EffectiveKind())}
	if ec.CLI != "" {
		m["cli"] = ec.CLI
	}
	if ec.CLIPath != "" {
		m["cli_path"] = ec.CLIPath
	}
	if len(ec.EnvOverrides) > 0 {
		m["env_overrides"] = ec.EnvOverrides
	}
	if ec.CLIArgs != "" {
		m["cli_args"] = ec.CLIArgs
	}
	return m
}

// setAgentExecutorResponse populates the generated Agent.Executor response field
// from the persisted config.SubagentsConfig so a GET (and the create/update echo)
// reflects the stored executor. When the agent has no executor configured the field
// is left nil (omitted) — a GET→edit→PUT round-trip then preserves "native".
func setAgentExecutorResponse(ag *gen.Agent, sub *config.SubagentsConfig) {
	if sub == nil || sub.Executor == nil {
		return
	}
	ec := sub.Executor
	// The literal below mirrors the inlined anonymous-struct shape oapi-codegen
	// generated for gen.Agent.Executor. Fields must be declared in the SAME
	// ORDER they appear in the generated struct (Cli, CliArgs, CliPath,
	// EnvOverrides, Kind) — Go struct literal positional initialisation.
	exec := struct { // not-wire-format: generated gen.Agent.Executor inline shape, only populates the generated field
		Cli          *gen.ExternalCliTool   `json:"cli,omitempty"`
		CliArgs      *string                `json:"cli_args,omitempty"`
		CliPath      *string                `json:"cli_path,omitempty"`
		EnvOverrides *map[string]string     `json:"env_overrides,omitempty"`
		Kind         *gen.AgentExecutorKind `json:"kind,omitempty"`
	}{}
	if ec.CLI != "" {
		cli := gen.ExternalCliTool(ec.CLI)
		exec.Cli = &cli
	}
	if ec.CLIPath != "" {
		cp := ec.CLIPath
		exec.CliPath = &cp
	}
	if len(ec.EnvOverrides) > 0 {
		eo := ec.EnvOverrides
		exec.EnvOverrides = &eo
	}
	if ec.CLIArgs != "" {
		ca := ec.CLIArgs
		exec.CliArgs = &ca
	}
	kind := gen.ExecutorConfigKind(ec.EffectiveKind())
	exec.Kind = executorKindPtr(kind)
	ag.Executor = &exec
}
