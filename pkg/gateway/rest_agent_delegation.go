//go:build !cgo

package gateway

import (
	"fmt"
	"strings"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// rest_agent_delegation.go — mapping/validation helpers for the agent
// delegation_policy wire field.
//
// delegation_policy is part of the AgentCreateRequestMain /
// AgentCreateRequestSubagent / AgentCreateRequestSubagent3p /
// AgentUpdateRequest / Agent generated contract types. These helpers map it
// between the wire request/response shapes and config.AgentConfig.DelegationPolicy
// in one place for createAgent, updateAgent, getAgent, and listAgents.
//
// The create-time wire contract is a discriminated union: three named
// variant types, each with its own anonymous DelegationPolicy struct
// (oapi-codegen inlines it per-usage). delegation_policy is allowed on ALL
// THREE variants, including subagent_3p (a subagent_3p worker is a valid
// delegation target), so createAgent needs one extractor per variant —
// delegationInputFromCreateRequestMain / …Subagent / …Subagent3p below —
// all normalizing into the same variant-agnostic delegationPolicyInput so
// buildDelegationPolicy is written once.
//
// v0.1.0 semantics (Spec-3):
//   - to / modes / depth are ENFORCED.
//   - accept_from / budget are INERT (present but not enforced) — the editor
//     does NOT send them. They are preserved across an update so a future
//     enforcement release does not lose seeded values (NFR-7 / M-6).

// delegationDepthCeilingFallback mirrors agent.defaultMaxSubTurnDepth (the
// subturn depth cap, default 3) for the case where the live config does not set
// agents.defaults.subturn.max_depth. The agent package's constant is unexported
// and pkg/gateway must not import pkg/agent's internals, so the ceiling is taken
// from the configured SubTurn.MaxDepth (see delegationDepthCeiling) and only
// falls back to this literal when that knob is unset (<= 0) — the exact same
// fallback the agent loop applies in getSubTurnConfig.
const delegationDepthCeilingFallback = 3

// delegationDepthCeiling returns the effective maximum delegation chain depth a
// caller may request, reusing the global subturn depth cap rather than inventing
// a new constant. It tracks getSubTurnConfig: the configured
// agents.defaults.subturn.max_depth when set (> 0), else the default of 3.
func delegationDepthCeiling(cfg *config.Config) int {
	if cfg != nil && cfg.Agents.Defaults.SubTurn.MaxDepth > 0 {
		return cfg.Agents.Defaults.SubTurn.MaxDepth
	}
	return delegationDepthCeilingFallback
}

// delegationRefInput is a request-shape-agnostic agent reference (kind + id).
type delegationRefInput struct {
	Kind string
	ID   string
}

// delegationPolicyInput is the request-shape-agnostic delegation policy decoded
// from either AgentCreateRequest or AgentUpdateRequest. The oapi-codegen inline
// anonymous structs are distinct named types per request, so callers normalise
// into this common shape via delegationInputFrom{Create,Update}Request before
// validation/mapping. Pointers preserve "field absent" (nil) vs "explicitly
// empty" (non-nil, len 0) so deny-all semantics survive.
type delegationPolicyInput struct {
	To         *[]delegationRefInput
	AcceptFrom *[]delegationRefInput
	Modes      *[]string
	Depth      *int
	HasBudget  bool
	Budget     *config.DelegationBudget
}

// delegationInputFromCreateRequestMain normalises the AgentCreateRequestMain
// inline policy struct into delegationPolicyInput. Returns nil when req
// carries no policy.
//
//nolint:dupl // parallel to …Subagent / …Subagent3p / delegationInputFromUpdateRequest — distinct oapi-codegen inline types per variant, intentionally not merged.
func delegationInputFromCreateRequestMain(req *gen.AgentCreateRequestMain) *delegationPolicyInput {
	if req == nil || req.DelegationPolicy == nil {
		return nil
	}
	dp := req.DelegationPolicy
	out := &delegationPolicyInput{Depth: dp.Depth}
	if dp.To != nil {
		refs := make([]delegationRefInput, 0, len(*dp.To))
		for _, r := range *dp.To {
			refs = append(refs, delegationRefInput{Kind: string(r.Kind), ID: r.Id})
		}
		out.To = &refs
	}
	if dp.AcceptFrom != nil {
		refs := make([]delegationRefInput, 0, len(*dp.AcceptFrom))
		for _, r := range *dp.AcceptFrom {
			refs = append(refs, delegationRefInput{Kind: string(r.Kind), ID: r.Id})
		}
		out.AcceptFrom = &refs
	}
	if dp.Modes != nil {
		modes := make([]string, 0, len(*dp.Modes))
		for _, m := range *dp.Modes {
			modes = append(modes, string(m))
		}
		out.Modes = &modes
	}
	if dp.Budget != nil {
		out.HasBudget = true
		out.Budget = &config.DelegationBudget{}
		if dp.Budget.MaxCostUsd != nil {
			out.Budget.MaxCostUSD = *dp.Budget.MaxCostUsd
		}
		if dp.Budget.MaxTokens != nil {
			out.Budget.MaxTokens = *dp.Budget.MaxTokens
		}
	}
	return out
}

// delegationInputFromCreateRequestSubagent normalises the
// AgentCreateRequestSubagent inline policy struct into delegationPolicyInput.
// Returns nil when req carries no policy.
//
//nolint:dupl // parallel to …Main / …Subagent3p / delegationInputFromUpdateRequest — distinct oapi-codegen inline types per variant, intentionally not merged.
func delegationInputFromCreateRequestSubagent(req *gen.AgentCreateRequestSubagent) *delegationPolicyInput {
	if req == nil || req.DelegationPolicy == nil {
		return nil
	}
	dp := req.DelegationPolicy
	out := &delegationPolicyInput{Depth: dp.Depth}
	if dp.To != nil {
		refs := make([]delegationRefInput, 0, len(*dp.To))
		for _, r := range *dp.To {
			refs = append(refs, delegationRefInput{Kind: string(r.Kind), ID: r.Id})
		}
		out.To = &refs
	}
	if dp.AcceptFrom != nil {
		refs := make([]delegationRefInput, 0, len(*dp.AcceptFrom))
		for _, r := range *dp.AcceptFrom {
			refs = append(refs, delegationRefInput{Kind: string(r.Kind), ID: r.Id})
		}
		out.AcceptFrom = &refs
	}
	if dp.Modes != nil {
		modes := make([]string, 0, len(*dp.Modes))
		for _, m := range *dp.Modes {
			modes = append(modes, string(m))
		}
		out.Modes = &modes
	}
	if dp.Budget != nil {
		out.HasBudget = true
		out.Budget = &config.DelegationBudget{}
		if dp.Budget.MaxCostUsd != nil {
			out.Budget.MaxCostUSD = *dp.Budget.MaxCostUsd
		}
		if dp.Budget.MaxTokens != nil {
			out.Budget.MaxTokens = *dp.Budget.MaxTokens
		}
	}
	return out
}

// delegationInputFromCreateRequestSubagent3p normalises the
// AgentCreateRequestSubagent3p inline policy struct into delegationPolicyInput.
// Returns nil when req carries no policy. delegation_policy is allowed on
// this variant — a subagent_3p worker is a valid delegation *target* (see
// the field matrix "delegation_policy / can_delegate_to" row).
//
//nolint:dupl // parallel to …Main / …Subagent / delegationInputFromUpdateRequest — distinct oapi-codegen inline types per variant, intentionally not merged.
func delegationInputFromCreateRequestSubagent3p(req *gen.AgentCreateRequestSubagent3p) *delegationPolicyInput {
	if req == nil || req.DelegationPolicy == nil {
		return nil
	}
	dp := req.DelegationPolicy
	out := &delegationPolicyInput{Depth: dp.Depth}
	if dp.To != nil {
		refs := make([]delegationRefInput, 0, len(*dp.To))
		for _, r := range *dp.To {
			refs = append(refs, delegationRefInput{Kind: string(r.Kind), ID: r.Id})
		}
		out.To = &refs
	}
	if dp.AcceptFrom != nil {
		refs := make([]delegationRefInput, 0, len(*dp.AcceptFrom))
		for _, r := range *dp.AcceptFrom {
			refs = append(refs, delegationRefInput{Kind: string(r.Kind), ID: r.Id})
		}
		out.AcceptFrom = &refs
	}
	if dp.Modes != nil {
		modes := make([]string, 0, len(*dp.Modes))
		for _, m := range *dp.Modes {
			modes = append(modes, string(m))
		}
		out.Modes = &modes
	}
	if dp.Budget != nil {
		out.HasBudget = true
		out.Budget = &config.DelegationBudget{}
		if dp.Budget.MaxCostUsd != nil {
			out.Budget.MaxCostUSD = *dp.Budget.MaxCostUsd
		}
		if dp.Budget.MaxTokens != nil {
			out.Budget.MaxTokens = *dp.Budget.MaxTokens
		}
	}
	return out
}

// delegationInputFromUpdateRequest normalises the update-request inline policy
// struct into delegationPolicyInput. Returns nil when req carries no policy.
//
//nolint:dupl // parallel to delegationInputFromCreateRequest — distinct oapi-codegen inline types (Create vs Update), intentionally not merged.
func delegationInputFromUpdateRequest(req *gen.AgentUpdateRequest) *delegationPolicyInput {
	if req == nil || req.DelegationPolicy == nil {
		return nil
	}
	dp := req.DelegationPolicy
	out := &delegationPolicyInput{Depth: dp.Depth}
	if dp.To != nil {
		refs := make([]delegationRefInput, 0, len(*dp.To))
		for _, r := range *dp.To {
			refs = append(refs, delegationRefInput{Kind: string(r.Kind), ID: r.Id})
		}
		out.To = &refs
	}
	if dp.AcceptFrom != nil {
		refs := make([]delegationRefInput, 0, len(*dp.AcceptFrom))
		for _, r := range *dp.AcceptFrom {
			refs = append(refs, delegationRefInput{Kind: string(r.Kind), ID: r.Id})
		}
		out.AcceptFrom = &refs
	}
	if dp.Modes != nil {
		modes := make([]string, 0, len(*dp.Modes))
		for _, m := range *dp.Modes {
			modes = append(modes, string(m))
		}
		out.Modes = &modes
	}
	if dp.Budget != nil {
		out.HasBudget = true
		out.Budget = &config.DelegationBudget{}
		if dp.Budget.MaxCostUsd != nil {
			out.Budget.MaxCostUSD = *dp.Budget.MaxCostUsd
		}
		if dp.Budget.MaxTokens != nil {
			out.Budget.MaxTokens = *dp.Budget.MaxTokens
		}
	}
	return out
}

// normalizeRefKind canonicalises an AgentRef kind the same way config.AgentRef
// validation does (lowercase + trim). An empty kind defaults to "local" (matching
// ResolveDelegationTo, which treats an absent kind as local).
func normalizeRefKind(kind string) string {
	k := strings.ToLower(strings.TrimSpace(kind))
	if k == "" {
		return config.AgentRefKindLocal
	}
	return k
}

// existingLocalRefSet builds a set of "kind:id" keys for the local refs already
// stored on an agent's delegation policy. Used to grandfather pre-existing to[]
// refs: a ref that is unchanged from the stored policy is NOT re-validated
// against the roster, so a dangling target (the agent it pointed at was deleted)
// does not brick every future edit to the pointing agent. Only NEWLY added refs
// are roster-checked. A stale ref can still be removed; it just no longer blocks
// unrelated edits.
func existingLocalRefSet(existing *config.DelegationPolicy) map[string]bool {
	out := map[string]bool{}
	if existing == nil {
		return out
	}
	for _, r := range existing.To {
		kind := normalizeRefKind(r.Kind)
		id := strings.TrimSpace(r.ID)
		out[kind+":"+id] = true
	}
	return out
}

// buildDelegationPolicy validates a normalised delegation policy input against
// the roster and maps it to the canonical config.DelegationPolicy.
//
// selfID is the id of the agent whose policy is being built (the PUT/POST
// target). It is used to reject a self-referential local to[] ref (A→A). On
// create, when the id is already chosen, pass it so a self-ref is rejected; pass
// "" only when no id is available.
//
// As of the S3 BE bounded-delegation unlock (M5 item 2) a worker/Subagent MAY
// delegate onward: the hard worker-leaf rejection has been removed and depth is
// the safety boundary. The agent tier no longer gates the to[] list — depth (and
// the global subturn ceiling) bound a Subagent's onward fan-out instead.
//
// Validation (all reject with a non-empty errMsg → caller returns 400):
//   - a local to[] ref equal to selfID is rejected (no self-delegation A→A).
//   - duplicate to[] refs are collapsed (first occurrence wins).
//   - every NEWLY-ADDED to[] ref with kind=="local" must resolve to an existing
//     agent id in rosterIDs ("delegation target <id> does not exist"). The
//     wildcard "*" is allowed. A ref already present in the stored policy is
//     grandfathered (not re-validated) so a pre-existing dangling target does not
//     block unrelated edits. kind=="remote-a2a" refs pass without roster
//     resolution (reserved).
//   - modes ⊆ {await, background, task}.
//   - depth must be >= 0 and <= ceiling (the global subturn depth cap).
//
// MERGE/preserve semantics: accept_from and budget are INERT in v0.1.0 and the
// editor does not send them. When the input omits them (nil / !HasBudget), the
// existing stored values are preserved from `existing` so a to/modes/depth edit
// never clobbers seeded inert fields. When the input DOES send them, they are
// taken from the input.
// peerDelegationGraph builds the local-ref delegation adjacency of every agent
// in cfg EXCEPT selfID (whose edges are being replaced by this PUT/POST). It is
// fed to the cycle check so a new selfID→To edge set is rejected when combined
// with the rest of the roster's policies it would form a delegation cycle
// (A→B→A). Wildcard ("*") and remote-a2a refs are skipped — they have no
// concrete in-roster target node.
func peerDelegationGraph(cfg *config.Config, selfID string) map[string][]string {
	if cfg == nil {
		return nil
	}
	self := strings.TrimSpace(selfID)
	graph := make(map[string][]string, len(cfg.Agents.List))
	for i := range cfg.Agents.List {
		ac := &cfg.Agents.List[i]
		if ac.ID == self {
			continue
		}
		dp := ac.DelegationPolicy
		if dp == nil {
			continue
		}
		for _, ref := range dp.To {
			if normalizeRefKind(ref.Kind) != config.AgentRefKindLocal {
				continue
			}
			id := strings.TrimSpace(ref.ID)
			if id == "" || id == "*" {
				continue
			}
			graph[ac.ID] = append(graph[ac.ID], id)
		}
	}
	return graph
}

func buildDelegationPolicy(
	in *delegationPolicyInput,
	existing *config.DelegationPolicy,
	rosterIDs map[string]bool,
	depthCeiling int,
	selfID string,
	peerGraph map[string][]string,
) (*config.DelegationPolicy, string) {
	out := &config.DelegationPolicy{}

	// to[]: validate refs, map into canonical AgentRef. A non-nil-but-empty To
	// is authoritative ("deny all delegation") and is preserved as an empty slice.
	//
	// Bounded subagent delegation (S3 BE, M5 item 2): a Subagent/worker MAY now
	// carry a non-empty to[] and delegate onward. The previous hard worker-leaf
	// rejection is removed — depth is the safety boundary, not the agent tier.
	// A worker's onward delegation is bounded by the depth cap below (and the
	// global subturn ceiling), so a specialist like the Planner can fan out to
	// Explorer/Researcher within depth while runaway chains are still rejected.
	if in.To != nil {
		selfRef := strings.TrimSpace(selfID)
		grandfathered := existingLocalRefSet(existing)
		seen := make(map[string]bool, len(*in.To))
		refs := make([]config.AgentRef, 0, len(*in.To))
		for _, r := range *in.To {
			kind := normalizeRefKind(r.Kind)
			id := strings.TrimSpace(r.ID)
			if id == "" {
				return nil, "delegation target id must not be empty"
			}
			// De-duplicate: collapse repeated (kind,id) refs ([X,X] → [X]).
			dedupeKey := kind + ":" + id
			if seen[dedupeKey] {
				continue
			}
			seen[dedupeKey] = true
			switch kind {
			case config.AgentRefKindLocal:
				// Reject self-reference (A→A): an agent cannot delegate to itself.
				if selfRef != "" && id == selfRef {
					return nil, "an agent cannot delegate to itself (self-referential delegation target)"
				}
				// Wildcard matches any local agent — no roster resolution needed.
				// A ref already present in the stored policy is grandfathered so a
				// pre-existing dangling target does not block unrelated edits.
				if id != "*" && !grandfathered[dedupeKey] && !rosterIDs[id] {
					return nil, fmt.Sprintf("delegation target %s does not exist", id)
				}
			case config.AgentRefKindRemoteA2A:
				// Reserved/future: accepted without roster resolution.
			default:
				return nil, fmt.Sprintf("delegation target kind %q is invalid (valid: local, remote-a2a)", r.Kind)
			}
			refs = append(refs, config.AgentRef{Kind: kind, ID: id})
		}
		out.To = refs
	} else if existing != nil {
		// Input omits "to": preserve the stored to-list unchanged.
		out.To = existing.To
	}

	// Cycle rejection (config footgun): combine this agent's new local To edges
	// with the rest of the roster's existing delegation graph and reject if the
	// result contains a directed cycle (A→B→A). The runtime depth cap bounds an
	// actual delegation chain, but a cyclic config graph is still a footgun, so it
	// is rejected at write time with a clear 400. Wildcard/remote refs are skipped
	// (no concrete in-roster node).
	if self := strings.TrimSpace(selfID); self != "" && len(out.To) > 0 {
		graph := make(map[string][]string, len(peerGraph)+1)
		for k, v := range peerGraph {
			graph[k] = v
		}
		for _, ref := range out.To {
			if normalizeRefKind(ref.Kind) != config.AgentRefKindLocal {
				continue
			}
			id := strings.TrimSpace(ref.ID)
			if id == "" || id == "*" {
				continue
			}
			graph[self] = append(graph[self], id)
		}
		if cycleNode := detectDelegationCycle(graph); cycleNode != "" {
			return nil, "delegation policy would create a cycle (a delegation chain that loops back, e.g. through " +
				cycleNode + "); cycles are not allowed"
		}
	}

	// modes: validate the enum subset, map to canonical DelegationMode.
	if in.Modes != nil {
		modes := make([]config.DelegationMode, 0, len(*in.Modes))
		for _, m := range *in.Modes {
			switch config.DelegationMode(m) {
			case config.DelegationModeAwait, config.DelegationModeBackground, config.DelegationModeTask:
				modes = append(modes, config.DelegationMode(m))
			default:
				return nil, fmt.Sprintf("delegation mode %q is invalid (valid: await, background, task)", m)
			}
		}
		out.Modes = modes
	} else if existing != nil {
		out.Modes = existing.Modes
	}

	// depth: >= 0 and <= ceiling. nil means "leave unchanged / uncapped".
	if in.Depth != nil {
		d := *in.Depth
		if d < 0 {
			return nil, fmt.Sprintf("delegation depth %d is invalid (must be >= 0)", d)
		}
		if d > depthCeiling {
			return nil, fmt.Sprintf("delegation depth %d exceeds the maximum allowed depth of %d", d, depthCeiling)
		}
		depthCopy := d
		out.Depth = &depthCopy
	} else if existing != nil {
		out.Depth = existing.Depth
	}

	// accept_from (INERT): preserve existing unless the input explicitly sends it.
	if in.AcceptFrom != nil {
		refs := make([]config.AgentRef, 0, len(*in.AcceptFrom))
		for _, r := range *in.AcceptFrom {
			refs = append(refs, config.AgentRef{Kind: normalizeRefKind(r.Kind), ID: strings.TrimSpace(r.ID)})
		}
		out.AcceptFrom = refs
	} else if existing != nil {
		out.AcceptFrom = existing.AcceptFrom
	}

	// budget (INERT): preserve existing unless the input explicitly sends it.
	if in.HasBudget {
		out.Budget = in.Budget
	} else if existing != nil {
		out.Budget = existing.Budget
	}

	return out, ""
}

// delegationPolicyToMap serializes a *config.DelegationPolicy into the JSON-map
// shape written under agents.list[*].delegation_policy by safeUpdateConfigJSON.
// The keys match the config.DelegationPolicy json tags so the value round-trips
// through a config reload. Empty/zero inert fields are omitted to match the
// `omitempty` tags on the Go struct.
func delegationPolicyToMap(dp *config.DelegationPolicy) map[string]any {
	m := map[string]any{}
	if dp.To != nil {
		m["to"] = agentRefsToMaps(dp.To)
	}
	if len(dp.Modes) > 0 {
		modes := make([]string, len(dp.Modes))
		for i, md := range dp.Modes {
			modes[i] = string(md)
		}
		m["modes"] = modes
	}
	if dp.Depth != nil {
		m["depth"] = *dp.Depth
	}
	if len(dp.AcceptFrom) > 0 {
		m["accept_from"] = agentRefsToMaps(dp.AcceptFrom)
	}
	if dp.Budget != nil {
		budget := map[string]any{}
		if dp.Budget.MaxCostUSD != 0 {
			budget["max_cost_usd"] = dp.Budget.MaxCostUSD
		}
		if dp.Budget.MaxTokens != 0 {
			budget["max_tokens"] = dp.Budget.MaxTokens
		}
		m["budget"] = budget
	}
	return m
}

// agentRefsToMaps serializes a slice of AgentRef into JSON maps (kind+id). A
// non-nil empty slice serializes to an empty []map so "deny all" (explicit empty
// to) round-trips as [] rather than a missing key.
func agentRefsToMaps(refs []config.AgentRef) []map[string]any {
	out := make([]map[string]any, 0, len(refs))
	for _, r := range refs {
		out = append(out, map[string]any{"kind": r.Kind, "id": r.ID})
	}
	return out
}

// setAgentDelegationPolicyResponse populates the generated Agent.DelegationPolicy
// response field from the persisted config.DelegationPolicy so a GET (and the
// create/update echo) reflects the stored policy. When the agent has no policy
// the field is left nil (omitted).
func setAgentDelegationPolicyResponse(ag *gen.Agent, dp *config.DelegationPolicy) {
	if dp == nil {
		return
	}
	// The literal below mirrors the inlined anonymous-struct shape oapi-codegen
	// generated for gen.Agent.DelegationPolicy (emitted inline, not as a named
	// schema), so the assignment target type must be spelled verbatim here.
	out := struct { // not-wire-format: generated gen.Agent.DelegationPolicy inline shape, only populates the generated field
		AcceptFrom *[]struct {
			Id   string                                  `json:"id"`
			Kind gen.AgentDelegationPolicyAcceptFromKind `json:"kind"`
		} `json:"accept_from,omitempty"`
		Budget *struct {
			MaxCostUsd *float64 `json:"max_cost_usd,omitempty"`
			MaxTokens  *int     `json:"max_tokens,omitempty"`
		} `json:"budget,omitempty"`
		Depth *int                              `json:"depth,omitempty"`
		Modes *[]gen.AgentDelegationPolicyModes `json:"modes,omitempty"`
		To    *[]struct {
			Id   string                          `json:"id"`
			Kind gen.AgentDelegationPolicyToKind `json:"kind"`
		} `json:"to,omitempty"`
	}{
		Depth: dp.Depth,
	}
	if dp.To != nil {
		to := make([]struct {
			Id   string                          `json:"id"`
			Kind gen.AgentDelegationPolicyToKind `json:"kind"`
		}, 0, len(dp.To))
		for _, r := range dp.To {
			to = append(to, struct {
				Id   string                          `json:"id"`
				Kind gen.AgentDelegationPolicyToKind `json:"kind"`
			}{Id: r.ID, Kind: gen.AgentDelegationPolicyToKind(r.Kind)})
		}
		out.To = &to
	}
	if len(dp.Modes) > 0 {
		modes := make([]gen.AgentDelegationPolicyModes, 0, len(dp.Modes))
		for _, md := range dp.Modes {
			modes = append(modes, gen.AgentDelegationPolicyModes(md))
		}
		out.Modes = &modes
	}
	if len(dp.AcceptFrom) > 0 {
		af := make([]struct {
			Id   string                                  `json:"id"`
			Kind gen.AgentDelegationPolicyAcceptFromKind `json:"kind"`
		}, 0, len(dp.AcceptFrom))
		for _, r := range dp.AcceptFrom {
			af = append(af, struct {
				Id   string                                  `json:"id"`
				Kind gen.AgentDelegationPolicyAcceptFromKind `json:"kind"`
			}{Id: r.ID, Kind: gen.AgentDelegationPolicyAcceptFromKind(r.Kind)})
		}
		out.AcceptFrom = &af
	}
	if dp.Budget != nil {
		budget := struct { // not-wire-format: mirrors the generated gen.Agent.DelegationPolicy.Budget inline shape; only populates that generated field, never marshaled on its own
			MaxCostUsd *float64 `json:"max_cost_usd,omitempty"`
			MaxTokens  *int     `json:"max_tokens,omitempty"`
		}{}
		if dp.Budget.MaxCostUSD != 0 {
			c := dp.Budget.MaxCostUSD
			budget.MaxCostUsd = &c
		}
		if dp.Budget.MaxTokens != 0 {
			tk := dp.Budget.MaxTokens
			budget.MaxTokens = &tk
		}
		out.Budget = &budget
	}
	ag.DelegationPolicy = &out
}

// rosterIDSet builds a set of all agent IDs currently in the config roster, used
// to resolve local delegation targets.
func rosterIDSet(cfg *config.Config) map[string]bool {
	ids := make(map[string]bool, len(cfg.Agents.List))
	for _, ac := range cfg.Agents.List {
		ids[ac.ID] = true
	}
	return ids
}
