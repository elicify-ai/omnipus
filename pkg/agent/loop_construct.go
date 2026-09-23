// loop_construct.go: Construct an AgentLoop: the NewAgentLoop stages

package agent

import (
	"os"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/commands"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/policy"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/state"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// initializeCore builds the registry, shared routing state, task executor, and session store.
func (nal *newAgentLoop) initializeCore() {
	nal.registry = NewAgentRegistry(nal.cfg, nal.provider)

	// Apply configurable default agent override.
	if nal.cfg.Agents.Defaults.DefaultAgentID != "" {
		nal.registry.SetDefaultAgentOverride(nal.cfg.Agents.Defaults.DefaultAgentID)
	}

	// Set up shared fallback chain with per-candidate timeout so a primary
	// provider timeout does not strand fallback candidates with an exhausted
	// context deadline (#235).
	cooldown := providers.NewCooldownTracker()
	fallbackChain := providers.NewFallbackChainWithTimeout(cooldown, perCandidateTimeoutFromConfig(nal.cfg))

	// Create state manager using default agent's workspace for channel recording
	defaultAgent := nal.registry.GetDefaultAgent()
	var stateManager *state.Manager
	if defaultAgent != nil {
		stateManager = state.NewManager(defaultAgent.Home)
	}

	// ADR-057 W17: a boot-time diagnostic only — genuine construction of the
	// root-delegation admission gate happens AFTER al exists, below, via a
	// LIVE resolver (concurrency-gate consolidation, 2026-08-04). A NEGATIVE
	// agents.defaults.subturn.max_concurrent is the only case
	// ResolveRootDelegationCap treats as an error (an unset/zero value now
	// resolves straight to the central Performance.EffectiveMaxParallelAgents()
	// authority, not an error — see ResolveRootDelegationCap's doc comment).
	// Logged loudly here so a genuine operator misconfiguration is
	// diagnosable at boot; does not abort construction, since the live
	// resolver's own error branch (below) keeps the gate GATED at the
	// central value either way, never nil (nil would mean UNLIMITED root
	// fan-out — the "silently reinterpreted as no gate" outcome ADR-037
	// bans).
	if _, err := ResolveRootDelegationCap(nal.cfg); err != nil {
		logger.ErrorCF("agent",
			"agents.defaults.subturn.max_concurrent is configured to a negative value — the root-delegation admission gate falls back to the central Performance.EffectiveMaxParallelAgents() authority; set it to 0 (inherit the central value) or a positive explicit override",
			map[string]any{"error": err.Error()})
	}

	eventBus := NewEventBus()
	nal.al = &AgentLoop{
		bus:                     nal.msgBus,
		cfg:                     nal.cfg,
		registry:                nal.registry,
		state:                   stateManager,
		eventBus:                eventBus,
		fallback:                fallbackChain,
		cmdRegistry:             commands.NewRegistry(commands.BuiltinDefinitions()),
		steering:                newSteeringQueue(parseSteeringMode(nal.cfg.Agents.Defaults.SteeringMode)),
		contextBuilderRegistry:  NewContextBuilderRegistry(),
		loadedTools:             make(map[string]map[string]bool),
		pendingSearchPromotions: make(map[string]map[string]int),
		bucketTurnCounter:       make(map[string]int),
		browserMgrs:             make(map[string]*browser.BrowserManager),
		browserRegisteredAgents: make(map[string]bool),
	}
	// Concurrency-gate consolidation (2026-08-04): session admission's cap is
	// resolved LIVE from the SAME central authority TaskExecutor's dispatch
	// semaphore uses (Performance.EffectiveMaxParallelAgents), instead of the
	// former independent, hardcoded runtime.NumCPU()*4 soft cap — see
	// AdmissionController.resolveCap's doc comment (admission.go) for why
	// this must be resolved fresh on every check rather than cached once
	// here at construction time.
	nal.al.admission = newAdmissionControllerWithResolver(func() int {
		n, _ := nal.al.GetConfig().Performance.EffectiveMaxParallelAgents()
		return n
	})
	// ADR-057 W17, same live-resolution treatment: root-level delegate()
	// fan-out must never drift from the central authority either. On
	// ResolveRootDelegationCap's error branch (a NEGATIVE configured value)
	// this falls back directly to EffectiveMaxParallelAgents() so the gate
	// stays GATED at the central value rather than degrading to unlimited.
	nal.al.rootDelegationAdmission = newRootDelegationAdmissionWithResolver(func() int {
		liveCfg := nal.al.GetConfig()
		if resolvedCap, capErr := ResolveRootDelegationCap(liveCfg); capErr == nil {
			return resolvedCap
		}
		if liveCfg != nil {
			n, _ := liveCfg.Performance.EffectiveMaxParallelAgents()
			return n
		}
		return 1
	})
	nal.al.hooks = NewHookManager(eventBus)
	configureHookManagerFromConfig(nal.al.hooks, nal.cfg)

	// Initialize the unified task store at ~/.omnipus/tasks/ (the single store —
	// the legacy GTD tasks/ and workflow-tasks/ split was removed in Sprint 2).
	nal.homePath = filepath.Dir(nal.cfg.AgentHomeBasePath())
	nal.al.homePath = nal.homePath
	nal.al.taskStore = task.New(filepath.Join(nal.homePath, "tasks"))
	nal.al.taskExecutor = newTaskExecutor(nal.al, nal.al.taskStore)
	// Founder decision 2026-09-14: expose the running task's live progress
	// stamp (reasoning counts) so the REST tasks surface can stamp
	// Task.last_activity_at. The loop is the authority; the executor is the
	// holder because the REST side already reaches the task engine here.
	nal.al.taskExecutor.SetLiveTaskActivitySource(nal.al)

	// Initialize shared session store at $OMNIPUS_HOME/sessions/.
	// All new chat sessions are created here (joined session model).
	sharedDir := filepath.Join(nal.homePath, "sessions")
	if err := os.MkdirAll(sharedDir, 0o700); err != nil {
		logger.ErrorCF("agent", "Shared session store unavailable — new sessions will use per-agent stores",
			map[string]any{"dir": sharedDir, "error": err.Error()})
	} else {
		sharedStore, ssErr := session.NewUnifiedStoreWithHome(sharedDir, nal.homePath)
		if ssErr != nil {
			logger.ErrorCF("agent", "Shared session store init failed — new sessions will use per-agent stores",
				map[string]any{"dir": sharedDir, "error": ssErr.Error()})
		} else {
			nal.al.sharedSessionStore = sharedStore
			// FR-067/SC-048 (ADR-057): apply the operator's resolved
			// stats-flush override onto the store's live periodic flusher.
			// Without this call, startStatsFlusher (unified_stats_flush.go,
			// invoked unconditionally from NewUnifiedStoreWithHome) always
			// runs on the hardcoded config.DefaultSessionStatsFlushInterval
			// (5s) constant — a seeded, documented
			// sessions.stats_flush_interval key in config.json would persist
			// but have zero runtime effect. cfg.Session.
			// EffectiveStatsFlushInterval() resolves the operator's value (or
			// the same 5s default when unset), exactly matching
			// startStatsFlusher's own doc comment naming this call site.
			sharedStore.SetStatsFlushInterval(nal.cfg.Session.EffectiveStatsFlushInterval())
			nal.al.rebuildChannelSessionIndex()
		}
	}
}

// initializeAudit constructs audit logging and wires it into registries when enabled.
func (nal *newAgentLoop) initializeAudit() (*AgentLoop, bool, error) {
	// SEC-15: Initialize structured audit logging (ON by default since the
	// 2026-09-11 founder decision — see cfg.Sandbox.AuditLog's doc comment)
	// and policy evaluation (always on). Audit directory is ~/.omnipus/system/
	// (sibling of workspace). The audit logger and the policy evaluator are
	// decoupled: disabling audit logging must NOT disable enforcement.
	if nal.cfg.Sandbox.AuditLog {
		auditDir := filepath.Join(nal.homePath, "system")
		auditLogger, auditErr := audit.NewLogger(audit.LoggerConfig{
			Dir:           auditDir,
			RetentionDays: 90,
			// CRIT-2: signal to NewLogger that audit logging is genuinely
			// wanted here. Without this, NewLogger would swallow
			// openCurrentFile errors and return a degraded logger + nil error
			// — the gateway would think audit_logger=ok at startup while every
			// subsequent write rejects in degraded mode. Setting
			// AuditLogRequested makes openCurrentFile failure surface as a
			// *LoggerConstructionError instead.
			//
			// This stays unconditionally true now that audit is on by default.
			// It is deliberately NOT wired to AuditLogExplicit: the flag's job
			// is to stop NewLogger from hiding a failure, and a failure must
			// never be hidden regardless of who asked. WHAT WE DO about the
			// surfaced failure is the part that depends on provenance, and
			// that decision is taken below, at this call site, which is the
			// only place that knows it.
			AuditLogRequested: true,
		})
		if auditErr != nil {
			// The audit logger could not be built. Two different populations
			// reach this line and they have earned different answers.
			//
			// (1) Somebody WROTE `"audit_log": true` — in config.json, or in
			//     a Config built directly in Go. Unchanged from B1.2(b):
			//     fail-closed boot abort. They asked for a compliance
			//     guarantee we cannot deliver, and running on without it
			//     would silently break the SEC-15 audit-everything contract.
			//     The gateway maps the returned typed error to a
			//     SandboxBootError + EX_CONFIG (78) exit code; see
			//     pkg/gateway/gateway.go around the agent.NewAgentLoop call,
			//     whose branch is still gated on cfg.Sandbox.AuditLog being
			//     true. This is the branch a zero-valued provenance field
			//     selects, deliberately — see AuditLogFromDefault's doc
			//     comment on the polarity being the safety property.
			//
			// (2) Audit is on because it is now the DEFAULT and this config
			//     never mentioned it. Degrade loudly and keep booting.
			//
			// Why (2) is not also an abort. Fail-closed is justified by
			// consent: the operator requested a guarantee, so not delivering
			// it silently is the regression. A default is not a request.
			// Aborting on it would convert a security improvement into a
			// denial of service for installs that never opted in and that
			// booted perfectly well yesterday with audit off — an upgrade
			// would turn "audit was off" into "the product does not start",
			// on a read-only filesystem, a full disk, or a partially-mounted
			// container volume. Strictly compared against the status quo this
			// branch is still an improvement: that population previously had
			// audit off AND no error; it now has audit off, a loud error, and
			// a degraded health endpoint.
			//
			// The failure is NOT silent. al.auditLogger stays nil, so
			// AgentLoop.AuditLogger() returns nil while the gateway's
			// SetAuditLoggerConfiguredFunc still reports configured=true from
			// cfg.Sandbox.AuditLog — the exact pair pkg/health/server.go
			// documents as "audit_logger=unavailable AND operator asked for
			// audit → degraded (broken)". /health reads degraded, and the
			// ERROR below names the directory and the underlying cause.
			//
			// In practice (2) should be close to unreachable: auditDir is
			// $OMNIPUS_HOME/system, the same tree that already holds
			// config.json, master.key, sessions and token_budget.json, so an
			// install that cannot write it is broken in ways that surface
			// elsewhere anyway. That is an argument for the blast radius of
			// this branch being small — not an argument for making a default
			// the thing that refuses to start.
			if !nal.cfg.Sandbox.AuditLogFromDefault {
				logger.ErrorCF("agent",
					"Audit logger construction failed; aborting boot because sandbox.audit_log=true was explicitly set",
					map[string]any{"error": auditErr.Error(), "dir": auditDir})
				return nil, true, &audit.LoggerConstructionError{Dir: auditDir, Err: auditErr}
			}
			logger.ErrorCF("agent",
				"Audit logger construction failed; continuing WITHOUT audit logging because audit_log is on by default, not by explicit configuration. "+
					"No security audit entries will be recorded for the lifetime of this process. "+
					"/health reports audit as degraded. Fix the directory, or set sandbox.audit_log=true to make this failure abort boot instead.",
				map[string]any{"error": auditErr.Error(), "dir": auditDir})
			auditLogger = nil
		}
		if auditLogger != nil {
			nal.al.auditLogger = auditLogger

			// Log startup event. CRIT-6: route through audit.EmitEntry so a
			// Log failure bumps the audit-skipped counter (/health audit_degraded).
			audit.EmitEntry(auditLogger, &audit.Entry{
				Event:    audit.EventStartup,
				Decision: audit.DecisionAllow,
				Details: map[string]any{
					"audit_dir": auditDir,
				},
			})

			// Wire audit logger into all agent tool registries. Factored out into
			// wireMemoryAuditLoggerOn so ReloadProviderAndConfig can re-apply the
			// same wiring against a freshly-built registry on hot reload (see that
			// method's doc comment) — without this, every agent's remember/
			// run_retrospective tools would silently lose audit logging (SEC-15)
			// the first time config reloads.
			nal.al.wireMemoryAuditLoggerOn(nal.registry, auditLogger)

			// ADR-072 D6.1.1/R4 fix: install the process-wide skills write-audit
			// logger. tools.SetSkillsWriteAuditLogger's own doc comment names
			// this exact call site ("a later integration phase wires this at
			// gateway boot, alongside the other audit-logger wiring") — until
			// this call existed nowhere in production, tools.ResolvePath's
			// write hook (and pkg/sysagent/tools' project-shelf authoring path,
			// via tools.EmitSkillWriteAudit) was a permanent silent no-op:
			// write_file/edit_file/edit_skill/remove_skill writes into a
			// recognised skills location produced zero audit entries regardless
			// of sandbox.audit_log. Idempotent (last caller wins), mirrors
			// audit.SetProcessChainKey's process-wide-var pattern exactly.
			tools.SetSkillsWriteAuditLogger(auditLogger)
		}
	}
	return nil, false, nil
}

// initializeSecurity builds policy enforcement, sandboxing, prompt protection, and the exec proxy.
func (nal *newAgentLoop) initializeSecurity() {
	// There is no config source for an exec binary allowlist any more, so
	// the evaluator below always constructs with an empty allowlist and the
	// "allow" default policy.
	//
	// policy.Evaluator/PolicyAuditor are still constructed here because
	// pkg/tools/shell.go's ExecPolicyAuditor interface and
	// ExecToolDeps.PolicyAuditor field (ADR-091 lane L4, out of this lane's
	// scope) still consume policy.Decision/PolicyAuditor as of this commit —
	// deleting pkg/policy/evaluator.go or auditor.go here would break that
	// concurrently-developed, unowned file. pkg/policy/saturation.go is
	// unrelated to ADR-091 (consumed by pkg/gateway/gateway_boot.go for the
	// approval-saturation cap) and was never a candidate for deletion.
	secCfg := &policy.SecurityConfig{
		DefaultPolicy: policy.PolicyAllow,
	}
	policyEval := policy.NewEvaluator(secCfg)

	// Wrap the evaluator in a PolicyAuditor so every decision is audit-logged
	// (ADR-002 §W-3). When audit logging is disabled the bridge is nil; the
	// PolicyAuditor tolerates a nil logger and still enforces — enforcement
	// must NOT depend on audit logging being enabled.
	var auditBridgeImpl *auditBridge
	if nal.al.auditLogger != nil {
		auditBridgeImpl = newAuditBridge(nal.al.auditLogger)
	}
	var policyAuditorLogger policy.AuditLogger
	if auditBridgeImpl != nil {
		policyAuditorLogger = auditBridgeImpl
	}
	nal.al.policyAuditor = policy.NewPolicyAuditor(policyEval, policyAuditorLogger, "")

	// SEC-01/02/03: Select the best-available sandbox backend. This never
	// fails: on unsupported kernels SelectBackend returns a FallbackBackend.
	backend, backendName := sandbox.SelectBackend()
	nal.al.sandboxBackend = backend
	logger.InfoCF("agent", "Sandbox backend selected", map[string]any{"backend": backendName})

	// SEC-25: Initialize the prompt-injection guard. NewPromptGuardFromConfig
	// defaults to "medium" strictness when the field is empty. Construction
	// is cheap and cannot fail, so we always build it — runTurn checks the
	// untrusted-tool allowlist before invoking it, so trusted results are
	// never sanitized even when the guard is non-nil.
	nal.al.promptGuard = security.NewPromptGuardFromConfig(policy.PromptGuardConfig{
		Strictness: string(nal.cfg.Sandbox.PromptInjectionLevel),
	})
	logger.InfoCF("agent", "Prompt guard initialized",
		map[string]any{"strictness": string(nal.al.promptGuard.Strictness())})

	// SEC-24: Build the singleton SSRFChecker from config. When SSRF is enabled,
	// all outbound HTTP tool surfaces receive this checker so allow_internal is
	// honored uniformly. When disabled the checker is nil and callers fall back
	// to their default (proxy-aware) HTTP clients.
	//
	// v0.2 (#155 item 4): cfg.Sandbox.EgressAllowCIDRs is the operator escape
	// hatch for the default-deny outbound posture. Entries here are merged
	// into the SSRFChecker's allow-list alongside the SSRF.AllowInternal list
	// so a single field per concern keeps semantics clear: SSRF allow-list =
	// "this hostname/IP/CIDR is exempt from SSRF blocking". Both fields feed
	// the same checker; the merge is order-stable so an operator who lists
	// "10.0.0.5" in AllowInternal AND "10.0.0.0/8" in EgressAllowCIDRs gets
	// both — the more specific exact-IP entry takes O(1) precedence in
	// CheckIP's lookup map.
	if nal.cfg.Sandbox.SSRF.Enabled {
		merged := make([]string, 0,
			len(nal.cfg.Sandbox.SSRF.AllowInternal)+len(nal.cfg.Sandbox.EgressAllowCIDRs))
		merged = append(merged, nal.cfg.Sandbox.SSRF.AllowInternal...)
		merged = append(merged, nal.cfg.Sandbox.EgressAllowCIDRs...)
		nal.al.ssrfChecker = security.NewSSRFChecker(merged)
		logger.InfoCF("agent", "SSRF protection enabled",
			map[string]any{
				"allow_internal_count":     len(nal.cfg.Sandbox.SSRF.AllowInternal),
				"egress_allow_cidrs_count": len(nal.cfg.Sandbox.EgressAllowCIDRs),
			})
	}

	// SEC-28: Start the loopback SSRF proxy for exec child processes when
	// enabled. On bind failure we log and fall back to degraded mode (child
	// processes run without HTTP_PROXY env vars — LIM-02) rather than
	// failing startup, because exec is a core tool and a proxy bind failure
	// on a shared port should not take the whole agent loop down.
	if nal.cfg.Tools.Exec.EnableProxy {
		// Reuse the singleton SSRF checker (which may be nil when SSRF is disabled).
		proxy := security.NewExecProxy(nal.al.ssrfChecker, nil)
		if err := proxy.Start(); err != nil {
			logger.ErrorCF("agent", "Failed to start exec SSRF proxy; child processes will run without proxy env vars",
				map[string]any{"error": err.Error()})
		} else {
			nal.al.execProxy = proxy
			logger.InfoCF("agent", "Exec SSRF proxy started",
				map[string]any{"addr": proxy.Addr()})
		}
	}
}

// initializeRuntime installs runtime limiters and finishes shared tool wiring.
func (nal *newAgentLoop) initializeRuntime() (*AgentLoop, error) {
	// Initialize cancel abuse detector (shared across all four cancel entry points).
	nal.al.cancelAbuse = newCancelAbuseDetector()

	// Initialize the pre-registration cancel latch table (cancel_prearm.go).
	nal.al.cancelPreArm = newCancelPreArm()

	// SEC-26: Initialize rate limiter registry. The registry always exists
	// so per-agent windows can be created even when no limit is configured.
	nal.al.rateLimiter = security.NewRateLimiterRegistry()
	logger.InfoCF("agent", "Rate limiter initialized",
		map[string]any{
			"max_agent_llm_calls_per_hour":    nal.cfg.Sandbox.RateLimits.MaxAgentLLMCallsPerHour,
			"max_agent_tool_calls_per_minute": nal.cfg.Sandbox.RateLimits.MaxAgentToolCallsPerMinute,
		})

	// Session-scoped tool-approval grant store (consent boundary fix): shared
	// by the gateway's tool-approval REST path and the delegate tool's
	// async/await paths. Always non-nil.
	nal.al.approvalGrants = security.NewApprovalGrantStore()

	// Process-wide AsyncNotifier (async-notifier-spec.md): the reusable
	// "wake the conversation when background work finishes" primitive,
	// extracted from the asyncCallback closure below. Always non-nil.
	nal.al.asyncNotifier = newAsyncNotifier(nal.al)

	// v0.2 #155 item 6: build the shared memory-write rate limiter and
	// propagate it to every agent's tool registry. One limiter is shared
	// across all agents so the per-caller bucket is genuinely global —
	// otherwise a malicious caller could route writes through different
	// agents to dodge the per-caller ceiling. The per-agent bucket is keyed
	// on the agent ID inside the limiter so independence is preserved.
	//
	// Defaults (60 per agent / minute, 600 per caller / minute) are intentional;
	// not configurable via cfg today because no operator has expressed a need
	// to tune them and exposing knobs invites footguns. The constructor accepts
	// a MemoryRateLimitConfig so a future config-backed override can be wired
	// in without a structural change.
	//
	// Stashed on al.memoryRateLimiter (not a bare local) so hot-reload
	// (ReloadProviderAndConfig) can re-apply the SAME limiter instance onto
	// the freshly-built registry via wireMemoryRateLimiterOn — constructing a
	// new limiter on every reload would reset every agent's sliding-window
	// buckets on any unrelated config change.
	nal.al.memoryRateLimiter = tools.NewMemoryRateLimiter(tools.MemoryRateLimitConfig{})
	nal.al.wireMemoryRateLimiterOn(nal.registry, nal.al.memoryRateLimiter)
	logger.InfoCF("agent", "Memory write rate limiter initialized",
		map[string]any{
			"per_agent_per_minute":  nal.al.memoryRateLimiter.PerAgentLimit(),
			"per_caller_per_minute": nal.al.memoryRateLimiter.PerCallerLimit(),
		})

	// Register shared tools to all agents (now that al is created)
	registerSharedTools(nal.al, nal.cfg, nal.msgBus, nal.registry, nal.provider)

	// Replace the exec tool in each agent's registry with a version that has
	// the policy auditor and sandbox backend wired in. Registering the same
	// tool name overwrites the previous entry (see ToolRegistry.Register).
	nal.al.wireExecToolDeps()

	// Fix A (FR-057): wire the environment provider into every agent's
	// ContextBuilder now that the sandbox backend is known. Also register each
	// ContextBuilder into the registry so config-change invalidation (FR-061)
	// can broadcast across all agents.
	nal.al.wireEnvProviders(nal.cfg, nal.registry)

	return nal.al, nil
}
