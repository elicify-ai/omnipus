// config_defaults_apply.go: Effective-value accessors, defaults and normalisation for loaded config sections (performance clamp, schedules, search-tool API keys, browser/delegate/judge caps)

package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// physicalConcurrencySafetyCeiling is the PHYSICAL OS-thread safety
// backstop. It is what EffectiveMaxParallelAgents returns when nothing is
// configured — paired with capped=false, because it is a bound on what the
// Go runtime survives, NOT an estimate of what this machine can run. It
// never clamps an explicit operator value (clampParallelExplicit honors any
// explicit value, warning loudly instead of silently capping it).
//
// Each concurrent agent turn/sub-turn can end up blocked on a synchronous
// syscall (file fsync, cgo, blocking DNS resolution) that parks its
// goroutine's OS thread rather than Go's netpoller: prior measurement found
// ~1000 concurrent fsyncing goroutines consumed ~999 OS threads. Go's
// runtime hard-aborts the entire process — not a graceful degradation —
// once it exceeds 10,000 OS threads ("runtime: program exceeds 10000-thread
// limit, fatal error: thread exhaustion"). This value leaves a 5x margin
// below that fatal threshold for every OTHER thread-consuming subsystem
// already running in the process (channels, browser tooling, the HTTP
// server, GC, etc.).
//
// Reaching it in practice would require the live memory gate
// (MemoryPressureHigh) to admit two thousand concurrent agents on one host,
// which is the scenario it exists to make impossible. The backstop is the
// answer to "what if the gate is wrong", not the operating point.
const physicalConcurrencySafetyCeiling = 2000

// EffectiveMaxParallelAgents returns the environment-override-aware value for
// MaxParallelAgents, together with whether that value is a real CAP an
// operator asked for.
//
// It applies, in priority order:
//
//  1. An env-var override (OMNIPUS_MAX_PARALLEL_AGENTS) if set and valid —
//     returns (v, true).
//  2. The configured value (p.MaxParallelAgents), if non-zero — an EXPLICIT
//     operator choice, honored as given (clampParallelExplicit floors it at 1
//     but never lowers a large explicit value) — returns (v, true).
//  3. Neither set — returns (physicalConcurrencySafetyCeiling, false).
//
// THE SECOND RETURN VALUE IS THE POINT (FR-067). capped=false means "nobody
// capped this; the number you are holding is a physical backstop, not a
// capacity estimate". Callers that display, log or reason about the value
// MUST branch on it: rendering the unset case as a bare integer tells an
// operator the system recommends 2000 concurrent agents, which is not a
// claim anything in this process is making.
//
// The shape is deliberately (int, bool) and never a bare 0 sentinel. A 0
// returned into newTaskExecutor's semaphore capacity would deadlock every
// dispatch in the process, and pkg/gateway's PUT /api/v1/performance
// regression tests exist precisely to catch a re-introduction of that.
//
// Steps 1 and 2 both take precedence over step 3 unconditionally: this
// function must never let the backstop override an operator's explicit
// choice, by config, env, or (via PUT /api/v1/performance) the UI.
func (p PerformanceConfig) EffectiveMaxParallelAgents() (int, bool) {
	// Env-var override has highest priority.
	if s := os.Getenv("OMNIPUS_MAX_PARALLEL_AGENTS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v >= 1 {
			return clampParallelExplicit(v), true
		}
	}
	if p.MaxParallelAgents > 0 {
		// Explicit user-set value: honored as configured (see
		// clampParallelExplicit's own doc comment for why there is no
		// ceiling here).
		return clampParallelExplicit(p.MaxParallelAgents), true
	}
	return physicalConcurrencySafetyCeiling, false
}

// clampParallelExplicit enforces only a floor (1, so an operator who
// deliberately sets max_parallel_agents=1 gets single-flight behavior) on an
// EXPLICITLY configured value (config.json or OMNIPUS_MAX_PARALLEL_AGENTS).
//
// There is deliberately NO ceiling here. Silently lowering an operator's
// explicit choice is the exact ADR-037 "silent clamping" anti-pattern this
// project bans (CLAUDE.md). When a configured value exceeds
// physicalConcurrencySafetyCeiling, it is still honored in full — but a loud
// WARN is logged, because Go's runtime hard-aborts the entire process (not a
// graceful degradation) once it exceeds 10,000 OS threads, and concurrent
// agent turns can each pin an OS thread on a blocking syscall (see
// physicalConcurrencySafetyCeiling's doc comment for the measured basis).
// The operator who set this value explicitly is assumed to have made that
// trade-off knowingly; the warning exists so a value that was a fat-fingered
// typo (e.g. an extra zero) is loudly diagnosable rather than a silent
// eventual crash.
func clampParallelExplicit(v int) int {
	const minPar = 1
	if v < minPar {
		return minPar
	}
	if v > physicalConcurrencySafetyCeiling {
		if shouldLogExplicitCeilingWarn(time.Now()) {
			logger.WarnCF("config",
				"performance.max_parallel_agents is configured far above the physical OS-thread-safety ceiling — honoring it as configured (explicit values are never silently clamped), but this risks Go runtime thread exhaustion (a fatal process abort) under real concurrent load",
				map[string]any{
					"configured_value": v,
					"safety_ceiling":   physicalConcurrencySafetyCeiling,
				})
		}
	}
	return v
}

// explicitCeilingWarnInterval bounds how often clampParallelExplicit's
// above-physical-ceiling WARN is logged (code review 2026-08-04, MINOR:
// config.go:487-493 was un-throttled). EffectiveMaxParallelAgents is invoked
// on every new-session admission check, every dispatch capacity sync, and
// every GET /api/v1/performance (see EffectiveMaxParallelAgents' own doc
// comment) — an un-throttled WARN on that live-resolved path can flood
// gateway.log under real traffic even though the underlying condition (an
// operator's configured value exceeding physicalConcurrencySafetyCeiling) is
// static for the life of the process. A bounded interval keeps the warning
// loud enough to be diagnosable (CLAUDE.md/ADR-037: never silently swallow
// it) without the volume, rather than moving it to a boot-time-only
// diagnostic — EffectiveMaxParallelAgents is deliberately never cached, and
// a boot-time-only warn would miss a value changed later via PUT
// /api/v1/performance while the gateway is running.
const explicitCeilingWarnInterval = 5 * time.Minute

// lastExplicitCeilingWarnNano stores the UnixNano timestamp of the last
// above-ceiling warning, 0 meaning "never logged yet". Package-level so the
// throttle is shared across every call site of clampParallelExplicit.
var lastExplicitCeilingWarnNano atomic.Int64

// shouldLogExplicitCeilingWarn reports whether at least
// explicitCeilingWarnInterval has elapsed since the last above-ceiling
// warning and, if so, atomically claims the slot so concurrent callers (this
// path is hit from concurrent session-admission checks) don't all log at
// once. Split out from clampParallelExplicit so the throttle logic can be
// tested deterministically (fake clock) without depending on capturing real
// logger output.
func shouldLogExplicitCeilingWarn(now time.Time) bool {
	nowNano := now.UnixNano()
	for {
		last := lastExplicitCeilingWarnNano.Load()
		if last != 0 && now.Sub(time.Unix(0, last)) < explicitCeilingWarnInterval {
			return false
		}
		if lastExplicitCeilingWarnNano.CompareAndSwap(last, nowNano) {
			return true
		}
	}
}

// DefaultSessionStatsFlushInterval is the FR-067 default: unforced periodic
// flush of a dirty session's stats.json fires every 5 seconds. Seeded
// explicitly in DefaultConfig (defaults.go) so a fresh install's config.json
// is self-documenting; EffectiveStatsFlushInterval re-applies it for any
// zeroed-out value.
const DefaultSessionStatsFlushInterval = 5 * time.Second

// EffectiveStatsFlushInterval resolves the FR-067 stats-flush throttle
// period: the configured value when positive, else the 5s default. A test
// MUST be able to assert the key exists, defaults to 5s, and that a
// non-default value is honoured end to end (#105, SC-048).
func (c SessionConfig) EffectiveStatsFlushInterval() time.Duration {
	if c.StatsFlushInterval > 0 {
		return time.Duration(c.StatsFlushInterval)
	}
	return DefaultSessionStatsFlushInterval
}

// Schedules config defaults (#264).
const (
	// DefaultSchedulesMaxConcurrentRuns is the fallback parallel-lane capacity.
	DefaultSchedulesMaxConcurrentRuns = 8
	// DefaultSchedulesRunTimeoutSeconds is the fallback per-run deadline.
	DefaultSchedulesRunTimeoutSeconds = 300
)

// DefaultSchedulesRetryBackoffMs is the fallback transient-error retry backoff
// schedule (FR-010): 1m, 2m, 5m offsets keyed by retry attempt.
var DefaultSchedulesRetryBackoffMs = []int64{60000, 120000, 300000}

// ApplyDefaults fills any unset/invalid field with its documented default
// (FR-003/FR-007). Idempotent. Bounds-checked: non-positive values reset to
// the default rather than being honored.
func (s *SchedulesConfig) ApplyDefaults() {
	if s.MaxConcurrentRuns <= 0 {
		s.MaxConcurrentRuns = DefaultSchedulesMaxConcurrentRuns
	}
	if s.RunTimeoutSeconds <= 0 {
		s.RunTimeoutSeconds = DefaultSchedulesRunTimeoutSeconds
	}
	if len(s.RetryBackoffMs) == 0 {
		s.RetryBackoffMs = append([]int64(nil), DefaultSchedulesRetryBackoffMs...)
	}
}

// DefaultStreamStallTimeoutSeconds is the shipped per-model default for
// ModelConfig.StreamStallTimeout: five minutes of total streaming silence
// before a provider call is aborted as a stall.
const DefaultStreamStallTimeoutSeconds = 300

// EffectiveStreamStallTimeout resolves the streaming silence limit for this
// model row: the row's own StreamStallTimeout when >= 1 second, otherwise the
// shipped default. Follows the Effective* resolver convention (see
// planning.go); safe on a zero-value ModelConfig.
func (c *ModelConfig) EffectiveStreamStallTimeout() time.Duration {
	if c != nil && c.StreamStallTimeout >= 1 {
		return time.Duration(c.StreamStallTimeout) * time.Second
	}
	return time.Duration(DefaultStreamStallTimeoutSeconds) * time.Second
}

// APIKey returns the resolved Brave API key from the process environment.
func (c *BraveConfig) APIKey() string {
	if c.APIKeyRef == "" {
		return ""
	}
	return os.Getenv(c.APIKeyRef)
}

// APIKey returns the resolved Tavily API key from the process environment.
func (c *TavilyConfig) APIKey() string {
	if c.APIKeyRef == "" {
		return ""
	}
	return os.Getenv(c.APIKeyRef)
}

// APIKey returns the resolved Perplexity API key from the process environment.
func (c *PerplexityConfig) APIKey() string {
	if c.APIKeyRef == "" {
		return ""
	}
	return os.Getenv(c.APIKeyRef)
}

// APIKey returns the resolved GLM API key from the process environment.
func (c *GLMSearchConfig) APIKey() string {
	if c.APIKeyRef == "" {
		return ""
	}
	return os.Getenv(c.APIKeyRef)
}

// APIKey returns the resolved Baidu API key from the process environment.
func (c *BaiduSearchConfig) APIKey() string {
	if c.APIKeyRef == "" {
		return ""
	}
	return os.Getenv(c.APIKeyRef)
}

// EffectiveRequireParentAgentID resolves tools.delegate.require_parent_agent_id
// (R2-MAJ-015). An unset key resolves to TRUE — the fail-closed posture is the
// default, and an operator must opt OUT of it explicitly.
func (d DelegateToolConfig) EffectiveRequireParentAgentID() bool {
	return ResolveBool(d.RequireParentAgentID, true)
}

// defaultControlIdleReleaseSec is FR-031a's shipped default: 900 seconds
// (15 minutes) of no proof of life before a held wheel is released back to
// the agent. Applied whenever ControlIdleReleaseSec is unset (its Go zero
// value) — see the field's own doc comment for why this differs from the
// spec's literal "0 disables" wording.
const defaultControlIdleReleaseSec = 900

// EffectiveControlIdleReleaseSec resolves tools.browser.control_idle_release
// into the seconds value FR-031a's idle sweeper should actually use:
//
//   - unset (0)  -> defaultControlIdleReleaseSec (900)
//   - negative   -> 0, the "disable expiry" sentinel this accessor defines
//     (see ControlIdleReleaseSec's doc comment for why 0 itself cannot serve
//     that role)
//   - positive   -> the configured value, unchanged (no ceiling — an
//     operator who wants a longer hold window is not clamped)
//
// The caller (pkg/agent/loop.go's registerSharedTools, wave B123) treats a
// returned 0 as "disabled" and anything > 0 as the window in seconds — the
// same >0-means-active convention IdleTTLSec's own reader already uses.
func (c BrowserToolConfig) EffectiveControlIdleReleaseSec() int {
	switch {
	case c.ControlIdleReleaseSec == 0:
		return defaultControlIdleReleaseSec
	case c.ControlIdleReleaseSec < 0:
		return 0
	default:
		return c.ControlIdleReleaseSec
	}
}

// EffectiveIdleCloseTTL resolves tools.browser.idle_close_ttl into the duration
// pkg/tools/browser expects. Zero means "unset" — the browser package applies
// its own default — and a negative value resolves the same way, because idle
// close has no off switch (FR-061). It is the ONE place the seconds→duration
// conversion happens, so the boot path and the reload path cannot drift.
func (c BrowserToolConfig) EffectiveIdleCloseTTL() time.Duration {
	if c.IdleCloseTTLSec <= 0 {
		return 0
	}
	return time.Duration(c.IdleCloseTTLSec) * time.Second
}

// EffectiveCacheTrimInterval resolves tools.browser.cache_trim_interval the
// same way: zero or negative means "unset", and the browser package's own
// default (1 hour) stands.
func (c BrowserToolConfig) EffectiveCacheTrimInterval() time.Duration {
	if c.CacheTrimIntervalSec <= 0 {
		return 0
	}
	return time.Duration(c.CacheTrimIntervalSec) * time.Second
}

// IsFilterSensitiveDataEnabled returns true if sensitive data filtering is enabled
func (c *ToolsConfig) IsFilterSensitiveDataEnabled() bool {
	return c.FilterSensitiveData
}

// GetFilterMinLength returns the minimum content length for filtering (default: 8)
func (c *ToolsConfig) GetFilterMinLength() int {
	if c.FilterMinLength <= 0 {
		return 8
	}
	return c.FilterMinLength
}

// IsPreviewEnabled resolves the effective value of gateway.preview_enabled.
// Read live on every call (ADR-044, FR-006) — not restart-gated. Receiver is
// *Config (not *GatewayConfig) per the shared cross-agent contract for this
// feature — callers use cfg.IsPreviewEnabled() directly.
//
// Nil-receiver contract (fail-closed, TDA-1): a nil *Config means there is no
// config to consult at all — e.g. a wiring bug, or a caller invoked before
// config is loaded — and this returns FALSE. Preview serves agent-workspace
// files and proxies loopback dev servers over the gateway's main listener;
// when we cannot even determine whether the feature is enabled, the safe
// default is to never serve it.
//
// This is deliberately DIFFERENT from the field-level default: once a real,
// non-nil *Config exists, an unset gateway.preview_enabled field still
// resolves to true via ResolveBool — the feature is on by default for a
// normal install. Only the "no config at all" case fails closed; "config
// exists but doesn't mention preview_enabled" does not.
//
// Existing call sites written as `cfg == nil || !cfg.IsPreviewEnabled()` are
// now redundant-but-harmless belt-and-suspenders — the method itself already
// returns false for a nil cfg — and do not need to change.
func (c *Config) IsPreviewEnabled() bool {
	if c == nil {
		return false
	}
	return ResolveBool(c.Gateway.PreviewEnabled, true)
}

// ApplyWarmupTimeoutDefault ensures the web_serve dev-mode warmup timeout
// (stored as tools.run_in_workspace.warmup_timeout_seconds in config.json)
// has the default value of 60 when unset. Called by the boot validator.
func (t *ToolsConfig) ApplyWarmupTimeoutDefault() {
	if t.RunInWorkspace.WarmupTimeoutSeconds <= 0 {
		t.RunInWorkspace.WarmupTimeoutSeconds = 60
	}
}

func MergeAPIKeys(apiKey string, apiKeys []string) []string {
	seen := make(map[string]struct{})
	var all []string

	if k := strings.TrimSpace(apiKey); k != "" {
		if _, exists := seen[k]; !exists {
			seen[k] = struct{}{}
			all = append(all, k)
		}
	}

	for _, k := range apiKeys {
		if trimmed := strings.TrimSpace(k); trimmed != "" {
			if _, exists := seen[trimmed]; !exists {
				seen[trimmed] = struct{}{}
				all = append(all, trimmed)
			}
		}
	}

	return all
}

// EffectiveTimeoutSec resolves JudgeConfig.TimeoutSec into the seconds value
// FR-049 requires: unset (<=0) becomes the 420 s default, and anything above
// the 900 s hard ceiling is clamped down to it. This is the FR-049 clamp
// only; FR-050's additional clamp against the goal/plan judge-round bound is
// applied to the stored field at config load (applyJudgeTimeoutRoundClamp),
// not here, because that clamp needs to WARN exactly once per load rather
// than on every read.
func (c JudgeConfig) EffectiveTimeoutSec() int {
	v := c.TimeoutSec
	if v <= 0 {
		v = DefaultJudgeTimeoutSeconds
	}
	if v > JudgeTimeoutHardCeilingSeconds {
		v = JudgeTimeoutHardCeilingSeconds
	}
	return v
}

// EffectiveToolCallCap resolves JudgeConfig.ToolCallCap per FR-051: unset
// (<=0) becomes the 25-call default, and anything above the 60-call hard
// ceiling is clamped down to it.
func (c JudgeConfig) EffectiveToolCallCap() int {
	v := c.ToolCallCap
	if v <= 0 {
		v = DefaultJudgeToolCallCap
	}
	if v > JudgeToolCallCapCeiling {
		v = JudgeToolCallCapCeiling
	}
	return v
}

// EffectiveByteCapBytes resolves JudgeConfig.ByteCapBytes per FR-051: unset
// (<=0) becomes the 2 MiB default, and anything above the 8 MiB hard
// ceiling is clamped down to it.
func (c JudgeConfig) EffectiveByteCapBytes() int64 {
	v := c.ByteCapBytes
	if v <= 0 {
		v = DefaultJudgeByteCapBytes
	}
	if v > JudgeByteCapCeilingBytes {
		v = JudgeByteCapCeilingBytes
	}
	return v
}

// EffectiveTokenCeiling resolves JudgeConfig.TokenCeiling per FR-081: unset
// (<=0) becomes the 120,000-token default. No hard ceiling is applied to an
// operator-configured value.
func (c JudgeConfig) EffectiveTokenCeiling() int {
	if c.TokenCeiling <= 0 {
		return DefaultJudgeTokenCeiling
	}
	return c.TokenCeiling
}

// Shipped defaults and hard ceilings for JudgeConfig's four fields
// (JUDGE-FR-049, FR-050, FR-051, FR-081) are declared in
// pkg/config/defaults.go (wave E1: DefaultJudgeTimeoutSeconds,
// JudgeTimeoutHardCeilingSeconds, DefaultJudgeToolCallCap,
// JudgeToolCallCapCeiling, DefaultJudgeByteCapBytes,
// JudgeByteCapCeilingBytes, DefaultJudgeTokenCeiling) — "exported now, ahead
// of their consumer" per that file's own doc comment, specifically so this
// wave (E2) would consume them rather than mint a second set. Only the
// WARN-threshold fraction and the FR-050 round-timeout mirror are new here;
// they have no defaults.go counterpart because neither is a JudgeConfig
// field's default value.
const (
	// judgeTokenCeilingWarnNumerator/Denominator is FR-081's "WARN at 75%"
	// threshold, expressed as an integer fraction so the comparison in
	// pkg/agent/verifier_budget.go never touches floating point.
	judgeTokenCeilingWarnNumerator   = 75
	judgeTokenCeilingWarnDenominator = 100

	// judgeTimeoutRoundCeilingSec mirrors pkg/agent/goal_loop.go's
	// goalJudgeRoundTimeout (itself pkg/agent/plan_engine.go's
	// planJudgeRoundTimeout, 10 minutes) — the per-round bound a verifier
	// turn's own timeout must never exceed (FR-050: "a per-turn bound above
	// the per-round bound can never fire"). Declared here rather than
	// imported because pkg/agent already imports pkg/config; importing the
	// reverse would be a cycle. This mirrors the existing precedent at
	// pkg/agent/goal_loop.go's own goalJudgeRoundTimeout doc comment ("Mirrors
	// plan_engine.go's planJudgeRoundTimeout exactly, same 10-minute…") —
	// two independent constants kept in step by convention, not by the
	// compiler. If either mirrored value ever changes, this one must change
	// with it; TestJudgeTimeout_RoundClampMirrorsGoalJudgeRoundTimeout pins
	// the number so a drift is caught here rather than as a
	// timeout-that-never-clamps in production.
	judgeTimeoutRoundCeilingSec = 600
)

// judgeTokenCeilingWarnThreshold returns the token count at which FR-081's
// 75%-of-ceiling WARN fires, for the given effective ceiling.
func (c JudgeConfig) judgeTokenCeilingWarnThreshold() int64 {
	return int64(c.EffectiveTokenCeiling()) * judgeTokenCeilingWarnNumerator / judgeTokenCeilingWarnDenominator
}

// JudgeTokenCeilingWarnThreshold is judgeTokenCeilingWarnThreshold exported
// for pkg/agent/verifier_budget.go (a different package), which needs FR-081's
// 75% mark to log its one-time WARN without duplicating the arithmetic.
func (c JudgeConfig) JudgeTokenCeilingWarnThreshold() int64 {
	return c.judgeTokenCeilingWarnThreshold()
}

// applyJudgeTimeoutRoundClamp is FR-050: "Config load MUST reject (or clamp
// with a WARN) a judge timeout greater than goalJudgeRoundTimeout, because a
// per-turn bound above the per-round bound can never fire." This project's
// established convention (ClampLeaseWait, lease_wait_clamp.go — the sibling
// clamp this function is modeled on, same file family) is clamp + loud WARN
// over reject, so that is what this does: mutate the STORED field down to
// judgeTimeoutRoundCeilingSec when the effective (already FR-049-clamped)
// value exceeds it, and log why. Called once per loadConfigInternal call,
// after every other field default/clamp has run, so the WARN's "configured"
// number is the operator's actual input rather than an intermediate
// default.
//
// Logs via raw slog.Warn, matching ClampLeaseWait's own choice in this same
// file family — NOT pkg/logger (zerolog-backed, a different sink), so a test
// harness that intercepts slog.Default() (this package's own
// captureWarnings, node_memory_warn_test.go) observes this WARN the same way
// it already observes ClampLeaseWait's.
func applyJudgeTimeoutRoundClamp(cfg *Config) {
	if cfg == nil {
		return
	}
	effective := cfg.Judge.EffectiveTimeoutSec()
	if effective <= judgeTimeoutRoundCeilingSec {
		return
	}
	slog.Warn("config.judge.timeout_sec is configured above the goal/plan judge-round timeout it runs inside and has been lowered — a per-turn bound above the per-round bound can never fire (JUDGE-FR-050)",
		"configured_timeout_sec", cfg.Judge.TimeoutSec,
		"effective_timeout_sec", effective,
		"round_ceiling_sec", judgeTimeoutRoundCeilingSec,
		"applied_timeout_sec", judgeTimeoutRoundCeilingSec,
	)
	cfg.Judge.TimeoutSec = judgeTimeoutRoundCeilingSec
}
