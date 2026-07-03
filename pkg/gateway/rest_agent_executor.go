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

// executorRequestInput is a request-shape-agnostic subset of the wire
// Executor object. gen.AgentCreateRequestSubagent3p is the only create
// variant that carries an executor (W1 discriminated union — Main and
// Subagent structurally have no Executor field at all), so createAgent
// normalizes that variant's anonymous Executor struct into this shape before
// building the persisted config.ExecutorConfig.
//
// Kind is intentionally NOT carried here: subagent_3p's executor.kind is
// always server-derived to "external-cli" regardless of what the client
// sent — the field "is exposed in responses but is NOT a writable field on
// create/update — clients cannot choose kind directly"
// (contracts/components/schemas/ExecutorConfig.yaml).
type executorRequestInput struct {
	Cli          string
	CliPath      *string
	EnvOverrides *map[string]string
	CliArgs      *string
}

// executorCliStr dereferences an optional generated CLI enum pointer to its
// string value. Returns "" when the pointer is nil. Verified against the
// generated output (not assumed): Agent.Executor.Cli, AgentUpdateRequest.
// Executor.Cli, and AgentCreateRequestSubagent3p.Executor.Cli all resolve to
// the SAME shared gen.ExternalCliTool type (contracts/components/schemas/
// ExternalCli.yaml, $ref'd — see ExecutorConfig.yaml#/properties/cli) — the
// discriminated union's oneOf variant inlines the surrounding Executor
// *struct* as anonymous, but the $ref'd Cli *field* inside it still points
// at the named shared type, not an anonymous one. Every current call site
// already passes *ExternalCliTool; the generic exists so this helper (and
// the locally-declared mirror struct in setAgentExecutorResponse below)
// keeps working unchanged if a future variant's Cli field ever is inlined
// as a genuinely distinct anonymous type.
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
	// generated for gen.Agent.Executor. This is NOT Go struct literal
	// positional initialisation — every field below is set by name via
	// dot-assignment (exec.Cli = ..., never struct{...}{val1, val2, ...}).
	// The real constraint is Go's struct type-identity rule: `exec`'s
	// declared field names, types, and tags (in the SAME order — struct
	// identity considers field sequence) must match the generated
	// gen.Agent.Executor inline struct for `ag.Executor = &exec` below to
	// type-check at all. If the generated shape ever changes, this
	// assignment fails to COMPILE — a build-time signal, not a silent
	// runtime field mismatch.
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
