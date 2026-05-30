// Tier 2 build_static tool per..FR-046b.
//
// What it does:
// - Spawns `npm install` followed by the framework-specific build command
// under the hardened-exec wrapper.
// - Routes HTTP/HTTPS through the egress proxy (host allow-list).
// - Uses a per-agent npm cache directory so concurrent builds
// do not fight over a shared cache.
// - Forces npm's --registry CLI flag to override any project-level
//.npmrc that points at an unallowed registry.
// - Caps wall-clock duration (cfg.Tools.BuildStatic.TimeoutSeconds,
// default 300 s) and address-space (cfg.Tools.BuildStatic.MemoryLimitBytes,
// default 512 MiB).
// - Per-gateway concurrency cap from cfg.Sandbox.MaxConcurrentBuilds
// (default 2). Concurrent invocations beyond the cap fail with a
// structured error.
//
// Threat acknowledgement: the build child has the gateway's full
// filesystem reach. Build scripts MAY read sensitive paths
// including $OMNIPUS_HOME/credentials.json. Operators must rotate the
// master key after running untrusted builds. The env preamble warns the
// agent of this with the exact rotation command (, FR-046b).

package tools

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/sandbox"
)

// supportedFrameworks enumerates the framework values build_static accepts.
// Each maps to the build command appended after `npm install`. The list is
// intentionally small — Tier 2 is for the static-site build use case, not
// arbitrary scripting.
var supportedFrameworks = map[string][]string{
	"next":      {"npm", "run", "build"},
	"vite":      {"npm", "run", "build"},
	"astro":     {"npm", "run", "build"},
	"sveltekit": {"npm", "run", "build"},
}

// defaultBuildTimeoutSeconds applies when cfg.Tools.BuildStatic.TimeoutSeconds
// is zero ( default). The boot validator clamps to [1, 3600], but
// callers that hand-construct a tool may pass zero — fall back here so the
// child never runs unbounded.
const defaultBuildTimeoutSeconds = 300

// defaultBuildMemoryBytes applies when cfg.Tools.BuildStatic.MemoryLimitBytes
// is zero. 512 MiB matches the spec default and the boot-validator floor.
const defaultBuildMemoryBytes uint64 = 512 << 20

// defaultRegistryURL is the default npm registry forced via --registry on
// every npm invocation. Operators can override per-agent via
// the build_static argument schema (registry field, optional).
const defaultRegistryURL = "https://registry.npmjs.org"

// stderrTailBytes is how many bytes of stderr we surface on failure.
// Enough to show a typical npm error stack without flooding the LLM
// context.
const stderrTailBytes = 4096

// BuildStaticConfig captures the runtime config snapshot needed by the tool.
// Built from cfg.Tools.BuildStatic + cfg.Sandbox at registration time;
// hot-reload is NOT supported (sandbox config is boot-time only per
// SEC-12 / FR-J-015).
type BuildStaticConfig struct {
	TimeoutSeconds   int32
	MemoryLimitBytes uint64
	MaxConcurrent    int32
	// EgressAllowList is the operator's allow-list at boot time. Stored
	// for audit emission on denied requests; the proxy itself uses its
	// own compiled copy.
	EgressAllowList []string

	// AuditFailClosed (HIGH-6, silent-failure-hunter) controls behavior
	// when audit-write fails on a Tier 2 invocation. When true (default
	// via ResolveBool(cfg.Sandbox.PathGuardAuditFailClosed, true)) the
	// tool refuses to run without a guaranteed compliance trail —
	// build_static is a TRUSTED-PROMPT FEATURE and the only after-the-fact
	// record of what was built is the audit log. When false, the audit
	// failure is logged at Error and the build proceeds (operator opt-out).
	AuditFailClosed bool
}

// BuildStaticTool is the agent-callable tool registered when Tier 2 is
// enabled. One instance per gateway; the per-gateway concurrency
// semaphore lives on the struct.
type BuildStaticTool struct {
	cfg         BuildStaticConfig
	workspace   string // per-agent workspace root (cwd of every build)
	proxy       *sandbox.EgressProxy
	auditLogger *audit.Logger

	// sem is a buffered channel acting as a counting semaphore. Capacity
	// = cfg.MaxConcurrent. Each Execute acquires a slot before spawning
	// and releases it on return. Cap-hit invocations return immediately
	// with the spec-mandated error wording.
	sem chan struct{}

	// activeMu protects activeStartTimes which is consulted to compute
	// the "previous registration expires at <iso8601>" hint in cap-hit
	// errors. Builds don't have an idle/hard timeout like Tier 3, so the
	// hint is the start time + the configured TimeoutSeconds (i.e. the
	// soonest moment the slot frees naturally).
	activeMu         sync.Mutex
	activeStartTimes []time.Time
}

// NewBuildStaticTool constructs the tool. workspace MUST be the agent's
// absolute workspace path; proxy is the shared EgressProxy started by
// the gateway; auditLogger may be nil (tests).
func NewBuildStaticTool(
	cfg BuildStaticConfig,
	workspace string,
	proxy *sandbox.EgressProxy,
	auditLogger *audit.Logger,
) *BuildStaticTool {
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = defaultBuildTimeoutSeconds
	}
	if cfg.MemoryLimitBytes == 0 {
		cfg.MemoryLimitBytes = defaultBuildMemoryBytes
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 2
	}
	return &BuildStaticTool{
		cfg:         cfg,
		workspace:   workspace,
		proxy:       proxy,
		auditLogger: auditLogger,
		sem:         make(chan struct{}, cfg.MaxConcurrent),
	}
}

// SetAuditLogger satisfies the auditLoggerAware contract used by the
// tool registry.
func (t *BuildStaticTool) SetAuditLogger(logger *audit.Logger) {
	t.auditLogger = logger
}

// Name, Description, Scope, Parameters implement the Tool interface.

func (t *BuildStaticTool) Name() string { return "build_static" }

func (t *BuildStaticTool) Description() string {
	return "Build a static site from the agent's workspace using npm. Tier 2 — TRUSTED PROMPT FEATURE: build scripts run with the gateway's full filesystem access and MAY read sensitive paths including $OMNIPUS_HOME/credentials.json, ~/.git-credentials, ~/.ssh/. After running untrusted builds, rotate the master key with: omnipus credentials rotate --old-key-file <path> --new-key-file <path>. HTTP/HTTPS egress is proxied through an operator-controlled allow-list; raw TCP egress is unblocked. Frameworks: next, vite, astro, sveltekit."
}

func (t *BuildStaticTool) Scope() ToolScope { return ScopeCore }

func (t *BuildStaticTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"framework": map[string]any{
				"type":        "string",
				"description": "Build framework. One of: next, vite, astro, sveltekit.",
				"enum":        []string{"next", "vite", "astro", "sveltekit"},
			},
			"entry": map[string]any{
				"type":        "string",
				"description": "Workspace-relative path to the project root (directory containing package.json). Must stay inside the workspace.",
			},
			"output": map[string]any{
				"type":        "string",
				"description": "Optional workspace-relative output directory. Defaults to the framework's default (e.g. .next/, dist/, build/).",
			},
			"registry": map[string]any{
				"type":        "string",
				"description": "Optional npm registry URL passed via --registry (overrides project-level .npmrc). Defaults to https://registry.npmjs.org. Must be on the egress allow-list.",
			},
		},
		"required": []string{"framework", "entry"},
	}
}

// Execute runs npm install + npm run build under the hardened-exec wrapper.
//
// Validation steps:
// 1. Acquire concurrency slot (non-blocking). Cap-hit returns spec error.
// 2. Validate framework (enum).
// 3. Resolve and validate entry path (must stay inside workspace).
// 4. Resolve registry URL (default if absent).
// 5. Spawn `npm install --registry=<url>` then framework build command.
// 6. Surface success / failure to the LLM.
//
// The slot is held for the duration of BOTH npm install AND the build
// command. This is intentional: build scripts trigger downstream npm
// installs ("postinstall" lifecycle hooks) and we want all of them to
// share one slot.
func (t *BuildStaticTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	// 1. Concurrency cap. Use a non-blocking acquire so the LLM gets a
	// fast actionable error rather than a hidden queue.
	select {
	case t.sem <- struct{}{}:
		// acquired
	default:
		earliest := t.earliestSlotFreeAt()
		return ErrorResult(fmt.Sprintf(
			"too many concurrent builds (%d/%d); previous registration expires at %s",
			len(t.sem), cap(t.sem), earliest.UTC().Format(time.RFC3339),
		))
	}
	t.recordStart()
	defer func() {
		t.recordEnd()
		<-t.sem
	}()

	// 2. Framework.
	framework, _ := args["framework"].(string)
	framework = strings.ToLower(strings.TrimSpace(framework))
	buildCmd, ok := supportedFrameworks[framework]
	if !ok {
		return ErrorResult(fmt.Sprintf(
			"unsupported framework %q; supported: next, vite, astro, sveltekit", framework,
		))
	}

	// 3. Entry path. Resolve relative-to-workspace and reject anything
	// that escapes the workspace via.. or absolute paths outside it.
	entry, _ := args["entry"].(string)
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return ErrorResult("entry is required")
	}
	resolvedEntry, err := resolveWorkspaceRelative(t.workspace, entry)
	if err != nil {
		return ErrorResult(fmt.Sprintf("invalid entry path: %v", err))
	}

	// 4. Registry. Default applies when absent. We do NOT validate the
	// registry against the egress allow-list here (the proxy will
	// reject the request at network time and surface the audit
	// entry); validating twice would duplicate logic and could
	// diverge from the runtime check.
	registry, _ := args["registry"].(string)
	registry = strings.TrimSpace(registry)
	if registry == "" {
		registry = defaultRegistryURL
	}

	// 5. Execute. npm install first, then the build command.
	limits := sandbox.Limits{
		TimeoutSeconds:   t.cfg.TimeoutSeconds,
		MemoryLimitBytes: t.cfg.MemoryLimitBytes,
		WorkspaceDir:     resolvedEntry,
		EgressProxyAddr:  t.proxyAddr(),
	}

	// `npm install --registry=<url>` — registry flag MUST come from
	// the CLI, not project.npmrc,/. We append
	// after subcommand args so npm's parser binds it to install.
	installArgs := []string{"npm", "install", "--registry=" + registry}
	if auditErr := t.auditExec(ctx, "npm install", installArgs); auditErr != nil {
		return auditErr
	}
	installResult, err := sandbox.Run(ctx, installArgs, nil, limits)
	if err != nil {
		return ErrorResult(fmt.Sprintf("build_static: npm install failed to start: %v", err))
	}
	if installResult.ExitCode != 0 || installResult.TimedOut {
		return t.failureResult("npm install", installResult)
	}

	// Build command. We re-use the same registry flag so any postinstall
	// or framework-internal `npm install` triggered during build also
	// uses the allow-listed registry.
	buildArgs := append([]string{}, buildCmd...)
	buildArgs = append(buildArgs, "--registry="+registry)
	if auditErr := t.auditExec(ctx, framework+" build", buildArgs); auditErr != nil {
		return auditErr
	}
	buildResult, err := sandbox.Run(ctx, buildArgs, nil, limits)
	if err != nil {
		return ErrorResult(fmt.Sprintf("build_static: %s build failed to start: %v", framework, err))
	}
	if buildResult.ExitCode != 0 || buildResult.TimedOut {
		return t.failureResult(framework+" build", buildResult)
	}

	// 6. Success. Resolve the output directory if the caller supplied
	// one; otherwise report the framework default.
	output, _ := args["output"].(string)
	output = strings.TrimSpace(output)
	if output == "" {
		output = frameworkDefaultOutput(framework)
	}
	resolvedOutput, _ := resolveWorkspaceRelative(
		t.workspace,
		filepath.Join(strings.TrimPrefix(resolvedEntry, t.workspace+string(filepath.Separator)), output),
	)

	// HIGH-1: surface unsupported memory caps to the agent. Either child
	// can flag this (currently darwin only sets it). The preamble is added
	// to BOTH success and failure results so the agent sees the same
	// limitation regardless of build outcome.
	preamble := buildPreamble(installResult, buildResult)

	return SilentResult(preamble + fmt.Sprintf(
		"build_static: %s build succeeded in %s. Output: %s. Duration: install=%s build=%s.",
		framework,
		resolvedEntry,
		resolvedOutput,
		installResult.Duration.Truncate(time.Second),
		buildResult.Duration.Truncate(time.Second),
	))
}

// buildPreamble assembles operator-visible warnings about platform
// degradation that occurred during the build. Currently only emits a
// memory-limit-unsupported warning (HIGH-1, silent-failure-hunter) for
// darwin builds; future platform-degradation flags should land here too
// so the agent sees them in one place.
func buildPreamble(install, build sandbox.Result) string {
	if !install.MemoryLimitUnsupported && !build.MemoryLimitUnsupported {
		return ""
	}
	return "WARNING: memory limit not enforced on this OS — child runs unbounded. " +
		"Tier 2 memory caps are best-effort outside Linux; the build completed but " +
		"the configured MemoryLimitBytes could not be applied.\n\n"
}

// proxyAddr returns the egress proxy address, or "" when the proxy is nil
// (test wiring). Callers that pass nil accept the trade-off that the child
// has unproxied HTTP/HTTPS access — production builds always wire it.
func (t *BuildStaticTool) proxyAddr() string {
	if t.proxy == nil {
		return ""
	}
	return t.proxy.Addr()
}

// failureResult builds an IsError result with a tail of the child's
// stderr. The LLM sees enough to understand the failure but not so much
// that long npm logs blow the context window.
//
// HIGH-1: when r.MemoryLimitUnsupported is true (darwin), prepends the
// platform-degradation warning so the agent does not blame the failure on
// the missing memory cap when the cap was never going to be applied.
func (t *BuildStaticTool) failureResult(stage string, r sandbox.Result) *ToolResult {
	tail := r.StderrTail(stderrTailBytes)
	preamble := ""
	if r.MemoryLimitUnsupported {
		preamble = "WARNING: memory limit not enforced on this OS — child runs unbounded. " +
			"Tier 2 memory caps are best-effort outside Linux.\n\n"
	}
	if r.TimedOut {
		return ErrorResult(preamble + fmt.Sprintf(
			"build_static: %s timed out after %s. Stderr tail:\n%s",
			stage, r.Duration.Truncate(time.Second), tail,
		))
	}
	return ErrorResult(preamble + fmt.Sprintf(
		"build_static: %s exited with code %d. Stderr tail:\n%s",
		stage, r.ExitCode, tail,
	))
}

// auditExec emits a tool_call audit entry for each child invocation so
// operators can see exactly what npm commands ran.
//
// HIGH-6 (silent-failure-hunter): build_static is a TRUSTED-PROMPT FEATURE
// . The audit log is the ONLY after-the-fact compliance
// trail of what npm command ran with the gateway's full filesystem reach.
// When the audit write fails AND AuditFailClosed is true (default), we
// REFUSE to run the build — the safety contract requires a guaranteed
// trail. When AuditFailClosed is false the operator has explicitly opted
// out and we continue with an Error log.
//
// Returns a non-nil *ToolResult ONLY when the build must abort. Returns nil
// to mean "continue".
func (t *BuildStaticTool) auditExec(ctx context.Context, label string, argv []string) *ToolResult {
	if t.auditLogger == nil {
		// No logger wired (test path). Continue without auditing — if
		// production wiring forgets the logger that is a separate bug
		// caught by integration tests. We do NOT fail closed here because
		// production wiring is responsible for providing the logger.
		return nil
	}
	logErr := t.auditLogger.Log(&audit.Entry{
		Event:    audit.EventExec,
		Decision: audit.DecisionAllow,
		AgentID:  ToolAgentID(ctx),
		Tool:     t.Name(),
		Command:  strings.Join(argv, " "),
		Details: map[string]any{
			"stage":            label,
			"workspace":        t.workspace,
			"timeout_seconds":  t.cfg.TimeoutSeconds,
			"memory_limit":     t.cfg.MemoryLimitBytes,
			"egress_allowlist": t.cfg.EgressAllowList,
		},
	})
	if logErr == nil {
		return nil
	}
	if t.cfg.AuditFailClosed {
		slog.Error("build_static: audit logger degraded; refusing to run trusted-prompt feature",
			"stage", label, "error", logErr, "audit_fail_closed", true)
		return &ToolResult{
			IsError: true,
			ForLLM:  "audit logger degraded; refusing to run trusted-prompt feature without compliance trail",
			ForUser: "Tier 2 requires audit logging; aborting",
		}
	}
	slog.Error("build_static: audit write failed (continuing — audit_fail_closed=false)",
		"stage", label, "error", logErr)
	return nil
}

// recordStart and recordEnd track the timestamps of in-flight builds so
// earliestSlotFreeAt can compute a useful "expires at" hint on cap-hit.
func (t *BuildStaticTool) recordStart() {
	t.activeMu.Lock()
	t.activeStartTimes = append(t.activeStartTimes, time.Now())
	t.activeMu.Unlock()
}

func (t *BuildStaticTool) recordEnd() {
	t.activeMu.Lock()
	if len(t.activeStartTimes) > 0 {
		// Pop the oldest — FIFO is a fine approximation for "the
		// build most likely to be the one that frees up next".
		t.activeStartTimes = t.activeStartTimes[1:]
	}
	t.activeMu.Unlock()
}

// earliestSlotFreeAt returns the earliest moment at which one of the
// in-flight builds will hit its TimeoutSeconds (and therefore release a
// slot). Used purely for the cap-hit error message; not authoritative
// (a build may finish naturally before its timeout).
func (t *BuildStaticTool) earliestSlotFreeAt() time.Time {
	t.activeMu.Lock()
	defer t.activeMu.Unlock()
	var earliest time.Time
	first := true
	for _, start := range t.activeStartTimes {
		expiry := start.Add(time.Duration(t.cfg.TimeoutSeconds) * time.Second)
		if first || expiry.Before(earliest) {
			earliest = expiry
			first = false
		}
	}
	if first {
		// No active builds — defensive default; the caller will
		// almost certainly succeed on the next attempt.
		return time.Now().Add(time.Duration(t.cfg.TimeoutSeconds) * time.Second)
	}
	return earliest
}

// frameworkDefaultOutput returns the conventional output directory for
// each supported framework. Used only for the success-message hint when
// the caller does not supply an explicit output path.
func frameworkDefaultOutput(framework string) string {
	switch framework {
	case "next":
		return ".next"
	case "vite":
		return "dist"
	case "astro":
		return "dist"
	case "sveltekit":
		return ".svelte-kit"
	}
	return ""
}

// resolveWorkspaceRelative joins workspace + relPath, then verifies the
// result stays inside workspace (no.. escape). Absolute relPath is
// rejected. Returns the cleaned absolute path on success.
func resolveWorkspaceRelative(workspace, relPath string) (string, error) {
	if workspace == "" {
		return "", fmt.Errorf("no workspace configured")
	}
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("entry must be workspace-relative; got absolute path %q", relPath)
	}
	abs := filepath.Clean(filepath.Join(workspace, relPath))
	wsClean := filepath.Clean(workspace)
	if abs != wsClean && !strings.HasPrefix(abs, wsClean+string(filepath.Separator)) {
		return "", fmt.Errorf("entry %q escapes workspace", relPath)
	}
	return abs, nil
}

// Compile-time assertions: the BuildStaticConfig surface used here
// matches the relevant config fields. If the config struct shape drifts
// these will fail to compile and force an explicit migration.
var (
	_ int32  = config.BuildStaticConfig{}.TimeoutSeconds
	_ uint64 = config.BuildStaticConfig{}.MemoryLimitBytes
)
