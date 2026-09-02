// Package config defines the Omnipus on-disk configuration schema — the
// Config struct persisted as ~/.omnipus/config.json — and the load/save
// path that reads, self-heals, and atomically writes it. Config covers
// agents, channel instances/bindings (Channels map[string]
// ChannelInstanceConfig), model providers, gateway/session/tooling/schedule
// settings, sandbox policy, and more. LoadConfig/LoadConfigWithStore load
// and self-heal a config on read (see the *_migration.go files in this
// package for the individual one-shot migrations); SaveConfig and
// safeUpdateConfigJSON (pkg/gateway) are the only sanctioned write paths —
// several sections (notably Providers/model_list) carry credential
// references that a naive re-serialization can corrupt.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	mathrand "math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/caarlos0/env/v11"
	"golang.org/x/crypto/bcrypt"

	"github.com/elicify-ai/omnipus/pkg"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// rrCounter is a global counter for round-robin load balancing across models.
var rrCounter atomic.Uint64

// FlexibleStringSlice is a []string that also accepts JSON numbers,
// so allow_from can contain both "123" and 123.
// It also supports parsing comma-separated strings from environment variables,
// including both English (,) and Chinese (，) commas.
type FlexibleStringSlice []string

func (f *FlexibleStringSlice) UnmarshalJSON(data []byte) error {
	// Accept a single JSON string for convenience, e.g.:
	// "text": "Thinking..."
	var singleString string
	if err := json.Unmarshal(data, &singleString); err == nil {
		*f = FlexibleStringSlice{singleString}
		return nil
	}

	// Accept a single JSON number too, to keep symmetry with mixed allow_from
	// payloads that may contain numeric identifiers.
	var singleNumber float64
	if err := json.Unmarshal(data, &singleNumber); err == nil {
		*f = FlexibleStringSlice{fmt.Sprintf("%.0f", singleNumber)}
		return nil
	}

	// Try []string first
	var ss []string
	if err := json.Unmarshal(data, &ss); err == nil {
		*f = ss
		return nil
	}

	// Try []interface{} to handle mixed types
	var raw []any
	if err := json.Unmarshal(data, &raw); err != nil {
		var s string
		// fail over to compatible to old format string
		if err = json.Unmarshal(data, &s); err != nil {
			return err
		}
		*f = []string{s}
		return nil
	}

	result := make([]string, 0, len(raw))
	for _, v := range raw {
		switch val := v.(type) {
		case string:
			result = append(result, val)
		case float64:
			result = append(result, fmt.Sprintf("%.0f", val))
		default:
			result = append(result, fmt.Sprintf("%v", val))
		}
	}
	*f = result
	return nil
}

// UnmarshalText implements encoding.TextUnmarshaler to support env variable parsing.
// It handles comma-separated values with both English (,) and Chinese (，) commas.
func (f *FlexibleStringSlice) UnmarshalText(text []byte) error {
	if len(text) == 0 {
		*f = nil
		return nil
	}

	s := string(text)
	// Replace Chinese comma with English comma, then split
	s = strings.ReplaceAll(s, "，", ",")
	parts := strings.Split(s, ",")

	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	*f = result
	return nil
}

// CurrentVersion is the latest config schema version
const CurrentVersion = 1

// Config is the current config structure with version support
type Config struct {
	Version   int                              `json:"version"             yaml:"-"` // schema version for migration
	Agents    AgentsConfig                     `json:"agents"              yaml:"-"`
	Bindings  []AgentBinding                   `json:"bindings,omitempty"  yaml:"-"`
	Session   SessionConfig                    `json:"session,omitempty"   yaml:"-"`
	Channels  map[string]ChannelInstanceConfig `json:"channels"            yaml:"channels"`
	Providers []*ModelConfig                   `json:"providers"           yaml:"providers"` // Configured providers with credentials
	Gateway   GatewayConfig                    `json:"gateway"             yaml:"-"`
	Hooks     HooksConfig                      `json:"hooks,omitempty"     yaml:"-"`
	Tools     ToolsConfig                      `json:"tools"               yaml:",inline"`
	Schedules SchedulesConfig                  `json:"schedules,omitempty" yaml:"-"`
	Devices   DevicesConfig                    `json:"devices"             yaml:"-"`
	Voice     VoiceConfig                      `json:"voice"               yaml:"-"`
	// Mailboxes holds email mailbox accounts (M11), keyed agent ID → workspace
	// ID → mailbox: every (agent, workspace) pair may hold its own mailbox — an
	// agent plays different roles in different workspaces and can have a
	// distinct inbox in each. Email is a TOOL surface (not a conversational
	// channel); the tools resolve the active workspace from the turn context at
	// execution time. The password is stored in the encrypted credential store
	// via PasswordRef — never inline here. MailboxesConfig.UnmarshalJSON
	// migrates the legacy flat agent-keyed shape on load.
	Mailboxes MailboxesConfig `json:"mailboxes,omitempty" yaml:"-"`
	// BuildInfo contains build-time version information
	BuildInfo BuildInfo `json:"build_info,omitempty" yaml:"-"`

	// Performance controls the max-parallel fan-out gate for task/subagent dispatch.
	Performance PerformanceConfig `json:"performance,omitempty" yaml:"-"`

	// Omnipus-specific sections (additive, does not break Omnipus compatibility).
	Storage         OmnipusStorageConfig            `json:"storage,omitempty"          yaml:"-"`
	ChannelPolicies map[string]OmnipusChannelPolicy `json:"channel_policies,omitempty" yaml:"-"`
	// Sandbox holds Wave 2 kernel sandboxing settings (SEC-01–SEC-20).
	// Empty/disabled in Wave 1; parsed now so forward-compatible configs load cleanly.
	Sandbox OmnipusSandboxConfig `json:"sandbox,omitempty" yaml:"-"`

	// Planning holds the Planning & Goals epic's global loop bounds — Plan
	// judge rounds, /goal, /loop, per-task attempt ceilings, the global
	// active-loop admission cap, and the machine-check timeout (ADR-049 D7,
	// spec Part A §G). Populated by DefaultConfig, range-validated at boot
	// (validateBootConfig, mirrors OmnipusSandboxConfig/PortRange.Validate).
	// Per-entity overrides (plan.PlanBounds, task.Task.MaxAttempts) take
	// precedence over these global values. No token/money fields (NFR-1).
	Planning PlanningConfig `json:"planning,omitempty" yaml:"-"`

	// SessionMessaging holds the ADR-053 §8 session-control-plane operability
	// config (FR-195's 21 keys): the global kill switch (enabled), the wake
	// kill switch (wake_enabled), and the caps/tunables the S2/S3 transport,
	// durable inbox, and bounded typed wake consult. Live-reload: the consumer
	// and tools read Enabled/WakeEnabled per event via the Effective* methods
	// (never a boot snapshot), so flipping session_messaging.enabled in
	// config.json takes effect without a restart (FR-196, SC-015).
	SessionMessaging SessionMessagingConfig `json:"session_messaging,omitempty" yaml:"-"`

	// Context holds the ADR-066 context-budget controls (D4 caps, D6 trigger,
	// D10 ingest bound, D2 window overrides) — the ONE place these values
	// live (FR-010). Served and edited via GET/PUT /api/v1/settings/context
	// (FR-036); consumers read it per call through the live config, never
	// from a boot snapshot. See context_settings.go.
	Context ContextSettings `json:"context" yaml:"-"`

	// UnknownFields preserves JSON keys not recognized by this version of Omnipus.
	// They are re-emitted verbatim during SaveConfig for round-trip safety (FR-004).
	// Never serialized by json.Marshal or yaml.Marshal — only written back by MarshalJSON.
	UnknownFields map[string]json.RawMessage `json:"-" yaml:"-"`

	// SkippedAgentIDs holds the IDs of agent entity records that exist on
	// disk (entities/agents/<id>.json, ADR-054 D2) but failed to load —
	// unparseable JSON, I/O error, etc. — during the most recent boot or
	// reload. Populated by the agent registry after it loads the roster from
	// the per-entity store (pkg/agentstore/pkg/agent), never by pkg/config
	// itself: config loading has no knowledge of entities/, a sibling
	// on-disk tree that is not part of config.json. Transient (json:"-",
	// never persisted) — mirrors UnknownFields' non-serialization contract.
	//
	// Consumed by RouteResolver (pkg/routing/route.go) to distinguish "this
	// binding names an ID that never existed" (WARN + fall back to the
	// default agent — a config error, not a security concern) from "this
	// binding names an ID whose record exists but failed to load" (ADR-054
	// D7/§9: fail CLOSED — Drop the route — because re-routing an
	// operator-configured, deliberately-restrictive binding target to the
	// default agent on load failure is a privilege change, not availability
	// graceful degradation). Empty/nil is always safe: it simply disables
	// the fail-closed branch, identical to pre-ADR-054 behavior.
	SkippedAgentIDs []string `json:"-" yaml:"-"`

	// cache for sensitive values and compiled regex (computed once)
	sensitiveCache *SensitiveDataCache

	// sensitiveMu guards registeredSensitive and sensitiveCache to prevent data
	// races between the agent-loop log scrubber (reads) and config reloads (writes).
	sensitiveMu sync.RWMutex

	// registeredSensitive holds plaintext secrets registered at runtime via
	// RegisterSensitiveValues (e.g., resolved credential store values). These
	// supplement the reflection-walked SecureString fields so that *Ref-based
	// credentials are also scrubbed from LLM output and audit logs.
	registeredSensitive []string
}

// OmnipusStorageConfig holds storage-related settings per Appendix E §E.5.4.
type OmnipusStorageConfig struct {
	Retention OmnipusRetentionConfig `json:"retention,omitempty"`
}

// OmnipusRetentionConfig controls session transcript retention per Appendix E §E.5.4.
type OmnipusRetentionConfig struct {
	// SessionDays is how many days transcript partitions are kept. 0 = use default (90 days).
	SessionDays int `json:"session_days,omitempty"`
	// Disabled means transcripts are kept forever (no retention enforcement).
	// This is orthogonal to SessionDays: setting Disabled=true suppresses deletion
	// regardless of what SessionDays says.
	Disabled bool `json:"disabled,omitempty"`
	// ArchiveBeforeDelete compresses old partitions to .jsonl.gz before deletion.
	ArchiveBeforeDelete bool `json:"archive_before_delete,omitempty"`
	// MemoryRetrosDays is how many days of retrospective files to keep per
	// agent. 0 = use default (30 days). Used by MemoryStore.SweepRetros.
	// Spec v7 FR-034.
	MemoryRetrosDays int `json:"memory_retros_days,omitempty"`
}

// RetentionSessionDays returns the configured session retention days, defaulting to 90.
func (r OmnipusRetentionConfig) RetentionSessionDays() int {
	if r.SessionDays <= 0 {
		return 90
	}
	return r.SessionDays
}

// IsDisabled reports whether retention enforcement is entirely suppressed (keep forever).
func (r OmnipusRetentionConfig) IsDisabled() bool { return r.Disabled }

// RetentionMemoryRetrosDays returns the configured retro retention, defaulting
// to 180. Retrospecives outlive their transcripts (session default is 90 days)
// so reflections remain queryable long after the raw transcript is swept.
// Spec v7 FR-034 — used by MemoryStore.SweepRetros and the recall search
// window for retrospectives.
func (r OmnipusRetentionConfig) RetentionMemoryRetrosDays() int {
	if r.MemoryRetrosDays <= 0 {
		return 180
	}
	return r.MemoryRetrosDays
}

// RetentionMode summarizes the (session_days, disabled) pair into one of
// three operator-facing states. Use Mode() on OmnipusRetentionConfig to
// derive it; the underlying struct fields remain the authoritative
// storage shape for backward compatibility (see
// TestRetention_ZeroSessionDaysStillMeansDefault90).
type RetentionMode int

const (
	RetentionDefault RetentionMode = iota // session_days <= 0 && !Disabled
	RetentionCustom                       // session_days > 0 && !Disabled
	RetentionForever                      // Disabled == true
)

// String returns a lowercase stable label ("default" / "custom" / "forever").
// Used by log lines and by TS consumers via the wire.
func (m RetentionMode) String() string {
	switch m {
	case RetentionCustom:
		return "custom"
	case RetentionForever:
		return "forever"
	default:
		return "default"
	}
}

// Mode classifies the retention config into one of three states.
// Disabled takes precedence over SessionDays — setting disabled: true with
// session_days: 99 still means "forever".
func (r OmnipusRetentionConfig) Mode() RetentionMode {
	if r.Disabled {
		return RetentionForever
	}
	if r.SessionDays > 0 {
		return RetentionCustom
	}
	return RetentionDefault
}

// OmnipusCompactionConfig holds context compression settings per Appendix E §E.5.3.
type OmnipusCompactionConfig struct {
	Enabled        bool `json:"enabled,omitempty"`
	ReserveTokens  int  `json:"reserve_tokens,omitempty"`
	PreserveRecent int  `json:"preserve_recent,omitempty"`
	MemoryFlush    bool `json:"memory_flush,omitempty"`
}

// OmnipusChannelPolicy holds per-channel Omnipus-specific policies.
// Stored in config.json under channel_policies.<channel-name>.
type OmnipusChannelPolicy struct {
	// RoutingRules maps user patterns to agents for this channel.
	// Loaded at startup and merged into config.Bindings automatically.
	RoutingRules []OmnipusChannelRoutingRule `json:"routing_rules,omitempty"`
}

// OmnipusChannelRoutingRule maps a channel+user pattern to an agent.
// Stored in config.json under channel_policies.<channel-name>.routing_rules[].
type OmnipusChannelRoutingRule struct {
	// UserID is the channel-specific user identifier. "*" matches any user.
	UserID string `json:"user_id,omitempty"`
	// AgentID is the agent that handles messages matching this rule.
	AgentID string `json:"agent_id"`
}

// Clone returns a deep copy of c via JSON round-trip. The clone is fully
// independent: mutations to slice or map fields in the original do not affect
// the clone and vice versa. Returns nil if marshaling or unmarshalling fails
// (should never happen for a valid Config in practice).
//
// Clone carries the runtime-registered sensitive plaintexts (registeredSensitive,
// populated by RegisterSensitiveValues) onto the clone — the JSON round-trip
// below drops them because they are unexported, and a clone that loses them would
// scrub nothing from LLM output/audit logs once published as the live config
// (e.g. via AgentLoop.MutateConfig's copy-then-swap, or UpsertAgentFast's
// rebase-then-publish). The compiled sensitiveCache is intentionally NOT shared:
// it is invalidated on the clone so SensitiveDataReplacer rebuilds it lazily from
// the carried set under the clone's own mutex.
//
// Agents.List is deliberately json:"-" on AgentsConfig (Bug 1 fix, see its
// doc comment) so it never rides through Config's own JSON round-trip above
// — but Clone's "fully independent deep copy" contract must still hold for
// it: callers such as pkg/gateway's candidate-config validate-then-commit
// pattern (cfg.Clone() -> mutate the candidate -> discard on validation
// failure) rely on a mutated clone never reaching back into the original's
// in-memory roster. List is therefore deep-copied via its own, independent
// JSON round-trip below, unaffected by the "-" tag (which only governs how
// AgentsConfig marshals AS A FIELD OF Config, not how []AgentConfig marshals
// on its own).
func (c *Config) Clone() (*Config, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(c); err != nil {
		return nil, fmt.Errorf("clone: marshal: %w", err)
	}
	clone := &Config{}
	if err := json.NewDecoder(&buf).Decode(clone); err != nil {
		return nil, fmt.Errorf("clone: unmarshal: %w", err)
	}

	if len(c.Agents.List) > 0 {
		var listBuf bytes.Buffer
		if err := json.NewEncoder(&listBuf).Encode(c.Agents.List); err != nil {
			return nil, fmt.Errorf("clone: marshal agents.list: %w", err)
		}
		var listClone []AgentConfig
		if err := json.NewDecoder(&listBuf).Decode(&listClone); err != nil {
			return nil, fmt.Errorf("clone: unmarshal agents.list: %w", err)
		}
		clone.Agents.List = listClone
	}
	// Carry the runtime-registered sensitive plaintexts onto the clone. Read
	// under the source's sensitiveMu so this races no concurrent
	// RegisterSensitiveValues on c; install under the clone's own (fresh, zero)
	// mutex via the established setter, which also invalidates the clone's cache
	// so the next SensitiveDataReplacer rebuilds from this carried set.
	c.sensitiveMu.RLock()
	registered := append([]string(nil), c.registeredSensitive...)
	c.sensitiveMu.RUnlock()
	if len(registered) > 0 {
		clone.RegisterSensitiveValues(registered)
	}
	return clone, nil
}

// MergeChannelPoliciesIntoBindings converts OmnipusChannelPolicy routing rules
// into AgentBinding entries and appends them to Bindings (if not already present).
// Called automatically after config load so the existing RouteResolver picks them up.
func (c *Config) MergeChannelPoliciesIntoBindings() {
	for channelName, policy := range c.ChannelPolicies {
		for _, rule := range policy.RoutingRules {
			b := AgentBinding{
				AgentID: rule.AgentID,
				Match: BindingMatch{
					Channel:   channelName,
					AccountID: rule.UserID,
				},
			}
			c.Bindings = append(c.Bindings, b)
		}
	}
}

// FilterSensitiveData filters sensitive values from content before sending to LLM.
// This prevents the LLM from seeing its own credentials.
// Uses strings.Replacer for O(n+m) performance (computed once per SecurityConfig).
// Short content (below FilterMinLength) is returned unchanged for performance.
func (c *Config) FilterSensitiveData(content string) string {
	// Check if filtering is enabled (default: true)
	if !c.Tools.IsFilterSensitiveDataEnabled() {
		return content
	}
	// Fast path: skip filtering for short content
	if len(content) < c.Tools.GetFilterMinLength() {
		return content
	}
	return c.SensitiveDataReplacer().Replace(content)
}

// PerformanceConfig controls the max-parallel fan-out gate for task/subagent dispatch.
// It is stored in config.json under the "performance" key and may also be overridden
// at runtime via the OMNIPUS_MAX_PARALLEL_AGENTS env var.
type PerformanceConfig struct {
	// MaxParallelAgents is the maximum number of concurrent task/subagent
	// dispatches.
	//
	// 0 means "not configured". It does NOT mean "the system computed a
	// number for you": there is no longer a computed default. The former
	// availableRAM/3.5-MB-per-agent formula, and the auto-detect and
	// auto-floor helpers that wrapped it, are deleted. It was one of two
	// memory mechanisms in this process, sizing a cap ONCE from a per-agent
	// byte constant while the browser pool sized itself LIVE from the same
	// host's real headroom — two mechanisms disagreeing about the same
	// machine.
	//
	// Concurrency is now bounded by LIVE memory at the moment of admission
	// (see MemoryPressureHigh), not by a number precomputed from a constant.
	// When this field is unset, EffectiveMaxParallelAgents returns the
	// PHYSICAL OS-thread safety backstop (physicalConcurrencySafetyCeiling)
	// — a "this many threads would abort the Go runtime" bound, not a claim
	// that the machine can run that many agents. The live gate is what
	// actually stops admission long before the backstop is approached.
	//
	// An explicit non-zero value here (or via OMNIPUS_MAX_PARALLEL_AGENTS) is
	// a DEFAULT OVERRIDE: it always wins outright and is honored as
	// configured — see clampParallelExplicit.
	MaxParallelAgents int `json:"max_parallel_agents,omitempty" env:"OMNIPUS_MAX_PARALLEL_AGENTS"`
}

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
// diagnostic — EffectiveMaxParallelAgents is deliberately never cached
// (config.go's own "known limitation" doc comment on availableRAMBytes), and
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

// memorySignal is one determinable (available, total) reading of this
// host's memory. Two sources can produce one — the kernel's own host-wide
// figures and the process's cgroup limit — and either, both, or neither may
// be determinable at any moment.
type memorySignal struct {
	available uint64
	total     uint64
}

// memorySignals returns every DETERMINABLE memory signal, in no particular
// order. An empty slice means this host's memory cannot be measured at all:
// a Windows host (no reader exists), a BSD host (no reader exists), or a
// Linux host whose /proc/meminfo is unreadable (gVisor, distroless, a
// hardened seccomp profile). That is a first-class answer, not an error, and
// it is the ONLY thing that makes the two-valued accessors below report
// ok=false.
//
// The two sources:
//
//  1. The host-wide reading — /proc/meminfo's MemAvailable and MemTotal on
//     Linux, an assembled sysctl approximation on Darwin. Accounts for
//     reclaimable page cache (unlike MemFree).
//  2. The process's cgroup memory limit and the headroom under it
//     (readCgroupMemoryBudgetBytes), when a finite limit is configured —
//     common in containerized deployments (Docker, Fly Machines,
//     Kubernetes), where the limit is frequently far tighter than the host's
//     own total memory and is a STABLE, explicitly configured ceiling rather
//     than a live kernel heuristic.
//
// Both are collected because they answer DIFFERENT questions and a caller
// wants the tighter answer to each. Critically (FR-079), an undeterminable
// signal is OMITTED rather than contributed as a zero: the previous code
// compared a cgroup reading against a host-wide reading that had silently
// fabricated 4 GB when /proc/meminfo was unreadable, so on a /proc-less
// container the invented number could win the comparison and discard the one
// signal that was real.
func memorySignals() []memorySignal {
	var out []memorySignal
	if avail, ok := readMemAvailableBytes(); ok {
		if total, ok := readMemTotalBytes(); ok && total > 0 {
			out = append(out, memorySignal{available: avail, total: total})
		}
	}
	if avail, limit, ok := readCgroupMemoryBudgetBytes(); ok && limit > 0 {
		out = append(out, memorySignal{available: avail, total: limit})
	}
	return out
}

// availableRAMBytes returns the current best estimate of memory available
// for starting new work, in bytes, and whether that estimate could be made
// at all.
//
// It is the MINIMUM over the DETERMINABLE signals only (FR-079), so a tight
// container limit is never exceeded by trusting an unconstrained host-wide
// reading, and an unreadable host-wide reading never discards a real cgroup
// one. ok is false when NEITHER signal is determinable — never when one is
// merely tighter than the other.
//
// Known limitation, accepted deliberately: MemAvailable can under-report for
// a period after a fresh boot/container start before the page-cache
// subsystem has warmed up (observed live: 28 MB measured on a box that
// settled at ~370 MB once warm — docs/internal/uat/
// max-parallel-concurrency-gap-2026-07-31.md G4, cross-referenced against
// parallelism-cost-measurement-2026-08-04.md's clean-idle baseline). This is
// NOT "solved" by re-sampling with a short in-process delay at boot — the
// warm-up lag observed is tied to the box's actual workload history, not
// milliseconds, so a boot-time retry loop would not reliably help and would
// only delay every gateway boot for no real benefit. Instead, this value is
// deliberately never frozen at boot for any live caller: every production
// call site re-reads it at the moment of admission, so a transient low
// boot-time reading self-corrects as soon as the host's real availability
// changes, with no operator action required.
func availableRAMBytes() (uint64, bool) {
	signals := memorySignals()
	if len(signals) == 0 {
		return 0, false
	}
	min := signals[0].available
	for _, sig := range signals[1:] {
		if sig.available < min {
			min = sig.available
		}
	}
	return min, true
}

// memoryPressureRatioThreshold is THE threshold. Singular, deliberately.
//
// Every admission consumer in this process — the browser pool at launch and
// at every tab open, agent admission on the delegation path — asks the same
// question of the same numbers through MemoryPressureHigh, and this is the
// number it compares against. A second threshold constant anywhere would
// re-create the exact defect this work exists to remove: two mechanisms
// disagreeing about one machine, each individually defensible, together
// incoherent.
//
// 0.85 means "85% of the memory budget is in non-reclaimable use". Under a
// cgroup limit that is memory.current-minus-reclaimable over memory.max —
// i.e. the ratio the browser-pool spec names directly. Off a cgroup it is
// the same shape against the host-wide figures. The value leaves roughly a
// seventh of the budget as headroom, which on any host large enough to run
// a browser at all is more than one Chrome's measured launch cost.
const memoryPressureRatioThreshold = 0.85

// MemoryPressureHigh reports whether this host is above
// memoryPressureRatioThreshold, and whether that could be determined.
//
// THIS IS THE SHARED SEAM (FR-068). It is the one accessor and the one
// threshold every consumer reads; sameness between the browser pool and
// agent admission is a property of them calling this function, not of them
// happening to compute equal answers. Test seams that stub memory do so by
// replacing this function's provider (see SetMemoryProviderForTest), which
// is what lets one stub drive both consumers in one test body.
//
// The two return values mean different things and must not be collapsed:
//
//   - (false, true) — measured, and there is headroom. Admit.
//   - (true, true)  — measured, and the host is under pressure. Refuse to
//     grow. This is a HARD stop, not a hint.
//   - (_, false)    — this host's memory cannot be measured at all. Each
//     consumer takes its own documented unmeasurable-host branch. Neither
//     refuses to RUN; both refuse to GROW past a conservative floor.
//
// The ratio is computed per determinable signal and the WORST (highest) is
// returned, matching availableRAMBytes taking the tightest available figure:
// a container at 90% of its cgroup limit is under pressure even if the host
// it sits on is idle.
func MemoryPressureHigh() (high bool, ok bool) {
	return memoryProvider()
}

// AvailableMemoryBytes is the exported two-valued live-memory accessor:
// bytes of headroom, and whether that could be determined at all.
//
// Callers wanting a yes/no admission decision should use MemoryPressureHigh
// instead — it carries the one shared threshold, so a caller that compares
// this figure against a threshold of its own has quietly created the second
// mechanism. This exists for callers that need an absolute figure, notably
// the browser pool's per-launch headroom check (does this host have room for
// one more Chrome), which is a bytes question and not a ratio question.
func AvailableMemoryBytes() (uint64, bool) {
	return availableMemoryProvider()
}

// memoryProvider and availableMemoryProvider are the injection seam. They
// are package-level vars, following the same pattern as procMeminfoPath and
// cgroupRoot in this package, purely so a test can drive every consumer of
// the memory mechanism off ONE stub and assert they behave identically at
// the seam rather than inferring sameness from equal outcomes. Production
// code never reassigns them.
var (
	memoryProvider          = liveMemoryPressureHigh
	availableMemoryProvider = availableRAMBytes
)

// liveMemoryPressureHigh is MemoryPressureHigh's real implementation.
func liveMemoryPressureHigh() (bool, bool) {
	signals := memorySignals()
	if len(signals) == 0 {
		return false, false
	}
	worst := 0.0
	for _, sig := range signals {
		if sig.total == 0 {
			continue
		}
		used := float64(0)
		if sig.available < sig.total {
			used = float64(sig.total-sig.available) / float64(sig.total)
		} else {
			used = 0
		}
		if used > worst {
			worst = used
		}
	}
	return worst > memoryPressureRatioThreshold, true
}

// SetMemoryProviderForTest replaces BOTH memory accessors with stubs for the
// duration of a test and returns a restore function. Exported because the
// consumers under test live in other packages (pkg/agent, pkg/tools/browser)
// and the whole point of the seam is that one stub drives all of them.
//
// It is a test helper in a production file for the same reason
// procMeminfoPath is a var: the alternative is threading an interface
// through every admission call site, which is a much larger change to make
// one property assertable.
func SetMemoryProviderForTest(pressure func() (bool, bool), available func() (uint64, bool)) func() {
	prevPressure, prevAvailable := memoryProvider, availableMemoryProvider
	if pressure != nil {
		memoryProvider = pressure
	}
	if available != nil {
		availableMemoryProvider = available
	}
	return func() {
		memoryProvider, availableMemoryProvider = prevPressure, prevAvailable
	}
}

type HooksConfig struct {
	Enabled   bool                         `json:"enabled"`
	Defaults  HookDefaultsConfig           `json:"defaults,omitempty"`
	Builtins  map[string]BuiltinHookConfig `json:"builtins,omitempty"`
	Processes map[string]ProcessHookConfig `json:"processes,omitempty"`
}

type HookDefaultsConfig struct {
	ObserverTimeoutMS    int `json:"observer_timeout_ms,omitempty"`
	InterceptorTimeoutMS int `json:"interceptor_timeout_ms,omitempty"`
	ApprovalTimeoutMS    int `json:"approval_timeout_ms,omitempty"`
}

type BuiltinHookConfig struct {
	Enabled  bool            `json:"enabled"`
	Priority int             `json:"priority,omitempty"`
	Config   json.RawMessage `json:"config,omitempty"`
}

type ProcessHookConfig struct {
	Enabled   bool              `json:"enabled"`
	Priority  int               `json:"priority,omitempty"`
	Transport string            `json:"transport,omitempty"`
	Command   []string          `json:"command,omitempty"`
	Dir       string            `json:"dir,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Observe   []string          `json:"observe,omitempty"`
	Intercept []string          `json:"intercept,omitempty"`
}

// BuildInfo contains build-time version information
type BuildInfo struct {
	Version   string `json:"version"`
	GitCommit string `json:"git_commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
}

// MarshalJSON implements custom JSON marshaling for Config.
// It omits session when empty and merges back any unknown fields that were
// collected during loading so that round-trip writes preserve forward-compat
// keys (FR-004).
func (c *Config) MarshalJSON() ([]byte, error) {
	type Alias Config
	aux := &struct {
		Session *SessionConfig `json:"session,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(c),
	}

	// Only include session if not empty
	if c.Session.DMScope != "" || len(c.Session.IdentityLinks) > 0 {
		aux.Session = &c.Session
	}

	data, err := json.Marshal(aux)
	if err != nil {
		return nil, err
	}

	// Merge unknown fields back for round-trip safety (FR-004).
	if len(c.UnknownFields) == 0 {
		return data, nil
	}
	var m map[string]json.RawMessage
	if unmarshalErr := json.Unmarshal(data, &m); unmarshalErr != nil {
		// best-effort: log and return original data without merging unknown fields.
		slog.Debug("config: MarshalJSON: could not parse for unknown-field merge", "error", unmarshalErr)
		return data, nil
	}
	for k, v := range c.UnknownFields {
		if _, exists := m[k]; !exists {
			m[k] = v
		}
	}
	return json.Marshal(m)
}

type AgentsConfig struct {
	Defaults AgentDefaults `json:"defaults"`
	// List is the live in-memory agent roster. Since ADR-054 (entity/config
	// separation) it is populated exclusively by the roster bridge
	// (pkg/gateway.populateAgentsListFromEntityStoreStrict, and
	// cmd/omnipus's own loaders) from the per-entity agent store
	// ($OMNIPUS_HOME/entities/agents/<id>.json, pkg/agentstore) — never from
	// config.json. It remains a normal, heavily-read in-memory field (dozens
	// of call sites across pkg/gateway, pkg/coreagent, pkg/routing,
	// pkg/tools, pkg/agent range over cfg.Agents.List directly), but
	// json:"-" makes its ABSENCE FROM THE WIRE STRUCTURAL rather than a
	// convention every caller must remember: no json.Marshal(cfg) — SaveConfig,
	// GET /api/v1/config's raw dump, the system.config.get/set dotGet/dotSet
	// round-trip in pkg/sysagent/tools/config.go, or any future caller — can
	// ever re-inject the roster into config.json, no matter how populated
	// this field is at the time of the call. Before this fix, the field's
	// ordinary `json:"list,omitempty"` tag meant ANY config.json write
	// (e.g. a bare `system.config.set gateway.log_level`, or
	// SaveConfigLocked) re-serialized the ENTIRE live roster back into
	// config.json, silently degrading ADR-054's headline guarantee
	// ("config.json no longer carries the roster") from a structural
	// property into a best-effort self-healing loop.
	//
	// A legacy config.json still carrying an "agents.list" key from before
	// ADR-054 is handled by legacy_agents_list.go's stripLegacyAgentsList —
	// which, because this tag means typed unmarshal can never see that key
	// in the first place, detects and drops it by reading the raw file
	// bytes directly rather than inspecting this field.
	//
	// Config.Clone() (this file) deep-copies this field via its own
	// independent JSON round-trip, since Config's own marshal/unmarshal no
	// longer touches it — see Clone's doc comment.
	List []AgentConfig `json:"-"`
}

// AgentModelConfig supports both string and structured model config.
// String format: "gpt-4" (just primary, no fallbacks)
// Object format: {"primary": "gpt-4", "fallbacks": ["claude-haiku"]}
type AgentModelConfig struct {
	Primary   string   `json:"primary,omitempty"`
	Fallbacks []string `json:"fallbacks,omitempty"`
	// Provider is the explicit routing key for the primary model (O3 two-field
	// model), mirroring FallbackModel.Provider. When set, model resolution uses it
	// directly and never infers a provider from the slug. Empty means "resolve via
	// the default/passthrough provider" (legacy single-slug behavior). A `/`
	// inside Primary is DATA, never a "<protocol>/<model>" prefix — there is no
	// config-load migration that splits it (C1 fix, ADR-067 FR-034; the former
	// migrateAgentPrimaryProvider split routed models to the wrong vendor and was
	// deleted). A bare-string Primary that needs an explicit provider legitimately
	// trips the pre-turn needs_provider gate (ADR-067 T067-09) instead.
	Provider string `json:"provider,omitempty"`
}

func (m *AgentModelConfig) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		m.Primary = s
		m.Fallbacks = nil
		m.Provider = ""
		return nil
	}
	type raw struct {
		Primary   string   `json:"primary"`
		Fallbacks []string `json:"fallbacks"`
		Provider  string   `json:"provider"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	m.Primary = r.Primary
	m.Fallbacks = r.Fallbacks
	m.Provider = r.Provider
	return nil
}

func (m AgentModelConfig) MarshalJSON() ([]byte, error) {
	// Emit the bare-string form only when there is nothing but a primary slug —
	// no fallbacks and no explicit provider. Once Provider is set the object form
	// is required so the routing key round-trips (O3).
	if len(m.Fallbacks) == 0 && m.Provider == "" && m.Primary != "" {
		return json.Marshal(m.Primary)
	}
	type raw struct {
		Primary   string   `json:"primary,omitempty"`
		Fallbacks []string `json:"fallbacks,omitempty"`
		Provider  string   `json:"provider,omitempty"`
	}
	return json.Marshal(raw(m))
}

// FallbackModel is one entry in an agent's fallback chain. It carries its
// own provider so a fallback can route through a different provider than
// the primary (FR-007). FR-005 pins the wire format to [{model, provider}]
// going forward; FR-006 accepts the legacy [string] form during migration —
// see FallbackModel.UnmarshalJSON.
//
// Spec reconciliation (Q1/Q7, phase-1-chat-model-and-errors.md §3 / §18):
// Q1 originally said "full cutover, no [string] support" and Q7 said
// "accept old form during migration". Implementation follows Q7: the read
// path (UnmarshalJSON) accepts both forms, the write path (MarshalJSON)
// always emits the object form. This file is the single read-side
// normalizer; do not duplicate the legacy-form parsing elsewhere.
type FallbackModel struct {
	// Model is the model slug the fallback will use (e.g. "claude-sonnet-4.6"
	// or "openrouter/anthropic/claude-opus-4.7").
	Model string `json:"model"`
	// Provider is the routing key (e.g. "openrouter", "anthropic",
	// "openai"). Distinct from any "provider/" prefix embedded in Model —
	// Provider is the credential/endpoint selection key.
	Provider string `json:"provider,omitempty"`
}

// FallbackModelSlice is the JSON wire shape for an agent's fallback chain.
// It accepts both the new [{model, provider}] form (FR-005) and the legacy
// [string] form (FR-006). Legacy strings are stored as
// {Model: <string>, Provider: ""} at unmarshal time; the empty Provider is
// filled in by NormalizeFallbacks after the parent *Config is available.
type FallbackModelSlice []FallbackModel

// UnmarshalJSON decodes either form (FR-005 + FR-006).
//
// Examples accepted:
//   - ["claude-sonnet-4.6", "gpt-4o-mini"]
//   - [{"model":"claude-sonnet-4.6","provider":"anthropic"}]
//   - ["openrouter/foo", {"model":"claude-sonnet-4.6","provider":"anthropic"}]
//
// Empty / missing / null decodes to a nil slice (semantically identical to
// "no fallback configured").
func (f *FallbackModelSlice) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*f = nil
		return nil
	}

	// 1) Try the homogeneous []FallbackModel form first.
	var objs []FallbackModel
	if err := json.Unmarshal(data, &objs); err == nil {
		*f = objs
		return nil
	}

	// 2) Try []string for the legacy wire form.
	var legacy []string
	if err := json.Unmarshal(data, &legacy); err == nil {
		out := make(FallbackModelSlice, len(legacy))
		for i, s := range legacy {
			out[i] = FallbackModel{Model: s}
		}
		*f = out
		return nil
	}

	// 3) Mixed form: an array of either strings or objects. Walk element by
	// element so we preserve order in a mixed legacy + new payload.
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("fallback_models must be a JSON array of strings or {model, provider} objects: %w", err)
	}
	out := make(FallbackModelSlice, 0, len(raw))
	for i, r := range raw {
		var s string
		if err := json.Unmarshal(r, &s); err == nil {
			out = append(out, FallbackModel{Model: s})
			continue
		}
		var fb FallbackModel
		if err := json.Unmarshal(r, &fb); err != nil {
			return fmt.Errorf("fallback_models[%d]: must be a string or an object: %w", i, err)
		}
		out = append(out, fb)
	}
	*f = out
	return nil
}

// MarshalJSON writes the canonical object form (FR-005). Always emits
// [{"model":"...","provider":"..."}]; the legacy string-only form is never
// emitted on write — loaders see only the normalized object form on
// round-trip.
func (f FallbackModelSlice) MarshalJSON() ([]byte, error) {
	type wire struct {
		Model    string `json:"model"`
		Provider string `json:"provider,omitempty"`
	}
	if len(f) == 0 {
		return []byte("[]"), nil
	}
	out := make([]wire, len(f))
	for i, fb := range f {
		out[i] = wire(fb)
	}
	return json.Marshal(out)
}

// NormalizeFallbacks is the single entry-point used at config load to
// resolve fallback entries that arrived without a provider field (legacy
// strings, or empty-providers on legacy objects). The resolver mirrors the
// chat-side `buildModelListResolver` passthrough logic: any slug that
// matches a configured provider entry is taken verbatim; any slug that
// doesn't match but where a passthrough provider (openrouter, vivgrid)
// is configured is routed through the passthrough provider; otherwise the
// entry is left with an empty provider (the resolver above will fail
// closed at apply time).
//
// Order is preserved (FR-006). nil input → nil output. Already-resolved
// entries (Provider != "") are passed through unchanged.
//
// Traces to: spec §11 Dataset 2 / FR-006 / FR-007.
func NormalizeFallbacks(cfg *Config, in []FallbackModel) []FallbackModel {
	if len(in) == 0 {
		return nil
	}
	out := make([]FallbackModel, len(in))
	for i, fb := range in {
		if strings.TrimSpace(fb.Model) == "" {
			continue // drop empty entries
		}
		if strings.TrimSpace(fb.Provider) != "" {
			out[i] = fb // already resolved
			continue
		}
		// Legacy string entry — resolve provider.
		out[i] = FallbackModel{
			Model:    fb.Model,
			Provider: resolveFallbackProvider(cfg, fb.Model),
		}
	}
	return out
}

// resolveFallbackProvider picks a provider for a fallback model slug when
// the caller didn't pin one. Mirrors the passthrough logic in
// pkg/agent/model_resolution.go::buildModelListResolver (kept duplicated
// here to avoid a config→agent import cycle — pkg/agent already imports
// pkg/config).
//
// Resolution order (mirrors FindModelConfigBySlug's rungs):
//  1. Exact match against any configured provider's Model (the slug)
//     → that provider's Provider field.
//  2. Any configured provider is a passthrough (openrouter / vivgrid) →
//     that passthrough provider.
//  3. Otherwise empty string (apply-time resolver will error out).
//
// The display-alias rung is gone with ModelConfig.ModelName (ADR-067
// FR-013 / X-25): a row is addressed by its (provider, model) pair.
//
// Step 3 cannot call providers.IsPassthroughProvider directly — pkg/providers
// already imports pkg/config, so the reverse direction would be a cycle. The
// check below mirrors that helper's passthrough-name list, but the provider-id
// comparison itself is exact after TrimSpace with no case folding (ADR-067
// FR-036: every provider-id comparison in pkg/agent, pkg/gateway, pkg/providers
// — and this in-package duplicate — MUST be exact). pkg/providers' own copy is
// deliberately case-insensitive on the name (its own doc comment says so) and
// is out of scope here; the two are no longer byte-identical by design.
func resolveFallbackProvider(cfg *Config, slug string) string {
	provider, _ := ResolveSlugProvider(cfg, slug)
	return provider
}

// ResolveSlugProvider resolves the provider a bare model slug would route
// through today, and reports whether that resolution happened only via the
// passthrough rung (rule 3: openrouter / vivgrid). It is the exported face
// of resolveFallbackProvider's rungs, added for ADR-068 FR-012: the
// dependents computation in pkg/gateway (provider_dependents.go) must apply
// the exact same rungs — an agent whose slug exact-matches a provider row is
// a `primary` dependent, one that resolves only through rule 3 is a
// `passthrough` dependent — so the rule lives here once and is consumed
// there, never duplicated.
//
// Returns ("", false) when nothing configured can serve the slug.
func ResolveSlugProvider(cfg *Config, slug string) (provider string, viaPassthrough bool) {
	if cfg == nil {
		return "", false
	}
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return "", false
	}

	// 1: match against what each provider row serves.
	for _, p := range cfg.Providers {
		if p == nil {
			continue
		}
		if strings.TrimSpace(p.Model) == slug {
			return strings.TrimSpace(p.Provider), false
		}
	}
	// 2: passthrough fallback (openrouter, vivgrid).
	for _, p := range cfg.Providers {
		if p == nil {
			continue
		}
		provName := strings.TrimSpace(p.Provider)
		if provName == "openrouter" || provName == "vivgrid" ||
			strings.Contains(strings.ToLower(p.APIBase), "openrouter.ai") {
			return provName, true
		}
	}

	return "", false
}

type AgentConfig struct {
	ID          string            `json:"id"`
	Default     bool              `json:"default,omitempty"`
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Home        string            `json:"workspace,omitempty"`
	Model       *AgentModelConfig `json:"model,omitempty"`
	// MaxToolIterations caps the LLM/tool rounds PER TURN for this agent
	// (one chat message, board task, or heartbeat run = one turn; the turn
	// pauses at the cap and can be continued). 0 = inherit
	// agents.defaults.max_tool_iterations (which itself falls back to 200).
	// This field previously existed only on the wire and in raw config.json —
	// it was silently dropped on load and never applied by the runtime
	// (P0 bug, fixed 2026-07-03).
	MaxToolIterations int              `json:"max_tool_iterations,omitempty"`
	Skills            []string         `json:"skills,omitempty"`
	Subagents         *SubagentsConfig `json:"subagents,omitempty"`
	// FallbackModels is the ordered fallback chain tried when the primary
	// model returns an error. Each entry carries its own provider so a
	// fallback can route through a different provider than the primary (e.g.
	// when the primary's provider is rate-limited, the fallback can use a
	// different provider's API key and credentials — FR-007).
	//
	// Wire format (preferred, FR-005):
	//   [{ "model": "claude-sonnet-4.6", "provider": "anthropic" }]
	//
	// Wire format (legacy, accepted during migration; FR-006):
	//   ["claude-sonnet-4.6"]
	//
	// Legacy entries are normalized at config-load time via NormalizeFallbacks,
	// which resolves each string against the configured providers using the
	// same passthrough lookup the chat-side model resolver uses.
	FallbackModels FallbackModelSlice `json:"fallback_models,omitempty"`
	// Voice is the per-agent persona voice identifier (e.g. TTS voice name).
	// Distinct from the global VoiceConfig engine settings.
	// Schema-pinned; not active until v0.2.0 TTS delivery.
	Voice string `json:"voice,omitempty"`
	// Color is the hex color code for this agent's avatar in the UI (e.g. "#22C55E").
	Color string `json:"color,omitempty"`
	// Icon is the Phosphor icon name for this agent's avatar in the UI (e.g. "robot").
	Icon string `json:"icon,omitempty"`
	// Type classifies the agent. Empty defaults to AgentTypeCustom for stored agents;
	// use ResolveType() to get the effective type.
	Type AgentType `json:"type,omitempty"`
	// Locked prevents modification of identity fields (name, description, color,
	// icon, prompt). Used by core agents to keep their identity stable.
	// Users CAN still change model, remove tools, and set heartbeat.
	Locked bool `json:"locked,omitempty"`
	// MemoryEnabled gates whether this agent's ContextBuilder injects its
	// episodic "# Memory" section into the system prompt (ADR-052 FR-039,
	// Judge/Verifier architecture). Pointer so nil means "inherit the
	// default" — true — distinguishing an operator who never set this field
	// from one who explicitly re-enabled it. A verifier-role agent (e.g. the
	// seeded Judge) is seeded false: its verdicts must be reproducible and
	// impartial (same evidence -> same verdict) rather than drifting with
	// accumulated episodic memory across adjudications. Every other agent
	// defaults to true (unchanged behavior). See MemoryEnabledEffective.
	MemoryEnabled *bool `json:"memory_enabled,omitempty"`
	// ContextWindowOverride is the per-agent context-window override
	// (tokens), rung 1 of the ADR-066 D2 resolution ladder. nil = unset.
	// Like every override it can only LOWER the window: the resolver clamps
	// it to the model's capability on every resolution (FR-002). Written by
	// PUT /api/v1/agents/{id} `context_window_override` (nullable to clear);
	// the derived effective window / source / clamped flag are computed by
	// the resolver and never persisted here.
	ContextWindowOverride *int `json:"context_window_override,omitempty"`
	// Tools, when non-nil, overrides scope-based tool visibility for this agent.
	// Nil means all tools allowed by the agent's type are available.
	Tools *AgentToolsCfg `json:"tools,omitempty"`
	// ShellPolicy configures per-agent shell command deny patterns.
	// When non-nil, its settings are merged with the global ShellDenyPatterns
	// at enforcement time.
	ShellPolicy *AgentShellPolicy `json:"shell_policy,omitempty"`
	// CreatedAt is the timestamp this agent record was created. Set once and
	// never modified thereafter. Added by ADR-054 D2 (docs/internal/architecture/
	// ADR-054-entity-config-separation.md) — the per-entity store's List()
	// ordering contract is "sort by (created_at, id)", deliberately NOT a
	// persisted sort_index (that would require a read-all-then-write on every
	// create, re-serializing exactly the operation the ADR exists to
	// parallelize, and would be an unowned global invariant). Pointer so
	// omitempty distinguishes a pre-ADR-054 record (nil, never set) from a
	// genuinely-zero timestamp, mirroring UpdatedAt immediately below.
	CreatedAt *time.Time `json:"created_at,omitempty"`
	// UpdatedAt is the timestamp of the last successful PUT /agents/{id} update.
	// It is returned in list and detail responses and used for optimistic concurrency.
	// Pointer so omitempty works; nil = never updated. A non-pointer time.Time
	// is a struct type and always serializes (writing "0001-01-01T00:00:00Z"
	// for agents that were never PUT-updated), defeating omitempty.
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

// MemoryEnabledEffective resolves the memory-injection flag (ADR-052
// FR-039): a non-nil MemoryEnabled wins; nil (the field was never set)
// resolves to true, preserving pre-FR-039 behavior for every agent that
// doesn't opt out.
func (a AgentConfig) MemoryEnabledEffective() bool {
	return a.MemoryEnabled == nil || *a.MemoryEnabled
}

// AgentType classifies an agent for scope-based tool visibility filtering.
type AgentType string

const (
	AgentTypeSystem AgentType = "system"
	AgentTypeCore   AgentType = "core"
	AgentTypeCustom AgentType = "custom"
	// AgentTypeWorker is a sub-agent worker: a depth-limited, ephemeral labor
	// tier invoked ONLY via delegation. A worker is NOT a chat target (it never
	// receives inbound channel messages and is never resolved as the default
	// agent), has no heartbeat, and cannot be marked as the routing default.
	// Workers carry an Executor (Subagents.Executor) selecting native /
	// external-cli / remote-a2a. "A tool you point at work, not a colleague."
	AgentTypeWorker AgentType = "worker"
)

// AgentShellPolicy configures per-agent shell command deny patterns for the
// workspace.shell tool. It is stored on AgentConfig so
// that the enforcement layer can merge per-agent patterns with the global
// OmnipusSandboxConfig.ShellDenyPatterns list.
type AgentShellPolicy struct {
	// EnableDenyPatterns activates shell command deny-pattern checking for this
	// agent. When false (default), neither custom nor global deny patterns are
	// applied. Operators must explicitly opt in per agent or globally.
	EnableDenyPatterns bool `json:"enable_deny_patterns,omitempty"`
	// CustomDenyPatterns lists agent-specific shell command deny patterns
	// (regular expressions). Merged with the global ShellDenyPatterns list when
	// EnableDenyPatterns is true. Patterns that fail to compile are logged at
	// Warn and skipped.
	CustomDenyPatterns []string `json:"custom_deny_patterns,omitempty"`
}

// AgentToolsCfg holds per-agent overrides for builtin tool visibility and MCP server bindings.
type AgentToolsCfg struct {
	Builtin AgentBuiltinToolsCfg `json:"builtin,omitempty"`
	MCP     AgentMCPToolsCfg     `json:"mcp,omitempty"`
}

// ToolPolicy defines the access policy for a tool on a specific agent.
// TODO(#70): Consolidate with policy.ToolPolicy to avoid duplicate type definitions.
type ToolPolicy string

const (
	ToolPolicyAllow ToolPolicy = "allow" // Tool runs immediately, no confirmation
	ToolPolicyAsk   ToolPolicy = "ask"   // Tool requires user approval before execution
	ToolPolicyDeny  ToolPolicy = "deny"  // Tool is blocked — agent cannot use it
)

// AgentBuiltinToolsCfg controls which builtin tools an agent can use and how.
//
// There is no default-policy fallback (CLAUDE.md hard constraint 6): every
// static builtin tool must resolve from an explicit, literal entry in
// Policies and/or the global sandbox.tool_policies map. Coverage gaps are a
// hard validation failure (config.ValidateToolPolicyCoverage), never a
// runtime default.
type AgentBuiltinToolsCfg struct {
	// Policies is a per-tool override map. Keys are tool names from the catalog.
	Policies map[string]ToolPolicy `json:"policies,omitempty"`
}

// AgentMCPToolsCfg controls which MCP servers are available to an agent.
type AgentMCPToolsCfg struct {
	Servers []AgentMCPServerBinding `json:"servers,omitempty"`
}

// AgentMCPServerBinding binds an MCP server to an agent.
type AgentMCPServerBinding struct {
	ID    string   `json:"id"`
	Tools []string `json:"tools,omitempty"` // empty or ["*"] = all tools from that server
}

// ResolveType returns the effective agent type. If the Type field is set, it is
// returned directly. Otherwise the type is inferred: known core agent IDs →
// AgentTypeCore; everything else → AgentTypeCustom. The caller must provide
// isCoreAgent to avoid an import cycle with the coreagent package.
func (a AgentConfig) ResolveType(isCoreAgent func(string) bool) AgentType {
	if a.Type != "" {
		return a.Type
	}
	if isCoreAgent != nil && isCoreAgent(a.ID) {
		return AgentTypeCore
	}
	return AgentTypeCustom
}

// IsWorker reports whether this agent is a sub-agent worker (Type==worker).
//
// Worker is an EXPLICIT classification — it is only ever set via the Type field
// (workers are not inferred from an ID list), so the check does not need the
// isCoreAgent resolver and is safe to call without it. A worker is a
// delegation-only labor tier: never a chat target, never the routing default,
// no heartbeat. See AgentTypeWorker.
func (a AgentConfig) IsWorker() bool {
	return a.Type == AgentTypeWorker
}

// IsSystem reports whether this agent is a System Agent (Type==system, ADR-049
// D3) — a seeded, locked, non-privileged internal-LLM agent (e.g. the Judge)
// that executes as a no-tools structured call. System Agents are NOT chat
// targets and are excluded from default-fallback, routing bindings, delegation
// pickers, and team rosters. Like IsWorker, this is an EXPLICIT classification
// carried only via the Type field (System Agents are never inferred from an ID
// list), so the check is safe to call without the isCoreAgent resolver.
func (a AgentConfig) IsSystem() bool {
	return a.Type == AgentTypeSystem
}

// IsChatTarget reports whether this agent may receive inbound channel messages
// and be resolved as the default/routing agent. Every agent kind is a chat
// target EXCEPT a worker and a System Agent. Routing (resolveDefaultAgentID,
// first-enabled fallback) and the default-agent setter/repair use this to
// exclude workers; System Agents are excluded for the same reason (ADR-049 D3):
// the Judge is an out-of-turn internal-LLM agent, never a live persona a user
// can address.
func (a AgentConfig) IsChatTarget() bool {
	return !a.IsWorker() && !a.IsSystem()
}

// IsExternalCLIWorker reports whether this agent is a subagent_3p — a worker
// that delegates to an external CLI tool (claude-code, codex, opencode, …)
// rather than running on the native Omnipus agent engine.
//
// The predicate is true when BOTH conditions hold:
//  1. Type == AgentTypeWorker (IsWorker() is true).
//  2. Subagents.Executor.Kind == ExecutorKindExternalCLI ("external-cli").
//
// Subagent_3p agents run on a separate engine and their token usage is not
// tracked through Omnipus's provider layer, so they must be excluded from
// token aggregation reports.  This is the single authoritative implementation;
// both rest_stats.go and the get_usage sysagent tool delegate to it via
// (*Config).IsExternalCLIWorkerID.
func (a AgentConfig) IsExternalCLIWorker() bool {
	return a.IsWorker() &&
		a.Subagents != nil &&
		a.Subagents.Executor != nil &&
		a.Subagents.Executor.Kind == ExecutorKindExternalCLI
}

// IsExternalCLIWorkerID reports whether the agent with the given ID is a
// subagent_3p (external CLI worker).  Returns false when agentID is empty,
// when cfg is nil, or when no agent with that ID exists in the config list.
//
// This is the lookup variant used by callers that have a *Config and an agent
// ID string (rest_stats.go, the get_usage sysagent tool) so that neither
// caller needs to inline the two-condition predicate.
func (c *Config) IsExternalCLIWorkerID(agentID string) bool {
	if c == nil || agentID == "" {
		return false
	}
	for i := range c.Agents.List {
		if c.Agents.List[i].ID == agentID {
			return c.Agents.List[i].IsExternalCLIWorker()
		}
	}
	return false
}

// DelegationMode is the mode in which delegation is allowed.
// "await" = synchronous subagent (blocks caller).
// "background" = async spawn (caller continues).
// "task" = task_create delegation.
type DelegationMode string

const (
	DelegationModeAwait      DelegationMode = "await"
	DelegationModeBackground DelegationMode = "background"
	DelegationModeTask       DelegationMode = "task"
)

// AgentRefKind enumerates the legal values for AgentRef.Kind.
//
// AgentRef.Validate() checks a non-empty value against this set, but ADR-037
// removed AgentRef's last production caller (AgentConfig.DelegationPolicy /
// AgentDefaults.DelegationPolicy no longer exist, and nothing else in the
// runtime constructs a user-supplied AgentRef) — Validate now has no
// production caller at all; it is exercised only by TestAgentRef_Validate.
// AgentRef itself survives as coreagent's compile-time seed-DTO shape
// (config.DelegationPolicy.To — see coreagent.SeedDelegationEdges), which is
// hardcoded Go data, not user input, so there is nothing left to validate at
// load time.
const (
	// AgentRefKindLocal resolves the ref by id within the running instance.
	AgentRefKindLocal = "local"
	// AgentRefKindRemoteA2A is reserved for the future A2A protocol; the kind is
	// accepted by validation but not enforced/dispatched in v0.1.0.
	AgentRefKindRemoteA2A = "remote-a2a"
)

// AgentRef is an agent reference used in delegation policy targets.
// Kind is currently "local" (resolved by id within the instance) or
// "remote-a2a" (reserved for future A2A protocol; not enforced in v0.1.0).
// The id "*" is a wildcard matching any agent of the given kind.
type AgentRef struct {
	Kind string `json:"kind"` // "local" or "remote-a2a"
	ID   string `json:"id"`
}

// Validate rejects a non-empty AgentRef.Kind that is outside the known set.
// An empty Kind is accepted for back-compat: callers default an absent kind to
// "local". Only a present-but-unknown value (a typo) is an error, so it fails
// loudly rather than silently downgrading routing.
//
// The Kind is canonicalized (lowercased + trimmed) BEFORE the membership check
// so Validate accepts exactly what the API write path and route.go accept —
// both normalize the kind the same way. Validating the raw value would reject a
// mixed-case/whitespace payload (e.g. {"kind":"Local"}) that the API gate let
// through and that routes fine, bricking the very next config load. Genuinely-
// unknown values (e.g. "robot") are still rejected.
func (r AgentRef) Validate() error {
	switch strings.ToLower(strings.TrimSpace(r.Kind)) {
	case "", AgentRefKindLocal, AgentRefKindRemoteA2A:
		return nil
	default:
		return fmt.Errorf("invalid agent ref kind %q (want %q or %q)",
			r.Kind, AgentRefKindLocal, AgentRefKindRemoteA2A)
	}
}

// DelegationPolicy is a plain seed DTO for a core agent's bootstrap delegation
// edges (ADR-037). It is NO LONGER attached to AgentConfig or AgentDefaults —
// the per-workspace delegation graph (pkg/workspace/delegation.go) is the sole
// runtime delegation-enforcement mechanism. This type survives only as the
// shape coreagent.SeedDelegationEdges returns, consumed by
// pkg/gateway/rest_workspace_delegation.go's defaultWorkspaceDelegationEdges to
// bootstrap a fresh workspace's delegation graph. AcceptFrom and Budget (the
// pre-ADR-037 schema's inert, never-enforced fields) are dropped — they carried
// no seed data even before this removal.
type DelegationPolicy struct {
	// To is the list of agent references this agent may delegate work to.
	To []AgentRef `json:"to,omitempty"`

	// Modes lists allowed delegation modes for the seeded edges.
	Modes []DelegationMode `json:"modes,omitempty"`

	// Depth is the maximum delegation chain depth for the seeded edges.
	// Zero means uncapped (nil pointer = uncapped).
	Depth *int `json:"depth,omitempty"`
}

// ExecutorKind enumerates the runtime used to execute a sub-agent task.
// "native" runs the task inside the existing Omnipus agent loop (default, existing behavior).
//
//   - "external-cli" is ACTIVE in v0.1.0: it drives an external CLI tool (Claude
//     Code, Codex, opencode) over JSON streaming. Each run is git-worktree-isolated
//     and executes under the external CLI's OWN sandbox (Codex = Landlock/seccomp +
//     Seatbelt; Claude Code = its permission model) — Omnipus adds no new confiner.
//     The CLI's permission prompts route to the Omnipus consent layer best-effort
//     post-hoc (the CLI's own sandbox + the worktree are the authoritative boundary
//     — see pkg/agent/runner/consent.go's POST-HOC CONSENT note).
//   - "remote-a2a" is RESERVED for future A2A protocol resolution: accepted in the
//     schema for forward-compatibility, rejected at dispatch in v0.1.0.
type ExecutorKind string

const (
	// ExecutorKindNative is the default: sub-agent runs inside the Omnipus agent loop.
	ExecutorKindNative ExecutorKind = "native"
	// ExecutorKindExternalCLI drives an external CLI agent (claude-code, codex,
	// opencode) over a JSON-streaming subprocess. ACTIVE in v0.1.0: dispatch resolves
	// it to runner.DispatchKindExternalCLI and runs it worktree-isolated under the
	// CLI's own sandbox (consent best-effort post-hoc — see consent.go).
	ExecutorKindExternalCLI ExecutorKind = "external-cli"
	// ExecutorKindRemoteA2A is reserved. Accepted in schema; rejected at dispatch in v0.1.0.
	ExecutorKindRemoteA2A ExecutorKind = "remote-a2a"
)

// ExecutorConfig specifies how a sub-agent's tasks are executed.
// When nil (the default), behavior is identical to Kind="native".
//
// Kind="native" and Kind="external-cli" are both functional in v0.1.0;
// Kind="remote-a2a" is RESERVED and rejected at dispatch (see ExecutorKind and
// runner.ResolveDispatch).
type ExecutorConfig struct { // not-wire-format
	// Kind selects the execution runtime. Defaults to "native" when absent.
	Kind ExecutorKind `json:"kind"`
	// CLI names the external CLI tool for Kind="external-cli".
	// Valid values: "claude-code", "codex", "opencode". Required when
	// Kind="external-cli"; ignored otherwise.
	// Locked after create (agent-form spec §4.16 / W3 F-10) — to switch CLIs,
	// create a new agent. Mutable on the wire via PUT is rejected at the handler.
	CLI string `json:"cli,omitempty"`
	// CLIPath is the absolute filesystem path to the CLI binary. Required when
	// Kind="external-cli" (per agent-form spec §4.17 / W3). Mutable on PUT (allows
	// upgrading the CLI binary without re-creating the agent). Empty means the
	// OS $PATH is used — fragile when multiple CLI versions are installed.
	CLIPath string `json:"cli_path,omitempty"`
	// EnvOverrides are additional environment variables merged into the spawned
	// CLI process's environment. Omnipus-internal env vars (OMNIPUS_*, the
	// master-key vars) are NOT overridable; user-supplied keys take precedence
	// only for non-Omnipus vars (per agent-form spec §4.18 / W3).
	EnvOverrides map[string]string `json:"env_overrides,omitempty"`
	// CLIArgs is free-form additional CLI arguments appended to the spawn
	// invocation. The spawn layer uses execve (no shell interpolation), so
	// values are passed safely; warn (but do not reject) on shell-injection
	// chars in the value (per agent-form spec §4.19 / W3).
	CLIArgs string `json:"cli_args,omitempty"`
}

// EffectiveKind returns the ExecutorKind with nil-safe defaulting to native.
func (ec *ExecutorConfig) EffectiveKind() ExecutorKind {
	if ec == nil || ec.Kind == "" {
		return ExecutorKindNative
	}
	return ec.Kind
}

type SubagentsConfig struct {
	AllowAgents []string          `json:"allow_agents,omitempty"`
	Model       *AgentModelConfig `json:"model,omitempty"`
	Executor    *ExecutorConfig   `json:"executor,omitempty"`
}

type PeerMatch struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type BindingMatch struct {
	Channel   string     `json:"channel"`
	AccountID string     `json:"account_id,omitempty"`
	Peer      *PeerMatch `json:"peer,omitempty"`
	GuildID   string     `json:"guild_id,omitempty"`
	TeamID    string     `json:"team_id,omitempty"`
}

type AgentBinding struct {
	AgentID string       `json:"agent_id"`
	Match   BindingMatch `json:"match"`
}

type SessionConfig struct {
	DMScope       string              `json:"dm_scope,omitempty"`
	IdentityLinks map[string][]string `json:"identity_links,omitempty"`

	// StatsFlushInterval controls how often UnifiedStore's periodic flusher
	// persists a dirty session's stats.json to disk when no forced flush
	// point (SetMeta/DeleteSession/Close/teardown) has fired (ADR-057 FR-067,
	// promoted from the spec's only SHOULD to a MUST — grill m-5, operator
	// decision 2). Zero means "unset" — EffectiveStatsFlushInterval
	// substitutes DefaultSessionStatsFlushInterval (5s). Accepts a JSON
	// string ("5s", "10s") or a bare number interpreted as seconds, mirroring
	// SessionMessagingConfig's duration fields (CancelGrace, NeedsInputTTL).
	// Owned by U28 (pkg/config/**); U6 (pkg/session/unified.go) reads it —
	// U28 does not wire it into the store.
	StatsFlushInterval duration `json:"stats_flush_interval,omitempty"`
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

// RoutingConfig controls the intelligent model routing feature.
// When enabled, each incoming message is scored against structural features
// (message length, code blocks, tool call history, conversation depth, attachments).
// Messages scoring below Threshold are sent to LightModel; all others use the
// agent's primary model. This reduces cost and latency for simple tasks without
// requiring any keyword matching — all scoring is language-agnostic.
type RoutingConfig struct {
	Enabled    bool    `json:"enabled"`
	LightModel string  `json:"light_model"` // model_name from model_list to use for simple tasks
	Threshold  float64 `json:"threshold"`   // complexity score in [0,1]; score >= threshold → primary model
}

// SubTurnConfig configures the SubTurn execution system.
type SubTurnConfig struct {
	MaxDepth int `json:"max_depth"      env:"OMNIPUS_AGENTS_DEFAULTS_SUBTURN_MAX_DEPTH"`
	// MaxConcurrent is an OPTIONAL per-delegation override of the concurrent
	// fan-out cap. Both consumers below apply the SAME rule (concurrency-gate
	// consolidation, 2026-08-04): Performance.EffectiveMaxParallelAgents() —
	// the single, UI-configurable authority for agent concurrency
	// (PerformanceSettings.max_parallel_agents) — is resolved LIVE whenever
	// this field is <= 0 (unset, the shipped default: see DefaultConfig,
	// defaults.go). A positive value here is an explicit, deliberate
	// per-delegation override, honored exactly as configured — it may differ
	// from the central value in either direction, an operator's own choice,
	// never silently overridden. A negative value is a configuration error.
	//   - getSubTurnConfig (pkg/agent/subturn.go) uses it, when > 0, as the
	//     per-parent-turn in-turn fan-out semaphore, falling back to
	//     Performance.EffectiveMaxParallelAgents() when <= 0.
	//   - The W17 root-delegation admission gate (pkg/agent/admission.go,
	//     ResolveRootDelegationCap) reads this field DIRECTLY and applies the
	//     identical fallback, so the two consumers can never disagree about
	//     what "unset" means.
	//
	// HISTORY (superseded 2026-08-04, commit 536b7340's follow-up fix): this
	// field used to be seeded to a fixed 16 (the retired
	// DefaultSubTurnMaxConcurrent constant) specifically so the root gate
	// would never take the EffectiveMaxParallelAgents() fallback branch —
	// reasoning that depended entirely on that function ALSO being
	// hard-clamped to 16 by clampParallelExplicit at the time, making the two
	// numbers coincidentally equal. Commit 536b7340 removed that ceiling
	// (clampParallelExplicit now only floors at 1), which invalidated the
	// premise: the fixed seed became a SECOND, independently-sized cap that
	// silently disagreed with an operator's own max_parallel_agents setting
	// once the two diverged — the exact ADR-037 "control that moves,
	// persists and governs nothing" anti-pattern this project bans. The seed
	// is removed; a fresh install now leaves this field at its Go zero value
	// (0) so both consumers take the central-authority branch by design.
	MaxConcurrent         int `json:"max_concurrent"          env:"OMNIPUS_AGENTS_DEFAULTS_SUBTURN_MAX_CONCURRENT"`
	DefaultTimeoutMinutes int `json:"default_timeout_minutes" env:"OMNIPUS_AGENTS_DEFAULTS_SUBTURN_DEFAULT_TIMEOUT_MINUTES"`
	DefaultTokenBudget    int `json:"default_token_budget"    env:"OMNIPUS_AGENTS_DEFAULTS_SUBTURN_DEFAULT_TOKEN_BUDGET"`
	ConcurrencyTimeoutSec int `json:"concurrency_timeout_sec" env:"OMNIPUS_AGENTS_DEFAULTS_SUBTURN_CONCURRENCY_TIMEOUT_SEC"`
}

type ToolFeedbackConfig struct {
	Enabled       bool `json:"enabled"         env:"OMNIPUS_AGENTS_DEFAULTS_TOOL_FEEDBACK_ENABLED"`
	MaxArgsLength int  `json:"max_args_length" env:"OMNIPUS_AGENTS_DEFAULTS_TOOL_FEEDBACK_MAX_ARGS_LENGTH"`
}

type AgentDefaults struct {
	Home string `json:"workspace" env:"OMNIPUS_AGENTS_DEFAULTS_WORKSPACE"`
	// RestrictToWorkspace and AllowReadOutsideWorkspace stay OUT of the v1
	// JSON schema at THIS path (FR-001): the tags are json:"-" so SaveConfig
	// never serializes them here, and validateRemovedKeys still rejects any
	// config JSON that carries `agents.defaults.restrict_to_workspace` or
	// `agents.defaults.allow_read_outside_workspace`. That contract is
	// unchanged and is pinned by TestFR001_RemovedKeysRejected.
	//
	// These are NOT dead fields. They are the values every file tool, the
	// bash tool, send_file and browser_screenshot are actually built with
	// (pkg/agent/instance.go, pkg/agent/loop.go). RestrictToWorkspace in
	// particular is the ADR-068 "third rule layer": an in-process path guard
	// that runs before any child process exists and is completely separate
	// from the kernel sandbox (`sandbox.mode`).
	//
	// WHAT CHANGED (ADR-068 §6, 2026-08-23). RestrictToWorkspace used to be
	// reachable ONLY through OMNIPUS_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE,
	// described here as "an intentional ops escape hatch". That was the
	// wrong trade and it produced a real defect: an operator saw the sandbox
	// switch on the Security page, turned it off, and commands were still
	// refused — by this setting, which had no control anywhere. An
	// unexplained denial is what drives operators to disable the whole
	// boundary instead of the one rule that is in their way.
	//
	// RestrictToWorkspace now has an operator-facing control:
	// `sandbox.workspace_path_guard` (OmnipusSandboxConfig.WorkspacePathGuard,
	// sandbox.go). applyWorkspacePathGuard (validator.go) resolves it into
	// this field at load time, so all five read sites keep reading exactly
	// one field and can never disagree about the answer.
	//
	// THE TRADE-OFF, stated plainly, because it is a real one: the new key
	// is a DIFFERENT JSON path from the removed one. Two spellings for one
	// behaviour is a cost. We pay it because the alternatives are worse:
	//
	//   - Giving this field a real JSON tag would make SaveConfig write
	//     `agents.defaults.restrict_to_workspace` back into config.json, and
	//     loadConfigInternal would then REJECT that same file on the next
	//     boot (validateRemovedKeys runs before unmarshal). The gateway
	//     re-saves config on load, so a single boot would brick the install.
	//   - Relaxing validateRemovedKeys would repeal a documented, tested
	//     contract in order to reinstate a key name whose meaning is
	//     simultaneously being narrowed (spec FR-2.5) — the same wire name
	//     with new semantics, which is exactly what that spec warns against.
	//   - `agents.defaults.*` is writable through the generic
	//     PUT /api/v1/config endpoint and through the sysagent's
	//     `system.config.set` tool, neither of which is re-auth-gated or
	//     audited. A security control there could be switched off by an
	//     agent itself. Everything under `sandbox.` is blocked from both.
	//
	// AllowReadOutsideWorkspace deliberately gets NO new control in this
	// change. It is the read-side companion, and ADR-068 step 2 (splitting
	// read from write inside the bash guard) has to land before a read-side
	// switch would mean what its label says. Until then it stays env-only —
	// documented here so the asymmetry is a decision, not an oversight.
	//
	// The env vars remain, and still WIN over the config key, so an operator
	// locked out by a bad config value can always recover from the shell.
	RestrictToWorkspace       bool `json:"-"                               env:"OMNIPUS_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE"`
	AllowReadOutsideWorkspace bool `json:"-"                               env:"OMNIPUS_AGENTS_DEFAULTS_ALLOW_READ_OUTSIDE_WORKSPACE"`
	// DefaultModel is the global default model as an exact (provider, model)
	// PAIR (ADR-068 D14.1 / FR-018; contract DefaultModel.yaml is both the
	// persisted shape and the GET body). It replaces the deleted model_name
	// alias (+ the separate provider field): an alias was resolved by
	// GetModelConfig → findMatches against each providers[] row's single
	// model_name, so a (provider, model) selection had nowhere to land.
	// GetModelConfig(pair) resolves it exactly. A fresh install leaves it at
	// the zero value (FR-040) — onboarding's explicit pick and the
	// default-model PUT are its only writers; no boot/reload path may back-fill
	// it (FR-020).
	DefaultModel        DefaultModel `json:"default_model"`
	ModelFallbacks      []string     `json:"model_fallbacks,omitempty"`
	ImageModel          string       `json:"image_model,omitempty"           env:"OMNIPUS_AGENTS_DEFAULTS_IMAGE_MODEL"`
	ImageModelFallbacks []string     `json:"image_model_fallbacks,omitempty"`
	MaxTokens           int          `json:"max_tokens"                      env:"OMNIPUS_AGENTS_DEFAULTS_MAX_TOKENS"`
	// context_window was deleted by ADR-066 D2 (FR-004, T066-09). The global
	// default lives in ContextSettings.DefaultContextWindow (the `context`
	// key, D9) and every agent's window is resolved by the D2 ladder
	// (pkg/agent ResolveWindow) — there is no agents.defaults.context_window
	// key and no matching env var. A stale key in an operator's config.json
	// is ignored (greenfield, no migration).
	Temperature       *float64 `json:"temperature,omitempty"           env:"OMNIPUS_AGENTS_DEFAULTS_TEMPERATURE"`
	MaxToolIterations int      `json:"max_tool_iterations"             env:"OMNIPUS_AGENTS_DEFAULTS_MAX_TOOL_ITERATIONS"`
	// summarize_token_percent was deleted by ADR-066 D6 (FR-004, T066-03).
	// The legacy summariser's percentage knob had outlived ADR-028 only to
	// scale the timeout-recovery trim trigger; every consumer now reads the
	// one budget B (pkg/agent/context_budget.go::contextBudget). A stale key
	// in config.json is ignored (greenfield rule: no migration, no rejection).
	MaxMediaSize   int                `json:"max_media_size,omitempty"        env:"OMNIPUS_AGENTS_DEFAULTS_MAX_MEDIA_SIZE"`
	Routing        *RoutingConfig     `json:"routing,omitempty"`
	SteeringMode   string             `json:"steering_mode,omitempty"         env:"OMNIPUS_AGENTS_DEFAULTS_STEERING_MODE"` // "one-at-a-time" (default) or "all"
	SubTurn        SubTurnConfig      `json:"subturn"`
	ToolFeedback   ToolFeedbackConfig `json:"tool_feedback,omitempty"`
	SplitOnMarker  bool               `json:"split_on_marker"                 env:"OMNIPUS_AGENTS_DEFAULTS_SPLIT_ON_MARKER"` // split messages on <|[SPLIT]|> marker
	TimeoutSeconds int                `json:"timeout_seconds"                 env:"OMNIPUS_AGENTS_DEFAULTS_TIMEOUT_SECONDS"` // per-turn timeout in seconds; 0 = disabled
	DefaultAgentID string             `json:"default_agent_id,omitempty"      env:"OMNIPUS_DEFAULT_AGENT_ID"`

	// AutoRecapEnabled gates the session-end recap pipeline (FR-033).
	// When false, CloseSession is a no-op and no background LLM calls are made.
	// Opt-in by design: recaps cost money.
	AutoRecapEnabled bool `json:"auto_recap_enabled" env:"OMNIPUS_AGENTS_DEFAULTS_AUTO_RECAP_ENABLED"`

	// IdleTimeoutMinutes drives the per-session idle ticker that triggers a
	// recap when the user disappears without explicitly closing. 0 → 30.
	// Spec v7 FR-035.
	IdleTimeoutMinutes int `json:"idle_timeout_minutes" env:"OMNIPUS_AGENTS_DEFAULTS_IDLE_TIMEOUT_MINUTES"`

	// BootstrapRecapEnabled is a SECOND opt-in specifically for the boot-time
	// pass that sweeps orphaned sessions and generates a recap for each. Split
	// from AutoRecapEnabled because a boot burst has a different risk profile
	// (unbounded cost amplification). FR-032a / MAJ-007. Default false.
	BootstrapRecapEnabled bool `json:"bootstrap_recap_enabled" env:"OMNIPUS_AGENTS_DEFAULTS_BOOTSTRAP_RECAP_ENABLED"`

	// BootstrapRecapMaxPerMinute rate-limits the bootstrap pass. Default 5.
	BootstrapRecapMaxPerMinute int `json:"bootstrap_recap_max_per_minute" env:"OMNIPUS_AGENTS_DEFAULTS_BOOTSTRAP_RECAP_MAX_PER_MINUTE"`

	// RecapModel is the model slug used for session recap / summarisation. A
	// fast, cheap model is recommended. Empty → falls back to GetModelName()
	// (the overall default model), then to the session's own agent model.
	RecapModel string `json:"recap_model,omitempty" env:"OMNIPUS_AGENTS_DEFAULTS_RECAP_MODEL"`

	// RecapFallbackModels is the ordered fallback chain for the recap model,
	// tried in order when the primary recap model fails — same shape and
	// behavior as an agent's FallbackModels field. Each entry carries its
	// own Provider so the fallback can route through a different provider than
	// the primary.
	RecapFallbackModels FallbackModelSlice `json:"recap_fallback_models,omitempty"`
}

// GetIdleTimeoutMinutes returns the idle timeout, defaulting to 30.
func (d *AgentDefaults) GetIdleTimeoutMinutes() int {
	if d.IdleTimeoutMinutes <= 0 {
		return 30
	}
	return d.IdleTimeoutMinutes
}

// GetBootstrapRecapMaxPerMinute returns the rate limit, defaulting to 5.
func (d *AgentDefaults) GetBootstrapRecapMaxPerMinute() int {
	if d.BootstrapRecapMaxPerMinute <= 0 {
		return 5
	}
	return d.BootstrapRecapMaxPerMinute
}

const DefaultMaxMediaSize = 20 * 1024 * 1024 // 20 MB

func (d *AgentDefaults) GetMaxMediaSize() int {
	if d.MaxMediaSize > 0 {
		return d.MaxMediaSize
	}
	return DefaultMaxMediaSize
}

// GetToolFeedbackMaxArgsLength returns the max args preview length for tool feedback messages.
func (d *AgentDefaults) GetToolFeedbackMaxArgsLength() int {
	if d.ToolFeedback.MaxArgsLength > 0 {
		return d.ToolFeedback.MaxArgsLength
	}
	return 300
}

// IsToolFeedbackEnabled returns true when tool feedback messages should be sent to the chat.
func (d *AgentDefaults) IsToolFeedbackEnabled() bool {
	return d.ToolFeedback.Enabled
}

// DefaultModel is the persisted (provider, model) pair at
// agents.defaults.default_model — the shape of contract DefaultModel.yaml
// minus the window fields ADR-066's ResolveWindow projects onto the GET body.
// Provider is the providers[] row's routing key (catalog id or custom row id);
// Model is that row's exact model id. An empty Model means "no default model"
// (IsZero). The default-model PUT (FR-018) requires both halves; GetModelConfig
// matches both halves exactly, an empty Provider included — it never widens
// to "any provider serving this model".
type DefaultModel struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// IsZero reports whether no default model is set (no model half).
func (d DefaultModel) IsZero() bool {
	return strings.TrimSpace(d.Model) == ""
}

// String renders the pair as "provider/model" ("model" alone when the
// provider half is empty) for logs and error text.
func (d DefaultModel) String() string {
	if d.IsZero() {
		return ""
	}
	if p := strings.TrimSpace(d.Provider); p != "" {
		return p + "/" + strings.TrimSpace(d.Model)
	}
	return strings.TrimSpace(d.Model)
}

// ChannelIdentityKind enumerates the legal values for ChannelIdentity.Kind.
// route.go treats ONLY "agent" as the agent-routing path and everything else as
// the user fallback; without validation a typo (e.g. "agnet") would silently
// downgrade to user routing. A non-empty value outside this set is therefore
// REJECTED at config-load time (see ChannelIdentity.Validate).
const (
	// ChannelIdentityKindAgent routes the inbound connection AS the given agent ID.
	ChannelIdentityKindAgent = "agent"
	// ChannelIdentityKindUser attributes the inbound connection to the user and
	// routes via the normal binding cascade to the default agent.
	ChannelIdentityKindUser = "user"
)

// ChannelIdentity identifies whether an inbound connection acts on behalf of an
// agent ("agent" kind) or routes as the default user ("user" kind). Persisted per
// instance; wired into ResolveRoute input (Spec-2 FR-2.9 / US-5).
type ChannelIdentity struct {
	Kind string `json:"kind"`         // "agent" or "user"
	ID   string `json:"id,omitempty"` // agent ID when kind=agent; empty when kind=user
}

// Validate rejects a non-empty ChannelIdentity.Kind outside the known set so a
// typo fails loudly at load instead of silently routing as the user. An empty
// Kind is accepted for back-compat — route.go's documented default for an
// absent/empty identity kind is the user-routing fallback. When kind=agent the
// id MUST be present (an agent identity with no id can never resolve).
//
// The Kind is canonicalized (lowercased + trimmed) BEFORE the membership check
// so Validate accepts exactly what the API write path (validateChannelIdentity
// in pkg/gateway/rest.go) and route.go accept — both normalize the kind the same
// way. Validating the raw value would reject a mixed-case/whitespace payload
// (e.g. {"kind":"Agent"}) that the API gate let through and that routes fine,
// bricking the very next config load. Genuinely-unknown values (e.g. "robot")
// are still rejected.
func (i ChannelIdentity) Validate() error {
	switch strings.ToLower(strings.TrimSpace(i.Kind)) {
	case "", ChannelIdentityKindUser:
		return nil
	case ChannelIdentityKindAgent:
		if strings.TrimSpace(i.ID) == "" {
			return fmt.Errorf("channel identity kind %q requires a non-empty id", i.Kind)
		}
		return nil
	default:
		return fmt.Errorf("invalid channel identity kind %q (want %q or %q)",
			i.Kind, ChannelIdentityKindAgent, ChannelIdentityKindUser)
	}
}

// ChannelInstanceConfig is the map value for Config.Channels.
// The map is keyed by instance ID (a bare type name like "telegram" for the
// default instance, or a namespaced key like "telegram.eu" for additional
// instances — multiple instances per type are supported). Each instance carries
// its type discriminator, the common enabled flag, an optional identity
// override, and the full union of all per-channel-type configuration fields —
// only the fields relevant to the instance type are non-zero.
//
// Wire-format note: this struct is NOT a gateway wire type — it lives in config.json,
// not in REST request/response bodies. The not-wire-format opt-out is not needed here
// because config structs are exempt from the gateway wire-format lint rule.
type ChannelInstanceConfig struct {
	// Common fields.
	Type     string           `json:"type"`
	Enabled  bool             `json:"enabled"`
	Identity *ChannelIdentity `json:"identity,omitempty"`
	// WorkspaceID binds this instance to one workspace (ADR-029). Empty = unbound.
	WorkspaceID string `json:"workspace_id,omitempty"`

	// --- Common per-channel fields shared across multiple channel types ---
	AllowFrom          FlexibleStringSlice `json:"allow_from,omitempty"`
	GroupTrigger       GroupTriggerConfig  `json:"group_trigger,omitempty"`
	Typing             TypingConfig        `json:"typing,omitempty"`
	Placeholder        PlaceholderConfig   `json:"placeholder,omitempty"`
	ReasoningChannelID string              `json:"reasoning_channel_id,omitempty"`
	Proxy              string              `json:"proxy,omitempty"`
	// token_ref is used by Telegram (bot token ref) and Weixin (account token ref).
	TokenRef string `json:"token_ref,omitempty"`
	// base_url is used by Telegram (API base URL) and Weixin (WeChat base URL).
	BaseURL string `json:"base_url,omitempty"`

	// --- WhatsApp-specific fields ---
	SessionStorePath string `json:"session_store_path,omitempty"`

	// --- Telegram-specific fields ---
	Streaming     StreamingConfig `json:"streaming,omitempty"`
	UseMarkdownV2 bool            `json:"use_markdown_v2,omitempty"`

	// --- Feishu / Lark-specific fields ---
	AppID                string              `json:"app_id,omitempty"`
	AppSecretRef         string              `json:"app_secret_ref,omitempty"`
	EncryptKeyRef        string              `json:"encrypt_key_ref,omitempty"`
	VerificationTokenRef string              `json:"verification_token_ref,omitempty"`
	RandomReactionEmoji  FlexibleStringSlice `json:"random_reaction_emoji,omitempty"`
	IsLark               bool                `json:"is_lark,omitempty"`

	// --- Discord-specific fields ---
	// TokenRef (see common above) is also used by Discord.
	// Proxy (see common above) is also used by Discord.
	MentionOnly bool `json:"mention_only,omitempty"`

	// --- QQ-specific fields ---
	// AppID (see Feishu above) is also used by QQ.
	MaxMessageLength     int   `json:"max_message_length,omitempty"`
	MaxBase64FileSizeMiB int64 `json:"max_base64_file_size_mib,omitempty"`
	SendMarkdown         bool  `json:"send_markdown,omitempty"`

	// --- DingTalk-specific fields ---
	ClientID        string `json:"client_id,omitempty"`
	ClientSecretRef string `json:"client_secret_ref,omitempty"`

	// --- Slack-specific fields ---
	BotTokenRef string `json:"bot_token_ref,omitempty"`
	AppTokenRef string `json:"app_token_ref,omitempty"`

	// --- Matrix-specific fields ---
	Homeserver          string `json:"homeserver,omitempty"`
	UserID              string `json:"user_id,omitempty"`
	AccessTokenRef      string `json:"access_token_ref,omitempty"`
	DeviceID            string `json:"device_id,omitempty"`
	JoinOnInvite        bool   `json:"join_on_invite,omitempty"`
	MessageFormat       string `json:"message_format,omitempty"`
	CryptoDatabasePath  string `json:"crypto_database_path,omitempty"`
	CryptoPassphraseRef string `json:"crypto_passphrase_ref,omitempty"`

	// --- LINE-specific fields ---
	ChannelSecretRef      string `json:"channel_secret_ref,omitempty"`
	ChannelAccessTokenRef string `json:"channel_access_token_ref,omitempty"`
	WebhookHost           string `json:"webhook_host,omitempty"`
	WebhookPort           int    `json:"webhook_port,omitempty"`
	WebhookPath           string `json:"webhook_path,omitempty"`

	// --- WeCom-specific fields ---
	BotID               string                      `json:"bot_id,omitempty"`
	SecretRef           string                      `json:"secret_ref,omitempty"`
	WebSocketURL        string                      `json:"websocket_url,omitempty"`
	SendThinkingMessage bool                        `json:"send_thinking_message,omitempty"`
	DMPolicy            string                      `json:"dm_policy,omitempty"`
	GroupPolicy         string                      `json:"group_policy,omitempty"`
	GroupAllowFrom      FlexibleStringSlice         `json:"group_allow_from,omitempty"`
	Groups              map[string]WeComGroupConfig `json:"groups,omitempty"`

	// --- Weixin-specific fields ---
	// TokenRef (see common above) is also used by Weixin.
	// BaseURL (see common above) is also used by Weixin.
	// Proxy (see common above) is also used by Weixin.
	AccountID  string `json:"account_id,omitempty"`
	CDNBaseURL string `json:"cdn_base_url,omitempty"`

	// --- IRC-specific fields ---
	Server              string              `json:"server,omitempty"`
	TLS                 bool                `json:"tls,omitempty"`
	Nick                string              `json:"nick,omitempty"`
	IRCUser             string              `json:"user,omitempty"`
	RealName            string              `json:"real_name,omitempty"`
	PasswordRef         string              `json:"password_ref,omitempty"`
	NickServPasswordRef string              `json:"nickserv_password_ref,omitempty"`
	SASLUser            string              `json:"sasl_user,omitempty"`
	SASLPasswordRef     string              `json:"sasl_password_ref,omitempty"`
	IRCChannels         FlexibleStringSlice `json:"channels,omitempty"`
	RequestCaps         FlexibleStringSlice `json:"request_caps,omitempty"`

	// --- Google Chat-specific fields ---
	Mode                  string       `json:"mode,omitempty"`
	WebhookURL            SecureString `json:"webhook_url,omitzero"`
	WebhookURLRef         string       `json:"webhook_url_ref,omitempty"`
	ServiceAccountFile    string       `json:"service_account_file,omitempty"`
	ServiceAccountJSON    SecureString `json:"service_account_json,omitzero"`
	ServiceAccountJSONRef string       `json:"service_account_json_ref,omitempty"`
	Space                 string       `json:"space,omitempty"`
	BotUser               string       `json:"bot_user,omitempty"`

	// --- Email-specific fields ---
	// IMAPHost is the IMAP server hostname. TLS-only (port 993).
	IMAPHost string `json:"imap_host,omitempty"`
	// IMAPPort is the IMAP server port. Defaults to 993.
	IMAPPort int `json:"imap_port,omitempty"`
	// SMTPHost is the SMTP server hostname for outbound email. TLS-only.
	SMTPHost string `json:"smtp_host,omitempty"`
	// SMTPPort is the SMTP server port. Defaults to 587 (STARTTLS).
	SMTPPort int `json:"smtp_port,omitempty"`
	// EmailUsername is the login username for IMAP and SMTP authentication.
	// Note: "password_ref" is reused from the IRC-specific PasswordRef field above.
	// Both IRC and email use the same password_ref JSON key for their connection
	// password, which is stored encrypted in the credential store.
	EmailUsername string `json:"username,omitempty"`
}

// ErrHalfBoundChannelInstance is the sentinel returned by ValidateChannels when
// a channel instance carries a non-empty WorkspaceID but is missing the agent
// identity required to fully bind it (Identity == nil, or Identity.Kind != "agent",
// or Identity.ID == ""). A half-bound state is an operator error: the instance
// would appear to be workspace-bound but routing would silently fall back to
// unbound behavior (ADR-029 FR-029). Fail-loud at load matches the existing
// type-vs-key mismatch error contract.
var ErrHalfBoundChannelInstance = errors.New(
	"channels: half-bound workspace instance (workspace_id set but agent identity missing or invalid)",
)

// IsWorkspaceBound reports whether this instance is fully workspace-bound per
// ADR-029 FR-029: WorkspaceID is non-empty AND Identity is non-nil AND
// Identity.Kind == "agent" AND Identity.ID is non-empty. The routing layer
// (pkg/routing/route.go) should set BoundInstance=true only when this predicate
// is true. Call sites that just want to know "is this bound?" should prefer this
// predicate over checking the individual fields directly.
func (c ChannelInstanceConfig) IsWorkspaceBound() bool {
	if strings.TrimSpace(c.WorkspaceID) == "" {
		return false
	}
	if c.Identity == nil {
		return false
	}
	if strings.ToLower(strings.TrimSpace(c.Identity.Kind)) != ChannelIdentityKindAgent {
		return false
	}
	return strings.TrimSpace(c.Identity.ID) != ""
}

// knownChannelTypes is the set of supported channel type identifiers.
// Any map entry whose key is not in this set is logged as WARN and dropped.
var knownChannelTypes = map[string]struct{}{
	"telegram":    {},
	"discord":     {},
	"slack":       {},
	"whatsapp":    {},
	"feishu":      {},
	"qq":          {},
	"dingtalk":    {},
	"matrix":      {},
	"line":        {},
	"wecom":       {},
	"weixin":      {},
	"irc":         {},
	"google-chat": {},
	// M11: "email" is intentionally NOT a known channel type — email is a TOOL
	// surface (pkg/email transport + per-agent email tools), not a conversational
	// channel. A legacy channels.email config entry is dropped on load with a WARN.
}

// KnownChannelTypes returns a sorted copy of the canonical supported-channel
// type identifier set (knownChannelTypes above). Exported so other packages
// — notably pkg/sysagent/tools's channel-management tools — can derive their
// own channel allow-lists from this single source of truth instead of
// hand-maintaining a second, driftable copy of the same ID set.
func KnownChannelTypes() []string {
	names := make([]string, 0, len(knownChannelTypes))
	for n := range knownChannelTypes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// normalizeChannelMap fills in the Type field from the map key when absent
// (so JSON-loaded entries without an explicit "type" field get a default),
// and drops entries whose effective channel type is not in knownChannelTypes,
// emitting a structured Warn for each unknown entry.
//
// ADR-029 (Gate 0, FR-016): the effective type is resolved via ParseInstanceKey
// so namespaced instance keys like "whatsapp.eu" (effective type: "whatsapp")
// are KEPT rather than dropped. The entry is stored under its original key so
// the instance ID (e.g. "whatsapp.eu") is preserved for per-instance routing.
// The Type field is backfilled to the effective type when absent.
func normalizeChannelMap(channels map[string]ChannelInstanceConfig) map[string]ChannelInstanceConfig {
	out := make(map[string]ChannelInstanceConfig, len(channels))
	for key, inst := range channels {
		// Determine effective type: explicit Type field wins; otherwise derive from
		// the map key via ParseInstanceKey (pre-dot segment).
		effectiveType := strings.TrimSpace(inst.Type)
		if effectiveType == "" {
			effectiveType, _ = ParseInstanceKey(key)
		}
		if _, ok := knownChannelTypes[effectiveType]; !ok {
			slog.Warn("config: unknown channel type in channels map — ignoring legacy or unsupported section",
				"key", key, "effective_type", effectiveType)
			continue
		}
		// Backfill the Type field to the effective type when absent so the
		// on-disk instance is self-describing.
		if inst.Type == "" {
			inst.Type = effectiveType
		}
		out[key] = inst
	}
	return out
}

// ParseInstanceKey splits a channel instance key into its base type and optional
// slug. The delimiter is the FIRST dot (`.`), which is filesystem-safe on
// Windows (unlike `:`) and legal as a JSON credential-store key, and no built-in
// channel type name contains a dot, so the key is unambiguously splittable.
//
// Examples:
//
//	ParseInstanceKey("whatsapp")      → ("whatsapp", "")
//	ParseInstanceKey("whatsapp.eu")   → ("whatsapp", "eu")
//	ParseInstanceKey("google-chat.s") → ("google-chat", "s")
//
// The returned channelType is always the pre-dot segment (which may equal the
// full key when there is no dot). The slug is everything after the first dot.
func ParseInstanceKey(key string) (channelType, slug string) {
	idx := strings.IndexByte(key, '.')
	if idx < 0 {
		return key, ""
	}
	return key[:idx], key[idx+1:]
}

// ErrInvalidInstanceKey is the sentinel wrapped by ValidateInstanceKey when a
// channel instance key does not meet the ADR-029 grammar requirements.
var ErrInvalidInstanceKey = errors.New("channels: invalid instance key")

// slugPattern matches a valid slug: lowercase alphanumeric and hyphens, 1–32 chars.
// Defined once to avoid repeated string parsing.
var slugPattern = func() func(string) bool {
	return func(s string) bool {
		if len(s) == 0 || len(s) > 32 {
			return false
		}
		for _, r := range s {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
		return true
	}
}()

// ValidateInstanceKey validates a channel instance key against the ADR-029
// grammar (FR-017, locked). A key is valid if it is EITHER:
//
//   - a bare known channel type ("whatsapp", "telegram", …), or
//   - a namespaced key "<type>.<slug>" where <type> ∈ knownChannelTypes and
//     <slug> matches [a-z0-9-]{1,32} (all lowercase; uppercase → error).
//
// Returns an error wrapping ErrInvalidInstanceKey on failure.
func ValidateInstanceKey(key string) error {
	channelType, slug := ParseInstanceKey(key)
	if _, ok := knownChannelTypes[channelType]; !ok {
		return fmt.Errorf("%w: unknown base type %q in key %q", ErrInvalidInstanceKey, channelType, key)
	}
	if !strings.Contains(key, ".") {
		// Bare type key (no delimiter) — valid.
		return nil
	}
	// Namespaced key ("<type>.<slug>"): the slug must be well-formed. A trailing
	// dot ("whatsapp.") yields an empty slug, which slugPattern rejects (BUG-1).
	if !slugPattern(slug) {
		return fmt.Errorf(
			"%w: slug %q in key %q is invalid — must match [a-z0-9-]{1,32} (all lowercase, 1–32 chars)",
			ErrInvalidInstanceKey, slug, key,
		)
	}
	return nil
}

// ValidateChannels validates the channel instance map for ADR-029 compliance.
// The one-per-type cap is lifted: N instances per type are allowed. Each key
// must pass ValidateInstanceKey (known base type, well-formed slug) and each
// workspace-bound entry must be FULLY bound (IsWorkspaceBound() == true) — a
// half-bound instance (workspace_id set but agent identity absent or invalid)
// is rejected to prevent silent routing degradation (ADR-029 FR-029).
func ValidateChannels(channels map[string]ChannelInstanceConfig) error {
	for key, inst := range channels {
		channelType, slug := ParseInstanceKey(key)
		// Cross-check FIRST: an entry that DECLARES a type must use a key whose
		// derived base type matches. A mismatch (e.g. key "telegram-2" with
		// type:"telegram") is an operator misconfiguration — the entry declares a
		// known channel but the key is malformed — and is rejected loudly rather
		// than silently dropped.
		if inst.Type != "" && inst.Type != channelType {
			return fmt.Errorf(
				"channels: %w: entry %q declares type=%q but the key implies type=%q — use %q.<slug>",
				ErrInvalidInstanceKey, key, inst.Type, channelType, inst.Type,
			)
		}
		// Undeclared unknown base type = legacy/unsupported section (e.g. a stale
		// "maixcam" entry with no type field). These are gracefully DROPPED by
		// normalizeChannelMap with a WARN — they must NOT hard-fail config load
		// (T28: legacy sections are ignored, not fatal).
		if _, ok := knownChannelTypes[channelType]; !ok {
			continue
		}
		// Known base type: a namespaced key's slug must be well-formed. Any dot in
		// the key means it is namespaced, so a trailing dot ("whatsapp.", empty
		// slug) is rejected here too (BUG-1) — slugPattern rejects the empty slug.
		if strings.Contains(key, ".") && !slugPattern(slug) {
			return fmt.Errorf(
				"channels: %w: slug %q in key %q must match [a-z0-9-]{1,32} (all lowercase, 1–32 chars)",
				ErrInvalidInstanceKey, slug, key,
			)
		}
		// ADR-029 FR-029 half-bound guard: a workspace_id without a fully-qualified
		// agent identity is an operator error. The instance would silently route as
		// UNBOUND (BoundInstance=false) — the opposite of the operator's intent.
		// Reject at load so the problem surfaces immediately rather than causing
		// mysterious routing degradation at runtime. Full binding requires WorkspaceID
		// non-empty AND Identity.Kind=="agent" AND Identity.ID non-empty; anything
		// in between is half-bound and is rejected here.
		if strings.TrimSpace(inst.WorkspaceID) != "" && !inst.IsWorkspaceBound() {
			return fmt.Errorf(
				"%w: instance %q has workspace_id=%q but is missing a valid agent identity (identity.kind==\"agent\" and non-empty identity.id are required)",
				ErrHalfBoundChannelInstance,
				key,
				inst.WorkspaceID,
			)
		}
	}
	return nil
}

// canonicalizeKind lowercases and trims a kind string so persisted configs are
// stored in the single canonical form that ChannelIdentity.Validate, AgentRef.
// Validate, the API write path (validateChannelIdentity), and route.go all agree
// on. An empty/whitespace-only kind canonicalizes to "" (its back-compat default).
func canonicalizeKind(kind string) string {
	return strings.ToLower(strings.TrimSpace(kind))
}

// validateIdentityKinds rejects any non-empty ChannelIdentity.Kind that is
// outside its known set. This runs at config load so a typo fails loudly with
// a clear error instead of silently downgrading routing (a mis-spelled channel
// identity kind would otherwise fall through to user routing in route.go).
// Empty/absent kinds keep their documented defaults and are NOT rejected
// (back-compat).
//
// On success it also normalizes the stored Kind in place (lowercase + trim) so a
// value the API accepted in mixed case (e.g. {"kind":"Agent"}) is persisted in
// canonical form. This keeps the on-disk config self-consistent with the
// case-tolerant API/route paths and prevents drift between what was accepted at
// write time and what is loaded later. Validate already normalizes before
// comparing, so this is belt-and-suspenders: even an un-normalized stored value
// would load, but we canonicalize it here so it does not stay mixed-case forever.
//
// ADR-037: this used to also validate AgentRef.Kind inside each agent's (and
// the global default's) DelegationPolicy — that field no longer exists on
// AgentConfig/AgentDefaults (the per-workspace delegation graph is the sole
// delegation mechanism now), so there is nothing left to validate there.
// Renamed from validateIdentityAndAgentRefKinds to reflect the narrower scope.
func validateIdentityKinds(cfg *Config) error {
	// Channel instance identity overrides.
	for key, inst := range cfg.Channels {
		if inst.Identity == nil {
			continue
		}
		if err := inst.Identity.Validate(); err != nil {
			return fmt.Errorf("channel %q identity: %w", key, err)
		}
		// Persist the canonical kind. inst is a copy (map value), so mutate and
		// write the entry back into the map.
		if canon := canonicalizeKind(inst.Identity.Kind); canon != inst.Identity.Kind {
			inst.Identity.Kind = canon
			cfg.Channels[key] = inst
		}
	}

	return nil
}

// --- Extraction helpers — convert ChannelInstanceConfig to the typed sub-config
// that each channel constructor expects. ---

// InstanceToTelegram returns the TelegramConfig for a ChannelInstanceConfig of
// type "telegram".
func InstanceToTelegram(inst ChannelInstanceConfig) TelegramConfig {
	return TelegramConfig{
		Enabled:            inst.Enabled,
		TokenRef:           inst.TokenRef,
		BaseURL:            inst.BaseURL,
		Proxy:              inst.Proxy,
		AllowFrom:          inst.AllowFrom,
		GroupTrigger:       inst.GroupTrigger,
		Typing:             inst.Typing,
		Placeholder:        inst.Placeholder,
		Streaming:          inst.Streaming,
		ReasoningChannelID: inst.ReasoningChannelID,
		UseMarkdownV2:      inst.UseMarkdownV2,
	}
}

// InstanceToWhatsApp returns the WhatsAppConfig for a ChannelInstanceConfig of
// type "whatsapp".
func InstanceToWhatsApp(inst ChannelInstanceConfig) WhatsAppConfig {
	return WhatsAppConfig{
		Enabled:            inst.Enabled,
		SessionStorePath:   inst.SessionStorePath,
		AllowFrom:          inst.AllowFrom,
		ReasoningChannelID: inst.ReasoningChannelID,
		GroupTrigger:       inst.GroupTrigger,
	}
}

// InstanceToFeishu returns the FeishuConfig for a ChannelInstanceConfig of
// type "feishu".
func InstanceToFeishu(inst ChannelInstanceConfig) FeishuConfig {
	return FeishuConfig{
		Enabled:              inst.Enabled,
		AppID:                inst.AppID,
		AppSecretRef:         inst.AppSecretRef,
		EncryptKeyRef:        inst.EncryptKeyRef,
		VerificationTokenRef: inst.VerificationTokenRef,
		AllowFrom:            inst.AllowFrom,
		GroupTrigger:         inst.GroupTrigger,
		Placeholder:          inst.Placeholder,
		ReasoningChannelID:   inst.ReasoningChannelID,
		RandomReactionEmoji:  inst.RandomReactionEmoji,
		IsLark:               inst.IsLark,
	}
}

// InstanceToDiscord returns the DiscordConfig for a ChannelInstanceConfig of
// type "discord".
func InstanceToDiscord(inst ChannelInstanceConfig) DiscordConfig {
	return DiscordConfig{
		Enabled:            inst.Enabled,
		TokenRef:           inst.TokenRef,
		Proxy:              inst.Proxy,
		AllowFrom:          inst.AllowFrom,
		MentionOnly:        inst.MentionOnly,
		GroupTrigger:       inst.GroupTrigger,
		Typing:             inst.Typing,
		Placeholder:        inst.Placeholder,
		ReasoningChannelID: inst.ReasoningChannelID,
	}
}

// InstanceToQQ returns the QQConfig for a ChannelInstanceConfig of type "qq".
func InstanceToQQ(inst ChannelInstanceConfig) QQConfig {
	return QQConfig{
		Enabled:              inst.Enabled,
		AppID:                inst.AppID,
		AppSecretRef:         inst.AppSecretRef,
		AllowFrom:            inst.AllowFrom,
		GroupTrigger:         inst.GroupTrigger,
		MaxMessageLength:     inst.MaxMessageLength,
		MaxBase64FileSizeMiB: inst.MaxBase64FileSizeMiB,
		SendMarkdown:         inst.SendMarkdown,
		ReasoningChannelID:   inst.ReasoningChannelID,
	}
}

// InstanceToDingTalk returns the DingTalkConfig for a ChannelInstanceConfig of
// type "dingtalk".
func InstanceToDingTalk(inst ChannelInstanceConfig) DingTalkConfig {
	return DingTalkConfig{
		Enabled:            inst.Enabled,
		ClientID:           inst.ClientID,
		ClientSecretRef:    inst.ClientSecretRef,
		AllowFrom:          inst.AllowFrom,
		GroupTrigger:       inst.GroupTrigger,
		ReasoningChannelID: inst.ReasoningChannelID,
	}
}

// InstanceToSlack returns the SlackConfig for a ChannelInstanceConfig of type
// "slack".
func InstanceToSlack(inst ChannelInstanceConfig) SlackConfig {
	return SlackConfig{
		Enabled:            inst.Enabled,
		BotTokenRef:        inst.BotTokenRef,
		AppTokenRef:        inst.AppTokenRef,
		AllowFrom:          inst.AllowFrom,
		GroupTrigger:       inst.GroupTrigger,
		Typing:             inst.Typing,
		Placeholder:        inst.Placeholder,
		ReasoningChannelID: inst.ReasoningChannelID,
	}
}

// InstanceToMatrix returns the MatrixConfig for a ChannelInstanceConfig of
// type "matrix".
func InstanceToMatrix(inst ChannelInstanceConfig) MatrixConfig {
	return MatrixConfig{
		Enabled:             inst.Enabled,
		Homeserver:          inst.Homeserver,
		UserID:              inst.UserID,
		AccessTokenRef:      inst.AccessTokenRef,
		DeviceID:            inst.DeviceID,
		JoinOnInvite:        inst.JoinOnInvite,
		MessageFormat:       inst.MessageFormat,
		AllowFrom:           inst.AllowFrom,
		GroupTrigger:        inst.GroupTrigger,
		Placeholder:         inst.Placeholder,
		ReasoningChannelID:  inst.ReasoningChannelID,
		CryptoDatabasePath:  inst.CryptoDatabasePath,
		CryptoPassphraseRef: inst.CryptoPassphraseRef,
	}
}

// InstanceToLINE returns the LINEConfig for a ChannelInstanceConfig of type
// "line".
func InstanceToLINE(inst ChannelInstanceConfig) LINEConfig {
	return LINEConfig{
		Enabled:               inst.Enabled,
		ChannelSecretRef:      inst.ChannelSecretRef,
		ChannelAccessTokenRef: inst.ChannelAccessTokenRef,
		WebhookHost:           inst.WebhookHost,
		WebhookPort:           inst.WebhookPort,
		WebhookPath:           inst.WebhookPath,
		AllowFrom:             inst.AllowFrom,
		GroupTrigger:          inst.GroupTrigger,
		Typing:                inst.Typing,
		Placeholder:           inst.Placeholder,
		ReasoningChannelID:    inst.ReasoningChannelID,
	}
}

// InstanceToWeCom returns the WeComConfig for a ChannelInstanceConfig of type
// "wecom".
func InstanceToWeCom(inst ChannelInstanceConfig) WeComConfig {
	return WeComConfig{
		Enabled:             inst.Enabled,
		BotID:               inst.BotID,
		SecretRef:           inst.SecretRef,
		WebSocketURL:        inst.WebSocketURL,
		SendThinkingMessage: inst.SendThinkingMessage,
		AllowFrom:           inst.AllowFrom,
		ReasoningChannelID:  inst.ReasoningChannelID,
	}
}

// InstanceToWeixin returns the WeixinConfig for a ChannelInstanceConfig of
// type "weixin".
func InstanceToWeixin(inst ChannelInstanceConfig) WeixinConfig {
	return WeixinConfig{
		Enabled:            inst.Enabled,
		TokenRef:           inst.TokenRef,
		AccountID:          inst.AccountID,
		BaseURL:            inst.BaseURL,
		CDNBaseURL:         inst.CDNBaseURL,
		Proxy:              inst.Proxy,
		AllowFrom:          inst.AllowFrom,
		ReasoningChannelID: inst.ReasoningChannelID,
	}
}

// InstanceToIRC returns the IRCConfig for a ChannelInstanceConfig of type
// "irc".
func InstanceToIRC(inst ChannelInstanceConfig) IRCConfig {
	return IRCConfig{
		Enabled:             inst.Enabled,
		Server:              inst.Server,
		TLS:                 inst.TLS,
		Nick:                inst.Nick,
		User:                inst.IRCUser,
		RealName:            inst.RealName,
		PasswordRef:         inst.PasswordRef,
		NickServPasswordRef: inst.NickServPasswordRef,
		SASLUser:            inst.SASLUser,
		SASLPasswordRef:     inst.SASLPasswordRef,
		Channels:            inst.IRCChannels,
		RequestCaps:         inst.RequestCaps,
		AllowFrom:           inst.AllowFrom,
		GroupTrigger:        inst.GroupTrigger,
		Typing:              inst.Typing,
		ReasoningChannelID:  inst.ReasoningChannelID,
	}
}

// InstanceToGoogleChat returns the GoogleChatConfig for a ChannelInstanceConfig
// of type "google-chat".
func InstanceToGoogleChat(inst ChannelInstanceConfig) GoogleChatConfig {
	return GoogleChatConfig{
		Enabled:               inst.Enabled,
		Mode:                  inst.Mode,
		WebhookURL:            inst.WebhookURL,
		WebhookURLRef:         inst.WebhookURLRef,
		ServiceAccountFile:    inst.ServiceAccountFile,
		ServiceAccountJSON:    inst.ServiceAccountJSON,
		ServiceAccountJSONRef: inst.ServiceAccountJSONRef,
		Space:                 inst.Space,
		BotUser:               inst.BotUser,
		AllowFrom:             inst.AllowFrom,
		GroupTrigger:          inst.GroupTrigger,
		Typing:                inst.Typing,
		Placeholder:           inst.Placeholder,
		ReasoningChannelID:    inst.ReasoningChannelID,
	}
}

// GroupTriggerConfig controls when the bot responds in group chats.
type GroupTriggerConfig struct {
	MentionOnly bool     `json:"mention_only,omitempty"`
	Prefixes    []string `json:"prefixes,omitempty"`
}

// TypingConfig controls typing indicator behavior.
type TypingConfig struct {
	Enabled bool `json:"enabled,omitempty"`
}

// PlaceholderConfig controls placeholder message behavior.
type PlaceholderConfig struct {
	Enabled bool                `json:"enabled"`
	Text    FlexibleStringSlice `json:"text,omitempty"`
}

// GetRandomText returns a random placeholder text, or default if none set.
func (p *PlaceholderConfig) GetRandomText() string {
	if len(p.Text) == 0 {
		return "Thinking..."
	}
	if len(p.Text) == 1 {
		return p.Text[0]
	}
	idx := mathrand.Intn(len(p.Text))
	return p.Text[idx]
}

type StreamingConfig struct {
	Enabled         bool `json:"enabled,omitempty"          env:"OMNIPUS_CHANNELS_TELEGRAM_STREAMING_ENABLED"`
	ThrottleSeconds int  `json:"throttle_seconds,omitempty" env:"OMNIPUS_CHANNELS_TELEGRAM_STREAMING_THROTTLE_SECONDS"`
	MinGrowthChars  int  `json:"min_growth_chars,omitempty" env:"OMNIPUS_CHANNELS_TELEGRAM_STREAMING_MIN_GROWTH_CHARS"`
}

type WhatsAppConfig struct {
	Enabled            bool                `json:"enabled"                 yaml:"-" env:"OMNIPUS_CHANNELS_WHATSAPP_ENABLED"`
	SessionStorePath   string              `json:"session_store_path"      yaml:"-" env:"OMNIPUS_CHANNELS_WHATSAPP_SESSION_STORE_PATH"`
	AllowFrom          FlexibleStringSlice `json:"allow_from"              yaml:"-" env:"OMNIPUS_CHANNELS_WHATSAPP_ALLOW_FROM"`
	ReasoningChannelID string              `json:"reasoning_channel_id"    yaml:"-" env:"OMNIPUS_CHANNELS_WHATSAPP_REASONING_CHANNEL_ID"`
	GroupTrigger       GroupTriggerConfig  `json:"group_trigger,omitempty" yaml:"-"`
}

type TelegramConfig struct {
	Enabled            bool                `json:"enabled"                 yaml:"-" env:"OMNIPUS_CHANNELS_TELEGRAM_ENABLED"`
	TokenRef           string              `json:"token_ref,omitempty"     yaml:"-" env:"OMNIPUS_CHANNELS_TELEGRAM_TOKEN_REF"`
	BaseURL            string              `json:"base_url"                yaml:"-" env:"OMNIPUS_CHANNELS_TELEGRAM_BASE_URL"`
	Proxy              string              `json:"proxy"                   yaml:"-" env:"OMNIPUS_CHANNELS_TELEGRAM_PROXY"`
	AllowFrom          FlexibleStringSlice `json:"allow_from"              yaml:"-" env:"OMNIPUS_CHANNELS_TELEGRAM_ALLOW_FROM"`
	GroupTrigger       GroupTriggerConfig  `json:"group_trigger,omitempty" yaml:"-"`
	Typing             TypingConfig        `json:"typing,omitempty"        yaml:"-"`
	Placeholder        PlaceholderConfig   `json:"placeholder,omitempty"   yaml:"-"`
	Streaming          StreamingConfig     `json:"streaming,omitempty"     yaml:"-"`
	ReasoningChannelID string              `json:"reasoning_channel_id"    yaml:"-" env:"OMNIPUS_CHANNELS_TELEGRAM_REASONING_CHANNEL_ID"`
	UseMarkdownV2      bool                `json:"use_markdown_v2"         yaml:"-" env:"OMNIPUS_CHANNELS_TELEGRAM_USE_MARKDOWN_V2"`
}

type FeishuConfig struct {
	Enabled              bool                `json:"enabled"                          yaml:"-" env:"OMNIPUS_CHANNELS_FEISHU_ENABLED"`
	AppID                string              `json:"app_id"                           yaml:"-" env:"OMNIPUS_CHANNELS_FEISHU_APP_ID"`
	AppSecretRef         string              `json:"app_secret_ref,omitempty"         yaml:"-" env:"OMNIPUS_CHANNELS_FEISHU_APP_SECRET_REF"`
	EncryptKeyRef        string              `json:"encrypt_key_ref,omitempty"        yaml:"-" env:"OMNIPUS_CHANNELS_FEISHU_ENCRYPT_KEY_REF"`
	VerificationTokenRef string              `json:"verification_token_ref,omitempty" yaml:"-" env:"OMNIPUS_CHANNELS_FEISHU_VERIFICATION_TOKEN_REF"`
	AllowFrom            FlexibleStringSlice `json:"allow_from"                       yaml:"-" env:"OMNIPUS_CHANNELS_FEISHU_ALLOW_FROM"`
	GroupTrigger         GroupTriggerConfig  `json:"group_trigger,omitempty"          yaml:"-"`
	Placeholder          PlaceholderConfig   `json:"placeholder,omitempty"            yaml:"-"`
	ReasoningChannelID   string              `json:"reasoning_channel_id"             yaml:"-" env:"OMNIPUS_CHANNELS_FEISHU_REASONING_CHANNEL_ID"`
	RandomReactionEmoji  FlexibleStringSlice `json:"random_reaction_emoji"            yaml:"-" env:"OMNIPUS_CHANNELS_FEISHU_RANDOM_REACTION_EMOJI"`
	IsLark               bool                `json:"is_lark"                          yaml:"-" env:"OMNIPUS_CHANNELS_FEISHU_IS_LARK"`
}

type DiscordConfig struct {
	Enabled            bool                `json:"enabled"                 yaml:"-" env:"OMNIPUS_CHANNELS_DISCORD_ENABLED"`
	TokenRef           string              `json:"token_ref,omitempty"     yaml:"-" env:"OMNIPUS_CHANNELS_DISCORD_TOKEN_REF"`
	Proxy              string              `json:"proxy"                   yaml:"-" env:"OMNIPUS_CHANNELS_DISCORD_PROXY"`
	AllowFrom          FlexibleStringSlice `json:"allow_from"              yaml:"-" env:"OMNIPUS_CHANNELS_DISCORD_ALLOW_FROM"`
	MentionOnly        bool                `json:"mention_only"            yaml:"-" env:"OMNIPUS_CHANNELS_DISCORD_MENTION_ONLY"`
	GroupTrigger       GroupTriggerConfig  `json:"group_trigger,omitempty" yaml:"-"`
	Typing             TypingConfig        `json:"typing,omitempty"        yaml:"-"`
	Placeholder        PlaceholderConfig   `json:"placeholder,omitempty"   yaml:"-"`
	ReasoningChannelID string              `json:"reasoning_channel_id"    yaml:"-" env:"OMNIPUS_CHANNELS_DISCORD_REASONING_CHANNEL_ID"`
}

type QQConfig struct {
	Enabled              bool                `json:"enabled"                  yaml:"-" env:"OMNIPUS_CHANNELS_QQ_ENABLED"`
	AppID                string              `json:"app_id"                   yaml:"-" env:"OMNIPUS_CHANNELS_QQ_APP_ID"`
	AppSecretRef         string              `json:"app_secret_ref,omitempty" yaml:"-" env:"OMNIPUS_CHANNELS_QQ_APP_SECRET_REF"`
	AllowFrom            FlexibleStringSlice `json:"allow_from"               yaml:"-" env:"OMNIPUS_CHANNELS_QQ_ALLOW_FROM"`
	GroupTrigger         GroupTriggerConfig  `json:"group_trigger,omitempty"  yaml:"-"`
	MaxMessageLength     int                 `json:"max_message_length"       yaml:"-" env:"OMNIPUS_CHANNELS_QQ_MAX_MESSAGE_LENGTH"`
	MaxBase64FileSizeMiB int64               `json:"max_base64_file_size_mib" yaml:"-" env:"OMNIPUS_CHANNELS_QQ_MAX_BASE64_FILE_SIZE_MIB"`
	SendMarkdown         bool                `json:"send_markdown"            yaml:"-" env:"OMNIPUS_CHANNELS_QQ_SEND_MARKDOWN"`
	ReasoningChannelID   string              `json:"reasoning_channel_id"     yaml:"-" env:"OMNIPUS_CHANNELS_QQ_REASONING_CHANNEL_ID"`
}

type DingTalkConfig struct {
	Enabled            bool                `json:"enabled"                     yaml:"-" env:"OMNIPUS_CHANNELS_DINGTALK_ENABLED"`
	ClientID           string              `json:"client_id"                   yaml:"-" env:"OMNIPUS_CHANNELS_DINGTALK_CLIENT_ID"`
	ClientSecretRef    string              `json:"client_secret_ref,omitempty" yaml:"-" env:"OMNIPUS_CHANNELS_DINGTALK_CLIENT_SECRET_REF"`
	AllowFrom          FlexibleStringSlice `json:"allow_from"                  yaml:"-" env:"OMNIPUS_CHANNELS_DINGTALK_ALLOW_FROM"`
	GroupTrigger       GroupTriggerConfig  `json:"group_trigger,omitempty"     yaml:"-"`
	ReasoningChannelID string              `json:"reasoning_channel_id"        yaml:"-" env:"OMNIPUS_CHANNELS_DINGTALK_REASONING_CHANNEL_ID"`
}

type SlackConfig struct {
	Enabled            bool                `json:"enabled"                 yaml:"-" env:"OMNIPUS_CHANNELS_SLACK_ENABLED"`
	BotTokenRef        string              `json:"bot_token_ref,omitempty" yaml:"-" env:"OMNIPUS_CHANNELS_SLACK_BOT_TOKEN_REF"`
	AppTokenRef        string              `json:"app_token_ref,omitempty" yaml:"-" env:"OMNIPUS_CHANNELS_SLACK_APP_TOKEN_REF"`
	AllowFrom          FlexibleStringSlice `json:"allow_from"              yaml:"-" env:"OMNIPUS_CHANNELS_SLACK_ALLOW_FROM"`
	GroupTrigger       GroupTriggerConfig  `json:"group_trigger,omitempty" yaml:"-"`
	Typing             TypingConfig        `json:"typing,omitempty"        yaml:"-"`
	Placeholder        PlaceholderConfig   `json:"placeholder,omitempty"   yaml:"-"`
	ReasoningChannelID string              `json:"reasoning_channel_id"    yaml:"-" env:"OMNIPUS_CHANNELS_SLACK_REASONING_CHANNEL_ID"`
}

type MatrixConfig struct {
	Enabled             bool                `json:"enabled"                         yaml:"-" env:"OMNIPUS_CHANNELS_MATRIX_ENABLED"`
	Homeserver          string              `json:"homeserver"                      yaml:"-" env:"OMNIPUS_CHANNELS_MATRIX_HOMESERVER"`
	UserID              string              `json:"user_id"                         yaml:"-" env:"OMNIPUS_CHANNELS_MATRIX_USER_ID"`
	AccessTokenRef      string              `json:"access_token_ref,omitempty"      yaml:"-" env:"OMNIPUS_CHANNELS_MATRIX_ACCESS_TOKEN_REF"`
	DeviceID            string              `json:"device_id,omitempty"             yaml:"-"`
	JoinOnInvite        bool                `json:"join_on_invite"                  yaml:"-"`
	MessageFormat       string              `json:"message_format,omitempty"        yaml:"-"`
	AllowFrom           FlexibleStringSlice `json:"allow_from"                      yaml:"-"`
	GroupTrigger        GroupTriggerConfig  `json:"group_trigger,omitempty"         yaml:"-"`
	Placeholder         PlaceholderConfig   `json:"placeholder,omitempty"           yaml:"-"`
	ReasoningChannelID  string              `json:"reasoning_channel_id"            yaml:"-"`
	CryptoDatabasePath  string              `json:"crypto_database_path,omitempty"  yaml:"-"`
	CryptoPassphraseRef string              `json:"crypto_passphrase_ref,omitempty" yaml:"-"`
}

type LINEConfig struct {
	Enabled               bool                `json:"enabled"                            yaml:"-" env:"OMNIPUS_CHANNELS_LINE_ENABLED"`
	ChannelSecretRef      string              `json:"channel_secret_ref,omitempty"       yaml:"-" env:"OMNIPUS_CHANNELS_LINE_CHANNEL_SECRET_REF"`
	ChannelAccessTokenRef string              `json:"channel_access_token_ref,omitempty" yaml:"-" env:"OMNIPUS_CHANNELS_LINE_CHANNEL_ACCESS_TOKEN_REF"`
	WebhookHost           string              `json:"webhook_host"                       yaml:"-" env:"OMNIPUS_CHANNELS_LINE_WEBHOOK_HOST"`
	WebhookPort           int                 `json:"webhook_port"                       yaml:"-" env:"OMNIPUS_CHANNELS_LINE_WEBHOOK_PORT"`
	WebhookPath           string              `json:"webhook_path"                       yaml:"-" env:"OMNIPUS_CHANNELS_LINE_WEBHOOK_PATH"`
	AllowFrom             FlexibleStringSlice `json:"allow_from"                         yaml:"-" env:"OMNIPUS_CHANNELS_LINE_ALLOW_FROM"`
	GroupTrigger          GroupTriggerConfig  `json:"group_trigger,omitempty"            yaml:"-"`
	Typing                TypingConfig        `json:"typing,omitempty"                   yaml:"-"`
	Placeholder           PlaceholderConfig   `json:"placeholder,omitempty"              yaml:"-"`
	ReasoningChannelID    string              `json:"reasoning_channel_id"               yaml:"-"`
}

type WeComGroupConfig struct {
	AllowFrom FlexibleStringSlice `json:"allow_from,omitempty"`
}

type WeComConfig struct {
	Enabled             bool                `json:"enabled"                 yaml:"-" env:"ENABLED"`
	BotID               string              `json:"bot_id"                  yaml:"-" env:"BOT_ID"`
	SecretRef           string              `json:"secret_ref,omitempty"    yaml:"-" env:"SECRET_REF"`
	WebSocketURL        string              `json:"websocket_url,omitempty" yaml:"-" env:"WEBSOCKET_URL"`
	SendThinkingMessage bool                `json:"send_thinking_message"   yaml:"-" env:"SEND_THINKING_MESSAGE"`
	AllowFrom           FlexibleStringSlice `json:"allow_from"              yaml:"-" env:"ALLOW_FROM"`
	ReasoningChannelID  string              `json:"reasoning_channel_id"    yaml:"-" env:"REASONING_CHANNEL_ID"`
}

type WeixinConfig struct {
	Enabled            bool                `json:"enabled"              yaml:"-" env:"OMNIPUS_CHANNELS_WEIXIN_ENABLED"`
	TokenRef           string              `json:"token_ref,omitempty"  yaml:"-" env:"OMNIPUS_CHANNELS_WEIXIN_TOKEN_REF"`
	AccountID          string              `json:"account_id,omitempty" yaml:"-" env:"OMNIPUS_CHANNELS_WEIXIN_ACCOUNT_ID"`
	BaseURL            string              `json:"base_url"             yaml:"-" env:"OMNIPUS_CHANNELS_WEIXIN_BASE_URL"`
	CDNBaseURL         string              `json:"cdn_base_url"         yaml:"-" env:"OMNIPUS_CHANNELS_WEIXIN_CDN_BASE_URL"`
	Proxy              string              `json:"proxy"                yaml:"-" env:"OMNIPUS_CHANNELS_WEIXIN_PROXY"`
	AllowFrom          FlexibleStringSlice `json:"allow_from"           yaml:"-" env:"OMNIPUS_CHANNELS_WEIXIN_ALLOW_FROM"`
	ReasoningChannelID string              `json:"reasoning_channel_id" yaml:"-" env:"OMNIPUS_CHANNELS_WEIXIN_REASONING_CHANNEL_ID"`
}

type GoogleChatConfig struct {
	Enabled               bool                `json:"enabled"                            yaml:"-"                              env:"OMNIPUS_CHANNELS_GOOGLECHAT_ENABLED"`
	Mode                  string              `json:"mode"                               yaml:"-"                              env:"OMNIPUS_CHANNELS_GOOGLECHAT_MODE"` // "webhook" | "bot"
	WebhookURL            SecureString        `json:"webhook_url,omitzero"               yaml:"webhook_url,omitempty"          env:"OMNIPUS_CHANNELS_GOOGLECHAT_WEBHOOK_URL"`
	WebhookURLRef         string              `json:"webhook_url_ref,omitempty"          yaml:"-"                              env:"OMNIPUS_CHANNELS_GOOGLECHAT_WEBHOOK_URL_REF"`
	ServiceAccountFile    string              `json:"service_account_file,omitempty"     yaml:"-"                              env:"OMNIPUS_CHANNELS_GOOGLECHAT_SERVICE_ACCOUNT_FILE"`
	ServiceAccountJSON    SecureString        `json:"service_account_json,omitzero"      yaml:"service_account_json,omitempty" env:"OMNIPUS_CHANNELS_GOOGLECHAT_SERVICE_ACCOUNT_JSON"`
	ServiceAccountJSONRef string              `json:"service_account_json_ref,omitempty" yaml:"-"                              env:"OMNIPUS_CHANNELS_GOOGLECHAT_SERVICE_ACCOUNT_JSON_REF"`
	Space                 string              `json:"space"                              yaml:"-"                              env:"OMNIPUS_CHANNELS_GOOGLECHAT_SPACE"`
	BotUser               string              `json:"bot_user"                           yaml:"-"                              env:"OMNIPUS_CHANNELS_GOOGLECHAT_BOT_USER"`
	AllowFrom             FlexibleStringSlice `json:"allow_from"                         yaml:"-"                              env:"OMNIPUS_CHANNELS_GOOGLECHAT_ALLOW_FROM"`
	GroupTrigger          GroupTriggerConfig  `json:"group_trigger,omitempty"            yaml:"-"`
	Typing                TypingConfig        `json:"typing,omitempty"                   yaml:"-"`
	Placeholder           PlaceholderConfig   `json:"placeholder,omitempty"              yaml:"-"`
	ReasoningChannelID    string              `json:"reasoning_channel_id"               yaml:"-"                              env:"OMNIPUS_CHANNELS_GOOGLECHAT_REASONING_CHANNEL_ID"`
}

type IRCConfig struct {
	Enabled             bool                `json:"enabled"                         yaml:"-" env:"OMNIPUS_CHANNELS_IRC_ENABLED"`
	Server              string              `json:"server"                          yaml:"-" env:"OMNIPUS_CHANNELS_IRC_SERVER"`
	TLS                 bool                `json:"tls"                             yaml:"-" env:"OMNIPUS_CHANNELS_IRC_TLS"`
	Nick                string              `json:"nick"                            yaml:"-" env:"OMNIPUS_CHANNELS_IRC_NICK"`
	User                string              `json:"user,omitempty"                  yaml:"-" env:"OMNIPUS_CHANNELS_IRC_USER"`
	RealName            string              `json:"real_name,omitempty"             yaml:"-"`
	PasswordRef         string              `json:"password_ref,omitempty"          yaml:"-" env:"OMNIPUS_CHANNELS_IRC_PASSWORD_REF"`
	NickServPasswordRef string              `json:"nickserv_password_ref,omitempty" yaml:"-" env:"OMNIPUS_CHANNELS_IRC_NICKSERV_PASSWORD_REF"`
	SASLUser            string              `json:"sasl_user"                       yaml:"-" env:"OMNIPUS_CHANNELS_IRC_SASL_USER"`
	SASLPasswordRef     string              `json:"sasl_password_ref,omitempty"     yaml:"-" env:"OMNIPUS_CHANNELS_IRC_SASL_PASSWORD_REF"`
	Channels            FlexibleStringSlice `json:"channels"                        yaml:"-" env:"OMNIPUS_CHANNELS_IRC_CHANNELS"`
	RequestCaps         FlexibleStringSlice `json:"request_caps,omitempty"          yaml:"-"`
	AllowFrom           FlexibleStringSlice `json:"allow_from"                      yaml:"-" env:"OMNIPUS_CHANNELS_IRC_ALLOW_FROM"`
	GroupTrigger        GroupTriggerConfig  `json:"group_trigger,omitempty"         yaml:"-"`
	Typing              TypingConfig        `json:"typing,omitempty"                yaml:"-"`
	ReasoningChannelID  string              `json:"reasoning_channel_id"            yaml:"-"`
}

// EmailConfig holds configuration for the email channel (IMAP inbound + SMTP outbound).
// Credentials (password) are stored in the encrypted credential store via PasswordRef;
// only public config (hosts, ports, username) is stored in config.json.
type EmailConfig struct {
	Enabled     bool   `json:"enabled"`
	IMAPHost    string `json:"imap_host,omitempty"`
	IMAPPort    int    `json:"imap_port,omitempty"`
	SMTPHost    string `json:"smtp_host,omitempty"`
	SMTPPort    int    `json:"smtp_port,omitempty"`
	Username    string `json:"username,omitempty"`
	PasswordRef string `json:"password_ref,omitempty"`
}

// MailboxConfig is one (agent, workspace) email mailbox account (M11). Email is
// modeled as a TOOL surface, not a conversational channel: every (agent,
// workspace) pair may hold its own mailbox — an agent plays different roles in
// different workspaces and can have a distinct inbox in each. The password is
// stored in the encrypted credential store and referenced here by PasswordRef
// only — the secret is never written inline to config.json (SEC-23 pattern).
//
// Addressing lives in the Config.Mailboxes map keys (agent ID → workspace ID);
// WorkspaceID mirrors the inner key for convenience and is normalized to match
// it on load. Each mailbox's unhandled mail becomes Board tasks in ITS
// workspace assigned to its owning agent, so multiple inboxes stay unambiguous.
type MailboxConfig struct {
	// Enabled gates whether the email tools are registered for the owning agent.
	Enabled bool `json:"enabled"`
	// WorkspaceID is the workspace the mailbox surfaces in.
	WorkspaceID string `json:"workspace_id"`
	IMAPHost    string `json:"imap_host,omitempty"`
	IMAPPort    int    `json:"imap_port,omitempty"`
	SMTPHost    string `json:"smtp_host,omitempty"`
	SMTPPort    int    `json:"smtp_port,omitempty"`
	// Username is the email address / login for IMAP and SMTP.
	Username string `json:"username,omitempty"`
	// PasswordRef is the credential-store key for the mailbox password. The
	// plaintext password never appears in config.json.
	PasswordRef string `json:"password_ref,omitempty"`
}

// MailboxesConfig maps agent ID → workspace ID → mailbox. Every (agent,
// workspace) pair may hold its own mailbox (the 0.1.0 "one mailbox per agent,
// cap-1 per workspace" model was lifted 2026-07-03, operator-approved).
type MailboxesConfig map[string]map[string]MailboxConfig

// UnmarshalJSON accepts BOTH the current nested shape
// {"<agent>": {"<workspace>": {…}}} and the legacy 0.1.0 flat shape
// {"<agent>": {"enabled": …, "workspace_id": "…", …}}, lifting legacy entries
// under their embedded workspace_id. A legacy entry without a workspace_id is
// dropped with a WARN — it was unreachable anyway (the drainer and tool
// registration both require a workspace binding).
//
// Shape detection is per-agent-entry and STRICT: an entry is legacy-flat iff
// ALL its inner values are non-objects, nested iff ALL its inner values are
// objects. A MIX of both within one entry is malformed and returns a hard
// error rather than being silently folded — the previous any-one-non-object
// heuristic misclassified a mixed entry as legacy-flat, decoded the (object-
// valued) nested keys against MailboxConfig's scalar fields, produced an
// empty WorkspaceID, and silently dropped the entire roster under a
// misleading "legacy...unreachable" WARN. A config-load failure that names
// the offending agent is preferable to that silent data loss.
func (m *MailboxesConfig) UnmarshalJSON(data []byte) error {
	var nested map[string]map[string]json.RawMessage
	if err := json.Unmarshal(data, &nested); err != nil {
		return fmt.Errorf("mailboxes: %w", err)
	}
	out := make(MailboxesConfig, len(nested))
	for agentID, inner := range nested {
		// Legacy detection: the flat shape's inner keys are FIELDS of
		// MailboxConfig ("enabled" is required-in-practice and always present in
		// persisted legacy entries), whose values are scalars — a nested entry's
		// inner values are objects. Probe every raw value so a mixed shape (some
		// object, some scalar) is caught rather than misclassified.
		allObjects := true
		allScalars := true
		for _, raw := range inner {
			trimmed := bytes.TrimLeft(raw, " \t\n\r")
			if len(trimmed) > 0 && trimmed[0] == '{' {
				allScalars = false
			} else {
				allObjects = false
			}
		}
		switch {
		case len(inner) == 0:
			// No inner keys — treat as an empty nested entry (nothing to fold).
			out[agentID] = map[string]MailboxConfig{}
		case allScalars:
			// Re-decode the whole inner object as ONE legacy MailboxConfig.
			rawEntry, err := json.Marshal(inner)
			if err != nil {
				return fmt.Errorf("mailboxes: agent %q: %w", agentID, err)
			}
			var mb MailboxConfig
			if err := json.Unmarshal(rawEntry, &mb); err != nil {
				return fmt.Errorf("mailboxes: legacy entry for agent %q: %w", agentID, err)
			}
			if mb.WorkspaceID == "" {
				slog.Warn("config: dropping legacy mailbox without workspace_id (unreachable)",
					"agent_id", agentID)
				continue
			}
			out[agentID] = map[string]MailboxConfig{mb.WorkspaceID: mb}
		case allObjects:
			byWorkspace := make(map[string]MailboxConfig, len(inner))
			for wsID, raw := range inner {
				if strings.TrimSpace(wsID) == "" {
					slog.Warn("config: dropping mailbox with empty workspace key",
						"agent_id", agentID)
					continue
				}
				var mb MailboxConfig
				if err := json.Unmarshal(raw, &mb); err != nil {
					return fmt.Errorf("mailboxes: agent %q workspace %q: %w", agentID, wsID, err)
				}
				// The inner map key is authoritative; keep the mirror field in sync.
				mb.WorkspaceID = wsID
				byWorkspace[wsID] = mb
			}
			out[agentID] = byWorkspace
		default:
			return fmt.Errorf("mailboxes: agent %q entry is malformed (mixed legacy/nested shape)", agentID)
		}
	}
	*m = out
	return nil
}

// SchedulesConfig holds global guardrail settings for scheduled agent runs
// (#264). These are deliberately separate from agents.defaults.timeout_seconds
// (which is intentionally 0/disabled): scheduled runs are unattended and need
// their own deadline + concurrency bounds.
type SchedulesConfig struct {
	// MaxConcurrentRuns bounds the parallel autonomous-run lane (FR-007).
	// Default 8; values <= 0 fall back to the default on load.
	MaxConcurrentRuns int `json:"max_concurrent_runs,omitempty" env:"OMNIPUS_SCHEDULES_MAX_CONCURRENT_RUNS"`
	// RunTimeoutSeconds is the global per-run deadline applied to every scheduled
	// run that does not set a per-schedule override (FR-003). Default 300.
	RunTimeoutSeconds int `json:"run_timeout_seconds,omitempty" env:"OMNIPUS_SCHEDULES_RUN_TIMEOUT_SECONDS"`
	// RetryBackoffMs is the transient-error retry backoff schedule (FR-010): the
	// next fire after the Nth consecutive transient failure is offset by
	// RetryBackoffMs[N] ms, capped at len(RetryBackoffMs) attempts before
	// resuming normal cadence. Default [60000,120000,300000].
	RetryBackoffMs []int64 `json:"retry_backoff_ms,omitempty"`
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

type DevicesConfig struct {
	Enabled    bool `json:"enabled"     env:"OMNIPUS_DEVICES_ENABLED"`
	MonitorUSB bool `json:"monitor_usb" env:"OMNIPUS_DEVICES_MONITOR_USB"`
}

type VoiceConfig struct {
	ModelName         string `json:"model_name,omitempty" env:"OMNIPUS_VOICE_MODEL_NAME"`
	EchoTranscription bool   `json:"echo_transcription"   env:"OMNIPUS_VOICE_ECHO_TRANSCRIPTION"`
	// ElevenLabsAPIKeyRef is the env-var name whose value holds the ElevenLabs API key.
	// Resolved at boot via credentials.InjectFromConfig; never store the key value here.
	ElevenLabsAPIKeyRef string `json:"elevenlabs_api_key_ref,omitempty" env:"OMNIPUS_VOICE_ELEVENLABS_API_KEY_REF"`
	// GroqAPIKeyRef is the env-var name whose value holds the Groq API key used by
	// the Groq transcriber (pkg/voice/groq_transcriber.go). Resolved at boot via
	// credentials.InjectFromConfig; never store the key value here.
	GroqAPIKeyRef string `json:"groq_api_key_ref,omitempty" env:"OMNIPUS_VOICE_GROQ_API_KEY_REF"`
}

// ModelConfig is one configured provider row.
//
// Since ADR-067 it is keyed by the EXACT pair (Provider, Model): a registry
// provider id and a bare catalog model id. Adding a provider is a catalog
// row plus a key — never a code change — and there is no protocol-prefix
// convention on Model, no display alias, and no id normalisation beyond
// trimming whitespace at the config boundary (FR-034, FR-036, A-19).
type ModelConfig struct {
	// Provider is the catalog provider id — the exact registry id
	// (`zai`, `openrouter`, `moonshotai-cn`, …) or an operator-named custom
	// row. It is compared exactly after TrimSpace, never case-folded
	// (ADR-067 FR-036, A-19), and it is half of the catalog key.
	Provider string `json:"provider,omitempty"`
	// Model is the BARE catalog model id — the other half of the key
	// (ADR-067 FR-034). A `/` inside it is data, not a prefix:
	// `z-ai/glm-5.2` under `openrouter` is one model id, and it is never
	// split. The retired `<protocol>/<model>` convention and its
	// `ExtractProtocol` splitter are gone.
	Model string `json:"model"`

	// Protocol optionally selects one of the SECONDARY wire protocols the
	// catalog row offers (ADR-067 FR-013, A-8) — e.g. `anthropic` on a
	// provider whose primary is `openai-compatible`. Empty means the row's
	// primary. A protocol the row does not offer is an error, never a
	// silent fallback. On a custom row it is required and closed to
	// `openai-compatible | anthropic`.
	Protocol string `json:"protocol,omitempty"`
	// Custom marks an operator-typed endpoint: an id the catalog does not
	// carry, defined entirely by this row's APIBase + Protocol (FR-014,
	// FR-035, X-13). Several custom rows with different ids coexist. Every
	// check is on this flag — never on a literal id.
	Custom bool `json:"custom,omitempty"`

	// HTTP-based providers
	APIBase   string   `json:"api_base,omitempty"`  // API endpoint URL
	Proxy     string   `json:"proxy,omitempty"`     // HTTP proxy URL
	Fallbacks []string `json:"fallbacks,omitempty"` // Fallback model names for failover

	// UpdatedAt is stamped on every PUT of this row (ADR-068 MAJ-015) and is
	// the picker's *Recent* ordering key (Provider.updated_at on the wire).
	// Mirrors AgentConfig.UpdatedAt. Nil for rows never written through the
	// REST PUT (seed templates, onboarding rows).
	UpdatedAt *time.Time `json:"updated_at,omitempty"`

	// AuthMethod is how this row authenticates — the closed set
	// AuthMethodAPIKey | AuthMethodSignIn (ADR-068 FR-003, X-25); empty means
	// api_key. The retired store-OAuth values `oauth` / `token` are rejected by
	// Validate, never silently accepted.
	AuthMethod string `json:"auth_method,omitempty"`
	Home       string `json:"workspace,omitempty"` // Home path (working directory) for CLI-based providers

	// Optional optimizations
	RPM            int            `json:"rpm,omitempty"`              // Requests per minute limit
	MaxTokensField string         `json:"max_tokens_field,omitempty"` // Field name for max tokens (e.g., "max_completion_tokens")
	RequestTimeout int            `json:"request_timeout,omitempty"`
	ThinkingLevel  string         `json:"thinking_level,omitempty"` // Extended thinking: off|low|medium|high|xhigh|adaptive
	ExtraBody      map[string]any `json:"extra_body,omitempty"`     // Additional fields to inject into request body

	// APIKeyRef references a named credential in credentials.json (e.g. "ANTHROPIC_API_KEY").
	// At runtime the system resolves the reference, decrypts the value, and injects it
	// via the process environment (SEC-22). Raw values must never appear in config files.
	APIKeyRef string `json:"api_key_ref,omitempty" yaml:"api_key_ref,omitempty"`

	// Name is the operator's own label for this row, shown where a row needs
	// a human-readable handle. It is display data only: nothing resolves,
	// routes or validates through it (the retired `model_name` alias did,
	// and that is exactly what ADR-068 CRIT-001 removed).
	Name string `json:"name,omitempty" yaml:"name,omitempty"`

	// Models is the user-supplied catalog of model slugs for providers that do
	// NOT expose a live /models endpoint (custom / unknown OpenAI-compatible
	// gateways). For providers WITH a live endpoint the catalog is fetched from
	// upstream and this field is ignored. The model picker is constrained to this
	// list when present (UAT model-catalog fix). Empty for endpoint-backed
	// providers.
	Models []string `json:"models,omitempty" yaml:"models,omitempty"`

	// isVirtual marks this model as a virtual model generated from multi-key expansion.
	// Virtual models should not be persisted to config files.
	isVirtual bool
}

// IsVirtual returns true if this model was generated from multi-key expansion.
func (c *ModelConfig) IsVirtual() bool {
	return c.isVirtual
}

// APIKey returns the resolved API key for this model. After InjectFromConfig runs,
// the ref value is available as an environment variable. Returns "" if no ref is set
// or the env var is unset.
func (c *ModelConfig) APIKey() string {
	if c.APIKeyRef == "" {
		return ""
	}
	return os.Getenv(c.APIKeyRef)
}

// ModelConfig.AuthMethod closed set (ADR-068 FR-003, X-25). These mirror the
// wire enum in contracts/components/schemas/Provider.yaml (`auth_method`).
const (
	// AuthMethodAPIKey — the row authenticates with a credential-store API key.
	AuthMethodAPIKey = "api_key"
	// AuthMethodSignIn — the row authenticates through a vendor CLI sign-in
	// whose credential file Omnipus reads but never writes (FR-007).
	AuthMethodSignIn = "sign_in"
)

// Validate checks if the ModelConfig has all required fields and that
// auth_method, when set, is one of the closed set.
func (c *ModelConfig) Validate() error {
	if c.Provider == "" {
		return fmt.Errorf("provider is required")
	}
	if c.Model == "" {
		return fmt.Errorf("model is required")
	}
	switch c.AuthMethod {
	case "", AuthMethodAPIKey, AuthMethodSignIn:
		return nil
	default:
		return fmt.Errorf("auth_method %q is not supported; must be %q or %q",
			c.AuthMethod, AuthMethodAPIKey, AuthMethodSignIn)
	}
}

// TokenEntry is a single bearer-token credential in a user's token set.
//
// SEC-1 / UAT #399: a user may hold several concurrent bearer tokens (one per
// tab / device / client). Each entry carries a short non-secret ID prefix that
// is also embedded in the issued raw token (omnipus_<id>_<hex>), so token
// verification can index directly to the matching hash instead of bcrypt-looping
// every entry. The ID is NOT a secret — it only selects which bcrypt hash to
// verify the (secret) token body against.
type TokenEntry struct {
	ID        string     `json:"id"`                   // short non-secret prefix, also embedded in the raw token
	Hash      BcryptHash `json:"hash"`                 // bcrypt hash of the full raw bearer token
	CreatedAt string     `json:"created_at,omitempty"` // RFC3339 issue time (for oldest-first eviction)
}

// UserConfig holds per-user authentication and authorization settings.
type UserConfig struct {
	Username     string `json:"username,omitempty"`
	PasswordHash string `json:"password_hash,omitempty"` // bcrypt hash
	// TokenHash is the LEGACY single bearer-token hash. New code writes the
	// Tokens set instead; this field is retained so pre-existing config.json
	// records (and the migrate-on-read path) still authenticate. Treated as a
	// one-element set during verification.
	TokenHash BcryptHash `json:"token_hash,omitempty"`
	// Tokens is the active bearer-token SET (SEC-1). Login appends; logout
	// removes a single entry; password reset clears all.
	Tokens           []TokenEntry `json:"tokens,omitempty"`
	SessionTokenHash BcryptHash   `json:"session_token_hash,omitempty"` // bcrypt hash of session cookie token
	Name             string       `json:"name,omitempty"`
}

// MaxUserTokens caps the number of concurrent bearer tokens kept per user.
// On login the newest token is appended; when the set exceeds this cap the
// oldest entries are evicted (logged by the caller). Prevents an unbounded
// token set from a long-lived account that logs in from many clients.
const MaxUserTokens = 10

// TokenIDFromRaw extracts the embedded non-secret ID prefix from a raw bearer
// token of the form "omnipus_<id>_<body>". Returns "" when the token is not in
// the ID-tagged form (e.g. a legacy "omnipus_<hex>" token or an env token),
// signaling callers to fall back to scanning the whole set.
func TokenIDFromRaw(raw string) string {
	const prefix = "omnipus_"
	if !strings.HasPrefix(raw, prefix) {
		return ""
	}
	rest := raw[len(prefix):]
	idx := strings.IndexByte(rest, '_')
	if idx <= 0 {
		// No second underscore → legacy "omnipus_<hex>" form with no ID.
		return ""
	}
	return rest[:idx]
}

// TokenSecret returns the substring of a raw bearer token that is bcrypt-hashed
// to produce a token-set entry's Hash.
//
// SEC-1 / bcrypt 72-byte limit: an ID-tagged token "omnipus_<id>_<body>" is
// 81 bytes — past bcrypt's 72-byte input ceiling, beyond which bytes are
// silently ignored. Because the ID prefix is NON-SECRET routing metadata, we
// bcrypt only the secret <body> (the 256-bit entropy). Generation and
// verification MUST agree on this, so both go through TokenSecret. A legacy
// token with no ID is returned whole (its stored hash was computed over the
// full "omnipus_<hex>" string, which is exactly 72 bytes).
func TokenSecret(raw string) string {
	if id := TokenIDFromRaw(raw); id != "" {
		// Strip "omnipus_<id>_" leaving the secret body.
		return raw[len("omnipus_")+len(id)+1:]
	}
	return raw
}

// VerifyTokenAgainst checks raw against the given token set (and, for
// backward compatibility, a single legacy hash). Returns nil on a match.
//
// Extracted from the (*UserConfig) VerifyToken method so any token-bearing
// config shape — the (now singular) human account's UserConfig.Tokens, or
// the standalone Gateway.CLIToken slot — can be verified without needing a
// full UserConfig. SEC-1: it first parses the embedded ID prefix and, when
// present, verifies against ONLY the matching entry's hash (constant-time
// bcrypt compare over the secret body). When the ID is absent (legacy token)
// it scans every token entry and the legacy single hash. Returns nil on a
// match, ErrNoHashSet when tokens is empty and legacyHash is zero, or the
// bcrypt mismatch error otherwise.
func VerifyTokenAgainst(tokens []TokenEntry, legacyHash BcryptHash, raw string) error {
	if raw == "" {
		return ErrNoHashSet
	}
	if len(tokens) == 0 && legacyHash.IsZero() {
		return ErrNoHashSet
	}

	secret := TokenSecret(raw)

	// Fast path: direct index by embedded ID prefix.
	if id := TokenIDFromRaw(raw); id != "" {
		for i := range tokens {
			if tokens[i].ID == id {
				return tokens[i].Hash.Verify(secret)
			}
		}
		// ID present but no matching entry — fall through to a full scan so a
		// race (entry just appended/evicted) or a colliding legacy token still
		// gets a fair chance, then report mismatch.
	}

	// Scan the full token set (legacy token, or ID lookup miss).
	for i := range tokens {
		if tokens[i].Hash.Verify(secret) == nil {
			return nil
		}
	}
	// Legacy single-token field — its hash was computed over the FULL raw token.
	if !legacyHash.IsZero() && legacyHash.Verify(raw) == nil {
		return nil
	}
	return bcrypt.ErrMismatchedHashAndPassword
}

// VerifyToken reports whether raw matches any active bearer token for this user.
//
// SEC-1: it first parses the embedded ID prefix and, when present, verifies
// against ONLY the matching entry's hash (constant-time bcrypt compare over the
// secret body). When the ID is absent (legacy token) it scans every token entry
// and the legacy single TokenHash. Returns nil on a match, ErrNoHashSet when the
// user holds no tokens at all, or the bcrypt mismatch error otherwise.
func (u *UserConfig) VerifyToken(raw string) error {
	return VerifyTokenAgainst(u.Tokens, u.TokenHash, raw)
}

// HasActiveToken reports whether the user holds at least one live bearer token
// (either in the new Tokens set or the legacy TokenHash field).
func (u *UserConfig) HasActiveToken() bool {
	return len(u.Tokens) > 0 || !u.TokenHash.IsZero()
}

// VerifyCLIToken checks raw against the machine-only CLI bearer credential
// (g.CLIToken), the decoupled counterpart of UserConfig.VerifyToken.
// Nil-safe: when no CLI token has been minted yet (g.CLIToken == nil) it
// returns the same ErrNoHashSet that VerifyTokenAgainst returns for an empty
// token set, so callers don't need their own nil check before calling this —
// replacing the `if cfg.Gateway.CLIToken != nil { ... }` guard that was
// previously duplicated at every call site.
func (g *GatewayConfig) VerifyCLIToken(raw string) error {
	if g.CLIToken == nil {
		return ErrNoHashSet
	}
	return VerifyTokenAgainst([]TokenEntry{*g.CLIToken}, "", raw)
}

type GatewayConfig struct {
	Host          string       `json:"host"                      env:"OMNIPUS_GATEWAY_HOST"`
	Port          int          `json:"port"                      env:"OMNIPUS_GATEWAY_PORT"`
	HotReload     bool         `json:"hot_reload"                env:"OMNIPUS_GATEWAY_HOT_RELOAD"`
	LogLevel      string       `json:"log_level,omitempty"       env:"OMNIPUS_LOG_LEVEL"`
	Token         string       `json:"token,omitempty"           env:"-"` // Bearer token stored for reference; runtime auth uses OMNIPUS_BEARER_TOKEN env var
	Users         []UserConfig `json:"users,omitempty"           env:"-"` // Per-account bearer-token list (single-user model: holds at most one entry)
	DevModeBypass bool         `json:"dev_mode_bypass,omitempty" env:"-"` // Opt-in flag to allow unauthenticated access in development. NEVER set to true in production.

	// PublicURL is the browser-facing origin of the main gateway listener, used when
	// a reverse proxy is in front (e.g. "https://omnipus.acme.com").
	// When set, it overrides the Host:Port-derived origin in frame-ancestors CSP directives.
	// Restart-gated (config.GatewayPublicURL / RestartGatedKeys) because it drives
	// boot-frozen CORS/CSP/WS-origin fences (ADR-044).
	PublicURL string `json:"public_url,omitempty" env:"OMNIPUS_GATEWAY_PUBLIC_URL"`
	// PreviewEnabled controls whether /preview/ (served on the main gateway
	// listener) and serve_web are live. Semantic default is TRUE — a nil value
	// means enabled. Read live (NOT restart-gated): toggling takes effect on the
	// next request, no process restart required (ADR-044, FR-006/FR-007).
	// Setting false 404s /preview/ and errors serve_web immediately; it does NOT
	// force-kill already-running dev servers (they idle-TTL out).
	PreviewEnabled *bool `json:"preview_enabled,omitempty" env:"OMNIPUS_GATEWAY_PREVIEW_ENABLED"`

	// OrphanedTurnGraceSeconds bounds an orphaned FOREGROUND (webchat) turn —
	// one whose last watching WebSocket connection has closed and never
	// reconnected — to at most this many seconds before the orphan watchdog
	// (ADR-045, pkg/agent/orphan_watch.go) reaps it. "Reaps" means: hands the
	// session to al.RequestCancel — the SAME cancellation state machine every
	// other cancel surface (web SPA Stop button, Tier A /cancel, Tier B
	// channels, CLI) uses, with its full graceful->hard->detached escalation,
	// approval auto-deny, background-session kill, and audit/transcript
	// writes — but ONLY once the watchdog has confirmed (a) a genuine live
	// ROOT turn still exists, (b) no Critical/background delegate sub-turn is
	// still alive on the session, and (c) nobody has reconnected. Condition
	// (b) is what protects a Critical/background delegate: RequestCancel's
	// own PHASE-B/PHASE-C hard-abort escalation
	// (InterruptSessionHard/sessionTurnsStillAlive) is SESSION-WIDE by
	// construction — note PHASE-A's own graceful cascade (Interrupt; ADR-057
	// FR-041 collapsed the retired InterruptSession into it) is
	// ALSO session-wide, just harmless there because a Critical delegate is
	// designed to ignore a mere graceful nudge — so rather than reuse it while
	// a delegate is still working, the watchdog defers reaping entirely for
	// that fire; see ADR-045 for the full mechanism.
	//
	// nil (unset) resolves to DefaultOrphanedTurnGraceSeconds (now 0 =
	// DISABLED) via config.ResolveInt — an abandoned tab does NOT cancel its
	// turn by default; the turn runs to completion (Omnipus is built for
	// background turns) and only an explicit user Stop cancels. 0 or negative
	// disables the watchdog entirely (matches the TimeoutSeconds: 0-disabled
	// convention elsewhere in this file); a positive value opts back in. Read
	// live (NOT restart-gated, matching GatewayPreviewEnabled's precedent) —
	// each WS teardown reads the current config fresh when arming.
	OrphanedTurnGraceSeconds *int `json:"orphaned_turn_grace_seconds,omitempty" env:"OMNIPUS_GATEWAY_ORPHANED_TURN_GRACE_SECONDS"`

	// AuthMismatchLogLevel controls the log level emitted when the gateway
	// detects an authentication mismatch (e.g. token supplied but does not
	// match, or user not found). Valid values: "debug", "info", "warn"
	// (default "warn", applied by the boot validator).
	//
	// Operators who run in environments where mismatches are expected (e.g.
	// a load balancer health-check that always sends a dummy token) can lower
	// this to "info" or "debug" to reduce noise in structured logs.
	AuthMismatchLogLevel string `json:"auth_mismatch_log_level,omitempty" env:"OMNIPUS_GATEWAY_AUTH_MISMATCH_LOG_LEVEL"`

	// TrustXFF controls whether the gateway reads the X-Forwarded-For header
	// to determine the client IP for audit logging. Default false.
	//
	// Set to true ONLY when the gateway is behind a trusted reverse proxy that
	// correctly sets X-Forwarded-For. On plain-HTTP / single-binary deployments
	// with no proxy, any client can spoof their audit IP by sending this header.
	// See docs/operations/reverse-proxy.md for setup instructions.
	TrustXFF bool `json:"trust_xff,omitempty" env:"OMNIPUS_GATEWAY_TRUST_XFF"`

	// Tool approval configuration (FR-016, SC-006).
	// ToolApprovalTimeout is the seconds to wait for a user to approve/deny a
	// tool call before auto-denying. 0 or negative uses the default (600 s —
	// gateway.go::defaultToolApprovalTimeout, raised from 300 s per #594).
	// Expiry always fails CLOSED: the call is denied with reason "timeout",
	// never approved.
	ToolApprovalTimeout int `json:"tool_approval_timeout,omitempty" env:"OMNIPUS_TOOL_APPROVAL_TIMEOUT"`
	// ToolApprovalMaxPending is the maximum number of concurrently-pending tool
	// approvals before new requests are auto-denied (FR-016, MAJ-009).
	// 0 uses the spec default (64). Negative values are rejected at startup.
	ToolApprovalMaxPending int `json:"tool_approval_max_pending,omitempty" env:"OMNIPUS_TOOL_APPROVAL_MAX_PENDING"`

	// ValidateInbound enables server-side JSON Schema validation of REST request
	// bodies against the OpenAPI component schemas before the body is decoded into
	// a Go struct. Defaults to false (opt-in). When true, handlers that accept a
	// JSON body validate it against the corresponding schema from
	// contracts/components/schemas/ and reject schema-invalid payloads with HTTP
	// 400 + a descriptive error message, preventing Go zero-value silently
	// accepted from a malformed or empty {}.
	//
	// Set via: {"gateway": {"validate_inbound": true}} in config.json
	// or OMNIPUS_GATEWAY_VALIDATE_INBOUND=true env var.
	ValidateInbound bool `json:"validate_inbound,omitempty" env:"OMNIPUS_GATEWAY_VALIDATE_INBOUND"`

	// CLIToken is the machine-only bearer credential the CLI uses to open a WS
	// session, decoupled from the (now singular) human account — a dedicated
	// slot, not a role-less entry in Users[]. Nil means no CLI token has been
	// minted yet.
	CLIToken *TokenEntry `json:"cli_token,omitempty" env:"-"`
}

type ToolDiscoveryConfig struct {
	Enabled          bool `json:"enabled"            env:"OMNIPUS_TOOLS_DISCOVERY_ENABLED"`
	TTL              int  `json:"ttl"                env:"OMNIPUS_TOOLS_DISCOVERY_TTL"`
	MaxSearchResults int  `json:"max_search_results" env:"OMNIPUS_MAX_SEARCH_RESULTS"`
	UseBM25          bool `json:"use_bm25"           env:"OMNIPUS_TOOLS_DISCOVERY_USE_BM25"`
	UseRegex         bool `json:"use_regex"          env:"OMNIPUS_TOOLS_DISCOVERY_USE_REGEX"`
}

type ToolConfig struct {
	Enabled bool `json:"enabled" yaml:"-" env:"ENABLED"`
}

type BraveConfig struct {
	Enabled bool `json:"enabled" yaml:"-" env:"OMNIPUS_TOOLS_WEB_BRAVE_ENABLED"`
	// APIKeyRef references a named credential in credentials.json (e.g. "BRAVE_API_KEY").
	// At runtime the system resolves the reference, decrypts the value, and injects it
	// via the process environment (SEC-22). Raw values must never appear in config files.
	APIKeyRef  string `json:"api_key_ref,omitempty" yaml:"api_key_ref,omitempty" env:"OMNIPUS_TOOLS_WEB_BRAVE_API_KEY_REF"`
	MaxResults int    `json:"max_results"           yaml:"-"                     env:"OMNIPUS_TOOLS_WEB_BRAVE_MAX_RESULTS"`
}

// APIKey returns the resolved Brave API key from the process environment.
func (c *BraveConfig) APIKey() string {
	if c.APIKeyRef == "" {
		return ""
	}
	return os.Getenv(c.APIKeyRef)
}

type TavilyConfig struct {
	Enabled bool `json:"enabled" yaml:"-" env:"OMNIPUS_TOOLS_WEB_TAVILY_ENABLED"`
	// APIKeyRef references a named credential in credentials.json (e.g. "TAVILY_API_KEY").
	// At runtime the system resolves the reference, decrypts the value, and injects it
	// via the process environment (SEC-22). Raw values must never appear in config files.
	APIKeyRef  string `json:"api_key_ref,omitempty" yaml:"api_key_ref,omitempty" env:"OMNIPUS_TOOLS_WEB_TAVILY_API_KEY_REF"`
	BaseURL    string `json:"base_url"              yaml:"-"                     env:"OMNIPUS_TOOLS_WEB_TAVILY_BASE_URL"`
	MaxResults int    `json:"max_results"           yaml:"-"                     env:"OMNIPUS_TOOLS_WEB_TAVILY_MAX_RESULTS"`
}

// APIKey returns the resolved Tavily API key from the process environment.
func (c *TavilyConfig) APIKey() string {
	if c.APIKeyRef == "" {
		return ""
	}
	return os.Getenv(c.APIKeyRef)
}

type DuckDuckGoConfig struct {
	Enabled    bool `json:"enabled"     env:"OMNIPUS_TOOLS_WEB_DUCKDUCKGO_ENABLED"`
	MaxResults int  `json:"max_results" env:"OMNIPUS_TOOLS_WEB_DUCKDUCKGO_MAX_RESULTS"`
}

type PerplexityConfig struct {
	Enabled bool `json:"enabled" yaml:"-" env:"OMNIPUS_TOOLS_WEB_PERPLEXITY_ENABLED"`
	// APIKeyRef references a named credential in credentials.json (e.g. "PERPLEXITY_API_KEY").
	// At runtime the system resolves the reference, decrypts the value, and injects it
	// via the process environment (SEC-22). Raw values must never appear in config files.
	APIKeyRef  string `json:"api_key_ref,omitempty" yaml:"api_key_ref,omitempty" env:"OMNIPUS_TOOLS_WEB_PERPLEXITY_API_KEY_REF"`
	MaxResults int    `json:"max_results"           yaml:"-"                     env:"OMNIPUS_TOOLS_WEB_PERPLEXITY_MAX_RESULTS"`
}

// APIKey returns the resolved Perplexity API key from the process environment.
func (c *PerplexityConfig) APIKey() string {
	if c.APIKeyRef == "" {
		return ""
	}
	return os.Getenv(c.APIKeyRef)
}

type SearXNGConfig struct {
	Enabled    bool   `json:"enabled"     env:"OMNIPUS_TOOLS_WEB_SEARXNG_ENABLED"`
	BaseURL    string `json:"base_url"    env:"OMNIPUS_TOOLS_WEB_SEARXNG_BASE_URL"`
	MaxResults int    `json:"max_results" env:"OMNIPUS_TOOLS_WEB_SEARXNG_MAX_RESULTS"`
}

type GLMSearchConfig struct {
	Enabled bool `json:"enabled" yaml:"-" env:"OMNIPUS_TOOLS_WEB_GLM_ENABLED"`
	// APIKeyRef references a named credential in credentials.json (e.g. "GLM_API_KEY").
	// At runtime the system resolves the reference, decrypts the value, and injects it
	// via the process environment (SEC-22). Raw values must never appear in config files.
	APIKeyRef string `json:"api_key_ref,omitempty" yaml:"api_key_ref,omitempty" env:"OMNIPUS_TOOLS_WEB_GLM_API_KEY_REF"`
	BaseURL   string `json:"base_url"              yaml:"-"                     env:"OMNIPUS_TOOLS_WEB_GLM_BASE_URL"`
	// SearchEngine specifies the search backend: "search_std" (default),
	// "search_pro", "search_pro_sogou", or "search_pro_quark".
	SearchEngine string `json:"search_engine" yaml:"-" env:"OMNIPUS_TOOLS_WEB_GLM_SEARCH_ENGINE"`
	MaxResults   int    `json:"max_results"   yaml:"-" env:"OMNIPUS_TOOLS_WEB_GLM_MAX_RESULTS"`
}

// APIKey returns the resolved GLM API key from the process environment.
func (c *GLMSearchConfig) APIKey() string {
	if c.APIKeyRef == "" {
		return ""
	}
	return os.Getenv(c.APIKeyRef)
}

type BaiduSearchConfig struct {
	Enabled bool `json:"enabled" yaml:"-" env:"OMNIPUS_TOOLS_WEB_BAIDU_ENABLED"`
	// APIKeyRef references a named credential in credentials.json (e.g. "BAIDU_API_KEY").
	// At runtime the system resolves the reference, decrypts the value, and injects it
	// via the process environment (SEC-22). Raw values must never appear in config files.
	APIKeyRef  string `json:"api_key_ref,omitempty" yaml:"api_key_ref,omitempty" env:"OMNIPUS_TOOLS_WEB_BAIDU_API_KEY_REF"`
	BaseURL    string `json:"base_url"              yaml:"-"                     env:"OMNIPUS_TOOLS_WEB_BAIDU_BASE_URL"`
	MaxResults int    `json:"max_results"           yaml:"-"                     env:"OMNIPUS_TOOLS_WEB_BAIDU_MAX_RESULTS"`
}

// APIKey returns the resolved Baidu API key from the process environment.
func (c *BaiduSearchConfig) APIKey() string {
	if c.APIKeyRef == "" {
		return ""
	}
	return os.Getenv(c.APIKeyRef)
}

type WebToolsConfig struct {
	ToolConfig  `                  yaml:"-"                      envPrefix:"OMNIPUS_TOOLS_WEB_"`
	Brave       BraveConfig       `yaml:"brave,omitempty"                                       json:"brave"`
	Tavily      TavilyConfig      `yaml:"tavily,omitempty"                                      json:"tavily"`
	DuckDuckGo  DuckDuckGoConfig  `yaml:"-"                                                     json:"duckduckgo"`
	Perplexity  PerplexityConfig  `yaml:"perplexity,omitempty"                                  json:"perplexity"`
	SearXNG     SearXNGConfig     `yaml:"-"                                                     json:"searxng"`
	GLMSearch   GLMSearchConfig   `yaml:"glm_search,omitempty"                                  json:"glm_search"`
	BaiduSearch BaiduSearchConfig `yaml:"baidu_search,omitempty"                                json:"baidu_search"`
	// PreferNative controls whether to use provider-native web search when
	// the active LLM supports it (e.g. OpenAI web_search_preview). When true,
	// the client-side web_search tool is hidden to avoid duplicate search surfaces,
	// and the provider's built-in search is used instead. Falls back to client-side
	// search when the provider does not support native search.
	PreferNative bool `yaml:"-" json:"prefer_native" env:"OMNIPUS_TOOLS_WEB_PREFER_NATIVE"`
	// Proxy is an optional proxy URL for web tools (http/https/socks5/socks5h).
	// For authenticated proxies, prefer HTTP_PROXY/HTTPS_PROXY env vars instead of embedding credentials in config.
	Proxy                string              `yaml:"-" json:"proxy,omitempty"                  env:"OMNIPUS_TOOLS_WEB_PROXY"`
	FetchLimitBytes      int64               `yaml:"-" json:"fetch_limit_bytes,omitempty"      env:"OMNIPUS_TOOLS_WEB_FETCH_LIMIT_BYTES"`
	Format               string              `yaml:"-" json:"format,omitempty"                 env:"OMNIPUS_TOOLS_WEB_FORMAT"`
	PrivateHostWhitelist FlexibleStringSlice `yaml:"-" json:"private_host_whitelist,omitempty" env:"OMNIPUS_TOOLS_WEB_PRIVATE_HOST_WHITELIST"`
}

type CronToolsConfig struct {
	ToolConfig         `     envPrefix:"OMNIPUS_TOOLS_CRON_"`
	ExecTimeoutMinutes int  `                                json:"exec_timeout_minutes" env:"OMNIPUS_TOOLS_CRON_EXEC_TIMEOUT_MINUTES"` // 0 means no timeout
	AllowCommand       bool `                                json:"allow_command"        env:"OMNIPUS_TOOLS_CRON_ALLOW_COMMAND"`
}

type ExecConfig struct {
	ToolConfig `envPrefix:"OMNIPUS_TOOLS_EXEC_"`

	// US-7: Interactive approval before exec commands.
	// "ask" (default) prompts the user; "off" skips the prompt.
	Approval string `json:"approval,omitempty" env:"OMNIPUS_TOOLS_EXEC_APPROVAL"`

	// US-7/US-5: Glob patterns for binaries the exec tool is allowed to run.
	// Non-empty list acts as an allowlist; all other commands are denied.
	AllowedBinaries []string `json:"allowed_binaries,omitempty" env:"OMNIPUS_TOOLS_EXEC_ALLOWED_BINARIES"`

	// US-14: Route exec child process HTTP traffic through the local SSRF proxy.
	// When true (default), HTTP_PROXY and HTTPS_PROXY are set on child processes.
	EnableProxy bool `json:"enable_proxy,omitempty" env:"OMNIPUS_TOOLS_EXEC_ENABLE_PROXY"`
}

type SkillsToolsConfig struct {
	ToolConfig            `                    yaml:"-" envPrefix:"OMNIPUS_TOOLS_SKILLS_"`
	Marketplaces          []MarketplaceConfig `yaml:"-"                                   json:"marketplaces,omitempty"`
	MaxConcurrentSearches int                 `yaml:"-"                                   json:"max_concurrent_searches" env:"OMNIPUS_TOOLS_SKILLS_MAX_CONCURRENT_SEARCHES"`
	SearchCache           SearchCacheConfig   `yaml:"-"                                   json:"search_cache"`
}

// MarketplaceConfig is the persisted shape of one skill-marketplace entry
// (FR-10.1). Each entry describes one marketplace registry (ClawHub, GitHub,
// or a future "omnipus" registry). Type selects the registry implementation;
// the remaining fields are interpreted per Type.
//
// Secret fields use credential REFS (env-var names resolved at boot via
// credentials.InjectFromConfig, SEC-23) — never plaintext values.
// MarketplaceType values for MarketplaceConfig.Type.
const (
	MarketplaceTypeClawHub = "clawhub"
	MarketplaceTypeGitHub  = "github"
)

type MarketplaceConfig struct {
	// Name is the unique display name (e.g. "clawhub", "github"). Defaults to
	// Type when empty.
	Name string `json:"name"`
	// Type selects the registry implementation: MarketplaceTypeClawHub |
	// MarketplaceTypeGitHub (future: "omnipus").
	Type string `json:"type"`
	// Enabled controls whether this marketplace is active.
	Enabled bool `json:"enabled"`

	// ClawHub-specific fields (Type == "clawhub").
	BaseURL         string `json:"base_url,omitempty"`
	AuthTokenRef    string `json:"auth_token_ref,omitempty"` // env-var name (credential ref)
	SearchPath      string `json:"search_path,omitempty"`
	SkillsPath      string `json:"skills_path,omitempty"`
	DownloadPath    string `json:"download_path,omitempty"`
	Timeout         int    `json:"timeout,omitempty"`
	MaxZipSize      int    `json:"max_zip_size,omitempty"`
	MaxResponseSize int    `json:"max_response_size,omitempty"`

	// GitHub-specific fields (Type == "github").
	TokenRef string `json:"token_ref,omitempty"` // env-var name (credential ref)
	Proxy    string `json:"proxy,omitempty"`
}

type MediaCleanupConfig struct {
	ToolConfig `    envPrefix:"OMNIPUS_MEDIA_CLEANUP_"`
	MaxAge     int `                                   json:"max_age_minutes"  env:"OMNIPUS_MEDIA_CLEANUP_MAX_AGE"`
	Interval   int `                                   json:"interval_minutes" env:"OMNIPUS_MEDIA_CLEANUP_INTERVAL"`
}

// ReadFileToolConfig fields use relative env tags (like ToolConfig.Enabled)
// because the outer ToolsConfig.ReadFile field supplies the
// "OMNIPUS_TOOLS_READ_FILE_" envPrefix. Previously these fields had no env
// tag at all, so the override was never wired despite the envPrefix being
// present on the outer field — no documented env var existed for either
// field until now.
type ReadFileToolConfig struct {
	Enabled         bool `json:"enabled"            env:"ENABLED"`
	MaxReadFileSize int  `json:"max_read_file_size" env:"MAX_READ_FILE_SIZE"`
}

type ToolsConfig struct {
	AllowReadPaths  []string `json:"allow_read_paths"  yaml:"-" env:"OMNIPUS_TOOLS_ALLOW_READ_PATHS"`
	AllowWritePaths []string `json:"allow_write_paths" yaml:"-" env:"OMNIPUS_TOOLS_ALLOW_WRITE_PATHS"`
	// FilterSensitiveData controls whether to filter sensitive values (API keys,
	// tokens, secrets) from tool results before sending to the LLM.
	// Default: true (enabled)
	FilterSensitiveData bool `json:"filter_sensitive_data" yaml:"-" env:"OMNIPUS_TOOLS_FILTER_SENSITIVE_DATA"`
	// FilterMinLength is the minimum content length required for filtering.
	// Content shorter than this will be returned unchanged for performance.
	// Default: 8
	FilterMinLength int                `json:"filter_min_length" yaml:"-"                env:"OMNIPUS_TOOLS_FILTER_MIN_LENGTH"`
	Web             WebToolsConfig     `json:"web"               yaml:"web,omitempty"`
	Cron            CronToolsConfig    `json:"cron"              yaml:"-"`
	Exec            ExecConfig         `json:"exec"              yaml:"-"`
	Skills          SkillsToolsConfig  `json:"skills"            yaml:"skills,omitempty"`
	MediaCleanup    MediaCleanupConfig `json:"media_cleanup"     yaml:"-"`
	MCP             MCPConfig          `json:"mcp"               yaml:"-"`
	AppendFile      ToolConfig         `json:"append_file"       yaml:"-"                                                      envPrefix:"OMNIPUS_TOOLS_APPEND_FILE_"`
	EditFile        ToolConfig         `json:"edit_file"         yaml:"-"                                                      envPrefix:"OMNIPUS_TOOLS_EDIT_FILE_"`
	TaskList        ToolConfig         `json:"task_list"         yaml:"-"                                                      envPrefix:"OMNIPUS_TOOLS_TASK_LIST_"`
	TaskCreate      ToolConfig         `json:"task_create"       yaml:"-"                                                      envPrefix:"OMNIPUS_TOOLS_TASK_CREATE_"`
	TaskUpdate      ToolConfig         `json:"task_update"       yaml:"-"                                                      envPrefix:"OMNIPUS_TOOLS_TASK_UPDATE_"`
	FindSkills      ToolConfig         `json:"find_skills"       yaml:"-"                                                      envPrefix:"OMNIPUS_TOOLS_FIND_SKILLS_"`
	InstallSkill    ToolConfig         `json:"install_skill"     yaml:"-"                                                      envPrefix:"OMNIPUS_TOOLS_INSTALL_SKILL_"`
	ListDir         ToolConfig         `json:"list_dir"          yaml:"-"                                                      envPrefix:"OMNIPUS_TOOLS_LIST_DIR_"`
	Message         ToolConfig         `json:"message"           yaml:"-"                                                      envPrefix:"OMNIPUS_TOOLS_MESSAGE_"`
	ReadFile        ReadFileToolConfig `json:"read_file"         yaml:"-"                                                      envPrefix:"OMNIPUS_TOOLS_READ_FILE_"`
	SendFile        ToolConfig         `json:"send_file"         yaml:"-"                                                      envPrefix:"OMNIPUS_TOOLS_SEND_FILE_"`
	WebFetch        ToolConfig         `json:"web_fetch"         yaml:"-"                                                      envPrefix:"OMNIPUS_TOOLS_WEB_FETCH_"`
	WriteFile       ToolConfig         `json:"write_file"        yaml:"-"                                                      envPrefix:"OMNIPUS_TOOLS_WRITE_FILE_"`
	// Browser deliberately has NO envPrefix tag (unlike its ToolConfig-typed
	// siblings above): every BrowserToolConfig field already carries a
	// fully-qualified env:"OMNIPUS_TOOLS_BROWSER_..." tag, so adding a prefix
	// here would double it (caarlos0/env accumulates opts.Prefix across
	// nesting levels). This was a real bug — see BrowserToolConfig's doc
	// comment for the full mechanism and why the embedded ToolConfig still
	// needs its own envPrefix.
	Browser BrowserToolConfig `json:"browser" yaml:"-"`
	// RunInWorkspace holds dev-mode configuration for the web_serve tool.
	// The on-disk key ("run_in_workspace") is preserved for back-compat with
	// operator config.json files written before the web_serve unification — do
	// not rename the JSON tag or existing deployments will silently lose their
	// warmup_timeout_seconds setting on next restart.
	RunInWorkspace RunInWorkspaceConfig `json:"run_in_workspace,omitempty" yaml:"-"`

	// BuildStatic holds configuration for the build_static Tier 2 tool
	// (FR-046b). Timeout and memory limits are validated at boot by
	// validateBootConfig (validator.go).
	BuildStatic BuildStaticConfig `json:"build_static,omitempty" yaml:"-"`

	// ServeWorkspace holds static-mode configuration for the web_serve tool.
	// The on-disk key ("serve_workspace") is preserved for back-compat with
	// operator config.json files written before the web_serve unification — do
	// not rename the JSON tag or existing deployments will silently lose their
	// duration bounds on next restart.
	ServeWorkspace ServeWorkspaceConfig `json:"serve_workspace,omitempty" yaml:"-"`

	// Manifest controls the tool-manifest optimization (v0.1.0). When
	// Compressed is true (the default), only the high-frequency "full" tools
	// plus ToolSearch are sent as callable defs each turn; all other
	// allowed tools appear in a compact manifest block in the system context
	// and are made callable on demand via ToolSearch. When false, every tool is
	// sent as a full callable def every turn (legacy behavior; backward-compat
	// kill-switch).
	Manifest ManifestConfig `json:"manifest" yaml:"manifest,omitempty"`

	// Delegate holds the operator controls for the `delegate` tool.
	// Maps to config.json: tools.delegate.*
	Delegate DelegateToolConfig `json:"delegate,omitempty" yaml:"-"`
}

// DelegateToolConfig holds operator controls for the `delegate` tool.
// Maps to config.json: tools.delegate.*
type DelegateToolConfig struct {
	// RequireParentAgentID gates the fail-closed parent-agent-id guard
	// (R2-MAJ-015): when it resolves TRUE (the default), a delegate call whose
	// context carries no resolvable calling-agent id is REFUSED outright rather
	// than minting a delegation record with an empty ParentAgentID — an
	// unattributable delegation is a broken audit chain, and a broken audit
	// chain is not a safe thing to persist.
	//
	// This exists because that guard's failure mode is "delegation stops
	// entirely": a wiring bug anywhere upstream of ToolAgentID turns every
	// delegate call in the install into an error, with no operator lever to
	// get work moving again while the real bug is diagnosed. Setting this to
	// FALSE downgrades the guard to a log-at-Error and mints with an empty
	// parent id — deliberately degraded attribution, chosen consciously, never
	// the default and never silent.
	//
	// Pointer + omitempty because the semantic default is TRUE: a plain bool
	// would make an explicit `false` indistinguishable from "unset" after a
	// round-trip through omitempty, so the kill switch could never actually be
	// turned off. nil = unset = true; an explicit false wins. Resolve via
	// EffectiveRequireParentAgentID, never by reading the pointer directly.
	RequireParentAgentID *bool `json:"require_parent_agent_id,omitempty"`
}

// EffectiveRequireParentAgentID resolves tools.delegate.require_parent_agent_id
// (R2-MAJ-015). An unset key resolves to TRUE — the fail-closed posture is the
// default, and an operator must opt OUT of it explicitly.
func (d DelegateToolConfig) EffectiveRequireParentAgentID() bool {
	return ResolveBool(d.RequireParentAgentID, true)
}

// ManifestConfig holds settings for the tool-manifest optimization.
// Maps to config.json: tools.manifest.*
type ManifestConfig struct {
	// Compressed controls whether the manifest optimization is active.
	// Default: true (enabled). When false, every tool is sent full (legacy behavior).
	// Controlled at runtime via PUT /api/v1/performance (tools_on_demand field).
	Compressed bool `json:"compressed" yaml:"compressed,omitempty"`

	// PreviewAllLazy reverts ADR-071 D3's three-tier visibility split: when
	// true, tools.ToolManifestVisibility returns ManifestPreviewed for EVERY
	// ManifestLazy tool, restoring the pre-D3 behavior where the whole lazy
	// catalog (Tier 2 + Tier 3) appears as preview lines in the compressed
	// manifest block. Default: false (the three-tier split — 17 always-listed,
	// 8 previewed, 63 search-only — is active on a fresh install and on every
	// upgrade). It does NOT revert User Story 1's permission filtering of
	// ToolSearch results, nor the per-(agent,session) scoping of the loaded-
	// tool set (ADR-071 §4.6) — those hold regardless of this flag.
	//
	// This is a stored-configuration dial, not an environment variable and
	// not a settings-screen control, read live inside ToolManifestVisibility
	// on every turn (no restart needed to flip it). It is explicitly
	// time-boxed (FR-043): it exists only to survive the operator observation
	// window for the omnipus_toolsearch_zero_result_total and
	// omnipus_toolsearch_no_followup_total detection counters
	// (pkg/gateway/metrics.go) — once those counters have produced enough
	// data to validate the split or motivate a widened Tier 2, this field
	// MUST be deleted in the same change that acts on that data.
	PreviewAllLazy bool `json:"preview_all_lazy,omitempty" yaml:"preview_all_lazy,omitempty"`
}

// RunInWorkspaceConfig holds dev-mode configuration for the web_serve tool.
// Maps to config.json: tools.run_in_workspace.* (key preserved for back-compat
// with operator configs predating the web_serve unification).
type RunInWorkspaceConfig struct {
	// WarmupTimeoutSeconds is the maximum number of seconds to wait for a dev server
	// to become responsive after spawning. Default is 60. The SPA polls via a
	// hidden probe-iframe (not cross-origin fetch) every 2 s up to this limit.
	WarmupTimeoutSeconds int32 `json:"warmup_timeout_seconds,omitempty"`
}

// BuildStaticConfig holds configuration for the build_static Tier 2 tool
// (FR-046b). Maps to config.json: tools.build_static.*
type BuildStaticConfig struct {
	// TimeoutSeconds is the wall-clock hard-kill deadline for the npm install
	// + build command pair. Default 300 s (applied by the boot validator).
	// Valid range: [1, 3600].
	TimeoutSeconds int32 `json:"timeout_seconds,omitempty"`

	// MemoryLimitBytes caps the child's address space (Linux RLIMIT_AS,
	// Windows JOB_OBJECT_LIMIT_PROCESS_MEMORY). Default 512 MiB (applied by
	// the boot validator). Valid range: [64 MiB, ~4 GiB].
	MemoryLimitBytes uint64 `json:"memory_limit_bytes,omitempty"`
}

// ServeWorkspaceConfig holds static-mode configuration for the web_serve tool.
// Maps to config.json: tools.serve_workspace.* (key preserved for back-compat
// with operator configs predating the web_serve unification).
type ServeWorkspaceConfig struct {
	// MaxDurationSeconds is the maximum registration lifetime in seconds.
	// Default 86400 (24 h), applied by the boot validator when zero.
	MaxDurationSeconds int32 `json:"max_duration_seconds,omitempty"`

	// MinDurationSeconds is the minimum registration lifetime in seconds.
	// Default 60, applied by the boot validator when zero.
	MinDurationSeconds int32 `json:"min_duration_seconds,omitempty"`
}

// BrowserToolConfig holds browser automation settings (Wave 4, US-4/US-6/US-7).
// Maps to config.json: tools.browser.*
//
// IMPORTANT (double-prefix bug, fixed): the sibling field ToolsConfig.Browser
// (above) intentionally carries NO `envPrefix` tag — every field below already
// has a fully-qualified `env:"OMNIPUS_TOOLS_BROWSER_..."` tag, so an envPrefix
// on the outer field would double it (caarlos0/env's Key = accumulated
// opts.Prefix + field's own env tag). The embedded ToolConfig.Enabled field
// below is the ONE exception: ToolConfig.Enabled's own tag is the relative
// `env:"ENABLED"` (shared by every other ToolConfig embedder, e.g. AppendFile,
// EditFile — each supplies its own envPrefix on ITS outer field instead). That
// means the envPrefix on the embedded ToolConfig line below is NOT redundant —
// it is the only remaining source of the "OMNIPUS_TOOLS_BROWSER_" prefix for
// Enabled and MUST stay, or OMNIPUS_TOOLS_BROWSER_ENABLED silently stops
// working. Verified empirically via env.GetFieldParams — see
// TestBrowserToolConfig_EnvKeys_NoDoublePrefix and
// TestBrowserToolConfig_EmbeddedToolConfig_RequiresEnvPrefix in
// env_prefix_guard_test.go.
type BrowserToolConfig struct {
	ToolConfig     `       envPrefix:"OMNIPUS_TOOLS_BROWSER_"`
	Headless       bool   `                                   json:"headless"        env:"OMNIPUS_TOOLS_BROWSER_HEADLESS"`
	CDPURL         string `                                   json:"cdp_url"         env:"OMNIPUS_TOOLS_BROWSER_CDP_URL"`
	PageTimeoutSec int    `                                   json:"page_timeout"    env:"OMNIPUS_TOOLS_BROWSER_PAGE_TIMEOUT"`
	MaxTabs        int    `                                   json:"max_tabs"        env:"OMNIPUS_TOOLS_BROWSER_MAX_TABS"`
	PersistSession bool   `                                   json:"persist_session" env:"OMNIPUS_TOOLS_BROWSER_PERSIST_SESSION"`
	ProfileDir     string `                                   json:"profile_dir"     env:"OMNIPUS_TOOLS_BROWSER_PROFILE_DIR"`
	// IdleTTLSec is how long an individual TAB may sit untouched before it is
	// reaped. Reaping is per tab, not per browsing context: each tab is judged
	// on its own last-touched time, and a context is torn down only once every
	// tab in it has gone. A context with an attached live-panel viewer is
	// exempt in full — every tab in it is listed in the panel's tab strip, so
	// all of them count as open in the UI. Zero (the unset default) leaves
	// pkg/tools/browser's own DefaultIdleTTL in force; a negative value
	// disables reaping entirely. Without reaping, closing the live panel leaks
	// the context forever — the panel close is a pure UI dismiss, so reopening
	// days later resurfaced the exact page left behind.
	IdleTTLSec int `json:"idle_ttl" env:"OMNIPUS_TOOLS_BROWSER_IDLE_TTL"`
	// StartPageURL is what a freshly created tab opens instead of about:blank.
	// Empty keeps about:blank.
	StartPageURL string `json:"start_page_url" env:"OMNIPUS_TOOLS_BROWSER_START_PAGE_URL"`
	// ExecPath overrides Chromium/Chrome binary discovery entirely — when
	// set, pkg/tools/browser.BrowserManager.resolveExecPath trusts this path
	// as-is (a stat check only, not the `--version` probe applied to $PATH
	// candidates) instead of probing $PATH or falling back to a managed
	// chrome-for-testing download. Empty (the default) means auto-discover:
	// a validated google-chrome/chromium binary on $PATH, else a managed
	// install under <profile_dir>/../chromium/.
	ExecPath string `json:"exec_path" env:"OMNIPUS_TOOLS_BROWSER_EXEC_PATH"`
	// MaxTotalTabs is the GLOBAL tab budget across ALL agents' browser contexts
	// in the shared Chrome (ADR-043 D7). 0/unset → UNLIMITED, like a normal
	// Chrome browser — this is the default. A positive value opts back into a
	// hard cross-agent ceiling for operators who want one. The per-agent
	// courtesy cap (tools.browser.max_tabs, default 5) is unaffected either
	// way and is the guard most operators actually want.
	//
	// The real limit on an unbounded tab count is host RAM, not a counter —
	// each renderer measured 74-268MB RSS on the UAT box — so an operator
	// running many agents on a small host should set this explicitly rather
	// than rely on the per-agent cap alone. Unlimited is safe as the default
	// because each BrowserManager's own idle reaper (ReapIdleSessions — a
	// manager method, swept per agent by the gateway; the coordinator has no
	// reaping role at all) runs per-tab on a short
	// TTL (tools.browser.idle_ttl, default 5 minutes as of the same change
	// that removed this cap), so steady-state tab count — and therefore RSS —
	// stays low without a global ceiling. Enforced by the coordinator's
	// TryOpenTab, which short-circuits with no budget arithmetic at all when
	// this is <=0 (browser_open_tab is only ever denied on this axis when a
	// positive value is configured and reached).
	MaxTotalTabs int `json:"max_total_tabs" env:"OMNIPUS_TOOLS_BROWSER_MAX_TOTAL_TABS"`
	// EvaluateEnabled gates browser.evaluate (arbitrary JS execution).
	// Defaults to false (deny-by-default per SEC-04/SEC-06). Must be explicitly
	// opted in by the operator since evaluate runs arbitrary JavaScript.
	EvaluateEnabled bool `json:"evaluate_enabled" env:"OMNIPUS_TOOLS_BROWSER_EVALUATE_ENABLED"`
	// LiveViewEnabled gates the ADR-038 live interactive browser panel: the
	// /api/v1/browser/ws screencast relay. Defaults to true.
	//
	// IMPORTANT: this does NOT control whether the /api/v1/browser/ws route
	// or HTTP handler exists — that WebSocket endpoint is ALWAYS registered
	// on the gateway's single listener (see gateway.go's newBrowserWSHandler
	// call site). The gateway has exactly one listener: ADR-044 retired the
	// separate preview listener/port, so /preview/ is now served on this same
	// main listener too. Setting this false only changes what happens AFTER a client connects and
	// authenticates: the gateway accepts the WS upgrade as normal, then
	// refuses the first browser_attach with a browser_status(error) frame
	// instead of starting a screencast. This is deliberate (ADR-038 D6's
	// comment on the handler): rejecting post-auth with a parseable error
	// frame gives the SPA a clear, typed reason, whereas refusing the
	// upgrade itself would surface to browser JS as an opaque WebSocket
	// error with no message.
	LiveViewEnabled bool `json:"live_view_enabled" env:"OMNIPUS_TOOLS_BROWSER_LIVE_VIEW_ENABLED"`
	// TakeControlEnabled gates interactive input injection on top of
	// LiveViewEnabled (ADR-038 D6). Defaults to true. Setting this false
	// keeps the live view watch-only: browser_control{action:"take"} is
	// refused with browser_status(error) even though the screencast itself
	// still streams.
	TakeControlEnabled bool `json:"take_control_enabled" env:"OMNIPUS_TOOLS_BROWSER_TAKE_CONTROL_ENABLED"`
	// WebRTCEnabled gates the ADR-047 WebRTC media path (audio+video via the
	// gateway's Pion SFU relay) on top of LiveViewEnabled. Defaults to true.
	// Post-auth gate, same posture as LiveViewEnabled: this does NOT affect
	// whether /api/v1/browser/ws or /api/v1/browser/capture-ingest exist —
	// both are always registered. Setting this false only changes what
	// happens after a viewer attaches: the gateway reports
	// browser_webrtc_state{available:false, reason:"disabled"} and never
	// starts a capture session or answers a browser_webrtc_offer, so every
	// viewer stays on the JPEG browser_screencast fallback tier (ADR-047
	// D3). Lite builds (-tags lite) compile the Pion relay out entirely
	// (ADR-047 D7) and behave as if this were false regardless of its
	// configured value (reason:"lite_build").
	WebRTCEnabled bool `json:"webrtc_enabled" env:"OMNIPUS_TOOLS_BROWSER_WEBRTC_ENABLED"`
	// PreferPackaged (ADR-052 D2/M1) makes the runtime package-managed Chrome
	// (sibling chromium/ dir next to the binary) outrank system Chrome on
	// $PATH for reproducibility across fleets.
	//
	// INTERACTION WITH TrustPathChrome: this preference only takes effect
	// when TrustPathChrome is ALSO true. With TrustPathChrome=false (the
	// SEC-ADR052-002 default), a $PATH Chrome is RECORDED by the resolver
	// (so operators can see what's happening) but the launch is REFUSED
	// and the gateway emits WARN-BROWSER-007 — the package Chrome becomes
	// the only candidate regardless of PreferPackaged's value. Set BOTH
	// fields together if you want a deliberately-newer $PATH Chrome to
	// outrank the package Chrome.
	//
	// Default false preserves operator autonomy: a deliberately
	// newer/patched $PATH Chrome still wins when TrustPathChrome is also
	// true. When PreferPackaged is true (and TrustPathChrome is also true),
	// the package Chrome — verified at package build via verifyGoogHashMD5
	// and stamped with chrome.sha256 — wins over both $PATH and the
	// runtime chrome-for-testing download path.
	PreferPackaged bool `json:"prefer_packaged" env:"OMNIPUS_TOOLS_BROWSER_PREFER_PACKAGED"`
	// TrustPathChrome (ADR-052 SEC-ADR052-002) gates whether the resolver
	// HONORS a system Chrome on $PATH when it outranks the verified package
	// Chrome. Default false: a $PATH Chrome is still RECORDED by the
	// resolver (so operators can see what's happening) but the launch is
	// refused — the resolver falls through to the package Chrome — and the
	// gateway emits WARN-BROWSER-007 at WARN severity. Operators who
	// deliberately want a custom $PATH Chrome (Homebrew, patched Chrome,
	// development) set this true. The integrity axis — "do we trust the
	// binary at the resolved path?" — is independent of
	// OMNIPUS_BROWSER_NO_SANDBOX (the inner-sandbox-suppression toggle).
	TrustPathChrome bool `json:"trust_path_chrome" env:"OMNIPUS_TOOLS_BROWSER_TRUST_PATH_CHROME"`
	// WebRTCStunServer is the STUN server URI (e.g.
	// "stun:stun.l.google.com:19302") the gateway's Pion relay uses for ICE
	// candidate gathering on both the viewer and capture-ingest legs.
	// Defaults to "stun:stun.l.google.com:19302". An empty string disables
	// STUN and restricts ICE to host candidates only (loopback-adjacent
	// deployments, or operators who want zero external network dependency
	// at the cost of NAT traversal). See ADR-047 D1.
	WebRTCStunServer string `json:"webrtc_stun_server" env:"OMNIPUS_TOOLS_BROWSER_WEBRTC_STUN_SERVER"`
	// WebRTCMediaUDPPort pins live-browser media to ONE fixed UDP port
	// (ADR-062 tier 1). 0 = the pre-ADR-062 ephemeral-port behaviour.
	//
	// Set this on ANY hosted install. Measured 2026-08-15 on Fly UAT: no
	// hosted provider routes inbound UDP to an undeclared ephemeral port, so
	// with 0 the viewer's ICE can never complete however healthy the network
	// is -- and the network WAS healthy there (raw datagrams and STUN
	// replies both traversed once the port was declared). Whatever value is
	// set here is the port the operator must expose/declare; it is also the
	// only port they need to expose for direct media.
	WebRTCMediaUDPPort int `json:"webrtc_media_udp_port,omitempty" env:"OMNIPUS_TOOLS_BROWSER_WEBRTC_MEDIA_UDP_PORT"`
	// WebRTCMediaUDPBindAddress is the address the fixed media socket binds.
	// Empty = all interfaces, which is right nearly everywhere.
	//
	// Exists because some platforms route inbound UDP to a specific address:
	// Fly.io requires "fly-global-services" and documents that binding
	// 0.0.0.0 makes Linux choose the WRONG SOURCE ADDRESS on replies, so the
	// peer discards them silently (fly.io/docs/networking/udp-and-tcp).
	WebRTCMediaUDPBindAddress string `json:"webrtc_media_udp_bind_address,omitempty" env:"OMNIPUS_TOOLS_BROWSER_WEBRTC_MEDIA_UDP_BIND_ADDRESS"`
	// WebRTCMediaTCPPort pins ICE-TCP to ONE fixed TCP port (ADR-062 tier 2).
	// 0 = UDP only. Hosted installs set this so a viewer whose network drops
	// raw UDP (VPN extensions) can open outbound TCP to the same public IP.
	WebRTCMediaTCPPort int `json:"webrtc_media_tcp_port,omitempty" env:"OMNIPUS_TOOLS_BROWSER_WEBRTC_MEDIA_TCP_PORT"`
	// WebRTCTurnUDPPort enables the embedded TURN relay (ADR-062 tier 3) on its
	// own UDP port. 0 = off, the default: tier 3 costs a declared port, so it is
	// opt-in. It serves the client that cannot send media directly to this
	// gateway at all -- a VPN extension eating Chrome's ICE traffic, a firewall
	// permitting only established outbound connections. Peers are restricted to
	// this gateway's own media address, so it can never become an open relay.
	WebRTCTurnUDPPort int `json:"webrtc_turn_udp_port,omitempty" env:"OMNIPUS_TOOLS_BROWSER_WEBRTC_TURN_UDP_PORT"`
	// WebRTCTurnTCPPort optionally adds a TCP listener for the same relay, for a
	// client that blocks UDP outright. On a hosted provider whose TCP proxy is
	// not a transparent byte pipe this will be reset (measured on Fly for
	// ICE-TCP, 2026-08-17).
	WebRTCTurnTCPPort int `json:"webrtc_turn_tcp_port,omitempty" env:"OMNIPUS_TOOLS_BROWSER_WEBRTC_TURN_TCP_PORT"`
	// WebRTCPublicIP overrides the address advertised to viewers as the media
	// host candidate. Normally EMPTY: the gateway derives it from
	// gateway.public_url, which an operator behind a domain has already set
	// -- ADR-062 deliberately adds no configuration the user must discover.
	// Set it only when the media address genuinely differs from the web
	// origin (split DNS, a separate media IP).
	WebRTCPublicIP string `json:"webrtc_public_ip,omitempty" env:"OMNIPUS_TOOLS_BROWSER_WEBRTC_PUBLIC_IP"`
	// CaptureSharedContext promotes the former OMNIPUS_BROWSER_CAPTURE_DEFAULT_CONTEXT
	// experimental env flag to a first-class config knob (ADR-048 condition 1).
	// When true, a browsing agent's own session is bootstrapped in Chrome's
	// DEFAULT browser context — the same context the WebRTC capture
	// extension's encoder page always lives in (capture_session.go's
	// defaultEncoderStarter) — instead of its own isolated, coordinator-owned
	// CDP browser context (ADR-043 D2). This is required for WebRTC capture to
	// work AT ALL: chrome.tabCapture cannot capture a tab living in a
	// CDP-created browser context ("Invalid tab specified."), and an
	// enableInIncognito-loaded extension gains VISIBILITY of such tabs
	// (chrome.tabs.query sees them) but never CAPTURABILITY (ADR-048,
	// verified against real Chrome 150).
	//
	// SECURITY / ISOLATION WARNING: enabling this REVERSES ADR-043 D2's
	// per-agent cookie/localStorage isolation for every agent whose session
	// lands in the shared default context — all such agents share ONE
	// cookie/storage partition, and ADR-043 D4's context-persistence-across-
	// reload and D7's per-agent tab budget no longer apply per-agent. Default
	// is TRUE (ADR-048 Option A: single-agent-dominant installs are the
	// common case, and video/audio live-view is a value operators expect out
	// of the box) — operators who need real cross-agent isolation and are
	// willing to give up WebRTC capture (the JPEG browser_screencast fallback
	// keeps working either way, ADR-047 D3) should set this false.
	//
	// ADR-048 condition 2 (the multi-agent capture-target gap): even with
	// this enabled, the shared default context's encoder captures whichever
	// tab is GLOBALLY active, not necessarily the attached agent's tab. When
	// more than one agent currently holds a live browser session, WHICH
	// agent's tab a viewer sees can be ambiguous — but a blanket "deny a new
	// capture whenever ANY other agent has a live session" fence (the
	// original ADR-048 shape) made the panel permanently video-less in the
	// most ordinary single-user flow, so the gateway's capture-start path
	// (pkg/gateway/browser_webrtc.go's handleWebRTCOffer) was re-scoped
	// (2026-07-18 UAT fix-wave) to: bring the REQUESTING agent's tab to
	// front before the encoder resolves its target (deterministically binds
	// THIS agent's tab even with other agents' windows present — see
	// capture_session.go's Start/bringAgentTabToFront), DENY starting a new
	// capture only when another agent's capture session is still ACTIVELY
	// VIEWED (ViewerCount() > 0 — a genuine, real-time focus conflict), and
	// silently SUPERSEDE (Stop) any other agent's viewerless leftover
	// session instead of denying against it. Single-agent-viewed-at-a-time
	// is still the v1 invariant; only the ambient "another agent merely has
	// a session" trigger was removed.
	//
	// OMNIPUS_BROWSER_CAPTURE_DEFAULT_CONTEXT is kept as a non-empty-string
	// override ("1"/"0") of this field, for tests and operators who need to
	// force a value without touching config.json — the coordinator reads
	// config, never a bare os.Getenv, for its own decision; the env var is
	// consulted only as an explicit override layered on top.
	CaptureSharedContext bool `json:"capture_shared_context" env:"OMNIPUS_TOOLS_BROWSER_CAPTURE_SHARED_CONTEXT"`

	// WarmAtBoot launches the shared Chrome eagerly during gateway boot
	// (BrowserCoordinator.WarmUp) instead of lazily on the first browser
	// tool call. Default true.
	//
	// Why eager: the lazy path resolves the binary, launches Chrome over the
	// CDP pipe, creates the first tab and loads the capture extension, all
	// inside whatever request first needs a browser. That cold start is
	// expensive (ADR-042 records ~30-60s historically on a fresh install)
	// and its cost lands on a user-facing interaction — including the
	// WebRTC offer path, where it has to fit inside the browser
	// WebSocket's own 60s read deadline.
	//
	// Turning this off does NOT disable the browser: tools stay available
	// and Chrome still launches lazily at first use. It only trades a
	// slower first interaction for a cheaper, quieter boot — useful on
	// memory-tight hosts, or where an operator does not want a browser
	// process running until something actually asks for one.
	//
	// Warm-up is best-effort and never blocks or fails boot (Hard
	// Constraint #4, graceful degradation): a failure is logged at WARN and
	// the lazy path remains the fallback. It is additionally skipped
	// entirely by OMNIPUS_SKIP_BROWSER_PREPROVISION=1, which test harnesses
	// use for a fully browser-inert boot, and it is never attempted when
	// browser tools are disabled or when CDPURL points at a remote Chrome
	// this gateway does not own.
	WarmAtBoot bool `json:"warm_at_boot" env:"OMNIPUS_TOOLS_BROWSER_WARM_AT_BOOT"`

	// WarmTabAtBoot creates the first browsing tab (parked on the configured
	// start page, tools.browser.start_page_url — normally the gateway's own
	// /browser-start landing page) during boot, right after WarmAtBoot brought
	// the shared Chrome process up. Default true.
	//
	// Why: WarmAtBoot alone launches the Chrome BROWSER process and stops
	// there — measured on a host whose Chrome binary is already present, the
	// process was live 2.8s after boot with ZERO renderer processes: no
	// browsing context, no tab, no page. The first panel open then paid
	// 1.0-2.2s to build that tab on demand, on the user's critical path. A tab
	// parked on a static local page costs almost nothing to keep (one idle
	// renderer) and removes that wait.
	//
	// It warms the SAME session the live panel and the agent's own browser
	// tools use (browser.DefaultSessionID) on ONE agent — the default agent
	// (agents.defaults.default_agent_id), or, when that is unset/has no
	// browser manager, the lexicographically-first agent that has one. It is
	// not a per-agent pool: warming every agent would spawn one renderer per
	// agent for a panel only one of them is likely to open first.
	//
	// Best-effort and never blocks or fails boot (Hard Constraint #4), exactly
	// like WarmAtBoot: a failure is logged at WARN, audited, and the ordinary
	// lazy path still builds the tab at first use. Turning it off leaves the
	// browser fully functional. It is skipped whenever WarmAtBoot itself is
	// skipped (browser tools disabled, a remote cdp_url, or
	// OMNIPUS_SKIP_BROWSER_PREPROVISION=1).
	//
	// Note the idle reaper still owns the tab's LIFETIME: an untouched warm
	// tab is closed after tools.browser.idle_ttl_sec like any other, so a
	// first open long after boot pays tab creation again (against an
	// already-warm Chrome, which is far cheaper than a cold start).
	WarmTabAtBoot bool `json:"warm_tab_at_boot" env:"OMNIPUS_TOOLS_BROWSER_WARM_TAB_AT_BOOT"`

	// WarmCaptureAtBoot starts the WebRTC capture pipeline (the capture
	// extension's encoder page + its ingest leg into the gateway's relay)
	// during boot instead of on the first viewer's offer. Default true.
	//
	// Why: with Chrome and the tab already warm, the encoder page's load,
	// ingest connect and WebRTC negotiation are what is left of a measured
	// ~9.5s first open (1.7-6.7s of it), and they are also what fails first
	// under load — one first open in three timed out waiting for the ingest
	// video track. Starting the pipeline at boot makes the first open
	// near-instant, because the viewer's offer joins a capture that is already
	// producing frames.
	//
	// COST, and why WarmCaptureIdleSec exists: unlike a parked tab, a running
	// capture encodes video continuously and burns CPU for as long as it runs.
	// On a shared-CPU host that is the very resource whose exhaustion produces
	// the 1fps stream this warm-up exists to avoid. So the warm capture is NOT
	// permanent: it stops itself after WarmCaptureIdleSec with no viewer ever
	// attached, and the next viewer offer starts a fresh one on the ordinary
	// path. Once a viewer HAS attached, the warm-up hands the session over to
	// the normal last-viewer-detach grace stop and never stops it itself.
	//
	// Same agent selection, same best-effort/never-fatal contract, and the
	// same opt-outs as WarmTabAtBoot. Additionally skipped whenever WebRTC
	// capture is unavailable for that agent anyway (webrtc_enabled=false, a
	// lite build with no WebRTC, or a not-capture-capable configuration) —
	// evaluated with the SAME gate a real viewer offer uses, so warm-up can
	// never start something an offer would have refused.
	WarmCaptureAtBoot bool `json:"warm_capture_at_boot" env:"OMNIPUS_TOOLS_BROWSER_WARM_CAPTURE_AT_BOOT"`

	// WarmCaptureIdleSec is how long (in seconds) a boot-warmed capture keeps
	// running with NO viewer ever attached before it stops itself. Default 300
	// (5 minutes) — long enough to cover the realistic "operator restarts the
	// gateway, then opens the panel" window, short enough that an unattended
	// host is not encoding video forever.
	//
	// <= 0 means "never stop it on idle": the warm capture then runs until a
	// viewer takes it over or the gateway shuts down. Only sensible on a host
	// with CPU to spare — see WarmCaptureAtBoot's cost note. Ignored entirely
	// when WarmCaptureAtBoot is false.
	WarmCaptureIdleSec int `json:"warm_capture_idle_sec" env:"OMNIPUS_TOOLS_BROWSER_WARM_CAPTURE_IDLE_SEC"`
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

type SearchCacheConfig struct {
	MaxSize    int `json:"max_size"    env:"OMNIPUS_SKILLS_SEARCH_CACHE_MAX_SIZE"`
	TTLSeconds int `json:"ttl_seconds" env:"OMNIPUS_SKILLS_SEARCH_CACHE_TTL_SECONDS"`
}

// MCPServerConfig defines configuration for a single MCP server
type MCPServerConfig struct {
	// Enabled indicates whether this MCP server is active
	Enabled bool `json:"enabled"`
	// Deferred controls whether this server's tools are registered as hidden (deferred/discovery mode).
	// When nil, the global Discovery.Enabled setting applies.
	// When explicitly set to true or false, it overrides the global setting for this server only.
	Deferred *bool `json:"deferred,omitempty"`
	// Command is the executable to run (e.g., "npx", "python", "/path/to/server")
	Command string `json:"command"`
	// Args are the arguments to pass to the command
	Args []string `json:"args,omitempty"`
	// Env are environment variables to set for the server process (stdio only).
	// Written directly here means the value is stored in config.json IN
	// PLAINTEXT — legacy/back-compat path only (e.g. a server added via the
	// gateway REST API). Prefer EnvRefs.
	Env map[string]string `json:"env,omitempty"`
	// EnvRefs are credential-store references for stdio env vars: key = env
	// var name, value = the credential-store key holding the real secret.
	// add_mcp_server (pkg/sysagent/tools/mcp.go) routes every value passed in
	// its `env` parameter through the encrypted credential store rather than
	// writing it to config.json in plaintext — this is where the resulting
	// refs land. At connect time, pkg/mcp.ResolveServerEnvRefs resolves each
	// ref to its real value and merges it into Env IN MEMORY ONLY (never
	// written back to config.json); an EnvRefs entry overrides a same-named
	// literal Env entry. See pkg/agent/loop_mcp.go's reconcileLocked.
	EnvRefs map[string]string `json:"env_refs,omitempty"`
	// EnvFile is the path to a file containing environment variables (stdio only)
	EnvFile string `json:"env_file,omitempty"`
	// Type is "stdio", "sse", or "http" (default: stdio if command is set, sse if url is set)
	Type string `json:"type,omitempty"`
	// URL is used for SSE/HTTP transport
	URL string `json:"url,omitempty"`
	// Headers are HTTP headers to send with requests (sse/http only)
	Headers map[string]string `json:"headers,omitempty"`
}

// MCPConfig defines configuration for all MCP servers
type MCPConfig struct {
	ToolConfig `                    envPrefix:"OMNIPUS_TOOLS_MCP_"`
	Discovery  ToolDiscoveryConfig `                               json:"discovery"`
	// Servers is a map of server name to server configuration
	Servers map[string]MCPServerConfig `json:"servers,omitempty"`
}

// CredentialStore is a minimal interface satisfied by credentials.Store.
// Using an interface here avoids a circular import (config → credentials).
// The caller (gateway, CLI commands) supplies the real store.
type CredentialStore interface {
	// Set stores a named credential value.
	Set(name, value string) error
}

// normalizeProviderRows trims whitespace off the identity fields of every
// configured provider row (ADR-067 FR-036). It never changes case, never
// rewrites an id, and never drops a row: an id that is wrong after trimming
// stays wrong, and is reported as an unknown provider by whoever asks the
// catalog about it.
func normalizeProviderRows(cfg *Config) {
	if cfg == nil {
		return
	}
	for i := range cfg.Providers {
		p := cfg.Providers[i]
		if p == nil {
			continue
		}
		p.Provider = strings.TrimSpace(p.Provider)
		p.Model = strings.TrimSpace(p.Model)
		p.Protocol = strings.TrimSpace(p.Protocol)
		p.APIBase = strings.TrimSpace(p.APIBase)
	}
}

// LoadConfigWithStore loads a config, threading store through to callers that
// need it for credential-store-backed operations during load (per the
// documented credential boot contract, ADR-004: NewStore → Unlock →
// LoadConfigWithStore → InjectFromConfig → ...).
func LoadConfigWithStore(path string, store CredentialStore) (*Config, error) {
	return loadConfigInternal(path, store, nil)
}

func LoadConfig(path string) (*Config, error) {
	return loadConfigInternal(path, nil, nil)
}

// SelfHealWriteHook is invoked with the exact bytes written to config.json
// whenever loadConfigInternal performs an in-place self-heal/migration write
// as a side effect of loading. Originally built for the (now-retired)
// single-user-role self-heal (selfHealUserRolesOnDisk); the CLI-token
// relocation (migrateCLITokenOutOfUsers, cli_token_migration.go) is the
// current writer that reports through this hook — any future load-time
// self-heal should do the same rather than writing to disk silently.
//
// Why this exists: pkg/gateway maintains a write-dedup registry
// (configSelfWriteRegistry) so its config-file-watcher polling loop can tell
// an app-initiated write apart from a genuine external edit (and skip a
// spurious full-service reload for the former). pkg/config cannot reach that
// registry directly — it cannot import pkg/gateway (see the import-cycle
// note on AuditEmitter in validate.go) — so the capability is threaded the
// other way: the self-heal reports what it wrote (if anything), and the
// caller decides whether/how to register it. Callers that don't need this
// (most callers of LoadConfig/LoadConfigWithStore) pass a nil hook via those
// functions, which is always safe — onSelfHeal is only ever invoked when a
// write actually happened.
type SelfHealWriteHook func(writtenBytes []byte)

// LoadConfigWithStoreAndSelfHealHook behaves exactly like LoadConfigWithStore,
// except that onSelfHeal (if non-nil) is invoked with the raw bytes written to
// config.json whenever a load-time self-heal/migration (currently: the
// CLI-token relocation, migrateCLITokenOutOfUsers) performs a write. Used by
// pkg/gateway's config-file-watcher polling loop and manual /reload trigger —
// the two read paths that call into config loading directly, bypassing
// safeUpdateConfigJSON's own configMu + selfWriteReg registration — so a
// self-heal write triggered from either of those paths is still recognized
// as app-initiated rather than a genuine external edit.
func LoadConfigWithStoreAndSelfHealHook(
	path string,
	store CredentialStore,
	onSelfHeal SelfHealWriteHook,
) (*Config, error) {
	return loadConfigInternal(path, store, onSelfHeal)
}

// seedPublicURLFromEnv fills cfg.Gateway.PublicURL from $DEVPOD_PREVIEW_URL when
// the operator hasn't set one (via config.json or OMNIPUS_GATEWAY_PUBLIC_URL).
//
// Fill-only-when-empty; trailing slash trimmed so `origin + "/preview/…"` can't
// produce a double slash. In-memory boot seed only (never written back to
// config.json), preserving ADR-044's boot-frozen public_url contract.
//
// Scope (Constraint #1, single-binary core, no vendor lock-in): the GENERIC,
// platform-agnostic override is OMNIPUS_GATEWAY_PUBLIC_URL
// (env:"OMNIPUS_GATEWAY_PUBLIC_URL" on PublicURL, resolved by env.Parse) or
// gateway.public_url in config.json — both always win over this. DEVPOD_PREVIEW_URL
// is a deliberately narrow devpod-platform CONVENIENCE fallback for zero-config
// pods; it is read nowhere else and must never become the primary mechanism.
//
// MUST be applied on EVERY loadConfigInternal return path — INCLUDING the
// fresh-install DefaultConfig() paths — because a brand-new pod has no
// config.json, which is exactly when this needs to fire (otherwise first boot
// leaves public_url empty and web_serve/preview links fall back to
// http://localhost:5000, unreachable from outside the pod).
func seedPublicURLFromEnv(cfg *Config) {
	if cfg == nil || strings.TrimSpace(cfg.Gateway.PublicURL) != "" {
		return
	}
	previewURL := strings.TrimRight(strings.TrimSpace(os.Getenv("DEVPOD_PREVIEW_URL")), "/")
	if previewURL == "" {
		return
	}
	cfg.Gateway.PublicURL = previewURL
	logger.WarnF("gateway.public_url not set; auto-detected from DEVPOD_PREVIEW_URL", map[string]any{
		"public_url": previewURL,
	})
}

func loadConfigInternal(path string, store CredentialStore, onSelfHeal SelfHealWriteHook) (*Config, error) {
	logger.Debugf("loading config from %s", path)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			logger.WarnF("config file not found, using default config", map[string]any{"path": path})
			c := DefaultConfig()
			seedPublicURLFromEnv(c) // fresh pod: no config.json, but $DEVPOD_PREVIEW_URL may be set
			return c, nil
		}
		logger.Errorf("failed to read config file: %v", err)
		return nil, err
	}

	// First, try to detect config version by reading the version field.
	// A POINTER so an absent "version" key is distinguishable from an
	// explicit "version": 0 — the two need different errors (a missing field
	// is an operator omission with an obvious fix; an explicit 0 is a config
	// predating the current schema, which has no migration path).
	var versionInfo struct {
		Version *int `json:"version"`
	}
	if e := json.Unmarshal(data, &versionInfo); e != nil {
		return nil, fmt.Errorf("failed to detect config version: %w", e)
	}

	if validateErr := validateRemovedKeys(data); validateErr != nil {
		return nil, validateErr
	}
	if len(data) <= 10 {
		logger.Warn(fmt.Sprintf("content is [%s]", string(data)))
		c := DefaultConfig()
		seedPublicURLFromEnv(c)
		return c, nil
	}

	// Load config based on detected version
	var cfg *Config
	if versionInfo.Version == nil {
		return nil, fmt.Errorf("config is missing a version field — add \"version\": %d", CurrentVersion)
	}
	switch *versionInfo.Version {
	case CurrentVersion:
		// Current version
		cfg, err = loadConfig(data)
		if err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("unsupported config version: %d", *versionInfo.Version)
	}

	if err := env.Parse(cfg); err != nil {
		return nil, err
	}

	// ADR-067 FR-036 / A-19: the config boundary is the ONE place a provider
	// id is normalised, and the normalisation is TRIM-ONLY. Case is
	// preserved and therefore significant: `" ZAI "` becomes `"ZAI"`, which
	// is an unknown provider — not a quietly-corrected `zai`. Case folding
	// here would be the last surviving alias mechanism, and an id that
	// silently changes meaning between the file and the catalog is exactly
	// the class of defect ADR-067 removed.
	normalizeProviderRows(cfg)

	// Phase 1B FR-006: normalize legacy `fallback_models: [string]` entries
	// into the new `[{model, provider}]` form. Provider resolution mirrors
	// the chat-side passthrough lookup (openrouter / vivgrid).
	for i := range cfg.Agents.List {
		if len(cfg.Agents.List[i].FallbackModels) == 0 {
			continue
		}
		cfg.Agents.List[i].FallbackModels = NormalizeFallbacks(cfg, cfg.Agents.List[i].FallbackModels)
	}

	// Migrate legacy channel config fields to new unified structures
	cfg.migrateChannelConfigs()

	if cfg.Channels == nil {
		cfg.Channels = make(map[string]ChannelInstanceConfig)
	}
	// ADR-029 (v0.3): validate channel instance keys, effective types, and
	// workspace binding completeness (half-bound instances are rejected).
	// Run on the RAW map BEFORE normalizeChannelMap so malformed keys are
	// caught before normalization silently discards them.
	if err := ValidateChannels(cfg.Channels); err != nil {
		return nil, err
	}

	// Normalize channel map: populate Type from map key when absent, drop unknown types.
	cfg.Channels = normalizeChannelMap(cfg.Channels)

	// Reject typo'd channel identity Kind values so a mis-spelled kind fails
	// loudly here instead of silently downgrading routing (route.go treats any
	// non-"agent" identity kind as the user fallback). Empty/absent kinds keep
	// their documented defaults.
	if err := validateIdentityKinds(cfg); err != nil {
		return nil, err
	}

	// Merge Omnipus channel_policies routing rules into Bindings.
	cfg.MergeChannelPoliciesIntoBindings()

	// Validate model_list for uniqueness and required fields
	if err := cfg.ValidateProviders(); err != nil {
		return nil, err
	}

	// Ensure Workspace has a default if not set. Routed through the single
	// mandatory OmnipusHomeDir helper (pkg/config/home.go) instead of
	// reimplementing $OMNIPUS_HOME / ~/.omnipus / temp-dir-fallback
	// resolution here — that reimplementation used to skip the temp-dir
	// safety net entirely when os.UserHomeDir() failed (silently continuing
	// with an empty home path) and never resolved a relative $OMNIPUS_HOME,
	// unlike OmnipusHomeDir(). Common case (env unset, real $HOME) is
	// byte-identical to before.
	if cfg.Agents.Defaults.Home == "" {
		cfg.Agents.Defaults.Home = filepath.Join(OmnipusHomeDir(), pkg.WorkspaceName)
	}

	// C1 fix (ADR-067 FR-034): the O3 "provider/model" prefix-splitting
	// migrations (migrateProviderFields for model_list, migrateAgentPrimaryProvider
	// for the per-agent primary model) are deleted, greenfield — no migration is
	// owed. A `/` inside Model/Primary is DATA, never a routing-protocol prefix
	// (see ModelConfig.Model's doc comment above); splitting on a stale table of
	// "known" protocol slugs silently rerouted models whose bare id happened to
	// start with a live provider id (e.g. "google/gemini-2.5-pro") to the WRONG
	// vendor with no error. A model_list row that still carries the old
	// "<protocol>/<model>" shape with an empty Provider now fails
	// ValidateProviders ("provider is required") instead of being silently
	// resolved — a clean, visible config error. An agent's bare-string Primary
	// keeps its full id with Provider empty, which legitimately trips the
	// pre-turn needs_provider gate (ADR-067 T067-09) so the operator picks a
	// provider explicitly.

	// ADR-054 D1/D2/§11 checklist item 8: agents are no longer entities inside
	// config.json — drop any legacy agents.list content (loudly, in-memory AND
	// best-effort on disk) so it never round-trips forward. Must run before
	// RepairMultipleDefaults/RepairIncompleteToolPolicyCoverage below so those
	// per-agent repairs operate on the post-cutover (now-empty, until the agent
	// registry separately populates it from entities/agents/) list, never on
	// stale legacy JSON content. See legacy_agents_list.go.
	stripLegacyAgentsList(cfg, path, onSelfHeal)

	// NOTE (ADR-054 D6.4): this used to call RepairMultipleDefaults(cfg) here
	// to enforce an at-most-one AgentConfig.Default==true invariant (F11).
	// That repair function — and the field's role in default-agent
	// resolution — is retired: RepairMultipleDefaults and
	// AgentInstance.IsRoutingDefault (which consumed it) are dead code and
	// have been removed. Splitting agents into independent per-entity files
	// means two concurrent writes to two different agents could each set
	// Default=true with no shared lock to serialize them (each delta
	// individually valid, the composition not). Rather than inventing a
	// cross-entity lock for a single bool, "the one default" signal moved
	// entirely to the settings singleton (Agents.Defaults.DefaultAgentID,
	// which already existed) — a single string field the existing
	// config-write lock already serializes, structurally incapable of having
	// two winners. See pkg/agent.AgentRegistry.GetDefaultAgent and
	// pkg/routing.RouteResolver.resolveDefaultAgentID for the current
	// (settings-only) resolution ladder. AgentConfig.Default itself is left
	// in place on the wire/struct (read/written by the REST agent
	// create/PUT/list handlers) purely for backward display compatibility
	// until those handlers are converted to the entity-store write path;
	// it is no longer consulted by any resolution logic.

	// ADR-029 FR-029/OBS-001: enforce the two-representation rule. A channel
	// instance that carries BOTH a bound representation (WorkspaceID + Identity)
	// AND a stale channel-wildcard AgentBinding in cfg.Bindings is inconsistent —
	// the bound representation wins. Drop the stale wildcard binding so the
	// on-disk state is self-consistent after this load (mirrors the old
	// RepairMultipleDefaults pattern this replaced).
	RepairStaleChannelWildcardBindings(cfg)

	// Apply schedules guardrail defaults (#264 FR-003/FR-007) so a loaded
	// config without a schedules block still gets 8 / 300.
	cfg.Schedules.ApplyDefaults()

	// CLI-token relocation (single-user model follow-up): a legacy config may
	// carry a role-less "cli" entry in Gateway.Users that exists only to hold
	// the CLI's bearer token. Now that the human-account/role concept is
	// gone, that token gets a dedicated Gateway.CLIToken slot instead of a
	// synthetic Users[] row. One-shot, best-effort, idempotent — see
	// migrateCLITokenOutOfUsers in cli_token_migration.go.
	migrateCLITokenOutOfUsers(cfg, path, onSelfHeal)

	// Single-account diagnostic (single-user model follow-up): advisory
	// only — every configured Gateway.Users account authenticates fine
	// today (checkBearerAuth/authenticateWS/withOptionalAuth in
	// pkg/gateway all loop the whole slice). This just flags a legacy
	// config that still carries more than one Gateway.Users entry from
	// before the Users CRUD API was deleted, since a couple of non-auth
	// code paths (default workspace-owner attribution, schedule-failure-
	// notification fallback) only ever consider the first entry and the
	// single-user model expects exactly one account. Deliberately
	// WARN-only, never truncates — see single_account_migration.go's
	// package doc for why a destructive self-heal here is unsafe (it would
	// race a second account's own in-flight login). Runs on every load,
	// all config versions.
	warnAboutExtraUsers(cfg.Gateway.Users)

	// Auto-detect gateway.public_url from $DEVPOD_PREVIEW_URL when unset — see
	// seedPublicURLFromEnv for the full rationale (Constraint #1 / ADR-044).
	// Runs here before validateBootConfig so the auto-detected value gets the
	// same well-formed-URL check as an operator-supplied one. NOTE: the same
	// call also runs on the two fresh-install DefaultConfig() return paths
	// above — a bare pod's first boot has no config.json at all, which is
	// exactly when the devpod fallback needs to fire.
	seedPublicURLFromEnv(cfg)

	// Apply defaults and validate bounds for all security-relevant fields
	// (FR-001, FR-002a, numeric sandbox fields, AuthMismatchLogLevel).
	if err := validateBootConfig(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// C1 fix (ADR-067 FR-034, confirmed-critical defect): knownProviderProtocols,
// migrateProviderFields (model_list) and migrateAgentPrimaryProvider (per-agent
// primary model, O3) are DELETED, greenfield — no migration is owed for this
// shape. The deleted table still held retired protocol spellings
// (anthropic-messages, gemini, qwen-intl, dashscope-intl, azure-openai,
// bedrock, coding-plan, alibaba-coding, mimo, ...) and both functions split
// ANY bare model id whose leading path segment happened to collide with a
// live provider id — e.g. a legacy bare-string primary of
// "google/gemini-2.5-pro" (AgentModelConfig.UnmarshalJSON leaves Provider=""
// for that form) silently became Primary="gemini-2.5-pro", Provider="google",
// even though the operator never named a provider at all. Measured against
// the shipped 202-provider catalog: 196/360 OpenRouter model ids collided
// with the table, 88 of those split into a VALID pair at a DIFFERENT vendor
// (the turn then succeeded silently on the wrong provider, wrong credential,
// wrong bill) and the remaining 16 minted a retired id no longer in the
// catalog (ErrUnknownProvider naming an id the operator never typed). This
// directly contradicted ModelConfig.Model's own doc comment (below): "a `/`
// inside it is data, not a prefix ... it is never split."
//
// What remains instead: a slash in Model/Primary is always data. A model_list
// row that still carries the old "<protocol>/<model>" shape with an empty
// Provider now fails ValidateProviders ("provider is required") — a clean,
// visible config error instead of a silent wrong-vendor route. An agent's
// bare-string Primary keeps its full id verbatim with Provider empty, which
// legitimately trips the pre-turn needs_provider gate (ADR-067 T067-09) so
// the operator picks a provider explicitly.

func (c *Config) migrateChannelConfigs() {
	// Discord: mention_only -> group_trigger.mention_only (preserved from the typed singleton era).
	// The map may have zero, one, or (after v0.3) more discord instances. Walk the
	// map and normalise any instance of type "discord" that has the legacy flag set
	// without the group_trigger equivalent.
	for id, inst := range c.Channels {
		if inst.Type == "discord" && inst.MentionOnly && !inst.GroupTrigger.MentionOnly {
			inst.GroupTrigger.MentionOnly = true
			c.Channels[id] = inst
		}
	}
}

func SaveConfig(path string, cfg *Config) error {
	if cfg.Version < CurrentVersion {
		cfg.Version = CurrentVersion
	}

	// Verify the target directory already exists. SaveConfig is a "save to an
	// existing location" operation — it does not provision new directory trees.
	// Callers that need to create a new config path must ensure the directory
	// exists first (e.g., the first-run gateway path via os.MkdirAll).
	dir := filepath.Dir(path)
	if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
		return fmt.Errorf("failed to create directory: directory does not exist: %s", dir)
	}

	// Filter out virtual models before serializing to config file
	nonVirtualModels := make([]*ModelConfig, 0, len(cfg.Providers))
	for _, m := range cfg.Providers {
		if !m.isVirtual {
			nonVirtualModels = append(nonVirtualModels, m)
		}
	}
	// Temporarily replace ModelList with filtered version for serialization
	originalModelList := cfg.Providers
	cfg.Providers = nonVirtualModels

	data, err := json.MarshalIndent(cfg, "", "  ")
	// Restore original ModelList after serialization regardless of outcome.
	cfg.Providers = originalModelList
	if err != nil {
		return err
	}
	logger.Infof("saving config to %s", path)
	if err := fileutil.WriteFileAtomic(path, data, 0o600); err != nil {
		return err
	}
	return nil
}

func (c *Config) AgentHomeBasePath() string {
	return expandHome(c.Agents.Defaults.Home)
}

func expandHome(path string) string {
	if path == "" {
		return path
	}
	if path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			logger.WarnCF("config", "UserHomeDir failed in expandHome; path expansion may be incorrect",
				map[string]any{"path": path, "error": err.Error()})
		}
		if len(path) > 1 && path[1] == '/' {
			return home + path[1:]
		}
		return home
	}
	return path
}

// GetModelConfig returns the ModelConfig for the EXACT (provider, model)
// pair (ADR-068 D14.1 / ADR-067's exact lookup). Both halves are required —
// the model id alone is not a key, a row's user-facing model_name alias is
// not a key (that alias resolution was deleted with agents.defaults.model_name,
// CRIT-001), and no prefix stripping or cross-provider fallback happens here.
// If several providers[] rows carry the same pair (load balancing) it
// round-robins over the USABLE ones only, see findMatches.
// Returns an error if no row carries the pair.
func (c *Config) GetModelConfig(provider, model string) (*ModelConfig, error) {
	matches := c.findMatches(provider, model)
	if len(matches) == 0 {
		return nil, fmt.Errorf("model %q not found in providers for provider %q", model, provider)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}

	// Multiple configs - use round-robin for load balancing
	idx := (rrCounter.Add(1) - 1) % uint64(len(matches))
	return matches[idx], nil
}

// modelConfigCredentialUsable reports whether m's credential requirement is
// satisfied: either it names no vault ref at all (local model, CLI/OAuth
// auth, api_base-only provider — these never had a vault credential to
// begin with), or its api_key_ref resolved to a non-empty value in the
// process environment (InjectFromConfig runs at boot/reload, before any
// caller reaches GetModelConfig). Mirrors the "usable" test
// gateway.go's defaultModelCredentialBlocked applies to the default model.
func modelConfigCredentialUsable(m *ModelConfig) bool {
	if m == nil {
		return false
	}
	if strings.TrimSpace(m.APIKeyRef) == "" {
		return true
	}
	return m.APIKey() != ""
}

// findMatches finds all ModelConfig entries carrying the exact
// (provider, model) pair, preferring USABLE ones (see
// modelConfigCredentialUsable) when at least one exists. Whitespace-trimmed
// on both sides; an empty model never matches, and an empty provider matches
// only rows whose own provider is empty (exact, never "any provider").
//
// Why (2026-08-15): several providers[] entries may share one pair for
// load balancing, round-robinned by GetModelConfig above. Before this
// change, an entry whose api_key_ref never resolved (missing from the
// vault, wrong master key while it was still degradable, …) stayed in the
// candidate pool on equal footing with a working sibling — round-robin,
// being a plain counter with no key-awareness, could hand the broken entry
// back to CreateProviderFromConfig, which happily builds an *HTTPProvider
// with an empty API key (api_key OR api_base satisfies its check) and
// produces a bare upstream 401 naming neither the provider nor the
// credential — non-deterministically, since which call in the rotation gets
// the broken entry depends on the shared global rrCounter. This was
// unreachable before gateway.go's reportInjectionErrors started degrading
// (rather than aborting) on a single unresolvable provider ref, because
// boot used to abort outright on that config; the degrade-not-abort fix
// made this reachable for the first time.
//
// Filtering to the usable subset (when non-empty) removes the broken
// entries from the rotation entirely — the exact fix load-balanced
// failover already implies: an unusable sibling behaves like it isn't
// there. If NONE of the matches are usable, all of them are returned
// unfiltered so the caller still gets a ModelConfig back (and, for the
// default model, gateway.go's defaultModelCredentialBlocked reports it as
// blocked rather than silently 401ing).
func (c *Config) findMatches(provider, model string) []*ModelConfig {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if model == "" {
		return nil
	}
	var all []*ModelConfig
	var usable []*ModelConfig
	for i := range c.Providers {
		if c.Providers[i] == nil ||
			strings.TrimSpace(c.Providers[i].Provider) != provider ||
			strings.TrimSpace(c.Providers[i].Model) != model {
			continue
		}
		all = append(all, c.Providers[i])
		if modelConfigCredentialUsable(c.Providers[i]) {
			usable = append(usable, c.Providers[i])
		}
	}
	if len(usable) > 0 {
		return usable
	}
	return all
}

// FindModelConfigBySlug resolves a bare model slug — a per-agent `model`, a
// `voice.model_name`, a composer pick — to the providers[] row that SERVES
// it, without a provider half. It is NOT the default-model lookup (that is
// the exact pair, GetModelConfig) and it applies no passthrough fallback
// (pkg/agent's ResolveModelCfg layers that on top).
//
// Order: a row whose Model equals the slug wins — and that is the ONLY
// rung. The display-alias rung is gone with ModelConfig.ModelName (ADR-067
// FR-013 / X-25). Among several hits the USABLE ones are preferred
// (modelConfigCredentialUsable), round-robinned like GetModelConfig.
func (c *Config) FindModelConfigBySlug(slug string) (*ModelConfig, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return nil, fmt.Errorf("model slug is required")
	}
	pick := func(match func(*ModelConfig) bool) *ModelConfig {
		var all, usable []*ModelConfig
		for i := range c.Providers {
			m := c.Providers[i]
			if m == nil || !match(m) {
				continue
			}
			all = append(all, m)
			if modelConfigCredentialUsable(m) {
				usable = append(usable, m)
			}
		}
		pool := all
		if len(usable) > 0 {
			pool = usable
		}
		switch len(pool) {
		case 0:
			return nil
		case 1:
			return pool[0]
		default:
			return pool[(rrCounter.Add(1)-1)%uint64(len(pool))]
		}
	}
	if m := pick(func(m *ModelConfig) bool { return strings.TrimSpace(m.Model) == slug }); m != nil {
		return m, nil
	}
	return nil, fmt.Errorf("model %q not found in model_list or providers", slug)
}

// ValidateProviders validates all ModelConfig entries in the providers config.
// It checks that each model config is valid.
// Note: Multiple entries with the same (provider, model) pair are allowed —
// that is how multi-key load balancing is expressed.
func (c *Config) ValidateProviders() error {
	for i := range c.Providers {
		if err := c.Providers[i].Validate(); err != nil {
			return fmt.Errorf("providers[%d]: %w", i, err)
		}
	}
	return nil
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

// DefaultOrphanedTurnGraceSeconds is the semantic default for
// gateway.orphaned_turn_grace_seconds when unset (ADR-045). It is 0 —
// meaning the orphaned-foreground-turn watchdog is DISABLED by default.
//
// Omnipus is built to run turns as background work: closing a chat tab (or
// otherwise dropping the watching WebSocket) must NOT cancel an in-progress
// turn — the turn keeps running and stops when it is done, and the user can
// reconnect later to see the result. ONLY an explicit user Stop cancels a
// turn. Auto-canceling on tab-close (the original ADR-045 5-minute default)
// contradicted that model and was reversed per operator decision.
//
// The watchdog mechanism itself is retained but off unless an operator
// explicitly opts in with a positive value via config.json
// (gateway.orphaned_turn_grace_seconds) or
// OMNIPUS_GATEWAY_ORPHANED_TURN_GRACE_SECONDS. Any value <= 0 keeps it
// disabled (ArmOrphanForegroundTurnWatch is a no-op).
const DefaultOrphanedTurnGraceSeconds = 0

// EffectiveOrphanedTurnGraceSeconds resolves gateway.orphaned_turn_grace_seconds
// (ADR-045): nil resolves to DefaultOrphanedTurnGraceSeconds; 0 or negative is
// returned as-is so callers (AgentLoop.ArmOrphanForegroundTurnWatch) can treat
// it as "watchdog disabled". Read live on every call, matching
// IsPreviewEnabled's precedent — a nil *Config returns 0 (disabled), the same
// fail-closed posture as IsPreviewEnabled's fail-closed-false.
func (c *Config) EffectiveOrphanedTurnGraceSeconds() int {
	if c == nil {
		return 0
	}
	return ResolveInt(c.Gateway.OrphanedTurnGraceSeconds, DefaultOrphanedTurnGraceSeconds)
}

// ApplyWarmupTimeoutDefault ensures the web_serve dev-mode warmup timeout
// (stored as tools.run_in_workspace.warmup_timeout_seconds in config.json)
// has the default value of 60 when unset. Called by the boot validator.
func (t *ToolsConfig) ApplyWarmupTimeoutDefault() {
	if t.RunInWorkspace.WarmupTimeoutSeconds <= 0 {
		t.RunInWorkspace.WarmupTimeoutSeconds = 60
	}
}

// SetUserTokenHash sets the token hash for a user identified by username.
func (c *Config) SetUserTokenHash(username, token string) error {
	for i := range c.Gateway.Users {
		if c.Gateway.Users[i].Username == username {
			hash, err := bcryptHash(token)
			if err != nil {
				return fmt.Errorf("bcrypt hash failed: %w", err)
			}
			c.Gateway.Users[i].TokenHash = BcryptHash(hash)
			return nil
		}
	}
	return fmt.Errorf("user %q not found", username)
}

// bcryptHash creates a bcrypt hash of the input string.
func bcryptHash(input string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(input), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
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

// Tool enablement is no longer gated by per-subsystem config flags.
// Every implemented tool is registered unconditionally at boot; whether an
// agent can actually invoke it is determined by the policy engine
// (allow / ask / deny) in pkg/policy. The old IsToolEnabled() /
// IsToolAvailable() functions were removed because the two-layer model
// (infrastructure enable + policy) was redundant and caused UI/behavior
// mismatches (the UI would show a tool as enabled via policy while the
// infrastructure flag silently kept it out of the agent's registry).
//
// For the case of "globally disable tool X", set its entry in
// security.tool_policies to "deny".
//
// Sub-structs in ToolsConfig (e.g. Browser.MaxTabs) are retained — they
// carry non-enable configuration like timeouts and limits that the tools
// still read at runtime.

// ReasonMemoryPressure is THE reason code every memory refusal in this
// process carries — the browser pool refusing to launch a second Chrome, the
// tab-open gate refusing an eleventh tab, agent admission refusing a third
// concurrent turn on a host whose memory cannot be measured.
//
// It lives in pkg/config, which pkg/agent and pkg/tools/browser both already
// import, for a specific reason: this is one mechanism and it must speak with
// one voice. The alternative — a `tabAdoptReason` constant in the browser
// package and a duplicated string literal on the agent side — is exactly the
// "two mechanisms" shape this work exists to remove. A model branching on the
// code, or an operator grepping a log for it, must find every memory refusal
// in the process under one name.
//
// Its VALUE is stable API in a weak sense: it appears in tool results the
// model reads and in structured log fields. Change it and both break
// silently.
const ReasonMemoryPressure = "memory_pressure"
