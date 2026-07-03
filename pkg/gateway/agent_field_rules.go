//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
)

// agent_field_rules.go — the single source of truth for which
// AgentUpdateRequest fields a subagent_3p (External CLI) agent is forbidden
// from setting on PUT.
//
// Create-time no longer needs an equivalent runtime check: the
// discriminated-union wire contract (W1) split AgentCreateRequest into three
// named variants — AgentCreateRequestMain / AgentCreateRequestSubagent /
// AgentCreateRequestSubagent3p (contracts/components/schemas/AgentCreateRequest*.yaml,
// each additionalProperties: false). AgentCreateRequestSubagent3p structurally
// has no tools_cfg / skills / fallback_models / model_params / sandbox_profile /
// shell_policy / max_tool_iterations property at all — a caller cannot even
// construct a JSON body that would trip this check at create, schema
// validation or not (docs/internal/architecture/agent-types-field-matrix.md).
//
// PUT still needs the runtime guard below because AgentUpdateRequest remains
// ONE flat type shared by every agent type (Main, Subagent, subagent_3p,
// core) — the union split is create-only.

// subagent3pForbiddenUpdateFields documents, by JSON field name, every
// AgentUpdateRequest property a subagent_3p (External CLI) agent rejects on
// PUT (400 "subagent_3p agents do not support <field>; this is fixed at
// create time."). The external runner (claude-code / codex / opencode)
// manages its own tool loop, isolation, retries, and per-turn budget, so
// these fields are CLI-owned and cannot be tuned through Omnipus at runtime.
//
// This slice is the single source of truth for "does this PUT field make
// sense on an external-CLI worker" — TestSubagent3pForbiddenFieldsDrift
// (agent_field_rules_test.go) walks every gen.AgentUpdateRequest field via
// reflection and asserts membership here matches firstForbiddenSubagent3pField's
// actual verdict, so adding a field to one without the other fails CI.
var subagent3pForbiddenUpdateFields = []string{
	"tools_cfg",
	"skills",
	"fallback_models",
	"model_params",
	"sandbox_profile",
	"shell_policy",
	"max_tool_iterations",
}

// firstForbiddenSubagent3pField returns the first forbidden field supplied on
// an AgentUpdateRequest PUT to a subagent_3p (External CLI) agent, or ("",
// false) when none are set. Fields NOT in this list — model, provider,
// timeout_seconds, color, icon, description, name, voice(*), steering_mode(*),
// rate_limits, executor (cli_path/env_overrides/cli_args only — cli is
// immutable after create, enforced separately), delegation_policy, default,
// updated_at — are valid on a subagent_3p PUT.
//
// (*) voice and steering_mode are ALSO rejected for any worker (Subagent or
// subagent_3p) by dedicated checks elsewhere in updateAgent — not because
// they are CLI-owned, but because workers are not chat personas. They are
// intentionally absent from this list to keep it scoped to "fields the
// external runner owns".
func firstForbiddenSubagent3pField(req *gen.AgentUpdateRequest) (string, bool) {
	if req == nil {
		return "", false
	}
	if req.ToolsCfg != nil {
		return "tools_cfg", true
	}
	if req.Skills != nil {
		return "skills", true
	}
	if req.FallbackModels != nil {
		return "fallback_models", true
	}
	if req.ModelParams != nil {
		return "model_params", true
	}
	if req.SandboxProfile != nil {
		return "sandbox_profile", true
	}
	if req.ShellPolicy != nil {
		return "shell_policy", true
	}
	if req.MaxToolIterations != nil {
		return "max_tool_iterations", true
	}
	return "", false
}
