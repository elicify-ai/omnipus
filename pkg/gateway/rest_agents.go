// rest_agents.go: Agent roster, read paths, delete, and shared wire/config translation

package gateway

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/clidetect"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// setAgentModelProvider echoes the agent's explicit primary-model provider (O3
// two-field model) onto the wire response. A nil model or an empty provider
// leaves ag.Provider unset (absent on the wire), signaling default-provider
// resolution. Used by every agent response builder so create/list/get/update all
// round-trip the provider field consistently.
func setAgentModelProvider(ag *gen.Agent, model *config.AgentModelConfig) {
	if model == nil || model.Provider == "" {
		return
	}
	p := model.Provider
	ag.Provider = &p
}

// --- Agents ---

// HandleAgents handles /api/v1/agents (list + create), /api/v1/agents/{id} (detail),
// and /api/v1/agents/{id}/sessions (sessions for agent).
func (a *restAPI) HandleAgents(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	remainder := strings.TrimPrefix(path, "/api/v1/agents")
	remainder = strings.TrimPrefix(remainder, "/")

	// Split remainder into agentID and optional sub-path.
	var agentID, subPath string
	if remainder != "" {
		parts := strings.SplitN(remainder, "/", 2)
		agentID = parts[0]
		if len(parts) > 1 {
			subPath = parts[1]
		}
	}

	// GET /api/v1/agents/executor-defaults — static reference data (agent-system-
	// fixes-2 ghost-text bug fix). This reservation is structurally different
	// from the "sessions"/"runner"/"tools"/"mailboxes" sub-path guards below:
	// those reserve a VERB-SUFFIX position that is only checked AFTER agentID
	// has already been split off and validated (so they can never collide with
	// a real agent ID, only with a same-named sub-resource segment). This guard
	// instead claims the agentID SLOT ITSELF — "executor-defaults" is matched
	// as if it were the {id} value before any agent lookup happens, so it is a
	// static path segment carved out of the agent-ID namespace, not a
	// sub-resource reservation. createAgent/updateAgent do not reject this
	// literal ID, so if an agent were ever created with it, that agent would
	// become permanently unreachable via GET /api/v1/agents/{id} (shadowed by
	// this branch). Practical risk is low — agent IDs are always
	// uuid.New().String(), never operator-chosen — but this is a narrower,
	// more fragile precedent than the sub-path guards below and should not be
	// copied casually for a future static route under /agents/.
	if agentID == "executor-defaults" && subPath == "" {
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.listExecutorDefaults(w)
		return
	}

	// POST /api/v1/agents/executor-preview — stateless real-command preview
	// (rest_executor_preview.go). Same agentID-SLOT carve-out pattern as
	// executor-defaults immediately above (see that block's comment for why
	// this is structurally different from the sessions/runner/tools/mailboxes
	// sub-path guards below). Body-driven and agent-agnostic — mirrors POST
	// /system/cli-validate — so it works both from the create wizard, where no
	// agent id exists yet, and from an existing agent's edit form.
	if agentID == "executor-preview" && subPath == "" {
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.postAgentsExecutorPreview(w, r)
		return
	}

	// POST /api/v1/agents/executor-smoke-test — actually RUN a bounded, real
	// test prompt through an external-CLI worker's real dispatch path
	// (rest_executor_smoketest.go). Same agentID-SLOT carve-out pattern as
	// executor-preview/executor-defaults immediately above. Unlike those two
	// (stateless computation only, no spawn), this endpoint DOES spend real
	// model tokens and DOES run a real, authenticated subprocess — it
	// enforces its own dedicated rate limit (smokeTestLimiter) and per-caller
	// in-flight cap (smokeTestInflight) inline, since it shares this route's
	// registration-time auth wrapping (api.withAuth(api.HandleAgents), same
	// create-parity as executor-preview) rather than getting its own
	// dedicated top-level route like /system/cli-validate does.
	if agentID == "executor-smoke-test" && subPath == "" {
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.postAgentsExecutorSmokeTest(w, r)
		return
	}

	// Validate agentID before any filesystem operations (path traversal guard, C1).
	if agentID != "" {
		if err := validateEntityID(agentID); err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid agent ID")
			return
		}
	}

	// GET /api/v1/agents/{id}/sessions
	if r.Method == http.MethodGet && agentID != "" && subPath == "sessions" {
		a.listAgentSessions(w, agentID)
		return
	}

	// POST /api/v1/agents/{id}/runner/test — external-CLI runner connection test (Spec-4 FR-4.2)
	if agentID != "" && subPath == "runner/test" {
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.testAgentRunner(w, r, agentID)
		return
	}

	// GET/PUT /api/v1/agents/{id}/tools — per-agent tool registry view (FR-028, FR-086)
	if agentID != "" && subPath == "tools" {
		switch r.Method {
		case http.MethodGet:
			a.HandleAgentToolsRegistry(w, r, agentID)
		case http.MethodPut:
			a.updateAgentTools(w, r, agentID)
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	// GET/PUT/DELETE /api/v1/agents/{id}/mailboxes/{workspaceId} — one
	// (agent, workspace) email mailbox account (M11, pair-addressed
	// 2026-07-03: the same agent may hold a different mailbox in each
	// workspace it belongs to). Match both the bare "mailboxes" prefix (so a
	// missing workspace segment gets a proper 400 instead of silently
	// falling through to the generic agent-CRUD switch below) and
	// "mailboxes/<workspaceId>".
	if agentID != "" && (subPath == "mailboxes" || strings.HasPrefix(subPath, "mailboxes/")) {
		mbParts := strings.SplitN(subPath, "/", 2)
		if len(mbParts) != 2 || mbParts[1] == "" || strings.Contains(mbParts[1], "/") {
			jsonErr(w, http.StatusBadRequest,
				"mailboxes path requires exactly one workspace ID segment: /agents/{id}/mailboxes/{workspaceId}")
			return
		}
		workspaceID := mbParts[1]
		if err := validateEntityID(workspaceID); err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
			return
		}
		switch r.Method {
		case http.MethodGet:
			a.getAgentMailbox(w, agentID, workspaceID)
		case http.MethodPut:
			a.setAgentMailbox(w, r, agentID, workspaceID)
		case http.MethodDelete:
			a.deleteAgentMailbox(w, agentID, workspaceID)
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	switch r.Method {
	case http.MethodGet:
		if agentID == "" {
			a.listAgents(w)
		} else {
			a.getAgent(w, agentID)
		}
	case http.MethodPost:
		if agentID == "" {
			a.createAgent(w, r)
		} else {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case http.MethodPut:
		if agentID != "" {
			a.updateAgent(w, r, agentID)
		} else {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case http.MethodDelete:
		if agentID != "" {
			a.deleteAgent(w, agentID)
		} else {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// testAgentRunner handles POST /api/v1/agents/{id}/runner/test (Spec-4 FR-4.2).
// It validates the agent's configured external-CLI runner WITHOUT running real work:
// binary present + version handshake + authenticated. Returns distinct reasons for
// missing-binary vs unauthenticated. When the agent's executor is not external-cli
// (native / remote-a2a / unset), there is no runner to test → reason "not-external-cli".
func (a *restAPI) testAgentRunner(w http.ResponseWriter, r *http.Request, agentID string) {
	cfg := a.agentLoop.GetConfig()

	var found bool
	var executor *config.ExecutorConfig
	for _, ac := range cfg.Agents.List {
		if ac.ID == agentID {
			found = true
			if ac.Subagents != nil {
				executor = ac.Subagents.Executor
			}
			break
		}
	}
	if !found {
		jsonErr(w, http.StatusNotFound, "agent not found")
		return
	}

	// The agent must be configured for external-cli to have a runner to test.
	if executor == nil || executor.EffectiveKind() != config.ExecutorKindExternalCLI {
		jsonOK(w, gen.RunnerTestResponse{
			Ok:      false,
			Reason:  gen.RunnerTestResponseReasonNotExternalCli,
			Message: "agent executor is not external-cli; no external runner to test",
		})
		return
	}
	cli := executor.CLI
	if cli == "" {
		jsonOK(w, gen.RunnerTestResponse{
			Ok:      false,
			Reason:  gen.RunnerTestResponseReasonUnknownCli,
			Message: "agent executor.cli is empty; set claude-code, codex, or opencode",
			Cli:     strPtr(""),
		})
		return
	}

	// Validate the agent's CONFIGURED binary (executor.cli_path), not just the
	// default $PATH binary — otherwise a custom cli_path that does not exist
	// would false-green. Empty cli_path falls back to the default name.
	res := runner.TestConnectionWithPath(r.Context(), cli, executor.CLIPath)
	resp := gen.RunnerTestResponse{
		Ok:      res.OK,
		Reason:  gen.RunnerTestResponseReason(res.Reason),
		Message: res.Message,
		Cli:     strPtr(cli),
	}
	if res.CLIVersion != "" {
		resp.CliVersion = strPtr(res.CLIVersion)
	}
	jsonOK(w, resp)
}

// listExecutorDefaults handles GET /api/v1/agents/executor-defaults (Agent
// System ghost-text bug fix). Returns static, byte-accurate reference data —
// for each supported subagent_3p external CLI, the ORDERED list of arguments
// pkg/agent/runner/driver_{claude,codex,opencode}.go's buildArgs() actually
// applies BEFORE any operator-supplied executor.cli_args, plus a note on how
// the prompt itself reaches the CLI. Not agent-scoped: this is pure reference
// documentation sourced directly from the three drivers. There is no runtime
// introspection of buildArgs — a change to any driver's own flags MUST be
// mirrored here by hand, or this endpoint drifts from reality.
func (a *restAPI) listExecutorDefaults(w http.ResponseWriter) {
	jsonOK(w, []gen.ExecutorDefaults{
		{
			Cli: gen.ExternalCliToolClaudeCode,
			AutoAppliedFlags: []string{
				"-p",
				"--output-format stream-json",
				"--verbose",
				"--no-chrome",
				"--model <configured model> (only when a model is configured)",
				"--dangerously-skip-permissions",
				"--max-turns <configured max turns> (only when a turn cap is configured)",
			},
			Notes: "The prompt is delivered via stdin, with no positional prompt argument at all — never via a --prompt flag. --resume/--session-id are never passed; every run starts a fresh claude session. --dangerously-skip-permissions is passed unconditionally (operator decision, issue #488, reversing the original FR-5.3/US-5 stance of using --permission-mode acceptEdits instead) — this matches codex/opencode, which already ran permission-bypassed; see the tracked issue for the sandbox-boundary follow-up this reversal implies for claude specifically. Operator cli_args are appended after this list; a redundant --dangerously-skip-permissions or an attempt to change --output-format away from stream-json is dropped with a WARN (see argsafety.go) — the latter because the driver's own NDJSON stream parser requires stream-json output.",
		},
		{
			Cli: gen.ExternalCliToolCodex,
			AutoAppliedFlags: []string{
				"--ask-for-approval never",
				"exec",
				"--json",
				"--sandbox workspace-write",
				"--skip-git-repo-check",
				"--color never",
				"-m <configured model> (only when a model is configured)",
				"-C <agent working directory> (only when a working directory is set — always populated for a real dispatched run)",
			},
			Notes: "--ask-for-approval is a GLOBAL codex flag and must precede the exec subcommand (codex errors if it follows exec); --sandbox is an exec-subcommand flag and is placed after exec instead. The prompt is delivered via stdin — a trailing \"-\" argument — never via a --prompt flag. Operator cli_args are appended after this list; --dangerously-bypass-approvals-and-sandbox, --sandbox danger-full-access, any --ask-for-approval override, and any --json override (bare or \"=false\"-shaped) are dropped with a WARN (see argsafety.go) — the last one because the driver's own NDJSON stream parser requires --json output.",
		},
		{
			Cli: gen.ExternalCliToolOpencode,
			AutoAppliedFlags: []string{
				"run",
				"--format json",
				"--model <configured model> (only when the configured model is shaped like \"provider/model\", e.g. \"anthropic/claude-3-5-sonnet\"; a bare model name is omitted so the CLI falls back to its own default)",
				"--dangerously-skip-permissions",
				"--",
			},
			Notes: "opencode's `run` command has no --prompt flag; the prompt is delivered as the POSITIONAL argument placed LAST, after the literal \"--\" end-of-options separator, so opencode's yargs-based argument parser never mistakes prompt text beginning with \"--\" for a flag. It is never sent via stdin (stdin is always an empty reader for opencode runs). --dangerously-skip-permissions is opencode's only non-interactive auto-approve posture in this CLI version (no middle-ground \"auto-accept edits\" flag exists) — Omnipus's own consent routing for opencode remains best-effort/post-hoc regardless of this flag. Operator cli_args are appended before the trailing \"--\"; a redundant --dangerously-skip-permissions or an attempt to change --format away from json is dropped with a WARN (see argsafety.go) — the latter because the driver's own NDJSON stream parser requires --format json output.",
		},
	})
}

// listAgentSessions returns the union of an agent's sessions from both
// session stores, deduplicated by session ID. Ordinary chat sessions moved
// to the shared store (AgentLoop.GetSessionStore — "the shared store for new
// sessions") some time ago; AgentLoop.GetAgentStore's own doc marks it "kept
// for legacy per-agent session access". This endpoint used to read
// GetAgentStore exclusively, so it silently omitted every session minted
// after that move.
//
// This does not call AgentLoop.ListAllSessions: that helper merges the
// shared store with EVERY registered agent's legacy store to build a
// cross-agent list, which would mean opening and reading every OTHER
// agent's session directory off disk just to filter the result back down to
// this one agent — needless I/O for a single-agent-scoped endpoint. Instead
// this inlines the same shared-primary/per-agent-secondary merge idiom
// ListAllSessions and createSessionHTTP already use, scoped to just the two
// stores that can hold this agent's sessions.
func (a *restAPI) listAgentSessions(w http.ResponseWriter, agentID string) {
	// agentID is already validated by HandleAgents before reaching here.
	seen := make(map[string]bool)
	var metas []*session.UnifiedMeta
	var errs []error

	if shared := a.agentLoop.GetSessionStore(); shared != nil {
		sharedMetas, err := shared.ListSessions()
		if err != nil {
			// Logged and collected. This used to be treated as non-fatal
			// whenever the OTHER store still produced data, on the theory
			// that a partial list beats an empty one — but the shared store
			// is the PRIMARY home for sessions minted after the migration
			// described in this function's doc comment, so "legacy store
			// still has data" typically means "most of this agent's real
			// sessions are the ones now missing". A 200 built from whatever
			// the healthy store returned would look complete to the caller
			// (the SPA has no way to tell "all sessions" from "some
			// sessions") while silently omitting the majority — reintroducing
			// one level up the exact bug this function was written to fix.
			// See the escalation check after both scans: ANY store error now
			// aborts with 500 rather than risk a caller trusting an
			// incomplete list as complete.
			slog.Warn("rest: list agent sessions: shared store", "agent_id", agentID, "error", err)
			errs = append(errs, fmt.Errorf("shared: %w", err))
		}
		for _, m := range sharedMetas {
			// The shared store holds sessions for every agent; membership is
			// AgentIDs (PostLoad-backfilled from the legacy single AgentID
			// field on every read, so this is never empty for a real session).
			if slices.Contains(m.AgentIDs, agentID) {
				metas = append(metas, m)
				seen[m.ID] = true
			}
		}
	}

	if legacy := a.agentLoop.GetAgentStore(agentID); legacy != nil {
		legacyMetas, err := legacy.ListSessions()
		if err != nil {
			slog.Warn("rest: list agent sessions: legacy store", "agent_id", agentID, "error", err)
			errs = append(errs, fmt.Errorf("legacy: %w", err))
		}
		for _, m := range legacyMetas {
			// A session can exist in both stores (a pre-fix duplicate-mint bug
			// produced exactly that) — the shared-store copy wins.
			if !seen[m.ID] {
				metas = append(metas, m)
				seen[m.ID] = true
			}
		}
	}

	// ANY store read failure escalates to a 500, even when the OTHER store
	// produced data. The wire shape for this endpoint is a bare
	// `type: array, items: Session` (contracts/openapi.yaml) — unlike e.g.
	// ChannelEntry's per-entry Degraded/DegradedReason fields (rest.go's
	// applyDegradedOverlay), a JSON array has no sibling slot to carry a
	// "this list is incomplete" signal, and changing the response to a
	// wrapped object would be a breaking wire-shape change for every
	// existing caller. Given that choice, silently returning whatever the
	// healthy store has — indistinguishable on the wire from "this really is
	// the complete list" — is worse than an honest 500: a partial 200 here
	// would repeat, one layer up, the exact bug this function was written to
	// fix (see the doc comment above). A future wire-shape change to carry an
	// explicit partial/degraded flag is a legitimate alternative but requires
	// a coordinated SPA update, not a decision to make unilaterally here.
	if len(errs) > 0 {
		slog.Error("rest: list agent sessions: store read failed", "agent_id", agentID, "errors", errs)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not list sessions: %v", errors.Join(errs...)))
		return
	}

	sort.Slice(metas, func(i, j int) bool {
		return metas[i].UpdatedAt.After(metas[j].UpdatedAt)
	})

	// Route every session through unifiedMetaToGenSession so required arrays
	// (Partitions) marshal as [] not null — Zod requires type:array on the SPA.
	genSessions := make([]gen.Session, 0, len(metas))
	for _, m := range metas {
		genSessions = append(genSessions, unifiedMetaToGenSession(m))
	}
	jsonOK(w, genSessions)
}

// Skill, SessionDetail, GatewayStatus, and Provider response types are defined
// in contracts/components/schemas/ and generated into pkg/api/generated/.
// Use gen.Skill, gen.SessionDetail, gen.GatewayStatus, and gen.Provider directly.

// strVal extracts a string value from a JSON-decoded map, returning "" if missing or wrong type.
func strVal(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func isSeedTemplateRow(m *config.ModelConfig) bool {
	if m == nil {
		return true
	}
	return strings.TrimSpace(m.Provider) == "" ||
		(m.APIKeyRef == "" &&
			m.APIBase == "" &&
			m.AuthMethod == "" &&
			m.UpdatedAt == nil &&
			len(m.Models) == 0)
}

// Agent response type is defined in contracts/components/schemas/Agent.yaml
// and generated into pkg/api/generated/. Use gen.Agent directly.

// agentWorkspacePath returns the expanded workspace directory for the named agent.
// Per FUNC-11 (BRD), each custom agent gets its own isolated workspace directory.
// If the agent has an explicit workspace set, that is used (with ~ expansion).
// Otherwise, a per-agent directory is derived: ~/.omnipus/agents/{agentID}/.
// The system agent uses the default workspace from config.
//
// Returns (path, error). Callers must handle the error; a non-nil error means the
// workspace could not be created and the returned path may be unusable.
func agentWorkspacePath(cfg interface {
	AgentHomeBasePath() string
}, agentID, agentWorkspace, omnipusHome string,
) (string, error) {
	if agentWorkspace != "" {
		// AgentConfig.Home may contain "~"; expand it the same way config does.
		if len(agentWorkspace) > 0 && agentWorkspace[0] == '~' {
			home, err := os.UserHomeDir()
			if err != nil {
				slog.Error("rest: agentWorkspacePath: UserHomeDir failed", "error", err)
				return agentWorkspace, fmt.Errorf("UserHomeDir: %w", err)
			}
			if len(agentWorkspace) > 1 && (agentWorkspace[1] == '/' || agentWorkspace[1] == filepath.Separator) {
				return home + agentWorkspace[1:], nil
			}
			return home, nil
		}
		return agentWorkspace, nil
	}
	// Per-agent isolated workspace (FUNC-11). Use OMNIPUS_HOME/agents/{id}
	// to match where system.agent.create writes SOUL.md.
	if agentID != "" {
		base := omnipusHome
		if base == "" {
			// Fallback to ~/.omnipus if homePath not provided.
			home, err := os.UserHomeDir()
			if err != nil {
				slog.Error("rest: agentWorkspacePath: UserHomeDir failed", "error", err)
				return cfg.AgentHomeBasePath(), fmt.Errorf("UserHomeDir: %w", err)
			}
			base = filepath.Join(home, ".omnipus")
		}
		agentDir := filepath.Join(base, "agents", agentID)
		cleaned := filepath.Clean(agentDir)
		safePrefix := filepath.Clean(base)
		if !strings.HasPrefix(cleaned, safePrefix) {
			return "", fmt.Errorf("agent workspace path escapes omnipus home: %s", cleaned)
		}
		if err := os.MkdirAll(cleaned, 0o755); err != nil {
			slog.Error("rest: agentWorkspacePath: MkdirAll failed", "path", cleaned, "error", err)
			return cleaned, fmt.Errorf("MkdirAll %s: %w", cleaned, err)
		}
		return cleaned, nil
	}
	return cfg.AgentHomeBasePath(), nil
}

// readSoulMD returns the contents of SOUL.md for the given workspace.
// Used by listAgents to determine draft status without reading all three agent files.
func readSoulMD(workspace string) string {
	data, err := os.ReadFile(filepath.Join(workspace, "SOUL.md"))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("rest: readSoulMD: cannot read SOUL.md", "workspace", workspace, "error", err)
		}
		return ""
	}
	return string(data)
}

// readAgentFiles returns the contents of SOUL.md and HEARTBEAT.md from the
// given workspace directory. Missing files return an empty string without
// logging an error — their absence is expected for newly created agents.
// Permission and other I/O errors (not IsNotExist) are logged at Warn level (M11).
func readAgentFiles(workspace string) (soul, heartbeat string) {
	if data, err := os.ReadFile(filepath.Join(workspace, "SOUL.md")); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("rest: readAgentFiles: cannot read SOUL.md", "workspace", workspace, "error", err)
		}
	} else {
		soul = string(data)
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "HEARTBEAT.md")); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("rest: readAgentFiles: cannot read HEARTBEAT.md", "workspace", workspace, "error", err)
		}
	} else {
		heartbeat = string(data)
	}
	return soul, heartbeat
}

// activeAgentIDSet returns a set of agent IDs that currently have an active turn.
func (a *restAPI) activeAgentIDSet() map[string]bool {
	ids := a.agentLoop.GetActiveAgentIDs()
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// computeAgentStatus determines the agent status based on whether it is active,
// has a non-empty SOUL.md, or is a locked core agent.
func computeAgentStatus(agentID string, activeIDs map[string]bool, soul string, locked bool) string {
	if activeIDs[agentID] {
		return "active"
	}
	// Core agents (locked) have compiled prompts — always idle (never draft).
	if locked {
		return "idle"
	}
	if strings.TrimSpace(soul) == "" {
		return "draft"
	}
	return "idle"
}

// applyAgentOverrides copies per-agent execution overrides onto a
// defaults-seeded wire Agent. MaxToolIterations: per-agent value wins when
// set (>0); 0 means "inherit" and leaves the effective default in place.
func applyAgentOverrides(ag *gen.Agent, ac *config.AgentConfig) {
	if ac.MaxToolIterations > 0 {
		ag.MaxToolIterations = ac.MaxToolIterations
	}
	// memory_enabled (ADR-052 FR-039): every response path (list/get/create/
	// update) funnels through this function, so populating it here once
	// covers all of them. Previously never set here — the wire field is
	// `omitempty` on the generated Go struct, so an unset pointer meant the
	// key was silently dropped from every JSON response and the SPA always
	// rendered memory as on, even for the seeded-memory-off Judge. Always
	// resolve via MemoryEnabledEffective() (nil → true) rather than echoing
	// the raw possibly-nil ac.MemoryEnabled, so the wire always carries the
	// agent's actual effective value.
	memEnabled := ac.MemoryEnabledEffective()
	ag.MemoryEnabled = &memEnabled
	// shell_policy: echo the persisted per-agent override. Previously this was
	// persisted (updateAgent) or should have been persisted (createAgent, fixed
	// alongside this) but never surfaced on any response path (list/get/create/
	// update all built gen.Agent without ever touching this field) — a GET
	// could never confirm what was saved.
	if ac.ShellPolicy != nil {
		// The literal below mirrors the inlined anonymous-struct shape
		// oapi-codegen generated for gen.Agent.ShellPolicy — field
		// names/types/tags (and order) must match for the assignment to
		// gen.Agent.ShellPolicy below to type-check.
		sp := struct { // not-wire-format: generated gen.Agent.ShellPolicy inline shape, only populates the generated field
			CustomDenyPatterns *[]string `json:"custom_deny_patterns,omitempty"`
			EnableDenyPatterns *bool     `json:"enable_deny_patterns,omitempty"`
		}{
			EnableDenyPatterns: boolPtr(ac.ShellPolicy.EnableDenyPatterns),
		}
		if len(ac.ShellPolicy.CustomDenyPatterns) > 0 {
			cdp := make([]string, len(ac.ShellPolicy.CustomDenyPatterns))
			copy(cdp, ac.ShellPolicy.CustomDenyPatterns)
			sp.CustomDenyPatterns = &cdp
		}
		ag.ShellPolicy = &sp
	}
	// fallback_models: P-F2 — this was persisted correctly (createAgent/updateAgent
	// both write ac.FallbackModels to config.json) but never echoed back on ANY
	// response path (list/get/update all built gen.Agent without ever touching
	// this field), so a GET could never confirm what was saved and a reopened
	// agent's UI always rendered the field empty even though it was safely on
	// disk — mirrors the ShellPolicy fix above. config.FallbackModel.Provider is
	// a bare string (empty when unset); gen.FallbackModel.Provider is a pointer,
	// so translate unconditionally (mirrors getMemorySettings' identical
	// config->wire FallbackModel translation for recap_fallback_models).
	if len(ac.FallbackModels) > 0 {
		fm := make([]gen.FallbackModel, len(ac.FallbackModels))
		for i, m := range ac.FallbackModels {
			fm[i] = gen.FallbackModel{Model: m.Model, Provider: &m.Provider}
		}
		ag.FallbackModels = &fm
	}
	// model_params: echo the persisted per-agent sampling-parameter override
	// (Q1 fix). Previously config.AgentConfig had no ModelParams field at
	// all, so a PUT that set it returned 200 and GET always echoed
	// model_params: null — the ADR-037 anti-pattern (mirrors the
	// FallbackModels/ShellPolicy echo fixes above). top_p was removed from
	// the wire entirely in T2 (see agentModelParamsInput's doc comment) — no
	// provider adapter in this codebase ever implemented it — so there is no
	// third field left to echo.
	if ac.ModelParams != nil {
		mp := struct { // not-wire-format: mirrors gen.Agent.ModelParams inline shape
			MaxTokens   *int     `json:"max_tokens,omitempty"`
			Temperature *float64 `json:"temperature,omitempty"`
		}{
			MaxTokens:   ac.ModelParams.MaxTokens,
			Temperature: ac.ModelParams.Temperature,
		}
		ag.ModelParams = &mp
	}
}

// buildAgentDefaults populates the execution-related fields from config defaults.
func buildAgentDefaults(cfg *config.Config) gen.Agent {
	// Effective per-turn tool-round cap: mirror the runtime resolution
	// (pkg/agent/instance.go) — defaults value when set, else 200 — so the
	// wire never reports a meaningless 0 (a zeroed default was the visible
	// half of the 2026-07-03 P0: the UI showed and re-persisted 0s).
	maxIter := cfg.Agents.Defaults.MaxToolIterations
	if maxIter <= 0 {
		maxIter = 200
	}
	return gen.Agent{
		TimeoutSeconds:    cfg.Agents.Defaults.TimeoutSeconds,
		MaxToolIterations: maxIter,
		// Required string fields — initialized to empty (overwritten per-agent).
		Soul: "",
	}
}

func (a *restAPI) listAgents(w http.ResponseWriter) {
	cfg := a.agentLoop.GetConfig()
	agents := make([]gen.Agent, 0, len(cfg.Agents.List))
	activeIDs := a.activeAgentIDSet()

	defaults := buildAgentDefaults(cfg)
	defaultModel := cfg.Agents.Defaults.DefaultModel.Model
	for _, ac := range cfg.Agents.List {
		model := defaultModel
		if ac.Model != nil && ac.Model.Primary != "" {
			model = ac.Model.Primary
		}
		workspace, wsErr := agentWorkspacePath(cfg, ac.ID, ac.Home, a.homePath)
		if wsErr != nil {
			slog.Warn("rest: listAgents: could not resolve workspace", "agent_id", ac.ID, "error", wsErr)
		}
		// M2: listAgents only needs SOUL.md to determine draft status — avoid reading
		// HEARTBEAT.md and AGENT.md unnecessarily in the list endpoint.
		// Core agents have compiled prompts — do not expose them via SOUL.md.
		// ADR-052 FR-038: System Agents (the Judge) are the carve-out — their soul
		// IS their (operator-editable) verifier rubric, not a compiled prompt, so
		// it must render like any custom agent's soul despite Locked==true.
		var soul string
		if !ac.Locked || ac.IsSystem() {
			soul = readSoulMD(workspace)
		}
		ag := defaults
		ag.Id = ac.ID
		ag.Name = ac.Name
		if ac.Description != "" {
			ag.Description = &ac.Description
		}
		if ac.Color != "" {
			ag.Color = &ac.Color
		}
		if ac.Icon != "" {
			ag.Icon = &ac.Icon
		}
		ag.Type = coreagent.ToWireType(ac)
		ag.Locked = ac.Locked
		applyAgentOverrides(&ag, &ac)
		// ADR-066 D2/D9: the persisted rung-1 override plus the three derived
		// read-only window fields the Advanced panel renders.
		applyAgentContextWindow(&ag, cfg, &ac)
		ag.Model = &model
		setAgentModelProvider(&ag, ac.Model)
		ag.Status = gen.AgentStatus(computeAgentStatus(ac.ID, activeIDs, soul, ac.Locked))
		ag.Soul = soul
		// The wire `default` is DERIVED from the settings singleton
		// (cfg.Agents.Defaults.DefaultAgentID), never read from the per-entity
		// ac.Default bool — see updateAgent's singleton-write block for why:
		// nothing (routing, registry.GetDefaultAgent) has consulted the
		// per-entity flag since ADR-054 D6.4, so echoing it back here would
		// silently disagree with which agent actually receives inbound
		// messages with no more-specific routing rule.
		ag.Default = boolPtr(ac.ID == cfg.Agents.Defaults.DefaultAgentID)
		// ADR-068 FR-014 (T068-08): needs_model is derived, never stored.
		ag.NeedsModel = agentNeedsModel(cfg, &ac)
		// ADR-067 FR-016/FR-031 (T067-09): degraded_reason is derived too,
		// from the SAME predicate the agent runtime's pre-turn gate uses.
		// Both flags may be true; `needs_provider` wins in copy (the SPA's
		// concern) and they stay separate fields on the wire.
		ag.DegradedReason = agentDegradedReason(a.providerCatalog, cfg, &ac)
		if len(ac.Skills) > 0 {
			skills := make([]string, len(ac.Skills))
			copy(skills, ac.Skills)
			ag.Skills = &skills
		}
		setAgentExecutorResponse(&ag, ac.Subagents)
		if ac.UpdatedAt != nil {
			ag.UpdatedAt = ac.UpdatedAt
		}
		agents = append(agents, ag)
	}

	jsonOK(w, agents)
}

func (a *restAPI) getAgent(w http.ResponseWriter, id string) {
	cfg := a.agentLoop.GetConfig()
	defaults := buildAgentDefaults(cfg)
	activeIDs := a.activeAgentIDSet()

	for _, ac := range cfg.Agents.List {
		if ac.ID == id {
			model := cfg.Agents.Defaults.DefaultModel.Model
			if ac.Model != nil && ac.Model.Primary != "" {
				model = ac.Model.Primary
			}
			workspace, wsErr := agentWorkspacePath(cfg, ac.ID, ac.Home, a.homePath)
			if wsErr != nil {
				slog.Warn("rest: getAgent: could not resolve workspace", "agent_id", ac.ID, "error", wsErr)
			}
			soul, _ := readAgentFiles(workspace)
			// Core agents have compiled prompts — do not expose them.
			// ADR-052 FR-038: System Agents (the Judge) are exempted — their soul
			// is their operator-editable verifier rubric, not a compiled prompt.
			if ac.Locked && !ac.IsSystem() {
				soul = ""
			}
			ag := defaults
			ag.Id = ac.ID
			ag.Name = ac.Name
			if ac.Description != "" {
				ag.Description = &ac.Description
			}
			if ac.Color != "" {
				ag.Color = &ac.Color
			}
			if ac.Icon != "" {
				ag.Icon = &ac.Icon
			}
			ag.Type = coreagent.ToWireType(ac)
			ag.Locked = ac.Locked
			applyAgentOverrides(&ag, &ac)
			// ADR-066 D2/D9 — see listAgents.
			applyAgentContextWindow(&ag, cfg, &ac)
			ag.Model = &model
			setAgentModelProvider(&ag, ac.Model)
			ag.Status = gen.AgentStatus(computeAgentStatus(ac.ID, activeIDs, soul, ac.Locked))
			ag.Soul = soul
			// Derived from the settings singleton — see listAgents' comment on
			// the same line shape for the full rationale.
			ag.Default = boolPtr(ac.ID == cfg.Agents.Defaults.DefaultAgentID)
			// ADR-068 FR-014 (T068-08): needs_model is derived, never stored.
			ag.NeedsModel = agentNeedsModel(cfg, &ac)
			// ADR-067 FR-016/FR-031 (T067-09) — see listAgents.
			ag.DegradedReason = agentDegradedReason(a.providerCatalog, cfg, &ac)
			if len(ac.Skills) > 0 {
				skills := make([]string, len(ac.Skills))
				copy(skills, ac.Skills)
				ag.Skills = &skills
			}
			setAgentExecutorResponse(&ag, ac.Subagents)
			if ac.UpdatedAt != nil {
				ag.UpdatedAt = ac.UpdatedAt
			}
			jsonOK(w, ag)
			return
		}
	}

	jsonErr(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", id))
}

// agentToolPolicyMapFromWire converts a generated per-tool policy map (whose
// values are a request-specific string enum, e.g.
// AgentUpdateRequestToolsCfgBuiltinPolicies) into config.ToolPolicy values.
// Used to build a candidate config.AgentBuiltinToolsCfg for
// config.ValidateToolPolicyCoverage before persisting a write (CLAUDE.md hard
// constraint 6) — never mutates anything, purely a type conversion.
func agentToolPolicyMapFromWire[V ~string](in map[string]V) map[string]config.ToolPolicy {
	if in == nil {
		return nil
	}
	out := make(map[string]config.ToolPolicy, len(in))
	for k, v := range in {
		out[k] = config.ToolPolicy(v)
	}
	return out
}

// validateCandidateToolPolicyCoverage clones cfg, applies mutate to the
// clone, then checks tool-policy coverage (config.ValidateToolPolicyCoverage,
// CLAUDE.md hard constraint 6). Returns gaps (nil = covered) or an error if
// cloning failed. Shared by all 4 REST write paths that must reject an
// incomplete tool-policy map before persisting: createAgent, updateAgent,
// updateAgentTools (this file) and putToolPolicies (rest_tool_policies.go).
//
// Callers must hold a.configMu across both this validation call and the
// subsequent persist (via updateConfigJSONLocked) so the two steps form one
// atomic critical section — otherwise a second concurrent write could slip
// in between validation and persist and reintroduce the exact coverage gap
// this check exists to prevent (see updateConfigJSONLocked's doc comment).
func (a *restAPI) validateCandidateToolPolicyCoverage(
	cfg *config.Config,
	mutate func(*config.Config),
) ([]config.CoverageGap, error) {
	candidateCfg, err := cfg.Clone()
	if err != nil {
		return nil, err
	}
	mutate(candidateCfg)
	return config.ValidateToolPolicyCoverage(candidateCfg, buildKnownBuiltinToolNames()), nil
}

// withToolPolicyCoverageGuard runs the shared "fetch-fresh, validate,
// persist" critical section for every write path that must reject an
// incomplete tool-policy map before persisting (CLAUDE.md hard constraint 6).
// It always fetches the CURRENT live config fresh, INSIDE a.configMu — never
// a caller-supplied snapshot — because fetching before the lock (as
// updateAgent/updateAgentTools used to) reopens the exact TOCTOU window the
// lock exists to close (see updateConfigJSONLocked's doc comment): a
// concurrent write between the pre-lock fetch and the lock acquisition could
// swap the live config, so validation would silently run against stale data.
//
// mutate receives a clone of that freshly-fetched config and must locate any
// agent it touches by ID (never a pre-lock-computed slice index — a
// concurrently-changed agent list can shift or shrink that index between
// fetch and lock). gapErrMsg renders the 400 body when coverage is
// incomplete. persist is handed to updateConfigJSONLocked as-is: it must
// perform its own fresh, ID-based lookup against the on-disk JSON map (never
// trust a pre-lock index there either) and return errAgentVanishedDuringUpdate
// if the target it expects to find is gone (mapped to 404 below), or
// errConflict for an optimistic-concurrency mismatch (mapped to 409) — both
// sentinels are safe to check unconditionally since only the call sites that
// actually use them will ever produce them.
//
// mutate may be nil: some writes (e.g. updateAgent fields that have nothing
// to do with tools_cfg — default flag, model, skills, …) must NOT run the
// coverage check at all, because config.ValidateToolPolicyCoverage checks
// EVERY agent in the whole roster, not just the one this write touches. A
// pre-existing agent with an incomplete tools map (a config seeded before
// full coverage enumeration, or a bare test fixture) would then turn an
// entirely unrelated field update into a spurious 400 — a real regression
// caught by rest_routing_test.go's default-flag fixtures, which carry no
// Tools field at all. Skipping the check when mutate is nil restores the
// original "only validate when this write actually changes tool policy"
// behavior while still closing the TOCTOU for call sites that DO validate.
//
// Returns false once it has already written the HTTP response (error/reject
// case); the caller just returns in that case.
func (a *restAPI) withToolPolicyCoverageGuard(
	w http.ResponseWriter,
	mutate func(*config.Config),
	gapErrMsg func(gaps []config.CoverageGap) string,
	persist func(map[string]any) error,
	persistErrLogMsg string,
) bool {
	a.configMu.Lock()
	defer a.configMu.Unlock()

	if mutate != nil {
		cfg := a.agentLoop.GetConfig()
		gaps, err := a.validateCandidateToolPolicyCoverage(cfg, mutate)
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("tool policy coverage check: %v", err))
			return false
		}
		if len(gaps) > 0 {
			jsonErr(w, http.StatusBadRequest, gapErrMsg(gaps))
			return false
		}
	}
	if err := a.updateConfigJSONLocked(persist); err != nil {
		if errors.Is(err, errConflict) {
			writeJSON(w, http.StatusConflict, gen.ErrorResponse{
				Error: "conflict",
				Code:  strPtr("conflict"),
			})
			return false
		}
		if errors.Is(err, errAgentVanishedDuringUpdate) {
			jsonErr(w, http.StatusNotFound, err.Error())
			return false
		}
		slog.Error(persistErrLogMsg, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return false
	}
	return true
}

// fastAgentUpsert is createAgent/updateAgent's ADR-054-completing fast path
// (issue #571) for publishing a single agent create/update into the live
// AgentRegistry, instead of a full config reload that restarts channels,
// cron, schedulers, and the plan engine (up to ~60s under load).
//
// By the time either handler calls this, updateConfigJSONLocked has ALREADY
// run (via withToolPolicyCoverageGuard) and its refreshConfigAndRewireServices
// call has ALREADY re-read config.json and repopulated cfg.Agents.List from
// the entity store — so a.agentLoop.GetConfig() here already contains
// agentID's just-persisted entity record, and (for updateAgent's
// default-agent-ID-flip case) the just-written agents.defaults.default_agent_id
// singleton too. This function only has to swap the ONE affected
// AgentInstance into the AgentRegistry; see AgentLoop.UpsertAgentFast for the
// resolver/default-override rebuild (TRAP 1) and the atomic, lost-update-safe
// publish (TRAP 2).
//
// Returns "" on success, or a non-empty warning string mirroring
// createAgent/updateAgent's existing "warning" response field. Any failure —
// the agent unexpectedly missing from the just-refreshed config, or a wiring
// error inside UpsertAgentFast — falls back to the slow, already-hardened
// full reload (triggerReloadAndWait) rather than leaving a half-wired agent
// live: the exact risk AgentRegistry.UpsertAgent's own doc comment warns a
// bare caller into.
//
// Defers to an ALREADY in-flight full reload rather than racing it:
// AgentLoop.UpsertAgentFast's own doc documents a known, narrow residual —
// a full ReloadProviderAndConfig that started with an OLDER config snapshot
// (predating this agent's entity write) can complete AFTER this function's
// fast-path publish and silently overwrite it, since that reload's snapshot
// never saw the new agent. This is exactly the class of race
// reload_coalescing_test.go's coalescing fix exists to close (a create
// landing mid-reload must never be lost). Checking IsReloadPending() first
// and, when true, going straight to the coalescing-aware
// triggerReloadAndWait (which waits out the in-flight reload AND any
// coalesced follow-up that re-reads config fresh) keeps that guarantee
// intact — it only costs the full reload's latency in the narrower case
// where one is already running for some other reason, not on every
// create/update.
func (a *restAPI) fastAgentUpsert(agentID string) string {
	if a.agentLoop.IsReloadPending() {
		return a.fallbackFullReload()
	}
	err := a.testForceFastUpsertErr
	if err == nil {
		cfg := a.agentLoop.GetConfig()
		_, err = a.agentLoop.UpsertAgentFast(cfg, agentID)
	}
	if err != nil {
		slog.Error("rest: fast agent upsert failed; falling back to full reload",
			"agent_id", agentID, "error", err)
		return a.fallbackFullReload()
	}
	return ""
}

// fallbackFullReload runs the slow, well-tested full config reload
// (triggerReloadAndWait) and renders its error, if any, as a warning string
// in the same shape createAgent/updateAgent already surface on their
// "warning" response field. Used when fastAgentUpsert cannot complete the
// narrow single-agent path.
func (a *restAPI) fallbackFullReload() string {
	if err := a.triggerReloadAndWait(); err != nil {
		return fmt.Sprintf("config reload failed: %v", err)
	}
	return ""
}

// joinCoverageGapMessages renders a []config.CoverageGap as a single
// semicolon-joined human-readable string for 400 error bodies — the smallest
// adaptation for existing strings.Join call sites now that
// config.ValidateToolPolicyCoverage returns a structured []CoverageGap
// instead of []string (see CoverageGap.String's doc comment).
func joinCoverageGapMessages(gaps []config.CoverageGap) string {
	msgs := make([]string, len(gaps))
	for i, g := range gaps {
		msgs[i] = g.String()
	}
	return strings.Join(msgs, "; ")
}

// agentModelParamsFromWire converts any of the three request variants'
// model_params wire object into the common agentModelParamsInput, or nil
// when mp is nil. gen.AgentCreateRequestSubagent3p has no model_params
// property at all (the external runner manages its own sampling
// parameters), so this is never called for that variant.
func agentModelParamsFromWire(mp *struct {
	MaxTokens   *int     `json:"max_tokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
},
) *agentModelParamsInput {
	if mp == nil {
		return nil
	}
	return &agentModelParamsInput{Temperature: mp.Temperature, MaxTokens: mp.MaxTokens}
}

// mergeAgentModelParams is the single persistence seam for model_params,
// introduced by commit 2b057e15 (Q1 fix) for updateAgent and reused here by
// createAgent so the two paths cannot drift — a createAgent that silently
// dropped model_params would be exactly the same ADR-037 anti-pattern the
// Q1 fix closed for PUT. Field-level merge: only the sub-fields the caller
// actually sent overwrite the persisted value (mirrors the ShellPolicy
// partial-patch pattern elsewhere in this file), so a partial patch (e.g.
// only max_tokens) does not clobber an existing temperature. existing may
// be nil (e.g. on create, or an agent with no prior override).
func mergeAgentModelParams(existing *config.AgentModelParams, in *agentModelParamsInput) *config.AgentModelParams {
	if in == nil {
		return existing
	}
	merged := &config.AgentModelParams{}
	if existing != nil {
		cp := *existing
		merged = &cp
	}
	if in.Temperature != nil {
		v := *in.Temperature
		merged.Temperature = &v
	}
	if in.MaxTokens != nil {
		v := *in.MaxTokens
		merged.MaxTokens = &v
	}
	return merged
}

// deleteAgent handles DELETE /api/v1/agents/{id}.
// Removes the agent from config.json and reloads the live config.
// Core (locked) agents cannot be deleted (403).
func (a *restAPI) deleteAgent(w http.ResponseWriter, id string) {
	cfg := a.agentLoop.GetConfig()
	var found *config.AgentConfig
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == id {
			found = &cfg.Agents.List[i]
			break
		}
	}
	if found == nil {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", id))
		return
	}
	// ADR-049 D3: System Agents (the Judge) are non-deletable. Checked BEFORE the
	// locked 403 because a System Agent is also locked — the spec requires the
	// system-specific 400 ("not deletable"), not the generic locked 403.
	if found.IsSystem() {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "system agents are not deletable",
			"code":  "system_agent_undeletable",
		})
		return
	}
	if found.Locked {
		// Surface the contract's "agent_locked" error code so the SPA can
		// distinguish the locked-agent 403 from generic forbidden. JSON shape
		// mirrors the ErrorResponse schema: { error, code }.
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "cannot delete a locked (core) agent",
			"code":  "agent_locked",
		})
		return
	}
	// ADR-049 D4/FR-065: an agent owning >=1 active (State=running) Plan
	// cannot be deleted outright — the plan engine has no owner left to wake
	// at its next decision point, which would silently stall the loop. The
	// operator must stop/reassign the plan(s) first (or disable the agent,
	// which pauses them instead — see the workspace member-heartbeat
	// enable/disable path in rest_workspaces.go, this codebase's only
	// per-agent enable/disable toggle). Checked before the destructive config
	// write below. Nil-safe: a pre-boot/degraded engine (not yet wired) is a
	// legitimate skip (Wave 2-C1's plan feature simply isn't available in
	// this process), not a fail-open on real data. When the engine IS wired,
	// HasActivePlansOwnedBy fails CLOSED on a plan-store read error (fix-wave
	// finding 1) — this handler mirrors that by refusing the delete (503)
	// rather than treating "could not verify" as "no active plans".
	if pe := agent.GetPlanEngine(a.agentLoop); pe != nil {
		hasActive, err := pe.HasActivePlansOwnedBy(id)
		if err != nil {
			slog.Error("delete agent: could not verify active plan ownership", "agent_id", id, "error", err)
			jsonErr(w, http.StatusServiceUnavailable,
				"could not verify plan ownership; try again")
			return
		}
		if hasActive {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": "agent owns active plans; stop or reassign them before deleting this agent",
				"code":  "agent_owns_active_plans",
			})
			return
		}
	}
	// Snapshot the audit fields BEFORE we mutate config.json — `found` still
	// points into the in-memory config and the safeUpdateConfigJSON callback
	// runs before the reload returns.
	deletedName := found.Name
	deletedType := string(found.Type)
	// ADR-054 D2/D6 rule 5/§11 checklist item 2: remove the agent's entity
	// record (entities/agents/<id>.json) FIRST — via the agent store, not by
	// splicing config.json's agents.list — before any best-effort directory
	// cleanup below. Dangling referrers (bindings, mailboxes, workspace
	// core_team) are surfaced for repair per D6 rule 2, never silently
	// pruned here.
	if err := agentstore.New(a.homePath).Delete(id); err != nil {
		slog.Error("rest: deleteAgent: delete agent entity record failed", "agent_id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "failed to delete agent")
		return
	}
	// Tell the roster regression guard this shrink was INTENTIONAL before the
	// reload below re-reads the store. Deleting the last agent legitimately
	// empties the roster, which is otherwise indistinguishable from the store
	// failing — see forgetRosterBaseline's doc comment. Without this the
	// post-delete reload is rejected and the in-memory roster keeps serving
	// the agent we just deleted from disk.
	forgetRosterBaseline(a.homePath)
	// UAT E-3 / ADR-086 D9: end the active goals this agent was working as an
	// honest `cleared` transition naming the deleted agent — never leave them
	// active for the keeper to push at a missing agent (which re-homed them
	// onto the default agent), and never erase them. Done BEFORE the reload so
	// no keeper tick can see a live goal whose agent has already left the
	// registry. The entity delete above already succeeded and cannot be rolled
	// back, so a failure here is logged at Error rather than failing the
	// request; goals that could not be ended stay active and are named in the
	// error.
	if ended, gerr := a.agentLoop.EndGoalsOfDeletedAgent(id, deletedName); gerr != nil {
		slog.Error("rest: deleteAgent: could not end every active goal the deleted agent was working",
			"agent_id", id, "goals_ended", ended, "error", gerr)
	} else if ended > 0 {
		slog.Info("rest: deleteAgent: ended the deleted agent's active goals", "agent_id", id, "goals_ended", ended)
	}
	// Reload the live config so the deleted agent is no longer in memory.
	// triggerReloadAndWait polls until reload completes (or 5s deadline) so the in-memory config is
	// updated before the 204 response is sent back to the caller (prevents a
	// race where an immediate GET /sessions/:id still sees agent_removed=false).
	if confirmed, err := a.triggerReloadAndWaitOutcome(); err != nil {
		slog.Error("rest: deleteAgent: reload failed", "agent_id", id, "error", err)
	} else if !confirmed {
		slog.Warn("rest: deleteAgent: reload did not confirm within the poll window; "+
			"deleted agent may still be resolvable in the runtime registry", "agent_id", id)
	}
	// Deny every tool approval the deleted agent is still waiting on. Left
	// pending, each one keeps its turn blocked and its dialog open in every
	// tab until the approval timeout, offering an Approve that would run a
	// tool for an agent that no longer exists. The registry's resolution
	// listener broadcasts tool_approval_resolved, so every tab drops it.
	if a.approvalReg != nil {
		a.approvalReg.cancelAllPendingForAgent(id, denialReasonCancel)
	}
	// Audit the destructive action. Emitted after the write succeeds; a
	// failed audit write is logged (not silently discarded) so audit-log
	// gaps stay visible. The auditor is nil only in unit-test fixtures
	// where audit isn't wired; the pre-existing workspace handlers use the
	// same nil-guard pattern.
	if a.auditor != nil {
		if err := a.auditor.Log(&audit.Entry{
			Event:    "agent.delete",
			Decision: audit.DecisionAllow,
			AgentID:  id,
			Details: map[string]any{
				"agent_id":   id,
				"agent_type": deletedType,
				"agent_name": deletedName,
			},
		}); err != nil {
			slog.Warn("audit write failed", "event", "agent.delete", "agent_id", id, "error", err)
		}
	}
	// O6 — drop any heartbeat schedule the deleted agent owned (the reconciler
	// removes heartbeat jobs whose agent is no longer in config).
	a.reconcileHeartbeatSchedules()
	w.WriteHeader(http.StatusNoContent)
}

// cliDetectAll is the detection function used by HandleSystemCliDetect. It is a
// package-level var (rather than a direct clidetect.DetectAll call) so unit
// tests can swap in a deterministic map without depending on the host layout.
// In production this is always clidetect.DetectAll.
var cliDetectAll = clidetect.DetectAll

// HandleSystemCliDetect handles GET /api/v1/system/cli-detect.
//
// Reports, per external-CLI runner (claude-code / codex / opencode), whether the
// binary is installed and — when it is — its absolute resolved path and how it
// was located ("path" via the gateway $PATH, "well-known" via a curated per-OS
// install-dir scan). The roster screen greys-out CLIs the host cannot run, and
// the create wizard / edit form prefill the executor cli_path field from `path`.
//
// Pure-Go filesystem probe — detection NEVER spawns a subprocess (FR-004) and is
// unaudited by design: no caller-supplied path is executed here (auditing is
// reserved for cli-validate, which does spawn). withAuth only.
func (a *restAPI) HandleSystemCliDetect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	all := cliDetectAll()

	var resp gen.CliDetect

	c := all["claude-code"]
	resp.Claude.Installed = c.Installed
	if c.Installed {
		resp.Claude.Path = strPtr(c.Path)
		if wire := cliDetectSourceWire(c.Source); wire != "" {
			src := gen.CliDetectClaudeSource(wire)
			resp.Claude.Source = &src
		}
	}

	cx := all["codex"]
	resp.Codex.Installed = cx.Installed
	if cx.Installed {
		resp.Codex.Path = strPtr(cx.Path)
		if wire := cliDetectSourceWire(cx.Source); wire != "" {
			src := gen.CliDetectCodexSource(wire)
			resp.Codex.Source = &src
		}
	}

	oc := all["opencode"]
	resp.Opencode.Installed = oc.Installed
	if oc.Installed {
		resp.Opencode.Path = strPtr(oc.Path)
		if wire := cliDetectSourceWire(oc.Source); wire != "" {
			src := gen.CliDetectOpencodeSource(wire)
			resp.Opencode.Source = &src
		}
	}

	jsonOK(w, resp)
}

// cliDetectSourceWire validates a clidetect.Source against the known set and
// returns its wire string, or "" for an unexpected value. The detector only ever
// emits the two known sources, so this is defense-in-depth: a future/unknown
// value normalizes to an omitted Source rather than being reflected onto the
// wire enum unvalidated.
func cliDetectSourceWire(s clidetect.Source) string {
	switch s {
	case clidetect.SourcePath, clidetect.SourceWellKnown:
		return string(s)
	default:
		return ""
	}
}

// isExternalSubagent reports whether the persisted agent is a subagent_3p
// (an External-CLI worker). Subagent (native worker) returns false; Main /
// core / system return false; only subagent_3p returns true.
func isExternalSubagent(ac config.AgentConfig) bool {
	if ac.Type != config.AgentTypeWorker {
		return false
	}
	if ac.Subagents == nil || ac.Subagents.Executor == nil {
		return false
	}
	return ac.Subagents.Executor.EffectiveKind() == config.ExecutorKindExternalCLI
}
