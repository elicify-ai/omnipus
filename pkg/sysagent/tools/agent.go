// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/datamodel"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	workspacepkg "github.com/elicify-ai/omnipus/pkg/workspace"
)

// slugRegexp matches characters that should be replaced in agent name → ID conversion.
var slugRegexp = regexp.MustCompile(`[^a-z0-9]+`)

// hexColorRe validates HTML hex colors like "#22C55E".
var hexColorRe = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// toSlug converts a display name to a URL-safe slug ID.
func toSlug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugRegexp.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = fmt.Sprintf("agent-%d", rand.Intn(99999))
	}
	return s
}

// validateAgentColor returns an error when color is non-empty and not a valid
// 6-digit hex color. Empty strings pass validation (field is optional).
func validateAgentColor(s string) error {
	if s == "" {
		return nil
	}
	if !hexColorRe.MatchString(s) {
		return fmt.Errorf("invalid color %q: must match ^#[0-9A-Fa-f]{6}$", s)
	}
	return nil
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

// validateAgentIcon returns an error when icon is non-empty and contains
// characters outside the Phosphor icon naming convention (alphanumeric + hyphens,
// max 64 chars). Empty strings pass (field is optional).
func validateAgentIcon(s string) error {
	if s == "" {
		return nil
	}
	if len(s) > 64 {
		return fmt.Errorf("invalid icon %q: must be ≤64 characters", s)
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' {
			return fmt.Errorf("invalid icon %q: must be alphanumeric + hyphens only", s)
		}
	}
	return nil
}

// ---- system.agent.create ----

// AgentCreateTool implements system.agent.create per BRD §D.4.2.
type AgentCreateTool struct{ deps *Deps }

func NewAgentCreateTool(d *Deps) *AgentCreateTool { return &AgentCreateTool{deps: d} }

func (t *AgentCreateTool) Name() string           { return "create_agent" }
func (t *AgentCreateTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *AgentCreateTool) Description() string {
	return "Create a new agent with personality, model, tools, and configuration. Use agent_type to choose the runtime: 'Main' (default, native chat colleague), 'Subagent' (native delegation-only worker), or 'subagent_3p' (delegation-only worker on an external CLI — set cli and cli_path). name, description, soul, model, color (6-digit hex, e.g. #22C55E) and icon (a Phosphor icon name, e.g. 'robot') are all required — color/icon are rejected if missing or malformed. A new agent created inside a workspace's turn context joins that workspace's core_team and is immediately runnable there; created with no workspace context it is metadata-only (a member of no team) and cannot run in chat or be delegated to until an operator adds it to a workspace's Team tab — check the response's status field (joined_workspace vs metadata_only) to tell which happened."
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
				"type":        "string",
				"description": "The agent's personality, role, and behavioral instructions (written to SOUL.md)",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Primary LLM model slug (e.g. 'z-ai/glm-5v-turbo')",
			},
			"color": map[string]any{"type": "string", "description": "Hex avatar color (e.g. '#22C55E')"},
			"icon": map[string]any{
				"type":        "string",
				"description": "Phosphor icon name (e.g. 'robot', 'pencil', 'book')",
			},
			// Optional — agent type + external-CLI worker runtime.
			"agent_type": map[string]any{
				"type":        "string",
				"enum":        []any{"Main", "Subagent", "subagent_3p"},
				"description": "Agent type. 'Main' (default) = native chat colleague on the Omnipus engine. 'Subagent' = native delegation-only worker. 'subagent_3p' = delegation-only worker that runs on an EXTERNAL CLI (requires cli + cli_path).",
			},
			"cli": map[string]any{
				"type":        "string",
				"enum":        []any{"claude-code", "codex", "opencode"},
				"description": "External CLI runtime. REQUIRED when agent_type='subagent_3p'. 'claude-code' for Claude (claudex), 'opencode' for opencode, 'codex' for Codex.",
			},
			"cli_path": map[string]any{
				"type":        "string",
				"description": "Optional. Absolute path to the external CLI binary. Leave EMPTY to use the CLI's default binary on $PATH (claude / codex / opencode). Set it only when this machine invokes the CLI via a wrapper or a non-standard path — and derive the real path on this system, never hardcode a guess.",
			},
			"provider": map[string]any{
				"type":        "string",
				"description": "Explicit provider routing key for the primary model (e.g. 'openrouter'). Pins model resolution to this provider instead of inferring one from the slug.",
			},
			"model_fallbacks": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Fallback model slugs tried in order if primary fails",
			},
			"heartbeat": map[string]any{
				"type":        "string",
				"description": "Proactive scheduling instructions (written to HEARTBEAT.md)",
			},
			"max_tool_iterations": map[string]any{
				"type":        "integer",
				"description": "Max tool calls per turn (0 = inherit the system default)",
			},
		},
		"required": []string{"name", "description", "soul", "model", "color", "icon"},
	}
}

func (t *AgentCreateTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	// Validate mandatory fields.
	name, _ := args["name"].(string)
	if strings.TrimSpace(name) == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "name is required", "Provide a name for the agent"))
	}
	description, _ := args["description"].(string)
	if strings.TrimSpace(description) == "" {
		return tools.ErrorResult(
			errorJSON("INVALID_INPUT", "description is required", "Provide a one-line description"),
		)
	}
	soul, _ := args["soul"].(string)
	if strings.TrimSpace(soul) == "" {
		return tools.ErrorResult(
			errorJSON(
				"INVALID_INPUT",
				"soul is required",
				"Provide the agent's personality and behavioral instructions",
			),
		)
	}
	model, _ := args["model"].(string)
	if strings.TrimSpace(model) == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "model is required", "Provide the LLM model slug"))
	}
	color, _ := args["color"].(string)
	if err := validateAgentColor(color); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_COLOR", err.Error(), "Use a 6-digit hex color, e.g. #22C55E"))
	}
	icon, _ := args["icon"].(string)
	if err := validateAgentIcon(icon); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_ICON", err.Error(), "Use alphanumeric + hyphens, e.g. robot"))
	}

	// Agent type + external-CLI worker runtime (W4 taxonomy). Default "Main".
	agentType, _ := args["agent_type"].(string)
	if agentType == "" {
		agentType = "Main"
	}
	var execCLI, execCLIPath string
	switch agentType {
	case "Main", "Subagent", "subagent_3p":
	default:
		return tools.ErrorResult(errorJSON("INVALID_INPUT",
			"agent_type must be one of Main, Subagent, subagent_3p",
			"Use 'subagent_3p' for an external CLI worker"))
	}
	if agentType == "subagent_3p" {
		execCLI, _ = args["cli"].(string)
		switch execCLI {
		case "claude-code", "codex", "opencode":
		default:
			return tools.ErrorResult(errorJSON("INVALID_INPUT",
				"cli is required for subagent_3p and must be one of claude-code, codex, opencode",
				"Set cli (e.g. 'opencode' or 'claude-code')"))
		}
		// cli_path is OPTIONAL: when empty the driver invokes the CLI's default
		// binary name on $PATH (claude / codex / opencode). Set it only when this
		// machine uses a wrapper or a non-standard binary path.
		execCLIPath, _ = args["cli_path"].(string)
	}

	id := toSlug(name)
	if err := validateID(id); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), ""))
	}

	// ADR-054 D2/§11 checklist item 6: agents are per-entity records under
	// entities/agents/<id>.json, not config.json's agents.list — persist via
	// the agent store instead of appending to cfg.Agents.List inside
	// t.deps.WithConfig. agentstore.Store.Create performs the existence
	// check (ErrAlreadyExists) and the write atomically under its own
	// per-entity lock, so there is no separate check-then-append TOCTOU
	// window to close here.
	newAgent := config.AgentConfig{
		ID:          id,
		Name:        name,
		Description: description,
		Color:       color,
		Icon:        icon,
		Model:       &config.AgentModelConfig{Primary: model},
	}
	// Agent type / runtime (W4). Subagent + subagent_3p persist as worker;
	// subagent_3p additionally carries an external-CLI executor so dispatch
	// runs it on the named CLI (claude-code / codex / opencode).
	switch agentType {
	case "Subagent":
		newAgent.Type = config.AgentTypeWorker
	case "subagent_3p":
		// External CLI worker: Type=worker + an external-cli executor. The
		// worker's own run model is the top-level Model.Primary (set above);
		// SubagentsConfig.Model is for THIS agent's own sub-delegations, so we
		// leave it unset to avoid a misleading duplicate.
		newAgent.Type = config.AgentTypeWorker
		newAgent.Subagents = &config.SubagentsConfig{
			Executor: &config.ExecutorConfig{
				Kind:    config.ExecutorKindExternalCLI,
				CLI:     execCLI,
				CLIPath: execCLIPath,
			},
		}
	}
	// Optional: model fallbacks.
	if fb, ok := args["model_fallbacks"].([]any); ok && len(fb) > 0 {
		for _, v := range fb {
			if s, ok := v.(string); ok && s != "" {
				newAgent.Model.Fallbacks = append(newAgent.Model.Fallbacks, s)
			}
		}
	}
	// Optional: explicit provider pin for the primary model (O3 two-field
	// model — config.AgentModelConfig.Provider's doc comment). Mirrors
	// REST's createAgent handling of the same wire field. Previously declared
	// in Parameters() but never read here — a caller passing it got a
	// success response implying it was applied when it silently was not.
	if v, ok := args["provider"].(string); ok {
		newAgent.Model.Provider = strings.TrimSpace(v)
	}
	// Optional: per-turn tool-call cap (config.AgentConfig.MaxToolIterations's
	// doc comment: 0/absent inherits agents.defaults.max_tool_iterations).
	// Same previously-silently-ignored-parameter bug as provider above.
	if v, ok := args["max_tool_iterations"].(float64); ok {
		n := int(v)
		if n < 0 {
			return tools.ErrorResult(errorJSON("INVALID_INPUT",
				"max_tool_iterations must be >= 0", "Use 0 to inherit the system default"))
		}
		newAgent.MaxToolIterations = n
	}
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
	newAgent.Tools = coreagent.NewCustomAgentToolsCfg()

	// Resolve home ONCE, before constructing the agent store, so the entity
	// record (below) and the agent's own workspace (SOUL.md/HEARTBEAT.md,
	// further down) always agree on the same $OMNIPUS_HOME — see
	// resolveOmnipusHome's doc comment for the split-brain this closes.
	omnipusHome, homeErr := resolveOmnipusHome(t.deps.Home)
	if homeErr != nil {
		return tools.ErrorResult(errorJSON("WORKSPACE_ERROR", homeErr.Error(),
			"Set OMNIPUS_HOME environment variable"))
	}

	finalID := id
	if err := agentstore.New(omnipusHome).Create(id, &newAgent); err != nil {
		if errors.Is(err, entity.ErrAlreadyExists) {
			return tools.ErrorResult(errorJSON(
				"AGENT_ALREADY_EXISTS",
				fmt.Sprintf("An agent with ID %q already exists", id),
				"Use update_agent to modify the existing agent or choose a different name",
			))
		}
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), "Check disk space and permissions"))
	}

	// Create agent workspace and write personality files.
	wsPath := omnipusHome + "/agents/" + finalID
	if err := datamodel.InitAgentHome(omnipusHome, finalID); err != nil {
		return tools.ErrorResult(errorJSON("WORKSPACE_ERROR",
			"could not create agent workspace: "+err.Error(),
			"Check disk space and permissions"))
	}

	// Write SOUL.md — this is the agent's personality and is mandatory.
	if err := os.WriteFile(wsPath+"/SOUL.md", []byte(soul), 0o644); err != nil {
		return tools.ErrorResult(errorJSON("WRITE_ERROR",
			"could not write SOUL.md: "+err.Error(),
			"Check disk space and permissions"))
	}
	// Write HEARTBEAT.md if provided.
	if hb, ok := args["heartbeat"].(string); ok && strings.TrimSpace(hb) != "" {
		if err := os.WriteFile(wsPath+"/HEARTBEAT.md", []byte(hb), 0o644); err != nil {
			slog.Warn("sysagent: could not write HEARTBEAT.md", "id", finalID, "error", err)
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
	// below (M1 fix, half_b_report.md): the caller must be able to tell
	// "joined_workspace" (immediately runnable there) from "metadata_only"
	// (a member of no team, cannot run in chat or be delegated to) without
	// re-deriving it from whether a workspace context happened to be
	// present, since a join attempt can also fail (logged, non-fatal).
	joinedWorkspace := false
	if wsID := tools.ToolWorkspaceID(ctx); wsID != "" {
		if err := t.joinWorkspaceTeam(wsID, finalID); err != nil {
			slog.Warn("sysagent: create_agent: could not add new agent to workspace core_team",
				"agent_id", finalID, "workspace_id", wsID, "error", err)
		} else {
			joinedWorkspace = true
		}
	}

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
	var publishWarning string
	if t.deps.UpsertAgentFastFunc != nil {
		if err := t.deps.UpsertAgentFastFunc(finalID); err != nil {
			slog.Warn("sysagent: fast agent upsert after agent create failed — agent available after restart",
				"id", finalID, "error", err)
			publishWarning = fmt.Sprintf(
				"agent %q was created but is not yet live: fast publish failed (%s); it will become routable "+
					"after the next config reload or gateway restart", finalID, err.Error())
		}
	} else if t.deps.ReloadFunc != nil {
		if err := t.deps.ReloadFunc(); err != nil {
			slog.Warn("sysagent: hot-reload after agent create failed — agent available after restart",
				"id", finalID, "error", err)
			publishWarning = fmt.Sprintf(
				"agent %q was created but is not yet live: hot-reload failed (%s); it will become routable "+
					"after the next gateway restart", finalID, err.Error())
		}
	}

	// M1 fix (half_b_report.md): status must reflect actual runnability, not
	// a fixed "active" — a metadata-only agent (no workspace context at
	// creation) cannot run in chat or be delegated to at all
	// (runTurn's ErrAgentNotWorkspaceMember) despite the entity record and
	// workspace files above having been written successfully.
	status := "metadata_only"
	if joinedWorkspace {
		status = "joined_workspace"
	}
	result := map[string]any{
		"id":     finalID,
		"name":   name,
		"model":  model,
		"type":   agentType,
		"status": status,
	}
	if agentType == "subagent_3p" {
		result["cli"] = execCLI
		result["cli_path"] = execCLIPath
	}
	if publishWarning != "" {
		result["publish_warning"] = publishWarning
	}
	return tools.NewToolResult(successJSON(result))
}

// joinWorkspaceTeam appends agentID to workspace wsID's CoreTeam (deduped)
// and persists the change. Used exclusively by create_agent's
// creation-in-context join (ADR-046 P1, US-3 AS-2): a custom agent created
// from within a workspace's turn context joins THAT workspace's team only —
// never any other, and never seeds a Delegation[] trust edge (FR-038:
// expanding a workspace team must not create or imply delegation trust).
//
// wsID here is the caller's tools.ToolWorkspaceID(ctx) — the channel-bound
// turn workspace id that memory routing uses — which is a different,
// independent signal from the identity-resolved workspace
// workspace.FindForAgentPreferring keys off (CoreTeam membership) for
// execution/work-dir purposes; the two can diverge, exactly as pkg/agent/loop.go's
// runTurn documents for its own mirrored resolution.
func (t *AgentCreateTool) joinWorkspaceTeam(wsID, agentID string) error {
	w, err := readWorkspaceFromDisk(t.deps.Home, wsID)
	if err != nil {
		return fmt.Errorf("read workspace %s: %w", wsID, err)
	}
	for _, id := range w.CoreTeam {
		if id == agentID {
			return nil // already a member
		}
	}
	w.CoreTeam = append(w.CoreTeam, agentID)
	w.UpdatedAt = nowISO()
	if err := writeEntity(workspacesDir(t.deps.Home), wsID, w); err != nil {
		return fmt.Errorf("write workspace %s: %w", wsID, err)
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
	return "Update an existing agent's configuration. Only provided fields are changed; omitted fields are left as-is. Locked core agents (Mia, Jim, Ava, Ray) cannot be modified at all — the call is refused. An empty string is silently IGNORED for name/description/color/icon (they cannot be cleared this way — omit the field instead), but an empty string for provider CLEARS an existing provider pin (falls back to default-provider resolution) — this is a deliberate asymmetry, not a bug. The agent's type (Main/Subagent/subagent_3p) and, for subagent_3p, its external CLI runtime (cli/cli_path) cannot be changed after creation — delete and recreate the agent to change these."
}

func (t *AgentUpdateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":          map[string]any{"type": "string", "description": "Agent ID to update"},
			"name":        map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"},
			"soul": map[string]any{
				"type":        "string",
				"description": "New personality/instructions (overwrites SOUL.md)",
			},
			"model":           map[string]any{"type": "string", "description": "New primary model slug"},
			"model_fallbacks": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"provider": map[string]any{
				"type":        "string",
				"description": "Explicit provider routing key for the primary model. Empty string clears an existing pin (falls back to default-provider resolution).",
			},
			"color":               map[string]any{"type": "string"},
			"icon":                map[string]any{"type": "string"},
			"heartbeat":           map[string]any{"type": "string", "description": "New HEARTBEAT.md content"},
			"max_tool_iterations": map[string]any{"type": "integer", "description": "New max tool calls per turn (0 = inherit the system default)"},
		},
		"required": []string{"id"},
	}
}

func (t *AgentUpdateTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}
	// Path traversal protection: validate id contains no path separators.
	if err := validateID(id); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), ""))
	}

	// Validate color and icon before any mutation.
	color, colorPresent := args["color"].(string)
	if colorPresent {
		if err := validateAgentColor(color); err != nil {
			return tools.ErrorResult(errorJSON("INVALID_COLOR", err.Error(), "Use a 6-digit hex color, e.g. #22C55E"))
		}
	}
	icon, iconPresent := args["icon"].(string)
	if iconPresent {
		if err := validateAgentIcon(icon); err != nil {
			return tools.ErrorResult(errorJSON("INVALID_ICON", err.Error(), "Use alphanumeric + hyphens, e.g. robot"))
		}
	}
	// Validate max_tool_iterations before any mutation (same pattern as
	// color/icon above) so an invalid value never reaches the store's
	// read-modify-write closure.
	maxToolIterations, maxToolIterationsPresent := args["max_tool_iterations"].(float64)
	if maxToolIterationsPresent && maxToolIterations < 0 {
		return tools.ErrorResult(errorJSON("INVALID_INPUT",
			"max_tool_iterations must be >= 0", "Use 0 to inherit the system default"))
	}
	provider, providerPresent := args["provider"].(string)

	// ADR-054 D2/§11 checklist item 6: agents are per-entity records under
	// entities/agents/<id>.json, not config.json's agents.list — persist via
	// the agent store's read-modify-write instead of t.deps.WithConfig +
	// cfg.Agents.List splicing.
	//
	// Resolve home ONCE, before constructing the agent store, so the entity
	// record and the workspace file writes below always agree on the same
	// $OMNIPUS_HOME (resolveOmnipusHome's doc comment; matches
	// AgentCreateTool's identical fix).
	omnipusHome, homeErr := resolveOmnipusHome(t.deps.Home)
	if homeErr != nil {
		return tools.ErrorResult(errorJSON("WORKSPACE_ERROR", homeErr.Error(),
			"Set OMNIPUS_HOME environment variable"))
	}

	var updated []string
	_, updateErr := agentstore.New(omnipusHome).Update(id, func(a *config.AgentConfig) error {
		if a.Locked {
			return fmt.Errorf("agent %q is a locked core agent and cannot be modified", id)
		}
		if v, ok := args["name"].(string); ok && v != "" {
			a.Name = v
			updated = append(updated, "name")
		}
		if v, ok := args["description"].(string); ok && v != "" {
			a.Description = v
			updated = append(updated, "description")
		}
		if colorPresent && color != "" {
			a.Color = color
			updated = append(updated, "color")
		}
		if iconPresent && icon != "" {
			a.Icon = icon
			updated = append(updated, "icon")
		}
		// Model config.
		if v, ok := args["model"].(string); ok && v != "" {
			if a.Model == nil {
				a.Model = &config.AgentModelConfig{}
			}
			a.Model.Primary = v
			updated = append(updated, "model")
		}
		if fb, ok := args["model_fallbacks"].([]any); ok {
			if a.Model == nil {
				a.Model = &config.AgentModelConfig{}
			}
			a.Model.Fallbacks = nil
			for _, v := range fb {
				if s, ok := v.(string); ok && s != "" {
					a.Model.Fallbacks = append(a.Model.Fallbacks, s)
				}
			}
			updated = append(updated, "model_fallbacks")
		}
		// O3 two-field model: persist (or clear) the explicit primary
		// provider — mirrors REST's updateAgent handling of the same wire
		// field (req.Provider). A non-empty value pins the provider; an
		// explicit empty string clears it (falls back to default-provider
		// resolution). Previously declared in Parameters() but never read
		// here — a caller passing it got a success response implying it was
		// applied when it silently was not.
		if providerPresent {
			if a.Model == nil {
				a.Model = &config.AgentModelConfig{}
			}
			a.Model.Provider = strings.TrimSpace(provider)
			updated = append(updated, "provider")
		}
		// Per-turn tool-call cap — mirrors REST's updateAgent handling of
		// the same wire field (req.MaxToolIterations). Same
		// previously-silently-ignored-parameter bug as provider above.
		if maxToolIterationsPresent {
			a.MaxToolIterations = int(maxToolIterations)
			updated = append(updated, "max_tool_iterations")
		}
		// ADR-037: can_delegate_to is retired — see the matching comment in
		// AgentCreateTool.Execute above. The workspace Team tab is the only
		// place delegation trust is configured.
		return nil
	})
	if updateErr != nil {
		if errors.Is(updateErr, entity.ErrNotFound) {
			return tools.ErrorResult(errorJSON("AGENT_NOT_FOUND",
				fmt.Sprintf("No agent with ID %q", id),
				"Use list_agents to see available agents",
			))
		}
		return tools.ErrorResult(errorJSON("SAVE_FAILED", updateErr.Error(), ""))
	}

	// Write workspace files if provided.
	wsPath := omnipusHome + "/agents/" + id
	if v, ok := args["soul"].(string); ok && strings.TrimSpace(v) != "" {
		if err := os.MkdirAll(wsPath, 0o700); err != nil {
			return tools.ErrorResult(errorJSON("WRITE_ERROR", "could not update SOUL.md: "+err.Error(), ""))
		} else if err := os.WriteFile(wsPath+"/SOUL.md", []byte(v), 0o644); err != nil {
			return tools.ErrorResult(errorJSON("WRITE_ERROR", "could not update SOUL.md: "+err.Error(), ""))
		} else {
			updated = append(updated, "soul")
		}
	}
	if v, ok := args["heartbeat"].(string); ok && strings.TrimSpace(v) != "" {
		if err := os.MkdirAll(wsPath, 0o700); err == nil {
			if err := os.WriteFile(wsPath+"/HEARTBEAT.md", []byte(v), 0o644); err != nil {
				slog.Warn("sysagent: could not write HEARTBEAT.md", "id", id, "error", err)
			} else {
				updated = append(updated, "heartbeat")
			}
		}
	}

	// Publish the update so it takes effect immediately in routing,
	// list_agents, and GET /api/v1/agents, without a restart — same
	// fast-path-first pattern as AgentCreateTool.Execute above. A publish
	// failure here is surfaced IN the success payload (fix-wave finding #3),
	// not just logged: the store.Update above already durably persisted
	// `updated`, so this is a real success, but an unqualified
	// {"id":...,"updated_fields":[...]} response would tell the calling
	// agent the change took effect when the LIVE routing/registry state
	// still reflects the OLD config until the next restart or reload.
	var publishWarning string
	if t.deps.UpsertAgentFastFunc != nil {
		if err := t.deps.UpsertAgentFastFunc(id); err != nil {
			slog.Warn("sysagent: fast agent upsert after agent update failed — change available after restart",
				"id", id, "error", err)
			publishWarning = fmt.Sprintf(
				"agent %q was updated but the change is not yet live: fast publish failed (%s); it will take "+
					"effect after the next config reload or gateway restart", id, err.Error())
		}
	} else if t.deps.ReloadFunc != nil {
		if err := t.deps.ReloadFunc(); err != nil {
			slog.Warn("sysagent: hot-reload after agent update failed — change available after restart",
				"id", id, "error", err)
			publishWarning = fmt.Sprintf(
				"agent %q was updated but the change is not yet live: hot-reload failed (%s); it will take "+
					"effect after the next gateway restart", id, err.Error())
		}
	}

	result := map[string]any{
		"id":             id,
		"updated_fields": updated,
	}
	if publishWarning != "" {
		result["publish_warning"] = publishWarning
	}
	return tools.NewToolResult(successJSON(result))
}

// ---- system.agent.delete ----

// AgentDeleteTool implements system.agent.delete per BRD §D.4.2.
type AgentDeleteTool struct{ deps *Deps }

func NewAgentDeleteTool(d *Deps) *AgentDeleteTool { return &AgentDeleteTool{deps: d} }

func (t *AgentDeleteTool) Name() string           { return "delete_agent" }
func (t *AgentDeleteTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *AgentDeleteTool) Description() string {
	return "Delete an agent. IRREVERSIBLE. Removes the agent's entity record and its home directory " +
		"($OMNIPUS_HOME/agents/<id>/ — SOUL.md, HEARTBEAT.md, MEMORY.md, its skills folder). Also cascades: " +
		"every chat session in the shared session store that belongs SOLELY to this agent is deleted, together " +
		"with its uploaded files — a session another agent also participated in (e.g. via a mid-conversation " +
		"agent switch) is left untouched to avoid destroying that agent's history, and is reported separately " +
		"in the response. Every GTD task assigned to this agent is unassigned (not deleted — the task itself " +
		"survives); tasks it merely created keep that historical attribution. This agent is also removed from " +
		"every workspace's core_team, and every delegation-trust edge naming it (as either side) is dropped from " +
		"each affected workspace's delegation graph. Locked core agents (Mia, Jim, Ava, Ray), the currently " +
		"configured default agent, and an agent that owns at least one active (running) Plan cannot be deleted " +
		"— set another agent as default first (Agents screen ★), or stop/reassign the Plan(s), before retrying. " +
		"A step that fails partway through is reported in the response rather than " +
		"silently swallowed — check for a warning field before assuming the cascade fully completed." +
		"\nParameters: id (required), confirm (bool, must be true)."
}

func (t *AgentDeleteTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":      map[string]any{"type": "string"},
			"confirm": map[string]any{"type": "boolean"},
		},
		"required": []string{"id", "confirm"},
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

func (t *AgentDeleteTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	confirm, _ := args["confirm"].(bool)
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}
	if !confirm {
		return tools.ErrorResult(errorJSON("CONFIRMATION_REQUIRED",
			"confirm must be true to delete an agent",
			"Set confirm=true to proceed with deletion",
		))
	}
	// Guard 0 (F8, half_b_report.md): refuse outright — BEFORE any
	// destructive action — if id is the configured default agent. Deleting
	// it would leave cfg.Agents.Defaults.DefaultAgentID pointing at a
	// nonexistent id, silently demoting default-agent resolution to its
	// Priority-2 fallback (Registry.GetDefaultAgent / route.go's
	// resolveDefaultAgentID — see CLAUDE.md's "Default agent (single source
	// of truth)" section, itself a former release blocker for exactly this
	// kind of silent divergence). A nil GetCfg/Config (test scaffolding
	// without a wired config) skips the check rather than trusting an
	// unknown default — matches this file's configAgentPresenceSet
	// precedent for "no config visible" defaulting to exclude, never allow.
	if t.deps.GetCfg != nil {
		if cfg := t.deps.GetCfg(); cfg != nil && cfg.Agents.Defaults.DefaultAgentID == id {
			return tools.ErrorResult(errorJSON("AGENT_IS_DEFAULT",
				fmt.Sprintf("agent %q is the configured default agent and cannot be deleted", id),
				"Set another agent as default first (Agents screen ★), then retry delete_agent"))
		}
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
	omnipusHome, homeErr := resolveOmnipusHome(t.deps.Home)
	if homeErr != nil {
		return tools.ErrorResult(errorJSON("WORKSPACE_ERROR", homeErr.Error(),
			"Set OMNIPUS_HOME environment variable"))
	}

	store := agentstore.New(omnipusHome)
	existing, getErr := store.Get(id)
	if getErr != nil {
		if errors.Is(getErr, entity.ErrNotFound) {
			return tools.ErrorResult(errorJSON("AGENT_NOT_FOUND",
				fmt.Sprintf("No agent with ID %q", id),
				"Use list_agents to see available agents",
			))
		}
		return tools.ErrorResult(errorJSON("SAVE_FAILED", getErr.Error(), ""))
	}
	if existing.Locked {
		return tools.ErrorResult(errorJSON("SAVE_FAILED",
			fmt.Sprintf("agent %q is a locked core agent and cannot be deleted", id), ""))
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
	if t.deps.PlanStore != nil {
		hasActive, planErr := agentOwnsActivePlan(t.deps.PlanStore, id)
		if planErr != nil {
			return tools.ErrorResult(errorJSON("SAVE_FAILED",
				fmt.Sprintf("could not verify plan ownership for agent %q: %s", id, planErr.Error()),
				"Try again"))
		}
		if hasActive {
			return tools.ErrorResult(errorJSON("AGENT_OWNS_ACTIVE_PLANS",
				fmt.Sprintf("agent %q owns active plans and cannot be deleted", id),
				"Stop or reassign its plan(s) first, then retry delete_agent"))
		}
	}

	// cascadeWarnings collects every best-effort cascade-step failure so the
	// response can report a real partial-failure instead of silently
	// claiming full success (F8, half_b_report.md — mirrors this file's
	// existing publish_warning pattern for create/update, generalized to the
	// several independent stores this cascade touches). None of these steps
	// abort the delete — the agent entity record is already durably removed
	// by the time any of them run (see store.Delete below) — so a failure
	// here is best-effort/non-fatal, matching delete_workspace's own
	// best-effort-cascade shape (a failed mount/delegation-store cleanup
	// there does not stop the workspace from being deleted either).
	var cascadeWarnings []string

	// store.Delete(id) — the authoritative entity-record delete — runs
	// FIRST, before any of the irreversible cascade steps below (bug-fix,
	// this session: sessions/tasks cascade used to run BEFORE this call,
	// which meant a store.Delete failure was reported to the caller as a
	// bare SAVE_FAILED with the sessions already gone and tasks already
	// unassigned — directly contradicting this tool's own Description(),
	// which promises "a step that fails partway through is reported in the
	// response rather than silently swallowed". With store.Delete FIRST: if
	// it fails, nothing destructive has happened yet — the fail-safe order.
	// This also matches the wsPath home-directory removal immediately below,
	// which was already correctly sequenced after store.Delete.
	if err := store.Delete(id); err != nil {
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), ""))
	}
	// Remove workspace directory (best-effort; failure is non-fatal but logged).
	wsPath := datamodel.AgentHomePath(omnipusHome, id)
	if err := os.RemoveAll(wsPath); err != nil {
		slog.Warn("sysagent: workspace cleanup incomplete",
			"agent_id", id, "path", wsPath, "error", err)
	}

	// Step 1a (F8): delete every session in the SHARED session store
	// ($OMNIPUS_HOME/sessions/) that belongs SOLELY to this agent, together
	// with its uploads. Runs AFTER the authoritative entity-record delete
	// above (see that call's comment for why) — this is best-effort cascade
	// cleanup of things that reference the now-deleted agent, mirroring
	// cascadeCleanAgentWorkspaceReferences below. See
	// cascadeDeleteAgentSessions's doc comment for why a session shared with
	// another agent is deliberately left untouched rather than deleted or
	// partially edited.
	sessionsDeleted, sessionsPreservedShared, sessionWarnings := cascadeDeleteAgentSessions(omnipusHome, id)
	cascadeWarnings = append(cascadeWarnings, sessionWarnings...)

	// Step 1b (F8): unassign (never delete) every GTD task currently
	// assigned to this agent, using the same task.Store.Update primitive
	// (and per-task locking) the ordinary task-update tools use — never a
	// hand-rolled read-modify-write. Runs AFTER the authoritative
	// entity-record delete above, same reasoning as Step 1a. See
	// cascadeUnassignAgentTasks's doc comment for why CreatedByAgentID is
	// deliberately left untouched.
	tasksUnassigned, taskWarnings := cascadeUnassignAgentTasks(omnipusHome, id)
	cascadeWarnings = append(cascadeWarnings, taskWarnings...)

	// Step 2 (F8): best-effort cleanup of DANGLING REFERENCES to the
	// now-deleted agent across every workspace — core_team membership and
	// delegation-trust edges naming it as either side. Runs AFTER the
	// authoritative entity-record delete above, mirroring delete_workspace's
	// own post-record-delete cleanup of its mount/delegation stores: this is
	// "clean up what still points at the thing that's gone", not data that
	// belongs to the agent itself.
	workspacesUpdated, edgesRemoved, wsWarnings := cascadeCleanAgentWorkspaceReferences(omnipusHome, id)
	cascadeWarnings = append(cascadeWarnings, wsWarnings...)

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
	var publishWarning string
	if t.deps.ReloadFunc != nil {
		if err := t.deps.ReloadFunc(); err != nil {
			slog.Warn("sysagent: hot-reload after agent delete failed — agent remains routable/listed until restart",
				"id", id, "error", err)
			publishWarning = fmt.Sprintf(
				"agent %q was deleted from storage but hot-reload failed (%s); it may remain routable and "+
					"listed until the next gateway restart", id, err.Error())
		}
	}

	result := map[string]any{
		"id":                        id,
		"deleted":                   true,
		"sessions_deleted":          sessionsDeleted,
		"sessions_preserved_shared": sessionsPreservedShared,
		"tasks_unassigned":          tasksUnassigned,
		"workspaces_updated":        workspacesUpdated,
		"delegation_edges_removed":  edgesRemoved,
	}
	if publishWarning != "" {
		result["publish_warning"] = publishWarning
	}
	// F8 (half_b_report.md): a per-step cascade failure is best-effort and
	// non-fatal (the agent record above is already durably deleted either
	// way), but must not be silently swallowed — an unqualified
	// {"deleted":true} response with no hint of a stuck session/task/
	// delegation-edge reference would tell the caller the cascade fully
	// completed when it did not.
	if len(cascadeWarnings) > 0 {
		result["cascade_warnings"] = cascadeWarnings
	}
	return tools.NewToolResult(successJSON(result))
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
// conversation another agent also participated in, e.g. via SwitchAgent) is
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
func cascadeDeleteAgentSessions(omnipusHome, agentID string) (deleted, sharedSkipped int, warnings []string) {
	sessionsDir := filepath.Join(omnipusHome, "sessions")
	store, err := session.NewUnifiedStore(sessionsDir)
	if err != nil {
		return 0, 0, []string{fmt.Sprintf("could not open session store for cascade delete: %v", err)}
	}
	// Close stops the store's periodic stats-flusher goroutine. This
	// function constructs a short-lived store instance for the duration of
	// one delete_agent call (same "fresh store per call" convention this
	// file already uses for agentstore.New in every Execute), so it must
	// also tear it down — otherwise every delete_agent call leaks one
	// ticker goroutine for the life of the process.
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			slog.Warn("sysagent: cascade session delete: session store close failed",
				"agent_id", agentID, "error", closeErr)
		}
	}()

	metas, err := store.ListSessionsFiltered(func(m *session.UnifiedMeta) bool {
		for _, a := range m.AgentIDs {
			if a == agentID {
				return true
			}
		}
		return false
	})
	if err != nil {
		return 0, 0, []string{fmt.Sprintf("could not list sessions for cascade delete: %v", err)}
	}

	for _, m := range metas {
		if len(m.AgentIDs) != 1 {
			sharedSkipped++
			continue
		}
		if delErr := store.DeleteSession(m.ID); delErr != nil {
			warnings = append(warnings, fmt.Sprintf("could not delete session %s: %v", m.ID, delErr))
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
		if os.IsNotExist(err) {
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
