//go:build !cgo

package gateway

import (
	"fmt"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// rest_agent_executor.go — mapping helpers for the sub-agent Executor wire field.
//
// The Executor (kind/cli) is part of the AgentCreateRequest / AgentUpdateRequest /
// Agent generated contract types but was previously never mapped to
// config.AgentConfig.Subagents.Executor — so the field was write-dropped on
// create/update and never echoed on GET. These helpers do the mapping in one place
// for createAgent, updateAgent, getAgent, and listAgents.

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

// executorConfigToMap serializes a *config.ExecutorConfig into the JSON-map shape
// written under agents.list[*].subagents.executor by the safeUpdateConfigJSON
// writers. cli is omitted when empty (matches the `omitempty` on the Go struct).
func executorConfigToMap(ec *config.ExecutorConfig) map[string]any {
	m := map[string]any{"kind": string(ec.Kind)}
	if ec.CLI != "" {
		m["cli"] = ec.CLI
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
		Cli  *gen.AgentExecutorCli `json:"cli,omitempty"`
		Kind gen.AgentExecutorKind `json:"kind"`
	}{
		Kind: gen.AgentExecutorKind(ec.EffectiveKind()),
	}
	if ec.CLI != "" {
		cli := gen.AgentExecutorCli(ec.CLI)
		exec.Cli = &cli
	}
	ag.Executor = &exec
}
