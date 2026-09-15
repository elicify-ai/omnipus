// gateway_sandbox.go: Sandbox and egress - apply the sandbox policy and tool-policy repair at boot

package gateway

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/audit"
	_ "github.com/elicify-ai/omnipus/pkg/channels/dingtalk"
	_ "github.com/elicify-ai/omnipus/pkg/channels/discord"
	_ "github.com/elicify-ai/omnipus/pkg/channels/feishu"
	_ "github.com/elicify-ai/omnipus/pkg/channels/googlechat"
	_ "github.com/elicify-ai/omnipus/pkg/channels/irc"
	_ "github.com/elicify-ai/omnipus/pkg/channels/line"
	_ "github.com/elicify-ai/omnipus/pkg/channels/qq"
	_ "github.com/elicify-ai/omnipus/pkg/channels/slack"
	_ "github.com/elicify-ai/omnipus/pkg/channels/telegram"
	_ "github.com/elicify-ai/omnipus/pkg/channels/wecom"
	_ "github.com/elicify-ai/omnipus/pkg/channels/weixin"
	_ "github.com/elicify-ai/omnipus/pkg/channels/whatsapp_native"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// resolveAllowGodMode is the single, boot-time computation of god-mode
// AVAILABILITY authorization (O14): the legacy --allow-god-mode CLI flag OR
// the config-persisted sandbox.god_mode_allowed grant (written by POST
// /api/v1/gateway/god-mode enabled=true from the Settings UI). It does NOT
// consult sandbox.GodModeAvailable (build support) — callers combine that
// separately so the two failure modes ("not authorized" vs "not supported by
// this build") stay distinguishable.
//
// Pure and deterministic so it is unit-testable without booting a gateway.
// A nil cfg only consults the flag — defensive for callers that have not yet
// loaded config (never happens on the real boot path, but keeps this safe to
// call early).
//
// Called exactly once at boot; the result flows unchanged into
// agent.AgentLoop.SetAllowGodMode (the process-wide availability atomic that
// gates the live tool-policy/sandbox override) and restAPI.allowGodMode (the
// REST predicate consulted by GET/POST /api/v1/gateway/god-mode). Because
// this is computed once and frozen for the process lifetime, a config-only
// grant (UI enable while allowGodMode was false at boot) does not change live
// enforcement until the next restart re-reads sandbox.god_mode_allowed —
// this is intentional and is exactly why setGodMode reports
// restart_required=true for that case.
func resolveAllowGodMode(cliFlag bool, cfg *config.Config) bool {
	if cliFlag {
		return true
	}
	return cfg != nil && cfg.Sandbox.GodModeAllowed
}

// Error makes SandboxBootError satisfy the error interface.
func (e *SandboxBootError) Error() string {
	if e == nil || e.Err == nil {
		return "sandbox boot error"
	}
	return e.Err.Error()
}

// Unwrap exposes the underlying error for errors.Is/As traversal.
func (e *SandboxBootError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

var (
	knownBuiltinToolNamesOnce  sync.Once
	knownBuiltinToolNamesCache map[string]struct{}
)

// buildKnownBuiltinToolNames returns the set of every static builtin tool name
// across the three built-in catalogs — general, browser, and sysagent (the
// system.* / "system agent" tools). This is the wildcard-free literal
// universe config.ValidateToolPolicyCoverage checks every agent's tool-policy
// map against (CLAUDE.md hard constraint 6). MCP tool names are deliberately
// excluded: they aren't known until an operator connects a server at runtime
// and remain governed by the per-server wildcard-bulk-policy exception.
//
// All three sources are metadata-only, never-Execute()d constructions:
//
//   - tools.GeneralBuiltinMetadata() and browser.BrowserBuiltinMetadata() are
//     the existing central-registry catalog functions (Issue #350, ADR-018
//     D-A1).
//
//     NOTE: this function is NOT the source GET /api/v1/tools uses (an
//     earlier version of this comment incorrectly claimed it was — confirmed
//     false by architect review). That endpoint (HandleToolsRegistry,
//     pkg/gateway/rest_tool_registry.go) enumerates tools independently via
//     a.builtinRegistry.All() (or, when unwired, the default agent's live
//     registered Tools.GetAll()) plus a.mcpRegistry.All() for MCP — a
//     completely separate, independently-maintained code path that only
//     coincidentally agrees with this function's output today (every static
//     tool registers with IsCore:true unconditionally, so it always appears
//     regardless of which agent is default; only MCP tools use a different,
//     TTL-gated registration path). See
//     TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog for
//     the drift-detection regression test this package carries instead.
//
//   - systools.AllTools(nil) has no equivalent metadata-only catalog
//     function, but is safe to call for name-harvesting alone: every
//     constructor in pkg/sysagent/tools does nothing but store the *Deps
//     pointer it is given (never dereferenced at construction time), and
//     every tool's Name() method is a static string literal that never reads
//     deps. Passing nil deps is therefore safe PROVIDED the returned tools
//     are never Execute()d here — and they never are; only .Name() is called
//     below.
//
// The three static catalogs never change at runtime, so the result is
// computed once (guarded by knownBuiltinToolNamesOnce) and the same shared
// map is returned to every caller thereafter. Safe only because no caller
// mutates the returned map — it is read-only past this function.
func buildKnownBuiltinToolNames() map[string]struct{} {
	knownBuiltinToolNamesOnce.Do(func() {
		out := make(map[string]struct{})
		for _, t := range tools.GeneralBuiltinMetadata() {
			out[t.Name()] = struct{}{}
		}
		for _, t := range browser.BrowserBuiltinMetadata() {
			out[t.Name()] = struct{}{}
		}
		for _, t := range systools.AllTools(nil) {
			out[t.Name()] = struct{}{}
		}
		// ADR-052 (autonomous agent plan execution, FR-027) — the four
		// planning/verifier tool names (create_plan, execute_plan, run_task,
		// inspect_session) are unioned in explicitly here, independent of
		// their pkg/tools|pkg/sysagent/tools implementation landing, so the
		// tool-policy coverage universe (config.ValidateToolPolicyCoverage /
		// config.ReconcileToolPolicyCeiling) recognizes them from the
		// config-seeding side immediately. Mirrors
		// pkg/coreagent/core.go's allStaticToolNames literal-for-literal
		// (TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog
		// enforces the two stay in sync). Once the real tool implementations
		// register themselves via GeneralBuiltinMetadata/AllTools, this union
		// is idempotent (same names, no duplicate entries in a set).
		for _, name := range []string{"create_plan", "execute_plan", "run_task", "inspect_session"} {
			out[name] = struct{}{}
		}
		// ADR-068 D15.3 (FR-070/FR-071) — the knowledge-base tool names (six
		// under ADR-068, plus KB-1/KB-2's knowledge_list and
		// knowledge_base_create — defect-list-knowledge-base-ux-2026-09-08.md,
		// founder-ratified 2026-09-08) are unioned in explicitly here for the
		// same reason, and under the same rule, as the ADR-052 four directly
		// above: independent of their pkg/knowledge/pkg/vaultprops
		// implementation landing, so the
		// tool-policy coverage universe (config.ValidateToolPolicyCoverage /
		// RepairIncompleteToolPolicyCoverage) recognizes them from the
		// config-seeding side immediately. Mirrors pkg/coreagent/core.go's
		// allStaticToolNames literal-for-literal
		// (TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog
		// enforces the two stay in sync). Idempotent once the real
		// implementations register themselves (same names, no duplicate
		// entries in a set).
		//
		// Why the union is load-bearing rather than tidy-up: BOTH the coverage
		// validator and the load-path repair derive their gap list from this
		// map, and neither reports anything for a name it does not contain. A
		// knowledge tool seeded in pkg/config/defaults.go and
		// pkg/coreagent/core.go but MISSING here is invisible to both — the
		// boot check passes, no gap is reported, and any test asserting "no
		// knowledge_* entry was backfilled to deny" passes vacuously because
		// nothing could ever have been backfilled. That is FR-071's failure
		// mode, and it is silent: repairAndValidateToolPolicyCoverage below
		// repairs BEFORE it validates, so a genuine gap ships as an explicit
		// deny plus one WARN line rather than aborting boot.
		//
		// ADR-067's nine (knowledge_search, knowledge_graph, knowledge_create,
		// knowledge_link, knowledge_set_property, knowledge_append_section,
		// knowledge_tasks, knowledge_move, knowledge_rename) are RETIRED and
		// deliberately absent below — see knowledge_tools_wire.go's header for
		// why their Go implementations are not deleted even though they are no
		// longer part of the agent-callable catalog.
		for _, name := range []string{
			// READ tier — touch nothing outside what the caller asked for.
			"knowledge_describe", "knowledge_find", "knowledge_read",
			// knowledge_list (KB-2a) — also read tier: which knowledge bases
			// this agent can reach.
			"knowledge_list",
			// EDIT — one named file.
			"knowledge_edit",
			// RESTRUCTURE — cascades: rewrites files the caller never named.
			"knowledge_restructure",
			// CONFIGURE — control plane: changes what existing notes MEAN.
			"knowledge_configure",
			// knowledge_base_create (KB-1) — makes a NEW knowledge base.
			"knowledge_base_create",
		} {
			out[name] = struct{}{}
		}
		// ADR-081 D11 (unified-search-and-grep-spec.md FR-008/FR-009) — the
		// grep agent tool is unioned in explicitly here, for the same reason
		// and under the same rule as the ADR-052 four and the ADR-068 six
		// directly above: independent of its own implementation package
		// landing, so the tool-policy coverage universe
		// (config.ValidateToolPolicyCoverage / RepairIncompleteToolPolicyCoverage)
		// recognizes it from the config-seeding side immediately. Mirrors
		// pkg/coreagent/core.go's allStaticToolNames literal-for-literal
		// (TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog
		// enforces the two stay in sync). Idempotent once the real tool
		// implementation registers itself (same name, no duplicate entry in
		// a set).
		out["grep"] = struct{}{}
		knownBuiltinToolNamesCache = out
	})
	return knownBuiltinToolNamesCache
}

// repairAndValidateToolPolicyCoverage runs the shared "migrate legacy keys,
// reconcile the global ceiling, then hard-validate the result" sequence used
// identically at boot (RunContextWithOptions) and at hot-reload
// (executeReload) — CLAUDE.md hard constraint 6. Both call sites previously
// hand-rolled this same knownTools-assembly + repair + summary-log sequence
// independently; sharing one helper means the two can no longer silently
// diverge on what "reconcile then validate" means.
//
// ADR-077 ratifies tool policy as exactly TWO layers, no implicit third: the
// global ceiling (cfg.Sandbox.ToolPolicies, kept complete for the whole
// static catalog by step 2 below) IS the default for every tool, and sparse
// per-agent overrides only ever tighten below it. The fail-closed per-agent
// "deny" backfill this helper used to run as a third step
// (config.RepairIncompleteToolPolicyCoverage) is retired — see that
// function's retirement comment in pkg/config/validate.go. Reconciling a
// tool to its shipped default (including bash=allow) is intended, not a gap
// to paper over.
//
// Order matters and must not be reshuffled (each step's own doc comment
// explains why it must run where it does):
//  1. config.MigrateLegacyToolPolicyKeys — rename retired keys forward first,
//     so step 2 never reconciles a "missing" entry for a name that was only
//     missing because it hadn't been renamed yet.
//  2. config.ReconcileToolPolicyCeiling (ADR-076) — backfill the GLOBAL
//     ceiling with the real shipped default for any static builtin tool
//     added to pkg/config/defaults.go since this install's config.json was
//     last written, so newly-added tools resolve to their intended
//     allow/ask/deny posture from the ceiling itself.
//  3. config.ValidateToolPolicyCoverage — a never-firing correctness
//     tripwire (ADR-077 D4): after step 2 guarantees ceiling completeness
//     for the static catalog, a both-sides gap can only mean a genuine
//     internal drift (a catalog tool with no defaults.go entry), so this
//     aborts boot loudly rather than resolving silently.
//
// Returns the remaining gaps (empty = fully covered) — the caller decides
// what "remaining gaps" means for it (abort boot vs. reject the reload and
// keep serving the previous config).
func repairAndValidateToolPolicyCoverage(cfg *config.Config) []config.CoverageGap {
	// ADR-071 §5.3.5a: this migration MUST run FIRST, before the ceiling is
	// reconciled below. ToolSearch/switch_agent are new names with no policy
	// entry anywhere until this migration folds the retired load_tool /
	// hand_off / return_to_default keys forward. Sequenced any later, the
	// first post-upgrade boot would reconcile a "missing" entry under the
	// stale name instead of recognizing it as already migrated.
	if config.MigrateLegacyToolPolicyKeys(cfg) {
		slog.Info("gateway: migrated legacy tool-policy keys to their ADR-071 replacements",
			"migrations", "load_tool->ToolSearch, hand_off/return_to_default->switch_agent",
		)
	}
	knownTools := buildKnownBuiltinToolNames()

	// ADR-076: reconcile the GLOBAL ceiling against the shipped static-catalog
	// defaults. Must run after the legacy-key migration (a renamed key must
	// not be re-added under its old name). Under ADR-077, this reconciled
	// ceiling IS the default for every tool a per-agent map does not mention
	// — there is no further backfill step after this one.
	if added := config.ReconcileToolPolicyCeiling(cfg, knownTools); len(added) > 0 {
		slog.Info("gateway: reconciled global tool-policy ceiling with shipped static-catalog defaults",
			"added_count", len(added),
			"added", strings.Join(added, ", "),
		)
	}

	return config.ValidateToolPolicyCoverage(cfg, knownTools)
}

// egressProxyConstructor abstracts sandbox.NewEgressProxy so
// buildEgressProxyOrAbort is unit-testable without depending on
// sandbox.NewEgressProxy's real failure modes (a malformed allow-list entry,
// or the practically-unforceable net.Listen("tcp", "127.0.0.1:0") error).
// Production always passes sandbox.NewEgressProxy itself.
type egressProxyConstructor func([]string, sandbox.EgressAuditFunc) (*sandbox.EgressProxy, error)

// buildEgressProxyOrAbort constructs the Tier 2/3 egress proxy and decides
// what a construction failure means, per CLAUDE.md hard constraint 6
// (deny-by-default / fail-closed for security controls).
//
// Downstream, a nil *sandbox.EgressProxy is NOT a "feature refuses to run"
// signal — it is silently interpreted as "run unrestricted":
//   - pkg/tools/web_serve.go's (*WebServeTool).proxyAddr() returns "" for a
//     nil proxy, so spawnDevChild's sandbox.Limits.EgressProxyAddr is "" and
//     the Tier 3 dev-server child gets no HTTP_PROXY/HTTPS_PROXY at all.
//   - pkg/tools/shell.go's sandboxLimitsEnv (~line 941-958) only injects
//     HTTP_PROXY/HTTPS_PROXY when lim.EgressProxyAddr != "" — an empty addr
//     means bash's hardened path runs with zero egress restriction.
//
// So: when the operator left sandbox.egress_allow_list EMPTY (never opted
// into egress restriction), a construction failure is logged and swallowed
// exactly as before — nil proxy, log-and-continue is the documented
// graceful-degradation behavior for a feature nobody asked for.
//
// When the operator configured a NON-EMPTY sandbox.egress_allow_list
// (explicit opt-in) and construction fails, continuing to boot would
// silently run web_serve dev-mode and bash's egress-dependent path fully
// unrestricted — the exact protection the operator asked for would vanish
// with only a Warn-level log line. That is a strictly worse outcome than
// refusing to start, so this mirrors the audit-logger-construction
// precedent a few hundred lines up (RunContextWithOptions's
// audit.LoggerConstructionError branch, ~line 723-741): wrap the error in
// *SandboxBootError so cmd/omnipus's existing exit-code mapping
// (EX_CONFIG=78, FR-J-004) applies without further plumbing, and emit the
// same stderr breadcrumb via audit.EmitBootAbortStderr.
func buildEgressProxyOrAbort(
	allowList []string,
	auditFn sandbox.EgressAuditFunc,
	newProxy egressProxyConstructor,
) (*sandbox.EgressProxy, error) {
	proxy, err := newProxy(allowList, auditFn)
	if err == nil {
		return proxy, nil
	}

	if len(allowList) == 0 {
		slog.Warn("gateway: egress proxy failed to start; web_serve dev mode will run without egress enforcement",
			"error", err)
		return nil, errEgressProxyDisabled
	}

	audit.EmitBootAbortStderr(
		"gateway.egress_proxy.construction_failed",
		"-",
		"",
		err,
		[]audit.KV{{Key: "allow_list_entries", Value: strconv.Itoa(len(allowList))}},
	)
	return nil, &SandboxBootError{Err: fmt.Errorf(
		"gateway: egress proxy failed to start with sandbox.egress_allow_list configured (%d entr%s) — "+
			"refusing to boot rather than silently running web_serve dev-mode and bash's hardened exec path "+
			"WITHOUT the egress enforcement you configured; fix the allow-list entries or the underlying "+
			"error and restart, or clear sandbox.egress_allow_list to accept unrestricted egress: %w",
		len(allowList), pluralSuffix(len(allowList)), err,
	)}
}

// pluralSuffix returns "y" for n == 1 ("entry") and "ies" otherwise
// ("entries") — small formatting helper for buildEgressProxyOrAbort's error
// message so it reads correctly for both singular and plural allow-lists.
func pluralSuffix(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
