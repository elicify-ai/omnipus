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
// kind="external-cli". It matches the AgentExecutorCli enum and
// runner.SupportedCLIs(). Kept here (not imported from runner) so this gateway
// validation has no dependency on the runner package's internal map.
var validExecutorCLIs = map[string]bool{
	"claude-code": true,
	"codex":       true,
	"opencode":    true,
}

// executorCliStr dereferences an optional generated CLI enum pointer to its string
// value. Returns "" when the pointer is nil. The concrete pointer type differs per
// request (AgentCreateRequestExecutorCli / AgentUpdateRequestExecutorCli /
// AgentExecutorCli) so callers pass the already-stringified value.
func executorCliStr[T ~string](cli *T) string {
	if cli == nil {
		return ""
	}
	return string(*cli)
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

// executorConfigUpdate reconciles a request executor with the agent's persisted
// executor, applying the cli-lock rule (agent-form spec §4.16 / W3 F-10):
//   - cli is LOCKED after create. Any attempt to mutate it on PUT returns 400
//     with "executor.cli is locked after create; create a new agent to switch CLIs."
//   - cli_path IS mutable on PUT (allows binary upgrades without re-creating).
//   - env_overrides and cli_args are fully mutable.
//
// Returns (updated *config.ExecutorConfig, ""), or (nil, errMsg) on lock violation.
func executorConfigUpdate(existing *config.ExecutorConfig, req *struct {
	Kind        string
	CLI         string
	CLIPath     string
	EnvOver     map[string]string
	CLIArgs     string
	HasCLIPath  bool
	HasEnvOver  bool
	HasCLIArgs  bool
}) (*config.ExecutorConfig, string) {
	// Lock check: if the persisted agent has an executor.cli and the request
	// carries a different cli value, reject 400.
	if existing != nil && existing.CLI != "" {
		if req.CLI != "" && req.CLI != existing.CLI {
			return nil, "executor.cli is locked after create; create a new agent to switch CLIs."
		}
	}

	// Start from the persisted executor (may be nil for a previously-native agent).
	var out *config.ExecutorConfig
	if existing != nil {
		cp := *existing
		out = &cp
	} else {
		out = &config.ExecutorConfig{}
	}

	// Apply kind/cli if the request sent them.
	if req.Kind != "" {
		out.Kind = config.ExecutorKind(req.Kind)
	}
	if req.CLI != "" {
		out.CLI = req.CLI
	}
	if req.HasCLIPath {
		out.CLIPath = req.CLIPath
	}
	if req.HasEnvOver {
		out.EnvOverrides = req.EnvOver
	}
	if req.HasCLIArgs {
		out.CLIArgs = req.CLIArgs
	}

	// Native with no executor surface → nil (callers persist nothing).
	if out.Kind == "" || out.Kind == config.ExecutorKindNative {
		if out.CLI == "" && out.CLIPath == "" && len(out.EnvOverrides) == 0 && out.CLIArgs == "" {
			return nil, ""
		}
	}
	return out, ""
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
	// generated for gen.Agent.Executor (the contract emits it inline, not as a named
	// schema), so the assignment target type must be spelled verbatim here.
	exec := struct { // not-wire-format: generated gen.Agent.Executor inline shape, only populates the generated field
		Cli          *gen.AgentExecutorCli     `json:"cli,omitempty"`
		Kind         gen.AgentExecutorKind     `json:"kind"`
		CliPath      *string                   `json:"cli_path,omitempty"`
		EnvOverrides *map[string]string        `json:"env_overrides,omitempty"`
		CliArgs      *string                   `json:"cli_args,omitempty"`
	}{
		Kind: gen.AgentExecutorKind(ec.EffectiveKind()),
	}
	if ec.CLI != "" {
		cli := gen.AgentExecutorCli(ec.CLI)
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
	ag.Executor = &exec
}
