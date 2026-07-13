//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// agent_field_rules.go — the single source of truth for which
// AgentUpdateRequest fields a subagent_3p (External CLI) agent is forbidden
// from setting on PUT.
//
// Create-time enforcement lives in createAgent (rest.go), not here: the wire
// contract is a discriminated union — AgentCreateRequestMain /
// AgentCreateRequestSubagent / AgentCreateRequestSubagent3p
// (contracts/components/schemas/AgentCreateRequest*.yaml, each
// additionalProperties: false). AgentCreateRequestSubagent3p structurally has
// no tools_cfg / skills / fallback_models / model_params /
// shell_policy / max_tool_iterations property at all, and createAgent's
// per-variant decode (decodeAgentCreateVariant) always runs a
// json.Decoder with DisallowUnknownFields — so a caller who sends one of
// these fields on a subagent_3p create gets a 400 naming the offending
// field, unconditionally (independent of cfg.Gateway.ValidateInbound, which
// defaults to false). ValidateInbound adds a second, richer validation layer
// (the field's variant schema, compiled from the .yaml above) ahead of the
// strict decode when enabled — the two layers agree on which fields are
// forbidden; they differ only in error-message detail.
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
	"shell_policy",
	"max_tool_iterations",
}

// subagent3pCreateOnlyExemptFields documents wire fields that are present on
// gen.AgentCreateRequestMain but structurally absent from
// gen.AgentCreateRequestSubagent3p WITHOUT being covered by
// subagent3pForbiddenUpdateFields above — i.e. fields excluded from
// subagent_3p create by some mechanism OTHER than the PUT forbidden-field
// list. TestSubagent3pCreateVsUpdateForbiddenFieldsDrift
// (agent_field_rules_test.go) asserts every Main-but-not-3p field is
// accounted for by exactly one of the two mechanisms (this map or
// subagent3pForbiddenUpdateFields), so a newly-added field that falls
// through both is caught by CI rather than silently unenforced. Each entry's
// value names the mechanism that actually excludes it, so an addition here
// is a deliberate, reviewable decision rather than a silent carve-out.
var subagent3pCreateOnlyExemptFields = map[string]string{
	// voice is Main-only on create (structurally absent from both
	// AgentCreateRequestSubagent and AgentCreateRequestSubagent3p). On PUT it
	// is rejected for ANY worker — not specifically subagent_3p — by the
	// unconditional "a worker cannot have a per-agent voice" guard in
	// updateAgent, so it never needed a place in subagent3pForbiddenUpdateFields.
	"voice": "rejected for every worker (not subagent_3p-specific) by updateAgent's unconditional worker-voice guard",
	// steering_mode is Main-only in intent (the server forces
	// "one-at-a-time" for every worker). On PUT it is silently overridden,
	// not 400-rejected, by updateAgent — so it is neither a PUT-forbidden
	// field nor structurally worker-exclusive; it just never appears on the
	// 3p create variant.
	"steering_mode": "silently forced to one-at-a-time for every worker by updateAgent, never a PUT-forbidden field",
	// executor is 3p-REQUIRED (present on AgentCreateRequestSubagent3p, not
	// Main) — it does not actually appear in the Main-but-not-3p diff this
	// test walks, but is documented here defensively: its per-field
	// mutability (cli locked after create; cli_path/env_overrides/cli_args
	// mutable) is guarded separately in updateAgent / rest_agent_executor.go,
	// not by subagent3pForbiddenUpdateFields.
	"executor": "3p-required; per-field mutability guarded separately in rest_agent_executor.go",
}

// firstForbiddenSubagent3pField returns the first forbidden field supplied on
// an AgentUpdateRequest PUT to a subagent_3p (External CLI) agent, or ("",
// false) when none are set. Fields NOT in this list — model, provider,
// timeout_seconds, color, icon, description, name, voice(*), steering_mode(*),
// rate_limits, executor (cli_path/env_overrides/cli_args only — cli is
// immutable after create, enforced separately), default, updated_at — are
// valid on a subagent_3p PUT.
//
// delegation_policy is NOT in this list, and is deliberately not this
// function's concern: ADR-037 retired it from the wire entirely, so it is
// now rejected 400 on EVERY agent type's PUT (not subagent_3p-specific) by
// updateAgent's raw-body sniff in rest.go, ahead of decode — a third
// mechanism alongside subagent3pForbiddenUpdateFields and
// subagent3pCreateOnlyExemptFields, scoped to "retired wire field" rather
// than "CLI-owned, worker-specific field."
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
	if req.ShellPolicy != nil {
		return "shell_policy", true
	}
	if req.MaxToolIterations != nil {
		return "max_tool_iterations", true
	}
	return "", false
}
