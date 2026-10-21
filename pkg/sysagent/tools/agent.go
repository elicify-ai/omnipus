// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/datamodel"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/memrooms"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/validation"
	workspacepkg "github.com/elicify-ai/omnipus/pkg/workspace"
)

// newAgentID returns the id of a newly created agent: a random UUID, exactly
// as the REST create path does (pkg/gateway/rest_agents_create.go). No agent
// id is ever derived from its name — the name is a free label kept verbatim,
// and two agents may share one (founder decision 2026-10-14). Built-in agents
// keep their fixed ids. Production ignores the name argument; it exists only
// so tests whose fixtures are keyed by a known id (or that plant a fault at
// the new agent's paths) can choose the id per created agent.
var newAgentID = func(_ string) string { return uuid.New().String() }

// canonicalAgentColor applies the REST colour rule (coreagent.CanonicalColor,
// AgentColor.yaml): a palette value in any letter-case is returned uppercase and
// anything else - a well-formed hex outside the ten included - is refused. Empty
// passes (the field is optional on update).
func canonicalAgentColor(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	canon, ok := coreagent.CanonicalColor(s)
	if !ok {
		return "", fmt.Errorf("invalid color %q: use one of the palette colors %s", s, strings.Join(coreagent.PaletteColors(), ", "))
	}
	return canon, nil
}

// resolveOmnipusHome returns the effective $OMNIPUS_HOME to use for both the
// agent entity store (agentstore.New) and the agent's own on-disk workspace
// (SOUL.md/HEARTBEAT.md, datamodel.InitAgentHome/AgentHomePath). depsHome is
// t.deps.Home (OMNIPUS_HOME) — reliable in containers where $HOME may be
// unset — used verbatim when non-empty; os.UserHomeDir()+"/.omnipus" is the
// fallback only when depsHome is empty.
//
// MUST be called exactly once per Execute, BEFORE constructing an
// agentstore.Store or computing any workspace path, so the entity record and
// the agent's workspace directory never resolve against two different
// "home" values. All three tools used to construct the store from the raw,
// possibly-empty t.deps.Home directly and only apply this fallback later
// (or, in Delete's case, not at all) when computing the workspace path — an
// empty t.deps.Home meant the entity record landed under
// ./entities/agents/<id>.json (relative to the process's CWD) while
// SOUL.md/HEARTBEAT.md landed under $HOME/.omnipus/agents/<id>/, a
// split-brain between the two halves of "the same agent's" on-disk state.
func resolveOmnipusHome(depsHome string) (string, error) {
	if depsHome != "" {
		return depsHome, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return h + "/.omnipus", nil
}

// ---- system.agent.create ----

// AgentCreateTool implements system.agent.create per BRD §D.4.2.
type AgentCreateTool struct{ deps *Deps }

func NewAgentCreateTool(d *Deps) *AgentCreateTool { return &AgentCreateTool{deps: d} }

func (t *AgentCreateTool) Name() string           { return "create_agent" }
func (t *AgentCreateTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *AgentCreateTool) Description() string {
	return "Create a new agent with personality, model, tools, and configuration. Use agent_type to choose the runtime: 'Main' (default, native chat colleague) and 'Subagent' (native delegation-only worker) require soul; 'subagent_3p' is a delegation-only worker on an external CLI — it takes no soul, requires cli, and its cli_path is optional (empty means the CLI's default binary on PATH). name, description and model are required; soul is required for native agents ('Main'/'Subagent') and must be omitted for 'subagent_3p' (an external CLI worker has no soul; supplying one is refused). A set cli_path must name an executable file this machine can run for the current user (an existing regular file with execute access on Unix; a PATHEXT-admitted extension on Windows); leave it empty to use the CLI's default binary on PATH. color is optional (a palette hex, e.g. #6E8BA8; default Pewter #8E9196) — a color outside the palette is rejected. A new agent created inside a workspace's turn context joins that workspace's core_team when membership succeeds; created with no workspace context it is metadata-only (a member of no team) and cannot run in chat or be delegated to until an operator adds it to a workspace's Team tab — the response's status field (joined_workspace vs metadata_only) reports membership, persistence_status reports durable-save completion (complete, partial, or none), and activation_status reports live publication (active, failed, or not_attempted); joined_workspace alone does not prove the agent is live or runnable — check persistence_status, activation_status and any publish_warning before reporting it ready."
}

func (t *AgentCreateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			// Mandatory
			"name": map[string]any{"type": "string", "description": "Display name for the new agent"},
			"description": map[string]any{
				"type":        "string",
				"description": "One-line description of the agent's purpose",
			},
			"soul": map[string]any{
				"type": "string",
				"description": "The agent's personality, role, and behavioral instructions (written to SOUL.md). " +
					"REQUIRED for native agents ('Main'/'Subagent'); NOT supported for 'subagent_3p' — an external " +
					"CLI worker has no native soul, and supplying one is refused.",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Primary LLM model slug (e.g. 'z-ai/glm-5v-turbo')",
			},
			"color": map[string]any{"type": "string", "description": "Avatar color, optional: one of the ten palette hex values #4F8A80, #5E6973, #6E8BA8, #7A8C9E, #7B7290, #8E9196, #9C8A5B, #A97142, #B76E79, #CDBA96 (letter-case is normalized). Omitted, null or empty creates the agent with the default Pewter #8E9196; any other value is rejected."},
			// Visual identity figure/role (#1303): the same catalogs the SPA
			// agent editor and REST create use; helpers in agent_visuals.go.
			"figure": figureCreateProperty(),
			"role":   roleCreateProperty(),
			// Optional — agent type + external-CLI worker runtime.
			"agent_type": map[string]any{
				"type":        "string",
				"enum":        []any{"Main", "Subagent", "subagent_3p"},
				"description": "Agent type. 'Main' (default) = native chat colleague on the Omnipus engine; 'Subagent' = native delegation-only worker — both native types require soul. 'subagent_3p' = delegation-only worker on an EXTERNAL CLI: requires cli, while cli_path is optional (leave it empty to use the CLI's default binary on PATH; set it only for a wrapper or a non-standard install location).",
			},
			"cli": map[string]any{
				"type":        "string",
				"enum":        []any{"claude-code", "codex", "opencode"},
				"description": "External CLI runtime. REQUIRED when agent_type='subagent_3p'. 'claude-code' for Claude (claudex), 'opencode' for opencode, 'codex' for Codex.",
			},
			"cli_path": map[string]any{
				"type":        "string",
				"description": "Optional. Absolute path to the external CLI binary. Leave EMPTY to use the CLI's default binary on $PATH (claude / codex / opencode). Set it only when this machine invokes the CLI via a wrapper or a non-standard path — and derive the real path on this system, never hardcode a guess. When set, it must name an existing regular file this machine can execute for the current user (Unix: the user holds execute access; Windows: the extension is in PATHEXT); a missing path, a directory, or a non-executable file is refused.",
			},
			"provider": map[string]any{
				"type":        "string",
				"description": "Explicit provider routing key for the primary model (e.g. 'openrouter'). Pins model resolution to this provider instead of inferring one from the slug.",
			},
			"fallback_models": fallbackModelsParameters(),
			"heartbeat": map[string]any{
				"type":        "string",
				"description": "Proactive scheduling instructions (written to HEARTBEAT.md)",
			},
			"max_tool_iterations": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     1000,
				"description": "Max tool calls per turn for this agent, 1-1000. Must not exceed the global limit set in Settings → Performance (a higher value is refused). Omit to use the global limit.",
			},
			"skills":              skillsParameters(false),
			"mcp_servers":         mcpServersParameters(),
			"tool_policy_changes": toolPolicyChangesParameters(),
			"memory_enabled":      map[string]any{"type": "boolean"},
			"voice": map[string]any{
				"type":        "string",
				"description": "Per-agent TTS persona identifier. Persisted; playback is inactive in this release.",
			},
			"context_window_override": map[string]any{
				"type":        "integer",
				"description": "Positive token override. JSON null clears the override.",
			},
			"model_params": modelParamsParameters(),
			// FR-040/FR-041: the workspace the new agent joins and gets its
			// main chat in; omitted = the conversation's workspace.
			workspaceIDParam: workspaceIDProperty("the new agent joins (and gets its main chat in)"),
		},
		// soul is deliberately NOT in the schema required list: it is required
		// for native agents ('Main'/'Subagent') only. External CLI workers
		// ('subagent_3p') have no native soul — validate() enforces the
		// conditional requirement, and the store refuses a supplied external
		// soul as an unsupported field (issue #1274, chief ruling Q1 B).
		"required": []string{"name", "description", "model"},
	}
}

// agentCreateToolExecute carries the shared state of Execute across its stages.
type agentCreateToolExecute struct {
	t               *AgentCreateTool
	ctx             context.Context
	args            map[string]any
	name            string
	description     string
	soul            string
	model           string
	color           string
	figure          string
	role            string
	agentType       string
	execCLI         string
	execCLIPath     string
	id              string
	newAgent        config.AgentConfig
	finalID         string
	joinedWorkspace bool
	// joinedWorkspaceID is the workspace the new agent joined ("" when none); its
	// mains are provisioned in publishAndRespond, AFTER the live roster has the agent.
	joinedWorkspaceID string
	// target is the destination workspace resolved before any write (FR-040).
	target   targetWorkspace
	mutation agentstore.MutationResult
	// mainsErr is the failure of the new member's main-chat provisioning.
	mainsErr error
	// joinMissed is set when a target workspace was resolved but the
	// membership did not land (joinErr carries the cause when there is one).
	joinMissed bool
	joinErr    error
}

func (t *AgentCreateTool) Execute(ctx context.Context, args map[string]any) (res *tools.ToolResult) {
	defer func() { res = finalizeChangeResult(t.Name(), res) }()
	ac := &agentCreateToolExecute{t: t, ctx: ctx, args: args}

	if r0, stop := ac.validate(); stop {
		return r0
	}

	if r0, stop := ac.prepareConfig(); stop {
		return r0
	}

	if r0, stop := ac.persistAndJoin(); stop {
		return r0
	}

	// agent-picker-freshness fix (#1009), corrected: NotifyAgentCreated used
	// to fire HERE, right after persistAndJoin's durable-persist success —
	// but persistAndJoin only writes the entity record to disk; it does NOT
	// put the new agent into the live, in-memory registry/config list that
	// pkg/gateway/rest_agents.go::listAgents (GET /api/v1/agents) reads. That
	// happens inside publishAndRespond, via publishAgentActivation. Firing
	// here raced a tab's agent_created handler (src/store/chat/slices/
	// frames.ts) against publishAgentActivation: a refetch that landed before
	// publishAgentActivation ran would get a response missing the new agent
	// and cache that miss for 30s — the exact bug #1009 exists to close,
	// reappearing via this ordering. The notify now lives inside
	// publishAndRespond, gated on the agent actually being live — see that
	// function for the exact point and why.
	return ac.publishAndRespond()
}

// validate validates the required fields, runtime selection, and generated agent ID.
func (ac *agentCreateToolExecute) validate() (*tools.ToolResult, bool) {
	// Validate mandatory fields.
	ac.name, _ = ac.args["name"].(string)
	if strings.TrimSpace(ac.name) == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "name is required", "Provide a name for the agent")), true
	}
	if coreagent.AgentNameTooLong(ac.name) {
		// #1075: refused before anything is stored, so a long name never
		// reaches the store as an opaque SAVE_FAILED.
		return tools.ErrorResult(errorJSON("INVALID_INPUT", coreagent.AgentNameTooLongRefusal,
			"Choose a shorter name")), true
	}
	if coreagent.IsReservedAgentName(ac.name) {
		return reservedNameResult(), true
	}
	ac.description, _ = ac.args["description"].(string)
	if strings.TrimSpace(ac.description) == "" {
		return tools.ErrorResult(
			errorJSON("INVALID_INPUT", "description is required", "Provide a one-line description"),
		), true
	}
	// Agent type + external-CLI worker runtime (W4 taxonomy). Default "Main".
	// Resolved BEFORE the soul check: soul is required for native agents only —
	// an external CLI worker ('subagent_3p') has no native soul, and supplying
	// one stays an unsupported-field refusal (agentmutation.ValidateFields,
	// issue #1274, chief ruling Q1 B).
	ac.agentType, _ = ac.args["agent_type"].(string)
	if ac.agentType == "" {
		ac.agentType = "Main"
	}
	switch ac.agentType {
	case "Main", "Subagent", "subagent_3p":
	default:
		return tools.ErrorResult(errorJSON("INVALID_INPUT",
			"agent_type must be one of Main, Subagent, subagent_3p",
			"Use 'subagent_3p' for an external CLI worker")), true
	}
	ac.soul, _ = ac.args["soul"].(string)
	if ac.agentType != "subagent_3p" && strings.TrimSpace(ac.soul) == "" {
		return tools.ErrorResult(
			errorJSON(
				"INVALID_INPUT",
				"soul is required",
				"Provide the agent's personality and behavioral instructions",
			),
		), true
	}
	ac.model, _ = ac.args["model"].(string)
	if strings.TrimSpace(ac.model) == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "model is required", "Provide the LLM model slug")), true
	}
	ac.color, _ = ac.args["color"].(string)
	canonColor, err := canonicalAgentColor(ac.color)
	if err != nil {
		return tools.ErrorResult(errorJSON("INVALID_COLOR", err.Error(), "Use a palette color, e.g. #6E8BA8")), true
	}
	ac.color = canonColor
	if ac.color == "" {
		ac.color = coreagent.DefaultColor // same default as REST create
	}
	// The shared apply step re-reads the argument map; give it the resolved value
	// so an explicit empty colour cannot overwrite the default.
	ac.args["color"] = ac.color

	// Visual identity figure/role (#1303, chief Q1 A 2026-10-14): omitted, null
	// (NormalizeArgs renders it "") or empty select the shipped default —
	// exactly like REST create — and any other non-catalog value is refused
	// with the full choice list, before any write. Same catalog and
	// canonicalizers as the color argument above.
	figure, figureStop := resolveCreateVisual(ac.args, "figure", coreagent.DefaultFigure)
	if figureStop != nil {
		return figureStop, true
	}
	ac.figure = figure
	role, roleStop := resolveCreateVisual(ac.args, "role", coreagent.DefaultRole)
	if roleStop != nil {
		return roleStop, true
	}
	ac.role = role
	if ac.agentType == "subagent_3p" {
		ac.execCLI, _ = ac.args["cli"].(string)
		switch ac.execCLI {
		case "claude-code", "codex", "opencode":
		default:
			return tools.ErrorResult(errorJSON("INVALID_INPUT",
				"cli is required for subagent_3p and must be one of claude-code, codex, opencode",
				"Set cli (e.g. 'opencode' or 'claude-code')")), true
		}
		// cli_path is OPTIONAL: when empty the driver invokes the CLI's default
		// binary name on $PATH (claude / codex / opencode). Set it only when this
		// machine uses a wrapper or a non-standard binary path — and then it must
		// name an executable file, refused with the path in the reason otherwise
		// (issue #1274): a bad path must fail here, before any entity, workspace
		// or config write, not at the worker's first delegated run.
		ac.execCLIPath, _ = ac.args["cli_path"].(string)
		if strings.TrimSpace(ac.execCLIPath) != "" {
			if err := validateExternalCLIPath(ac.execCLIPath); err != nil {
				return fieldErrorResult(err), true
			}
		}
	}

	if err := rejectUnknownAgentFields(ac.args, agentCreateArgNames); err != nil {
		return fieldErrorResult(err), true
	}
	if r0, stop := refuseManagementGrants(ac.args); stop {
		return r0, true
	}
	if _, _, err := parseOptionalHeartbeat(ac.args); err != nil {
		return fieldErrorResult(err), true
	}
	ac.id = newAgentID(ac.name)
	if err := validateID(ac.id); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), "")), true
	}
	// FR-040/FR-043: the destination workspace is validated before any
	// write; an unknown one fails with nothing saved.
	target, refusal := resolveTargetWorkspace(ac.t.deps, ac.ctx, ac.args)
	if refusal != nil {
		return refusal, true
	}
	ac.target = target
	return nil, false
}

// refuseManagementGrants refuses a create whose tool_policy_changes grant any
// management-family tool with a non-deny policy — "allow" and "ask" are both
// grants — or remove one (dropping the constructor's Deny lets the agent ride
// the ceiling) (omnipus-system-agent FR-025, BDD-14.3). Runs before any
// write. A malformed patch is refused with the shared decoder's own message.
func refuseManagementGrants(args map[string]any) (*tools.ToolResult, bool) {
	raw, present := args["tool_policy_changes"]
	if !present {
		return nil, false
	}
	patch, err := decodePolicyPatch(raw)
	if err != nil {
		return fieldErrorResult(err), true
	}
	if tool, bad := coreagent.ManagementGrantViolation(patch.Set); bad {
		return tools.ErrorResult(errorJSON("MANAGEMENT_GRANT_REFUSED", coreagent.ManagementGrantRefusal(tool),
			"Set "+tool+" to deny or leave it out; ask Omnipus to make management changes")), true
	}
	// Removing the constructor's explicit Deny lets the new agent ride the
	// ceiling (allow or ask for this family) — the same grant, refused the
	// same way (founder-approved security fix, 2026-10-14).
	if tool, bad := coreagent.ManagementRemoveViolation(patch.Remove); bad {
		return tools.ErrorResult(errorJSON("MANAGEMENT_GRANT_REFUSED", coreagent.ManagementRemoveRefusal(tool),
			"Leave "+tool+" out of remove; ask Omnipus to make management changes")), true
	}
	return nil, false
}

// validateExternalCLIPath refuses a supplied cli_path the current user could
// not execute: a missing path, a path that is not a regular file (e.g. a
// directory), or a file the platform would not run — on Unix faccessat
// (AT_EACCESS, X_OK) for the effective user, on Windows a PATHEXT-admitted
// extension (issue #1274, fix round 1). The refusal names cli_path, the exact
// supplied path and the specific fault so the model can correct it. Symlinks
// are resolved by os.Stat, matching what the driver would exec. An empty path
// is the caller's "use the CLI's default binary on PATH" request and never
// reaches this check.
func validateExternalCLIPath(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fieldErr("cli_path", fmt.Sprintf("%q does not exist: set cli_path to an existing executable CLI binary, or leave it empty to use the CLI's default binary on PATH (stat: %v)", path, err))
	}
	if !fi.Mode().IsRegular() {
		return fieldErr("cli_path", fmt.Sprintf("%q is a directory, not a regular file: point cli_path at the CLI executable itself, or leave it empty to use the CLI's default binary on PATH", path))
	}
	if err := currentUserCanExecute(path); err != nil {
		return fieldErr("cli_path", fmt.Sprintf("%q is not executable: %v. Point cli_path at a file this machine can execute, or leave it empty to use the CLI's default binary on PATH", path, err))
	}
	return nil
}

// prepareConfig builds the persisted agent configuration from required and optional arguments.
func (ac *agentCreateToolExecute) prepareConfig() (*tools.ToolResult, bool) {
	// ADR-054 D2/§11 checklist item 6: agents are per-entity records under
	// entities/agents/<id>.json, not config.json's agents.list — persist via
	// the agent store instead of appending to cfg.Agents.List inside
	// t.deps.WithConfig. agentstore.Store.Create performs the existence
	// check (ErrAlreadyExists) and the write atomically under its own
	// per-entity lock, so there is no separate check-then-append TOCTOU
	// window to close here.
	ac.newAgent = config.AgentConfig{
		ID:          ac.id,
		Name:        ac.name,
		Description: ac.description,
		Color:       ac.color,
		Figure:      ac.figure, // resolved default or canonical catalog value (#1303)
		Role:        ac.role,
		Model:       &config.AgentModelConfig{Primary: ac.model},
	}
	// Agent type / runtime (W4). Subagent + subagent_3p persist as worker;
	// subagent_3p additionally carries an external-CLI executor so dispatch
	// runs it on the named CLI (claude-code / codex / opencode).
	switch ac.agentType {
	case "Subagent":
		ac.newAgent.Type = config.AgentTypeWorker
	case "subagent_3p":
		// External CLI worker: Type=worker + an external-cli executor. The
		// worker's own run model is the top-level Model.Primary (set above);
		// SubagentsConfig.Model is for THIS agent's own sub-delegations, so we
		// leave it unset to avoid a misleading duplicate.
		ac.newAgent.Type = config.AgentTypeWorker
		ac.newAgent.Subagents = &config.SubagentsConfig{
			Executor: &config.ExecutorConfig{
				Kind:    config.ExecutorKindExternalCLI,
				CLI:     ac.execCLI,
				CLIPath: ac.execCLIPath,
			},
		}
	}
	if _, retired := ac.args["model_fallbacks"]; retired {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "Use fallback_models with model/provider objects", "")), true
	}
	if raw, present := ac.args["fallback_models"]; present {
		if err := applyFallbackModels(&ac.newAgent, raw); err != nil {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), "")), true
		}
	}
	// Optional: explicit provider pin for the primary model (O3 two-field
	// model — config.AgentModelConfig.Provider's doc comment). Mirrors
	// REST's createAgent handling of the same wire field. Previously declared
	// in Parameters() but never read here — a caller passing it got a
	// success response implying it was applied when it silently was not.
	if v, ok := ac.args["provider"].(string); ok {
		ac.newAgent.Model.Provider = strings.TrimSpace(v)
	}
	// Optional: per-turn tool-call cap — applied (1..1000, not above the
	// global, #904 D15) by applyAgentToolArgs below, the same code path
	// update_agent uses.
	// ADR-037: can_delegate_to is retired — it was write-only (its last real
	// reader, config.ResolveDelegationTo, was deleted as part of the
	// delegation-policy removal). Delegation trust is configured
	// exclusively via the per-workspace Team tab now
	// (PUT /api/v1/workspaces/{id}/delegation); this tool does not — and
	// must not — pretend to grant it.
	// Seed the privilege rail (FR-008/FR-022, plus bash:deny per CRIT-001 /
	// bash-tool-spec.md FR-B12): system.agent.create has no tools_cfg
	// parameter (see Parameters() above — there is no caller-supplied
	// override for Tools), so newAgent.Tools is always nil here and the
	// default seed IS the agent's entire tools config. Delegate to the
	// single shared constructor — also used by the REST create path
	// (pkg/gateway/rest.go's createAgent, via
	// coreagent.NewCustomAgentToolsCfg()) — so the two agent-creation
	// paths cannot drift out of sync on this seed again.
	ac.newAgent.Tools = coreagent.NewCustomAgentToolsCfg()
	if ac.newAgent.Type == config.AgentTypeWorker {
		// §6.5 (FR-007): the worker tier gets no secret card, secret list or
		// conversation reading.
		coreagent.RestrictForWorkerTier(ac.newAgent.Tools)
	}
	fields := mutationFieldNames(ac.args)
	if err := agentmutation.ValidateFields(ac.newAgent, fields); err != nil {
		return fieldErrorResult(err), true
	}
	if err := applyAgentToolArgs(&ac.newAgent, ac.args, knownToolPolicies(ac.t.deps), ac.t.deps.currentInventory(),
		ac.t.deps.agentDefaults()); err != nil {
		return fieldErrorResult(err), true
	}
	return nil, false
}

// persistAndJoin persists the agent and its workspace files, then joins the contextual workspace when present.
//
// Three of the early-return (stop=true) branches below — default-singleton
// write failure, InitAgentHome failure, and the HEARTBEAT.md write failure —
// run AFTER agentstore.CreateState has already durably saved the entity
// record. None of them call Deps.NotifyAgentCreated, and that is intentional,
// not a gap: returning stop=true here means Execute never reaches
// publishAndRespond(), so publishAgentActivation() never runs and the agent
// is never put into the live, in-memory config/registry that
// pkg/gateway/rest_agents.go::listAgents reads — the agent is durably
// persisted but genuinely not yet listable. This is consistent with the
// #1009 ordering fix in Execute/publishAndRespond: firing agent_created for
// an agent that isn't listable yet would just recreate that fix's exact
// race for these three edge cases. Do not add a notify call to any of them.
func (ac *agentCreateToolExecute) persistAndJoin() (*tools.ToolResult, bool) {
	// Resolve home ONCE, before constructing the agent store, so the entity
	// record (below) and the agent's own workspace (SOUL.md/HEARTBEAT.md,
	// further down) always agree on the same $OMNIPUS_HOME — see
	// resolveOmnipusHome's doc comment for the split-brain this closes.
	omnipusHome, homeErr := resolveOmnipusHome(ac.t.deps.Home)
	if homeErr != nil {
		return tools.ErrorResult(errorJSON("WORKSPACE_ERROR", homeErr.Error(),
			"Set OMNIPUS_HOME environment variable")), true
	}

	ac.finalID = ac.id
	// #904 D10/D15: re-check the agent's own limit against a FRESH read of
	// the global immediately before the write — prepareConfig checked it
	// against the config current at call start. Residual window: this path
	// does not hold the gateway's configMu, so a PUT /performance lowering
	// that commits between this read and CreateState does not see the new
	// agent; it then sits above the new global, which the resolver caps and
	// flags (D1). Never unbounded, and visible on the profile.
	if ac.newAgent.MaxToolIterations > 0 {
		if err := config.ValidateAgentMaxToolIterations(ac.newAgent.MaxToolIterations, ac.t.deps.agentDefaults()); err != nil {
			return fieldErrorResult(fieldErr("max_tool_iterations", err.Error())), true
		}
	}
	mutation, err := agentstore.New(omnipusHome).CreateState(ac.id, &ac.newAgent, ac.soul)
	ac.mutation = mutation
	if err != nil {
		if errors.Is(err, entity.ErrAlreadyExists) {
			return tools.ErrorResult(errorJSON(
				"AGENT_ALREADY_EXISTS",
				fmt.Sprintf("An agent with ID %q already exists", ac.id),
				"Use update_agent to modify the existing agent or choose a different name",
			)), true
		}
		return createSaveFailedResult(ac.id, mutation, err), true
	}

	// Create agent workspace and write personality files.
	if err := datamodel.InitAgentHome(omnipusHome, ac.finalID); err != nil {
		// Entity already durable. Do not
		// delete them: report the actual revision so the caller can read
		// and recover instead of claiming a complete create.
		// No NotifyAgentCreated here — see this function's doc comment: the
		// entity is durable but not yet listable.
		slog.Error("sysagent: create_agent: InitAgentHome failed after entity save",
			"id", ac.finalID, "error", err)
		return createHomePartialResult(ac.finalID, ac.mutation.Revision, mutationFieldNames(ac.args), err), true
	}

	heartbeat, _, hbErr := parseOptionalHeartbeat(ac.args)
	if hbErr != nil {
		return fieldErrorResult(hbErr), true
	}
	if strings.TrimSpace(heartbeat) != "" {
		hbPath := filepath.Join(omnipusHome, "agents", ac.finalID, "HEARTBEAT.md")
		if err := os.WriteFile(hbPath, []byte(heartbeat), 0o600); err != nil {
			// No NotifyAgentCreated here either — same reason: durable, not
			// yet listable (see this function's doc comment).
			slog.Error("sysagent: create_agent: HEARTBEAT.md write failed after entity save",
				"id", ac.finalID, "error", err)
			return createHeartbeatPartialResult(ac.finalID, ac.mutation.Revision, mutationFieldNames(ac.args), err), true
		}
	}

	// ADR-046 P1 (FR-007/008, US-3 AS-2/AS-3): agents are metadata — creating
	// one must NOT auto-add it to any global roster. If this call is running
	// inside a workspace's turn context (create_agent invoked by an agent
	// that is itself a workspace CoreTeam member, or a future workspace-scoped
	// entry point), the new agent joins THAT workspace's team only —
	// creation-in-context — so it becomes runnable there immediately. With no
	// workspace context, the agent is intentionally metadata-only: a member
	// of no team, unable to execute (runTurn's ErrAgentNotWorkspaceMember)
	// until an operator adds it to a workspace's Team tab. Best-effort: a
	// join failure is logged, not fatal — the agent still exists, just not
	// yet joined anywhere.
	// joinedWorkspace tracks whether this call actually enrolled the new agent
	// on a workspace's core_team — it drives the response's status field
	// below: the caller must be able to tell
	// "joined_workspace" (immediately runnable there) from "metadata_only"
	// (a member of no team, cannot run in chat or be delegated to) without
	// re-deriving it from whether a workspace context happened to be
	// present, since a join attempt can also fail (logged, non-fatal).
	ac.joinedWorkspace = false
	if wsID := ac.target.ID; wsID != "" {
		joined, joinErr := ac.t.joinWorkspaceTeam(wsID, ac.finalID)
		ac.joinedWorkspace = joined
		if joined {
			ac.joinedWorkspaceID = wsID
		}
		switch {
		case joinErr == nil:
			// Fully joined: the membership landed AND the self-row was seeded.
		case joined:
			// U5A-FIX-R1 / N3: the membership landed, but the self-row seed did
			// not. The agent IS a workspace member, so returning the normal
			// "metadata_only" success would both misdescribe its state and hide
			// a missing required authorization row. Return the truthful
			// partial-failure result naming what landed and how to recover.
			slog.Error("sysagent: create_agent: joined workspace but could not seed the self-delegation row",
				"agent_id", ac.finalID, "workspace_id", wsID, "error", joinErr)
			return createJoinSeedPartialResult(ac.finalID, ac.mutation.Revision, mutationFieldNames(ac.args), wsID, joinErr), true
		default:
			// The membership itself did not land, so the agent is genuinely
			// metadata-only (a member of no team). The agent record is durable,
			// so the result is "partly saved" naming the missing membership
			// (FR-030, BDD-9.4) — never a plain success.
			slog.Warn("sysagent: create_agent: could not add new agent to workspace core_team",
				"agent_id", ac.finalID, "workspace_id", wsID, "error", joinErr)
			ac.joinMissed, ac.joinErr = true, joinErr
		}
	}
	return nil, false
}

// publishAndRespond publishes the agent to the live registry and returns its resulting status.
func (ac *agentCreateToolExecute) publishAndRespond() *tools.ToolResult {
	// Publish the new agent so it is immediately available for chat — the
	// fast path (issue #571, sysagent half) when wired, falling back to a
	// full config reload otherwise. UpsertAgentFastFunc mirrors
	// pkg/gateway/rest.go's fastAgentUpsert: it swaps ONLY this agent's
	// *AgentInstance into the live AgentRegistry (AgentLoop.UpsertAgentFast)
	// instead of restarting channels/cron/schedulers/the plan engine for a
	// one-agent change. See Deps.UpsertAgentFastFunc's doc comment.
	// publishWarning surfaces a live-publish failure IN the success payload
	// (fix-wave finding #3) rather than only in the server log: the entity
	// record and workspace files above are already durably written by this
	// point, so this remains a real success, but an unqualified
	// {"id":...,"status":"active"} response would tell the calling agent the
	// new agent is live and routable when it is NOT — it keeps 404ing on
	// chat/delegate until the next restart or config reload. Pattern mirrors
	// pkg/tools/task.go's update_task advance_warning field.
	activation, publishErr := publishAgentActivation(ac.t.deps, ac.finalID)
	var publishWarning string
	if publishErr != "" {
		slog.Warn("sysagent: publish after agent create failed — agent available after restart",
			"id", ac.finalID, "error", publishErr)
		publishWarning = fmt.Sprintf(
			"agent %q was created but is not yet live: publish failed (%s); it will become routable "+
				"after the next config reload or gateway restart", ac.finalID, publishErr)
	}

	// Final-gate H2 / founder-reported 2026-10-11: the member's main chat is
	// provisioned through the workspace-save path, and that path's eligibility
	// reads the LIVE roster (an agent absent from it is skipped without error).
	// So it must run only after publishAgentActivation has put the new agent in
	// that roster, and before the agent_created notify below so the client's
	// refetch sees the main. A failure is logged, not fatal: the main is
	// re-attempted when its chat is opened.
	if ac.joinedWorkspaceID != "" && ac.t.deps.EnsureWorkspaceMains == nil {
		// FR-041: a joined member's main chat is part of the change; without
		// the provisioning hook it was not created, so the result must say so.
		ac.mainsErr = errors.New("main chat provisioning is not available here")
	}
	if ac.joinedWorkspaceID != "" && ac.t.deps.EnsureWorkspaceMains != nil {
		if mainsErr := ac.t.deps.EnsureWorkspaceMains(ac.joinedWorkspaceID); mainsErr != nil {
			slog.Error("sysagent: create_agent: the new member's main chat was not created; it is re-attempted when the chat is opened",
				"agent_id", ac.finalID, "workspace_id", ac.joinedWorkspaceID, "error", mainsErr)
			ac.mainsErr = mainsErr
		}
	}

	// agent-picker-freshness fix (#1009), corrected ordering: notify ONLY
	// after publishAgentActivation reports ActivationActive — i.e. only once
	// the live, in-memory config/registry that pkg/gateway/rest_agents.go::
	// listAgents reads actually contains the new agent. publishAgentActivation
	// BLOCKS on deps.WaitForPendingReloadFunc after the publish hook's nil
	// return to close the async-reload race the production wiring leaves open
	// (the gateway's UpsertAgentFastFunc closure falls back to the async
	// reloadTrigger on any failure, and the plain ReloadFunc is also
	// fire-and-forget — see publishAgentActivation's own doc comment for the
	// mechanism and why it uses the wait-only WaitForPendingReloadFunc rather
	// than WaitForReloadFunc, whose production wiring calls TriggerReload
	// unconditionally as its first action and would therefore kick off a fresh
	// full reload cycle on every fast-path success). On the queued-reload
	// branch this blocks until the queued reload has actually rebuilt the
	// registry; on the fast-path branch (no reload queued) the pending flag is
	// already clear so the call returns IMMEDIATELY. A nil return from
	// publishAgentActivation is now a genuine "agent is live and listable"
	// rather than "agent was queued for publication". Both
	// ActivationFailed (the call ran but errored — publishWarning above) and
	// ActivationNotAttempted (neither hook wired — degraded/test deps) leave
	// the agent NOT listable, so notifying on either would recreate the exact
	// race this ordering fix closes: a tab's agent_created handler
	// (src/store/chat/slices/frames.ts) refetching ['agents'] and getting a
	// response that still lacks the new agent. See Deps.NotifyAgentCreated's
	// doc comment for why this tool needs its own hook at all: create_agent
	// persists straight to the entity store and never reaches the gateway's
	// REST createAgent handler, the only OTHER place this broadcast fires —
	// that path is already correct here because its own equivalent
	// (emitAgentCreated, pkg/gateway/rest_agents_create.go::createAgent) is
	// called only after persistAgent's withToolPolicyCoverageGuard has
	// already refilled the in-memory list via refreshConfigAndRewireServices
	// AND waited it out via triggerReloadAndWait (the trigger-and-wait
	// counterpart to WaitForPendingReloadFunc's wait-only shape; AgentDeleteTool
	// uses the same WaitForPendingReloadFunc's sibling WaitForReloadFunc for
	// its own delete_agent → list_agents ghost-listing race).
	if activation == agentstore.ActivationActive {
		if fn := ac.t.deps.NotifyAgentCreated; fn != nil {
			fn(ac.finalID)
		}
	}

	// status must reflect actual runnability, not
	// a fixed "active" — a metadata-only agent (no workspace context at
	// creation) cannot run in chat or be delegated to at all
	// (runTurn's ErrAgentNotWorkspaceMember) despite the entity record and
	// workspace files above having been written successfully.
	status := "metadata_only"
	if ac.joinedWorkspace {
		status = "joined_workspace"
	}
	result := map[string]any{
		"id":                 ac.finalID,
		"name":               ac.name,
		"model":              ac.model,
		"type":               ac.agentType,
		"status":             status,
		"revision":           ac.mutation.Revision,
		"persistence_status": ac.mutation.PersistenceStatus,
		"activation_status":  activation,
		"changed_fields":     mutationFieldNames(ac.args),
	}
	if ac.agentType == "subagent_3p" {
		result["cli"] = ac.execCLI
		result["cli_path"] = ac.execCLIPath
	}
	if publishWarning != "" {
		result["publish_warning"] = publishWarning
	}
	if ac.joinedWorkspaceID != "" {
		result["workspace_id"] = ac.joinedWorkspaceID
	}
	return changeSuccess(result, ac.createVerdict(activation, publishWarning))
}

// createVerdict is create_agent's FR-030 verdict, built from the existing
// read-backs: the durable entity revision, the joined workspace (membership
// re-read from disk in joinWorkspaceTeam), the main-chat provisioning and the
// live activation.
func (ac *agentCreateToolExecute) createVerdict(activation agentstore.ActivationStatus, publishWarning string) changeOutcome {
	const scope = "saved agent record; workspace membership re-read from disk; main chat provisioning; live activation"
	notTested := []string{"a conversation turn with the new agent", "its model answering"}
	var missing, reasons []string
	if ac.joinMissed {
		missing = append(missing, "workspace membership in "+ac.target.ID)
		r := "the agent was saved but did not join workspace " + ac.target.ID
		if ac.joinErr != nil {
			r += ": " + ac.joinErr.Error()
		}
		reasons = append(reasons, r)
	}
	if ac.mainsErr != nil {
		missing = append(missing, "main chat in "+ac.joinedWorkspaceID)
		reasons = append(reasons, "the main chat was not created: "+ac.mainsErr.Error())
	}
	switch {
	case publishWarning != "":
		missing = append(missing, "live activation")
		reasons = append(reasons, publishWarning)
	case activation != agentstore.ActivationActive:
		missing = append(missing, "live activation")
		reasons = append(reasons, fmt.Sprintf("live activation was not confirmed (%s)", activation))
	}
	if len(missing) > 0 {
		return partlySaved(strings.Join(reasons, "; "), scope, missing, notTested...)
	}
	if !ac.joinedWorkspace {
		return savedNotWorking("the agent belongs to no workspace, so it cannot chat or be delegated to until "+
			"it is added to a workspace's team", scope, notTested...)
	}
	return working(scope, notTested...)
}

// createSaveFailedResult reports a failed entity save. The store's own
// persistence status decides the verdict: a partial write is "partly saved";
// otherwise nothing was saved. The stage, revision and cause are always named
// — never blank fields (#1064 A2).
func createSaveFailedResult(id string, mutation agentstore.MutationResult, err error) *tools.ToolResult {
	// The store marks a status as soon as any file is published; an empty
	// status means it failed before publishing anything (same reading as
	// update_agent's mutateErrorResult).
	stage := mutation.ErrorStage
	if stage == "" {
		stage = "prepare"
	}
	persistence := string(mutation.PersistenceStatus)
	if persistence == "" {
		persistence = string(agentstore.PersistenceNone)
	}
	body := map[string]any{
		"success": false,
		"error": map[string]any{
			"code":       "SAVE_FAILED",
			"message":    strings.TrimSpace(fmt.Sprintf("the agent %q could not be saved at stage %s: %v. %s", id, stage, err, mutation.Message)),
			"suggestion": "Read the agent state (get_agent) before retrying",
		},
		"id":                 id,
		"persistence_status": persistence,
		"error_stage":        stage,
		"revision":           mutation.Revision,
	}
	if mutation.PersistenceStatus == agentstore.PersistencePartial {
		// The store's own list of what it published is the durable part.
		changed := mutation.ChangedFields
		if changed == nil {
			changed = []string{}
		}
		body["changed_fields"] = changed
		return changeErrorWithOutcome(body, partlySaved("the agent record was partly written: "+err.Error(), "",
			[]string{"complete agent record"}))
	}
	return tools.ErrorResult(successJSON(body))
}

func parseOptionalHeartbeat(args map[string]any) (string, bool, error) {
	raw, present := args["heartbeat"]
	if !present {
		return "", false, nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", true, fieldErr("heartbeat", "heartbeat must be a string")
	}
	return s, true, nil
}

func createHomePartialResult(id, revision string, fields []string, err error) *tools.ToolResult {
	return changeErrorWithOutcome(map[string]any{
		"id":                 id,
		"code":               "SAVE_FAILED",
		"message":            "agent was saved but the agent home could not be initialized. Read current state before retrying.",
		"persistence_status": agentstore.PersistencePartial,
		"activation_status":  agentstore.ActivationNotAttempted,
		"revision":           revision,
		"changed_fields":     fields,
		"error_stage":        "init_home",
		"detail":             err.Error(),
	}, partlySaved("the agent record was saved but its home folder could not be created: "+err.Error(), "",
		[]string{"agent home folder", "workspace membership", "live activation"}))
}

func createHeartbeatPartialResult(id, revision string, fields []string, err error) *tools.ToolResult {
	return changeErrorWithOutcome(map[string]any{
		"id":                 id,
		"code":               "SAVE_FAILED",
		"message":            "agent was saved but HEARTBEAT.md could not be written. Read current state before retrying.",
		"persistence_status": agentstore.PersistencePartial,
		"activation_status":  agentstore.ActivationNotAttempted,
		"revision":           revision,
		"changed_fields":     fields,
		"error_stage":        "heartbeat",
		"detail":             err.Error(),
	}, partlySaved("the agent record was saved but HEARTBEAT.md could not be written: "+err.Error(), "",
		[]string{"HEARTBEAT.md", "workspace membership", "live activation"}))
}

// createJoinSeedPartialResult is the U5A-FIX-R1 / N3 truthful partial-failure
// result for a creation-in-context whose MEMBERSHIP landed but whose ordinary
// self-delegation row could not be seeded. It follows the same shape as the
// other create_agent partial results (createHomePartialResult /
// createHeartbeatPartialResult): the entity and (here) the workspace membership
// are already durably written, so this is a partial success that must NOT be
// dressed up as the ordinary "metadata_only" success — that status would say
// the agent belongs to no team when it is in fact a member, and would hide the
// missing row.
//
// The suggestion names a recovery that actually re-seeds: the self-row is a
// normal, editable Team-tab row, so the operator can add it directly (or remove
// and re-add the agent on the team). Re-running update_workspace with an
// UNCHANGED team does NOT recover it — that tool only seeds for NEWLY added
// members — so it is deliberately not offered as the fix.
func createJoinSeedPartialResult(id, revision string, fields []string, wsID string, err error) *tools.ToolResult {
	return changeErrorWithOutcome(map[string]any{
		"id":                 id,
		"code":               "SAVE_FAILED",
		"message":            fmt.Sprintf("agent %q was saved and added to workspace %q, but its self-delegation row could not be written. Read the workspace's Team tab before retrying.", id, wsID),
		"persistence_status": agentstore.PersistencePartial,
		"activation_status":  agentstore.ActivationNotAttempted,
		"revision":           revision,
		"changed_fields":     fields,
		"error_stage":        "seed_self_edge",
		"workspace_id":       wsID,
		"joined_workspace":   true,
		"self_edge_seeded":   false,
		"detail":             err.Error(),
		"suggestion":         "Open the workspace's Team tab and add this agent's self-delegation row (or remove and re-add the agent on the team), then confirm the row is present.",
	}, partlySaved(fmt.Sprintf("the agent was saved and joined workspace %q but its self-delegation row was not written: %v",
		wsID, err), "workspace membership re-read from disk",
		[]string{"self-delegation row in " + wsID, "live activation"}))
}

// joinWorkspaceTeam appends agentID to workspace wsID's CoreTeam (deduped)
// and persists the change, then seeds agentID's ordinary self-delegation row.
// Used exclusively by create_agent's creation-in-context join (ADR-046 P1,
// US-3 AS-2): a custom agent created from within a workspace's turn context
// joins THAT workspace's team only — never any other.
//
// session-core C-DELEGATE (FR-014/015, BDD-05.7): an authorized
// creation-with-membership seeds the new agent's ordinary {id→id} self-row
// IMMEDIATELY, through the same shared workspace-graph computation the install
// seed and the team-growth writer use. This is not the retired "expanding a
// team implies delegation trust" grant — it is the single, removable self-row
// the operator data list governs, and no other agent's trust changes. A join
// that must not seed (metadata-only creation with no membership) never reaches
// here.
//
// wsID here is the caller's tools.ToolWorkspaceID(ctx) — the channel-bound
// turn workspace id that memory routing uses — which is a different,
// independent signal from the identity-resolved workspace
// workspace.FindForAgentPreferring keys off (CoreTeam membership) for
// execution/work-dir purposes; the two can diverge, exactly as pkg/agent/loop.go's
// runTurn documents for its own mirrored resolution.
func (t *AgentCreateTool) joinWorkspaceTeam(wsID, agentID string) (joined bool, seedErr error) {
	// U5A-FIX-R1 / N1: the authoritative membership read-and-decision and the
	// self-row seed publication run in ONE workspace critical section
	// (workspacepkg.LockID + the authoritative re-read below). The membership
	// write used to be unlocked: a concurrent explicit delegation graph PUT
	// (which holds LockID and replaces the whole edge set) could land after the
	// membership became visible but before the seed acquired its own lock, and
	// the late implicit seed then re-added the self-row the operator had just
	// excluded — reversing a successful explicit policy decision. Holding one
	// lock across both phases means a competing writer lands wholly before or
	// wholly after this join, never between.
	unlock := workspacepkg.LockID(wsID)
	defer unlock()

	w, err := readWorkspaceFromDisk(t.deps.Home, wsID)
	if err != nil {
		return false, fmt.Errorf("read workspace %s: %w", wsID, err)
	}
	if !teamContains(w.CoreTeam, agentID) {
		w.CoreTeam = append(w.CoreTeam, agentID)
		w.UpdatedAt = nowISO()
		if err := writeEntity(workspacesDir(t.deps.Home), wsID, w); err != nil {
			return false, fmt.Errorf("write workspace %s: %w", wsID, err)
		}
	}

	// Deterministic test seam (always nil in production): fires with LockID
	// held, AFTER the membership write and BEFORE the authoritative re-read, so
	// a test can land a competing membership/graph edit at exactly this point.
	if joinWorkspaceMembershipTestHook != nil {
		joinWorkspaceMembershipTestHook(wsID, agentID)
	}

	// Re-read the AUTHORITATIVE membership (fresh from disk) and seed ONLY when
	// the agent is genuinely on the team. A member that is no longer on the team
	// (a competing removal that landed) gets no self-row: seeding one would
	// invent a delegation row for a non-member. The re-read is what turns "this
	// call appended a member" into "the member is actually on the team now".
	fresh, readErr := readWorkspaceFromDisk(t.deps.Home, wsID)
	if readErr != nil {
		// The membership write above already landed; only the confirm failed.
		return true, fmt.Errorf("re-read workspace %s membership: %w", wsID, readErr)
	}
	if !teamContains(fresh.CoreTeam, agentID) {
		return false, nil
	}

	// Seed the new agent's ordinary self-row. A failure here means the agent IS
	// a workspace member but its required self-row was not written — the caller
	// reports the truthful partial result (never a metadata_only success).
	if err := t.seedSelfEdgeLocked(wsID, agentID); err != nil {
		return true, err
	}
	return true, nil
}

// teamContains reports whether agentID is a (non-empty) entry of team.
func teamContains(team []string, agentID string) bool {
	for _, id := range team {
		if id == agentID {
			return true
		}
	}
	return false
}

// joinWorkspaceMembershipTestHook is a test-only synchronization seam fired by
// joinWorkspaceTeam while it holds workspacepkg.LockID(wsID), after the
// membership write and before the seed's authoritative membership re-read. It
// lets a test land a competing membership/graph edit at exactly that point
// deterministically (no sleeps), proving the join is one critical section and
// that the seed never fires for a member that is no longer on the team. Always
// nil in production; never set outside a _test.go file.
var joinWorkspaceMembershipTestHook func(wsID, agentID string)

// seedSelfEdgeLocked writes agentID's ordinary self-delegation row into
// workspace wsID's delegation store, preserving every existing edge. It uses
// the shared workspace.SelfEdgeSeedRows computation, so the row is identical to
// the install-seed and team-growth rows, and it threads the OPERATOR exclusion
// data (config.workspace_seed_defaults.self_edge.exclude_agent_ids) through
// selfEdgeExcludedAgentIDs — the exclusion is config DATA, never a Go identity
// predicate. Idempotent: an agent already carrying a self-row is left untouched.
//
// The caller MUST already hold workspacepkg.LockID(wsID) (joinWorkspaceTeam
// does, for its whole membership-plus-seed span): the lock pool is not
// reentrant, so this must NOT acquire it again.
func (t *AgentCreateTool) seedSelfEdgeLocked(wsID, agentID string) error {
	existing, ok := workspacepkg.LoadDelegation(t.deps.Home, wsID)
	if !ok {
		return fmt.Errorf("workspace %s delegation record is unreadable — refusing to seed a self-row over it", wsID)
	}
	rows := workspacepkg.SelfEdgeSeedRows(
		[]string{agentID}, existing, selfEdgeExcludedAgentIDs(t.deps), workspaceDelegationDepthCeiling(t.deps))
	if len(rows) == 0 {
		return nil
	}
	merged := append(append([]workspacepkg.DelegationEdge(nil), existing...), rows...)
	if err := saveWorkspaceDelegation(t.deps.Home, wsID, merged); err != nil {
		return fmt.Errorf("seed self-row for %s in workspace %s: %w", agentID, wsID, err)
	}
	return nil
}

// ---- system.agent.update ----

// AgentUpdateTool implements system.agent.update per BRD §D.4.2.
type AgentUpdateTool struct{ deps *Deps }

func NewAgentUpdateTool(d *Deps) *AgentUpdateTool { return &AgentUpdateTool{deps: d} }

func (t *AgentUpdateTool) Name() string           { return "update_agent" }
func (t *AgentUpdateTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *AgentUpdateTool) Description() string {
	return "Update an existing agent using the revision returned by get_agent. Only provided fields are changed; omitted fields are left as-is. Mia, Jim, Omnipus, Planner, Researcher, and General Purpose keep their built-in identity and instructions, while their skills are editable. An existing agent's per-agent tool policies and connector (MCP) assignments are changed with set_agent_permissions, never update_agent. Judge and Plan Supervisor allow instruction and supported tuning changes but keep fixed tool, connector, and skill capabilities. An empty provider clears its pin. Use fallback_models with model/provider objects; [] clears the fallback chain. The agent type and an external agent's cli/cli_path cannot change after creation; create a new agent for a different type."
}

func (t *AgentUpdateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":          map[string]any{"type": "string", "description": "Agent ID to update"},
			"revision":    map[string]any{"type": "string", "description": "Opaque revision returned by get_agent"},
			"name":        map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"},
			"soul": map[string]any{
				"type":        "string",
				"description": "New personality/instructions (overwrites SOUL.md)",
			},
			"model":           map[string]any{"type": "string", "description": "New primary model slug"},
			"fallback_models": fallbackModelsParameters(),
			"provider": map[string]any{
				"type":        "string",
				"description": "Explicit provider routing key for the primary model. Empty string clears an existing pin (falls back to default-provider resolution).",
			},
			"color": map[string]any{"type": "string", "description": "New avatar color: one of the ten palette hex values #4F8A80, #5E6973, #6E8BA8, #7A8C9E, #7B7290, #8E9196, #9C8A5B, #A97142, #B76E79, #CDBA96 (letter-case is normalized). Omitted leaves the stored color unchanged; null or empty is refused."},
			// Visual identity figure/role (#1303): same catalogs as create;
			// helpers in agent_visuals.go.
			"figure": figureUpdateProperty(),
			"role":   roleUpdateProperty(),
			// #904 D15: JSON null clears the own value ("use the global
			// limit"), so the schema admits null; 1..1000 bound structurally.
			"max_tool_iterations": map[string]any{
				"type": []string{"integer", "null"}, "minimum": 1, "maximum": 1000,
				"description": "New max tool calls per turn for this agent, 1-1000. Must not exceed the global limit set in Settings → Performance (a higher value is refused). Pass null to clear the agent's own value and use the global limit; omit to leave it unchanged.",
			},
			// tool_policy_changes and mcp_servers are deliberately absent:
			// set_agent_permissions owns them on an existing agent (FR-024).
			"skills":         skillsParameters(true),
			"memory_enabled": map[string]any{"type": "boolean"},
			"voice": map[string]any{
				"type":        "string",
				"description": "Per-agent TTS persona identifier. Persisted; playback is inactive in this release.",
			},
			// Like max_tool_iterations above: the schema admits JSON null so the
			// documented null clear reaches Execute through the registry's type
			// check (CHECK F1) — the tool's own validator
			// (applyContextWindow/jsonInt) still refuses non-integers.
			"context_window_override": map[string]any{"type": []string{"integer", "null"}, "description": "Positive token override. JSON null clears the override."},
			"model_params":            modelParamsParameters(),
			// #1064: AGENT.md, the agent definition file read on every turn,
			// beside soul. (Heartbeats are per workspace and live in the
			// workspace's member settings, not in a file, so there is no
			// heartbeat field here.)
			"agent_md": map[string]any{
				"type":        "string",
				"description": "New AGENT.md content (overwrites the agent's AGENT.md).",
			},
		},
		"required": []string{"id", "revision"},
	}
}

func (t *AgentUpdateTool) Execute(ctx context.Context, args map[string]any) (res *tools.ToolResult) {
	defer func() { res = finalizeChangeResult(t.Name(), res) }()
	return t.executeADR090(args)
}

// ---- system.agent.delete ----

// AgentDeleteTool implements system.agent.delete per BRD §D.4.2.
type AgentDeleteTool struct{ deps *Deps }

func NewAgentDeleteTool(d *Deps) *AgentDeleteTool { return &AgentDeleteTool{deps: d} }

func (t *AgentDeleteTool) Name() string           { return "delete_agent" }
func (t *AgentDeleteTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *AgentDeleteTool) Description() string {
	return "Delete an agent. IRREVERSIBLE. Removes the agent's entity record and applicable SOUL.md instructions. Preserves unrelated files in its home, including HEARTBEAT.md, local skills, and any vestigial MEMORY.md left by an older install. Also cascades: " +
		"every chat session in the shared session store that belongs SOLELY to this agent is deleted, together " +
		"with its uploaded files — a session another agent also participated in (e.g. via a mid-conversation " +
		"agent switch) is left untouched to avoid destroying that agent's history, and is reported separately " +
		"in the response. Every GTD task assigned to this agent is unassigned (not deleted — the task itself " +
		"survives); tasks it merely created keep that historical attribution. This agent is also removed from " +
		"every workspace's core_team, and every delegation-trust edge naming it (as either side) is dropped from " +
		"each affected workspace's delegation graph. Seeded built-ins (Mia, Jim, Omnipus, Planner, Researcher, General Purpose, Judge, and Plan Supervisor) cannot be deleted. " +
		"An agent that owns at least one active (running) Plan cannot be deleted — stop/reassign the Plan(s) before retrying. " +
		"A step that fails partway through is reported in the response rather than " +
		"silently swallowed — check for a warning field before assuming the cascade fully completed." +
		"\nParameters: id (required), revision (required, from get_agent), confirm (bool, must be true)."
}

func (t *AgentDeleteTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":       map[string]any{"type": "string"},
			"confirm":  map[string]any{"type": "boolean"},
			"revision": map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"},
		},
		"required": []string{"id", "confirm", "revision"},
	}
}

// agentOwnsActivePlan reports whether agentID owns at least one active
// (State=running, paused or not) Plan, by querying store directly. This
// mirrors pkg/agent's PlanEngine.HasActivePlansOwnedBy exactly — same
// semantics, same field/state check — but cannot call that method or import
// its type: pkg/agent imports this package (systools), so importing
// pkg/agent back from here would be an import cycle. store is the same
// *plan.Store instance the wired PlanEngine holds (see t.deps.PlanStore's
// doc comment), so this reads identical on-disk data through the identical
// query.
//
// Fails CLOSED (mirrors PlanEngine.HasActivePlansOwnedBy's fix-wave
// finding-1 contract): a plan-store List() error is returned to the caller
// rather than silently folded into a bare `false`. Callers MUST check err
// before trusting the bool — on error the correct behavior is to refuse the
// delete, never to fall back to this return value.
func agentOwnsActivePlan(store *plan.Store, agentID string) (bool, error) {
	plans, err := store.List(plan.Filter{})
	if err != nil {
		return false, fmt.Errorf("list plans: %w", err)
	}
	for i := range plans {
		if plans[i].OwnerAgentID == agentID && plans[i].State == plan.StateRunning {
			return true, nil
		}
	}
	return false, nil
}

// agentDeleteToolExecute carries the shared state of Execute across its stages.
type agentDeleteToolExecute struct {
	t                       *AgentDeleteTool
	args                    map[string]any
	id                      string
	revision                string
	omnipusHome             string
	store                   *agentstore.Store
	cascadeWarnings         []string
	sessionsDeleted         int
	sessionsPreservedShared int
	tasksUnassigned         int
	workspacesUpdated       int
	edgesRemoved            int
	publishWarning          string
	deletionResult          agentstore.MutationResult
}

func (t *AgentDeleteTool) Execute(ctx context.Context, args map[string]any) (res *tools.ToolResult) {
	defer func() { res = finalizeChangeResult(t.Name(), res) }()
	ad := &agentDeleteToolExecute{t: t, args: args}

	if r0, stop := ad.validateAndLoad(); stop {
		return r0
	}

	if r0, stop := ad.deleteAndCascade(); stop {
		return r0
	}

	ad.reload()

	return ad.respond()
}

// validateAndLoad validates the request and refuses deletion when agent or plan safeguards apply.
func (ad *agentDeleteToolExecute) validateAndLoad() (*tools.ToolResult, bool) {
	ad.id, _ = ad.args["id"].(string)
	ad.revision, _ = ad.args["revision"].(string)
	confirm, _ := ad.args["confirm"].(bool)
	if ad.id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", "")), true
	}
	if !confirm {
		return tools.ErrorResult(errorJSON("CONFIRMATION_REQUIRED",
			"confirm must be true to delete an agent",
			"Set confirm=true to proceed with deletion",
		)), true
	}
	if err := agentstore.ValidateRevision(ad.revision); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_REVISION", err.Error(), "Read the agent again and retry with its current revision")), true
	}
	// ADR-054 D2/D6 rule 5/§11 checklist item 6: agents are per-entity
	// records under entities/agents/<id>.json, not config.json's
	// agents.list — remove the entity record (via the agent store) FIRST,
	// before the best-effort workspace-directory cleanup below, instead of
	// splicing cfg.Agents.List inside t.deps.WithConfig.
	//
	// Resolve home ONCE, before constructing the agent store, so the entity
	// record and the workspace-directory cleanup below always agree on the
	// same $OMNIPUS_HOME (resolveOmnipusHome's doc comment; matches
	// AgentCreateTool/AgentUpdateTool's identical fix — this tool previously
	// used raw t.deps.Home for BOTH with no fallback at all, unlike the
	// other two).
	var homeErr error
	ad.omnipusHome, homeErr = resolveOmnipusHome(ad.t.deps.Home)
	if homeErr != nil {
		return tools.ErrorResult(errorJSON("WORKSPACE_ERROR", homeErr.Error(),
			"Set OMNIPUS_HOME environment variable")), true
	}

	ad.store = agentstore.New(ad.omnipusHome)
	existing, getErr := ad.store.Get(ad.id)
	if getErr != nil {
		if errors.Is(getErr, entity.ErrNotFound) {
			return tools.ErrorResult(errorJSON("AGENT_NOT_FOUND",
				fmt.Sprintf("No agent with ID %q", ad.id),
				"Use list_agents to see available agents",
			)), true
		}
		return tools.ErrorResult(errorJSON("SAVE_FAILED", getErr.Error(), "")), true
	}
	if existing.Locked {
		// AGENT_LOCKED (not the generic SAVE_FAILED): this is a deliberate,
		// permanent policy refusal, not a transient I/O failure — a caller
		// that only checks for SAVE_FAILED-shaped retriable errors must not
		// mistake "this delete will never succeed" for "try again". Mirrors
		// REST's DELETE /api/v1/agents/{id} handler (pkg/gateway/rest.go's
		// deleteAgent), which returns a dedicated "agent_locked" code (403)
		// for this exact refusal — a UAT run observed the tool and REST
		// paths disagreeing on the error code for the identical condition.
		return tools.ErrorResult(errorJSON("AGENT_LOCKED",
			fmt.Sprintf("agent %q is a locked seeded agent and cannot be deleted", ad.id),
			"seeded built-in agents cannot be deleted")), true
	}
	// Guard (ADR-049 D4/FR-065), ported from the REST deleteAgent handler
	// (pkg/gateway/rest.go, search "agent_owns_active_plans"): an agent
	// owning >=1 active (State=running) Plan cannot be deleted outright — the
	// plan engine would have no owner left to wake at its next decision
	// point, which would silently stall that plan. Checked BEFORE any
	// destructive action, alongside the locked-core-agent and default-agent
	// guards above.
	//
	// t.deps.PlanStore is nil in tests/degraded boot paths that never wired
	// the Plan feature (Wave 2-C1) — matches this file's configAgentPresenceSet
	// precedent (guard 0 above) for "no store visible" defaulting to skip the
	// check, never to fail-open on real data it can't see. When PlanStore IS
	// wired, agentOwnsActivePlan fails CLOSED on a store-read error (mirrors
	// PlanEngine.HasActivePlansOwnedBy's fix-wave finding-1 contract) — this
	// refuses the delete rather than treating "could not verify" as "no
	// active plans", the same posture REST's own 503 takes for that case.
	//
	// Cannot call agent.GetPlanEngine/PlanEngine.HasActivePlansOwnedBy
	// directly here: pkg/agent imports pkg/sysagent/tools (systools), so
	// importing pkg/agent back from this package would be a cycle.
	// t.deps.PlanStore is the same *plan.Store instance the wired PlanEngine
	// holds, so agentOwnsActivePlan below reads identical data via the
	// identical query the engine method uses.
	if ad.t.deps.PlanStore != nil {
		hasActive, planErr := agentOwnsActivePlan(ad.t.deps.PlanStore, ad.id)
		if planErr != nil {
			return tools.ErrorResult(errorJSON("SAVE_FAILED",
				fmt.Sprintf("could not verify plan ownership for agent %q: %s", ad.id, planErr.Error()),
				"Try again")), true
		}
		if hasActive {
			return tools.ErrorResult(errorJSON("AGENT_OWNS_ACTIVE_PLANS",
				fmt.Sprintf("agent %q owns active plans and cannot be deleted", ad.id),
				"Stop or reassign its plan(s) first, then retry delete_agent")), true
		}
	}
	return nil, false
}

// AgentDeleteCascadeResult is the outcome of RunAgentDeleteCascade, the
// shared FR-037/C-DELETE cleanup-first / record-last agent delete cascade.
type AgentDeleteCascadeResult struct {
	// SessionsDeleted, SessionsPreservedShared, TasksUnassigned,
	// WorkspacesUpdated and EdgesRemoved are the per-step cleanup counts, so a
	// caller can report exactly what the cascade did.
	SessionsDeleted         int
	SessionsPreservedShared int
	TasksUnassigned         int
	WorkspacesUpdated       int
	EdgesRemoved            int
	// Warnings collects every per-step cleanup failure so the caller reports
	// what happened rather than claiming full success.
	Warnings []string
	// CleanupFailed is true when at least one owned-data cleanup step failed.
	// The record was then deliberately left in place (record-last), so Deletion
	// and DeletionErr are zero-valued and the same Delete retries idempotently.
	CleanupFailed bool
	// Deletion is the record-removal result and DeletionErr its error. Only
	// meaningful when CleanupFailed is false; DeletionErr is nil on success.
	Deletion    agentstore.MutationResult
	DeletionErr error
}

// sessionStoreForDelete returns the live store from Deps, or a true nil interface
// when none is wired (never a typed-nil), so the cascade fails closed.
func (t *AgentDeleteTool) sessionStoreForDelete() AgentSessionStore {
	if t.deps == nil || t.deps.DeleteSessionStore == nil {
		return nil
	}
	return t.deps.DeleteSessionStore()
}

// AgentSessionStore is the narrow view of the loop's ONE live session store the
// delete cascade needs. The cascade never opens a store (DEL-10: one lock set per
// directory); its callers pass the shared store in.
type AgentSessionStore interface {
	ListSessionsFiltered(keep func(*session.UnifiedMeta) bool) ([]*session.UnifiedMeta, error)
	DeleteSession(id string) error
}

// CheckAgentDeleteRevision is the read-only revision comparison a delete does
// before any cleanup. It returns agentstore.ErrRevisionConflict (wrapped) when
// the agent's current revision differs from the caller's. A MISSING record is
// not decided here (the cascade keeps its idempotent-retry behaviour and
// DeleteState reports it); a record that exists but cannot be read is an error.
func CheckAgentDeleteRevision(omnipusHome, agentID, revision string) error {
	// A lock-free peek: this is an advisory pre-check, and DeleteState repeats the
	// authoritative comparison under the lock.
	current, err := agentstore.New(omnipusHome).PeekRevision(agentID)
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) {
			return nil // a missing record is the cascade's idempotent retry; DeleteState reports it
		}
		// A record that exists but cannot be read (corrupt, unreadable) must stop
		// the delete here, BEFORE sessions and task assignments are destroyed and
		// DeleteState then fails (delta review R1 #10).
		return fmt.Errorf("read the agent record before deleting it: %w", err)
	}
	if current != revision {
		return fmt.Errorf("%w: expected %s, current %s", agentstore.ErrRevisionConflict, revision, current)
	}
	return nil
}

// RunAgentDeleteCascade performs FR-037/C-DELETE's cleanup-first, record-last
// agent delete cascade — the SAME sequence BOTH the delete_agent tool and the
// REST DELETE /api/v1/agents/{id} handler run, so the two entry points cannot
// drift on the order (spec §C-DELETE: "UI/tool/API same cleanup-first/
// record-last cascade").
//
// Every artifact the agent owns or that dangles off it — its solely-owned
// sessions (with uploads), its task assignments, and its workspace core_team /
// delegation-edge references — is cleaned FIRST. If any cleanup step fails the
// record is deliberately left in place (CleanupFailed=true, so the record stays
// visible) and the caller reports the agent as only PARTLY DELETED, so the same
// Delete retries idempotently, including after a restart. Only when every
// cleanup step succeeds is the authoritative agent entity record (with any
// owned SOUL bytes) removed LAST, via store.DeleteState.
//
// omnipusHome is the resolved $OMNIPUS_HOME (see resolveOmnipusHome) and
// revision the caller's expected record revision. The agent store is built here
// so both callers pass identical inputs.
func RunAgentDeleteCascade(omnipusHome, agentID, revision string, sessions AgentSessionStore, stopSession func(sessionID string) string) AgentDeleteCascadeResult {
	res := AgentDeleteCascadeResult{Warnings: make([]string, 0, 3)}

	// Final-gate H1: a stale revision refuses BEFORE any destructive step. The
	// cleanup below deletes sessions and unassigns tasks, and DeleteState (the
	// authoritative, atomic comparison) runs last - without this read-only
	// pre-check a stale delete destroyed the agent's data and then answered
	// 409 with the agent still alive.
	if err := CheckAgentDeleteRevision(omnipusHome, agentID, revision); err != nil {
		res.DeletionErr = err
		return res
	}

	// Step 1a: delete every session in the SHARED session store
	// ($OMNIPUS_HOME/sessions/) that belongs SOLELY to this agent, together
	// with its uploads. See cascadeDeleteAgentSessions's doc comment for why a
	// session shared with another agent is deliberately left untouched rather
	// than deleted or partially edited.
	var w []string
	res.SessionsDeleted, res.SessionsPreservedShared, w = cascadeDeleteAgentSessions(sessions, stopSession, agentID)
	res.Warnings = append(res.Warnings, w...)

	// Step 1b: unassign (never delete) every GTD task currently assigned to
	// this agent, using the same task.Store.Update primitive (and per-task
	// locking) the ordinary task-update tools use. See
	// cascadeUnassignAgentTasks's doc comment for why CreatedByAgentID is
	// deliberately left untouched.
	res.TasksUnassigned, w = cascadeUnassignAgentTasks(omnipusHome, agentID)
	res.Warnings = append(res.Warnings, w...)

	// Step 2: cleanup of DANGLING REFERENCES to the agent across every
	// workspace — core_team membership and delegation-trust edges naming it as
	// either side.
	res.WorkspacesUpdated, res.EdgesRemoved, w = cascadeCleanAgentWorkspaceReferences(omnipusHome, agentID)
	res.Warnings = append(res.Warnings, w...)

	// Step 2b (WU-21 / #1197, amendment §3.2(6)): the agent's end-of-chat
	// summaries are workspace-owned files — one per (agent, workspace) pair —
	// so delete_agent removes this agent's pair file in EVERY workspace.
	// Other agents' files, and workspaces the agent merely left (membership
	// removal), stay untouched. A failure is an FR-037 warning that keeps the
	// record visible for retry, never a recursive erase of an unexpected
	// directory and never a claimed success.
	w = cascadeDeleteAgentCarryForwardFiles(omnipusHome, agentID)
	res.Warnings = append(res.Warnings, w...)

	// FR-037: if any owned-data cleanup step failed, STOP — the record must
	// remain VISIBLE so the same Delete can retry, and the result must say the
	// agent is only partly deleted rather than claim success.
	if len(res.Warnings) > 0 {
		res.CleanupFailed = true
		return res
	}

	// Step 3 (LAST): remove the authoritative entity record and any SOUL bytes
	// it owns. Unrelated files in the agent home are deliberately preserved;
	// DeleteState removes only the entity and applicable SOUL bytes owned by
	// this resource.
	res.Deletion, res.DeletionErr = agentstore.New(omnipusHome).DeleteState(agentID, revision)
	return res
}

// cascadeDeleteAgentCarryForwardFiles removes this agent's end-of-chat summary
// file — workspaces/<id>/.omnipus/carry-forward/<agentID>.md — in every
// workspace under $OMNIPUS_HOME (WU-21 / #1197, amendment §3.2(6)). It returns
// one warning per failure (an FR-037 cleanup failure: the caller keeps the
// entity record for retry) and none when there was nothing to remove.
// Enumeration is done with per-workspace ReadDir, not filepath.Glob: Glob
// silently skips subtrees it cannot read, so a permission-denied carry-forward
// directory would be indistinguishable from an empty one and the agent record
// would be deleted while its summary survived. A subtree that cannot be
// scanned is therefore a warning naming the owning workspace, never a silent
// skip; a workspace with no carry-forward room yet is the normal empty case.
// Each warning labels the owning workspace id, so the diagnostic names the
// owner and not merely a path buried in wrapped OS error text.
// Membership-only removal never reaches this function.
func cascadeDeleteAgentCarryForwardFiles(omnipusHome, agentID string) []string {
	if err := validation.EntityID(agentID); err != nil {
		return []string{fmt.Sprintf("carry-forward cleanup skipped: invalid agent id: %v", err)}
	}
	workspacesDir := filepath.Join(omnipusHome, memrooms.WorkspacesDirName)
	entries, err := os.ReadDir(workspacesDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // no workspaces exist, so no workspace-owned summary can.
		}
		return []string{fmt.Sprintf("carry-forward cleanup failed: cannot enumerate workspaces: %v", err)}
	}
	var warnings []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue // workspace records (workspaces/<id>.json) are not rooms.
		}
		workspaceID := entry.Name()
		carryForwardDir := filepath.Join(workspacesDir, workspaceID, memrooms.OmnipusRoomDir, "carry-forward")
		files, err := os.ReadDir(carryForwardDir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue // this workspace holds no carry-forward room yet.
			}
			// An unscannable subtree is a cleanup failure, never evidence of
			// an empty directory: report it instead of silently skipping the
			// agent's owned data there.
			warnings = append(warnings, fmt.Sprintf("carry-forward cleanup failed for workspace %q: cannot enumerate: %v", workspaceID, err))
			continue
		}
		for _, file := range files {
			if file.Name() != agentID+".md" {
				continue // only this agent's own summary; siblings stay.
			}
			// os.Remove deliberately refuses a directory: a non-file at the
			// pair path is reported, never recursively erased.
			if rmErr := os.Remove(filepath.Join(carryForwardDir, file.Name())); rmErr != nil {
				warnings = append(warnings, fmt.Sprintf("carry-forward cleanup failed for workspace %q: remove %s: %v", workspaceID, file.Name(), rmErr))
			}
		}
	}
	return warnings
}

// deleteAndCascade runs the shared cleanup-first, record-last cascade
// (RunAgentDeleteCascade) and maps its outcome onto the tool result: a cleanup
// failure or a failed record removal becomes the honest partly-deleted result
// (the record stays visible and the same Delete retries with its current
// revision), a stale revision becomes a REVISION_CONFLICT error, and a clean
// run returns stop=false so Execute proceeds to reload/respond.
func (ad *agentDeleteToolExecute) deleteAndCascade() (*tools.ToolResult, bool) {
	res := RunAgentDeleteCascade(ad.omnipusHome, ad.id, ad.revision, ad.t.sessionStoreForDelete(), ad.t.deps.StopSessionForDelete)
	ad.cascadeWarnings = res.Warnings
	ad.sessionsDeleted = res.SessionsDeleted
	ad.sessionsPreservedShared = res.SessionsPreservedShared
	ad.tasksUnassigned = res.TasksUnassigned
	ad.workspacesUpdated = res.WorkspacesUpdated
	ad.edgesRemoved = res.EdgesRemoved

	if res.CleanupFailed {
		return ad.partlyDeletedResult("cleanup"), true
	}

	ad.deletionResult = res.Deletion
	if res.DeletionErr != nil {
		if errors.Is(res.DeletionErr, agentstore.ErrRevisionConflict) {
			if ad.cascadeChangedAnything() {
				// The cleanup steps ran before the record's revision check
				// refused: the agent is partly deleted, never "nothing saved".
				return ad.partlyDeletedResult("record_delete_revision_conflict"), true
			}
			return tools.ErrorResult(errorJSON("REVISION_CONFLICT", res.DeletionErr.Error(), "Read the agent again and retry with its current revision")), true
		}
		return ad.partlyDeletedResult("record_delete"), true
	}
	return nil, false
}

// partlyDeletedResult builds the honest FR-037 failure result: the agent's
// owned data may already be partly cleaned, but its record is still visible and
// the same Delete retries with the agent's current revision. It carries the
// cascade counts already performed and the collected warnings so the caller can
// see exactly what did and did not happen — never a bare success.
func (ad *agentDeleteToolExecute) partlyDeletedResult(errorStage string) *tools.ToolResult {
	body := map[string]any{
		"success": false,
		"error": map[string]any{
			"code":       "SAVE_FAILED",
			"message":    fmt.Sprintf("agent %q is partly deleted (%s failed); its record remains visible and the same delete retries with its current revision", ad.id, errorStage),
			"suggestion": "Read the agent again and retry delete_agent with its current revision",
		},
		"id":                        ad.id,
		"partly_deleted":            true,
		"error_stage":               errorStage,
		"persistence_status":        string(agentstore.PersistencePartial),
		"activation_status":         string(agentstore.ActivationNotAttempted),
		"sessions_deleted":          ad.sessionsDeleted,
		"sessions_preserved_shared": ad.sessionsPreservedShared,
		"tasks_unassigned":          ad.tasksUnassigned,
		"workspaces_updated":        ad.workspacesUpdated,
		"delegation_edges_removed":  ad.edgesRemoved,
	}
	if len(ad.cascadeWarnings) > 0 {
		body["cascade_warnings"] = ad.cascadeWarnings
	}
	return changeErrorWithOutcome(body, partlySaved(
		fmt.Sprintf("agent %q is partly deleted: the %s step failed", ad.id, errorStage), "",
		append([]string{"agent record deletion"}, ad.cascadeWarnings...)))
}

// cascadeChangedAnything reports whether the delete cascade changed any
// durable data before stopping.
func (ad *agentDeleteToolExecute) cascadeChangedAnything() bool {
	return ad.sessionsDeleted+ad.tasksUnassigned+ad.workspacesUpdated+ad.edgesRemoved > 0
}

// reload reloads live configuration and records a warning if publication fails.
func (ad *agentDeleteToolExecute) reload() {
	// Trigger hot-reload so the deleted agent is immediately unroutable and
	// unlisted (RouteResolver, list_agents, GET /api/v1/agents) without a
	// restart — same pattern as AgentCreateTool.Execute above. Without this,
	// a deleted agent stayed live until the next process restart: its entity
	// record was gone from disk, but the in-memory cfg.Agents.List/registry
	// snapshot every routing/list read consults was never told to refresh.
	//
	// Deliberately NOT fast-pathed (issue #571): unlike create/update above,
	// delete has no UpsertAgentFastFunc-shaped counterpart to call.
	// AgentRegistry.RemoveAgent (pkg/agent/registry.go) is today only a bare
	// map delete under the write lock — its own doc comment states plainly
	// that no production code calls it — with NEITHER of UpsertAgentFast's
	// two safety properties: (1) it does not rebuild the registry's cached
	// RouteResolver/defaultAgentOverride, so a stale resolver would keep
	// routing to a deleted agent (the exact "reports success, changes
	// nothing" anti-pattern this project bans), and (2) it is not published
	// via the atomic clone-wire-then-CAS-swap sequence UpsertAgentFast uses,
	// so a concurrent reader could observe a half-updated registry. Adding
	// that parity belongs in pkg/agent alongside UpsertAgentFast itself, not
	// as a second, divergent implementation reinvented here. REST's own
	// deleteAgent (pkg/gateway/rest.go) has not been fast-pathed either — it
	// still calls triggerReloadAndWait unconditionally — so keeping delete on
	// the full reload here maintains parity with that call rather than
	// introducing a new asymmetry between the two delete entry points.
	// A reload failure here is surfaced IN the success payload (fix-wave
	// finding #3), not just logged: store.Delete above already durably
	// removed the entity record, so this stays a real success, but an
	// unqualified {"id":...,"deleted":true} response would tell the calling
	// agent the agent is gone when it in fact keeps ROUTING (stale
	// in-memory registry/RouteResolver, per this function's own doc comment
	// above) until the next restart.
	//
	// WaitForReloadFunc (preferred) BLOCKS until the reload actually lands —
	// see its doc comment on Deps for the ghost-listing race this closes: a
	// caller that calls delete_agent and then immediately list_agents in the
	// same turn must not still see the deleted agent because the reload was
	// only queued, not yet applied. Falls back to the fire-and-forget
	// ReloadFunc when WaitForReloadFunc is nil (tests/degraded wiring).
	//
	// WHY WaitForReloadFunc (TRIGGER-and-wait) AND NOT WaitForPendingReloadFunc
	// (wait-only): delete has no fast path on the publish side — nothing has
	// already removed the agent from the live in-memory list the way
	// UpsertAgentFast does for create/update — so we WANT the reload to
	// actually fire here. WaitForPendingReloadFunc would only poll
	// IsReloadPending and return immediately if nothing else happens to be
	// pending, WITHOUT ever triggering the reload delete actually needs —
	// leaving the just-deleted agent still ROUTED/listed (reachable) until
	// some OTHER caller happens to trigger a reload for its own reasons,
	// which may be never on an idle gateway.
	// WaitForReloadFunc's production wiring (rest_auth.go::waitForReload,
	// which calls TriggerReload unconditionally as its first action) is the
	// right shape here. Compare to publishAgentActivation's comment, which
	// uses WaitForPendingReloadFunc because that path's publish hook is the
	// fast path that ALREADY updated the live registry when it succeeds
	// inline — triggering again would be the bug #571 closed.

	if ad.t.deps.WaitForReloadFunc != nil {
		if err := ad.t.deps.WaitForReloadFunc(); err != nil {
			slog.Warn("sysagent: hot-reload after agent delete failed — agent remains routable/listed until restart",
				"id", ad.id, "error", err)
			ad.publishWarning = fmt.Sprintf(
				"agent %q was deleted from storage but hot-reload failed (%s); it may remain routable and "+
					"listed until the next gateway restart", ad.id, err.Error())
		}
	} else if ad.t.deps.ReloadFunc != nil {
		if err := ad.t.deps.ReloadFunc(); err != nil {
			slog.Warn("sysagent: hot-reload after agent delete failed — agent remains routable/listed until restart",
				"id", ad.id, "error", err)
			ad.publishWarning = fmt.Sprintf(
				"agent %q was deleted from storage but hot-reload failed (%s); it may remain routable and "+
					"listed until the next gateway restart", ad.id, err.Error())
		}
	}
}

// respond returns deletion and cascade results, including any partial-failure warnings.
func (ad *agentDeleteToolExecute) respond() *tools.ToolResult {
	result := map[string]any{
		"id":                        ad.id,
		"deleted":                   true,
		"sessions_deleted":          ad.sessionsDeleted,
		"sessions_preserved_shared": ad.sessionsPreservedShared,
		"tasks_unassigned":          ad.tasksUnassigned,
		"workspaces_updated":        ad.workspacesUpdated,
		"delegation_edges_removed":  ad.edgesRemoved,
	}
	if ad.publishWarning != "" {
		result["publish_warning"] = ad.publishWarning
		result["activation_status"] = agentstore.ActivationFailed
	} else {
		result["activation_status"] = agentstore.ActivationActive
	}
	result["persistence_status"] = ad.deletionResult.PersistenceStatus
	result["revision"] = ad.deletionResult.Revision
	result["changed_fields"] = ad.deletionResult.ChangedFields
	// A per-step cascade failure is best-effort and
	// non-fatal (the agent record above is already durably deleted either
	// way), but must not be silently swallowed — an unqualified
	// {"deleted":true} response with no hint of a stuck session/task/
	// delegation-edge reference would tell the caller the cascade fully
	// completed when it did not.
	if len(ad.cascadeWarnings) > 0 {
		result["cascade_warnings"] = ad.cascadeWarnings
	}
	const scope = "agent record deleted; cascade over its sessions, tasks and workspace references; live registry reload"
	var missing, reasons []string
	if ad.publishWarning != "" {
		missing = append(missing, "live registry update")
		reasons = append(reasons, ad.publishWarning)
	}
	if len(ad.cascadeWarnings) > 0 {
		missing = append(missing, ad.cascadeWarnings...)
		reasons = append(reasons, "some references to the agent could not be cleaned up")
	}
	if len(missing) > 0 {
		return changeSuccess(result, partlySaved(strings.Join(reasons, "; "), scope, missing))
	}
	return changeSuccess(result, working(scope))
}

// cascadeDeleteAgentSessions removes every session in the shared session
// store ($OMNIPUS_HOME/sessions/) that belongs SOLELY to agentID, together
// with its uploads ($OMNIPUS_HOME/uploads/<sessionID>/) — UnifiedStore's
// DeleteSession already cascades the uploads directory itself, so no
// separate upload-cleanup call is needed here.
//
// "Belongs solely" means agentID is the ONLY entry in the session's
// AgentIDs (the v2 multi-agent / "joined session model" field — see
// pkg/session's SessionMeta doc comment — PostLoad-backfilled from the
// legacy AgentID on every disk read, so this check is safe even against
// pre-v2 sessions). A session with MORE than one agent in AgentIDs (a
// conversation another agent also participated in) is
// deliberately left COMPLETELY untouched rather than partially edited:
// there is no supported primitive to remove a single id from AgentIDs
// (session.MetaPatch has no AgentIDs field), and hand-rolling a direct
// identity-file rewrite here would risk corrupting the record for an agent
// that still exists. Deleting the WHOLE session because one of several
// participants is being deleted would also destroy conversation history
// that legitimately still belongs to another, live agent. Given this
// operation is irreversible, leaving shared sessions alone — and reporting
// how many were preserved — is the safer of the two interpretations.
//
// Returns the count of sessions deleted, the count of shared sessions left
// untouched, and any non-fatal per-session failures. Best-effort throughout:
// one failure does not abort the sweep, matching every other cascade step in
// AgentDeleteTool.Execute.
func cascadeDeleteAgentSessions(store AgentSessionStore, stopSession func(sessionID string) string, agentID string) (deleted, sharedSkipped int, warnings []string) {
	// DEL-10 (architect final-gate item 1): the cascade works on the loop's ONE
	// live store, passed in by its caller. It never opens a store of its own: a
	// second store would take different shard locks over the same directory. A
	// missing store fails the cleanup (the record stays and the delete retries).
	if store == nil {
		return 0, 0, []string{"session store unavailable: the agent's sessions could not be cleaned up; retry the delete"}
	}
	metas, err := store.ListSessionsFiltered(func(m *session.UnifiedMeta) bool {
		for _, a := range m.AgentIDs {
			if a == agentID {
				return true
			}
		}
		return false
	})
	if err != nil {
		slog.Error("sysagent: cascade session delete: list failed", "agent_id", agentID, "error", err)
		return 0, 0, []string{"could not list the agent's sessions for cleanup; retry the delete"}
	}

	for _, m := range metas {
		if len(m.AgentIDs) != 1 {
			sharedSkipped++
			continue
		}
		// Stop the session's running work (the session and its helper tree)
		// BEFORE deleting it, as every other delete surface does. A session that
		// will not stop is kept and the cleanup fails, so the agent record
		// stays and the same delete retries.
		if stopSession != nil {
			if summary := stopSession(m.ID); summary != "" {
				slog.Warn("sysagent: cascade session delete: a session did not stop; it is kept",
					"agent_id", agentID, "session_id", m.ID, "summary", summary)
				warnings = append(warnings, "a running session of this agent did not stop and was kept; retry the delete once it has stopped")
				continue
			}
		}
		if delErr := store.DeleteSession(m.ID); delErr != nil {
			slog.Error("sysagent: cascade session delete: delete failed",
				"agent_id", agentID, "session_id", m.ID, "error", delErr)
			warnings = append(warnings, "a session of this agent could not be deleted; retry the delete")
			continue
		}
		deleted++
	}
	return deleted, sharedSkipped, warnings
}

// cascadeUnassignAgentTasks clears AgentID on every GTD task currently
// assigned to agentID, using task.Store.Update (the same primitive — and
// per-task locking — the ordinary update_task/update_task_in_workspace tools
// use) rather than a hand-rolled read-modify-write. The task itself is
// preserved, not deleted: a task's value is independent of which agent
// currently owns it, unlike a workspace's tasks (delete_workspace deletes
// those outright because they cannot exist without their parent workspace —
// a different relationship). Unassigning returns the task to "no current
// assignee" so a human or another agent can pick it up.
//
// Tasks the agent merely CREATED (CreatedByAgentID) are deliberately left
// untouched: that field is historical attribution, not a live routing
// reference — like a commit's author, it stays accurate even after the
// author is gone, and rewriting it would falsify the task's own history.
//
// Every task naming agentID as its AgentID is unassigned, REGARDLESS of its
// current status string. This deliberately does NOT filter on
// isValidTaskStatus, unlike delete_workspace's computeWorkspaceTaskCount
// cascade filter (which this function's status filter was originally copied
// from): there, the filter decides what to DELETE, so restricting it to
// valid-GTD-status tasks makes sense — a workspace being deleted only takes
// its live GTD tasks down with it. Here the goal is different: removing a
// DANGLING REFERENCE to a vanished agent from any task record, so a task
// sitting in a non-GTD/workflow status (e.g. a plan-linked or otherwise
// out-of-band status) still gets its stale agent_id cleared — leaving it
// there would be exactly the dangling-reference bug this cascade exists to
// prevent, just scoped to one status value instead of all of them. Returns
// the count of tasks unassigned and any non-fatal per-task failures;
// best-effort, one failure does not abort the sweep.
func cascadeUnassignAgentTasks(omnipusHome, agentID string) (unassigned int, warnings []string) {
	tasks, err := listEntities[unifiedTask](tasksDir(omnipusHome))
	if err != nil {
		return 0, []string{fmt.Sprintf("could not list tasks for cascade unassign: %v", err)}
	}
	store := taskStoreFor(omnipusHome)
	empty := ""
	for i := range tasks {
		if tasks[i].AgentID != agentID {
			continue
		}
		if _, updErr := store.Update(tasks[i].ID, task.Patch{AgentID: &empty}); updErr != nil {
			warnings = append(warnings, fmt.Sprintf("could not unassign task %s: %v", tasks[i].ID, updErr))
			continue
		}
		unassigned++
	}
	return unassigned, warnings
}

// cascadeCleanAgentWorkspaceReferences removes agentID from every
// workspace's core_team it appears on, and drops every delegation edge (in
// that workspace's own delegation store) naming it as from_agent or
// to_agent. Best-effort per workspace: a failure reading/writing one
// workspace's record is reported and does not block cleanup of the others.
//
// This mirrors delete_workspace's own post-record-delete cascade of its
// mount/delegation stores — "clean up what still points at the thing that's
// gone" — applied here to every workspace that referenced the deleted agent,
// rather than to the single workspace being deleted.
//
// Each workspace's load-modify-write is serialized under workspacepkg.LockID
// (the required guard for any load-modify-write of an existing
// workspaces/{id}.json — see that function's doc comment), matching
// update_workspace/delete_workspace's own locking discipline. SaveDelegation
// requires that same lock to already be held, which is why the delegation
// cleanup for a workspace happens inside the same locked span as its
// core_team edit rather than as a separate pass.
//
// Returns the number of workspaces actually modified (core_team and/or
// delegation edges), the number of delegation edges removed in total, and
// any non-fatal per-workspace failures.
func cascadeCleanAgentWorkspaceReferences(home, agentID string) (workspacesUpdated, edgesRemoved int, warnings []string) {
	dir := workspacesDir(home)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, 0, nil
		}
		return 0, 0, []string{fmt.Sprintf("could not list workspaces for cascade reference cleanup: %v", err)}
	}

	for _, e := range entries {
		if e.IsDir() || len(e.Name()) < 6 || e.Name()[len(e.Name())-5:] != ".json" {
			continue
		}
		wsID := e.Name()[:len(e.Name())-5]

		unlock := workspacepkg.LockID(wsID)
		wsUpdated, wsEdgesRemoved, wsWarn := cascadeCleanOneWorkspaceLocked(home, dir, wsID, agentID)
		unlock()

		if wsWarn != "" {
			warnings = append(warnings, wsWarn)
		}
		if wsUpdated {
			workspacesUpdated++
		}
		edgesRemoved += wsEdgesRemoved
	}
	return workspacesUpdated, edgesRemoved, warnings
}

// cascadeCleanOneWorkspaceLocked performs the actual core_team + delegation
// cleanup for a single workspace. Caller MUST already hold
// workspacepkg.LockID(wsID) for the duration of this call — split out of
// cascadeCleanAgentWorkspaceReferences only for readability, not as an
// independently-lockable entry point.
func cascadeCleanOneWorkspaceLocked(home, workspaceDir, wsID, agentID string) (updated bool, edgesRemoved int, warning string) {
	w, err := readWorkspaceFromDisk(home, wsID)
	if err != nil {
		return false, 0, fmt.Sprintf("could not read workspace %s for cascade reference cleanup: %v", wsID, err)
	}

	newTeam := make([]string, 0, len(w.CoreTeam))
	teamChanged := false
	for _, member := range w.CoreTeam {
		if member == agentID {
			teamChanged = true
			continue
		}
		newTeam = append(newTeam, member)
	}
	if teamChanged {
		w.CoreTeam = newTeam
		w.UpdatedAt = nowISO()
		if err := writeEntity(workspaceDir, wsID, w); err != nil {
			return false, 0, fmt.Sprintf("could not remove agent %s from workspace %s core_team: %v", agentID, wsID, err)
		}
		updated = true
	}

	edges, ok := workspacepkg.LoadDelegation(home, wsID)
	if !ok {
		// FAIL SAFE, not fail closed: the delegation store being unreadable
		// here must not block the rest of this best-effort cascade (the
		// core_team edit above, if any, already succeeded and stays). It is
		// reported so the operator knows a dangling edge for the deleted
		// agent may remain in that one workspace's graph.
		return updated, 0, fmt.Sprintf(
			"workspace %s delegation store unreadable — any edges naming the deleted agent were left in place",
			wsID)
	}
	var kept []workspacepkg.DelegationEdge
	removedHere := 0
	for _, edge := range edges {
		if edge.FromAgent == agentID || edge.ToAgent == agentID {
			removedHere++
			continue
		}
		kept = append(kept, edge)
	}
	if removedHere > 0 {
		if err := workspacepkg.SaveDelegation(home, wsID, kept); err != nil {
			return updated, 0, fmt.Sprintf("could not save workspace %s delegation edges: %v", wsID, err)
		}
		updated = true
		edgesRemoved = removedHere
	}
	return updated, edgesRemoved, ""
}
