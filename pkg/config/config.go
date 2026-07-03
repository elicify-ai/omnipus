package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	mathrand "math/rand"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/caarlos0/env/v11"
	"golang.org/x/crypto/bcrypt"

	"github.com/dapicom-ai/omnipus/pkg"
	"github.com/dapicom-ai/omnipus/pkg/fileutil"
	"github.com/dapicom-ai/omnipus/pkg/logger"
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
	Version   int                              `json:"version"             yaml:"-"` // Config schema version for migration
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

	// UnknownFields preserves JSON keys not recognized by this version of Omnipus.
	// They are re-emitted verbatim during SaveConfig for round-trip safety (FR-004).
	// Never serialized by json.Marshal or yaml.Marshal — only written back by MarshalJSON.
	UnknownFields map[string]json.RawMessage `json:"-" yaml:"-"`

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
	// KeepCompactionSummary preserves last_compaction_summary in meta.json
	// even when all partitions are purged by the retention policy.
	KeepCompactionSummary bool `json:"keep_compaction_summary,omitempty"`
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
// Clone does NOT copy the sensitiveCache or registeredSensitive fields — those
// are runtime-only and must be re-registered on the clone if needed.
func (c *Config) Clone() (*Config, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(c); err != nil {
		return nil, fmt.Errorf("clone: marshal: %w", err)
	}
	clone := &Config{}
	if err := json.NewDecoder(&buf).Decode(clone); err != nil {
		return nil, fmt.Errorf("clone: unmarshal: %w", err)
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
	// MaxParallelAgents is the maximum number of concurrent task/subagent dispatches.
	// 0 means "use the auto-detected default" (clamped from CPU and RAM).
	// The runtime clamps this to [2, min(NumCPU-2, RAM/1.5 GB)] ≤ 16.
	// Overridden by OMNIPUS_MAX_PARALLEL_AGENTS env var when set.
	MaxParallelAgents int `json:"max_parallel_agents,omitempty" env:"OMNIPUS_MAX_PARALLEL_AGENTS"`
}

// EffectiveMaxParallelAgents returns the clamped, environment-override-aware
// value for MaxParallelAgents. It applies:
//  1. An env-var override (OMNIPUS_MAX_PARALLEL_AGENTS) if set and valid.
//  2. The configured value (p.MaxParallelAgents), if non-zero.
//  3. An auto-detect heuristic: min(NumCPU-2, RAM_GB/1.5), floor 2, ceiling 16.
//
// An explicit MaxParallelAgents=1 is honored — only the auto-detect path
// enforces a floor of 2 (to prevent accidental single-flight on capable hardware).
func (p PerformanceConfig) EffectiveMaxParallelAgents() int {
	// Env-var override has highest priority.
	if s := os.Getenv("OMNIPUS_MAX_PARALLEL_AGENTS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v >= 1 {
			return clampParallelExplicit(v)
		}
	}
	if p.MaxParallelAgents > 0 {
		// Explicit user-set value: allow 1 (single-flight); cap at 16.
		return clampParallelExplicit(p.MaxParallelAgents)
	}
	return autoDetectMaxParallel()
}

// clampParallelExplicit clamps an explicitly configured value to [1, 16].
// The floor is 1 so that a user who deliberately sets max_parallel_agents=1
// gets single-flight behavior. Use autoDetectMaxParallel for the auto path
// (which floors at 2 to avoid accidental single-flight on capable hardware).
func clampParallelExplicit(v int) int {
	const minPar, maxPar = 1, 16
	if v < minPar {
		return minPar
	}
	if v > maxPar {
		return maxPar
	}
	return v
}

// clampParallel clamps the auto-detected value to [2, 16].
// Only used by autoDetectMaxParallel; explicit user values use clampParallelExplicit.
func clampParallel(v int) int {
	const minPar, maxPar = 2, 16
	if v < minPar {
		return minPar
	}
	if v > maxPar {
		return maxPar
	}
	return v
}

// autoDetectMaxParallel returns min(NumCPU-2, RAM_GB/1.5) clamped to [2, 16].
// RAM_GB is derived from the virtual memory total reported by the OS.
func autoDetectMaxParallel() int {
	cpuBased := runtime.NumCPU() - 2
	ramBased := int(float64(totalRAMBytes()) / (1.5 * 1024 * 1024 * 1024))
	val := cpuBased
	if ramBased < val {
		val = ramBased
	}
	return clampParallel(val)
}

// totalRAMBytes returns the total physical memory in bytes. It reads
// /proc/meminfo on Linux and falls back to a conservative 4 GB constant
// on other platforms.
func totalRAMBytes() uint64 {
	return readMemTotalBytes()
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
	List     []AgentConfig `json:"list,omitempty"`
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
	// the default/passthrough provider" (legacy single-slug behavior). The
	// config-load migration (migrateAgentPrimaryProvider) splits a combined slug
	// such as "openrouter/google/gemini-2.5-flash" into Primary + Provider.
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
	return json.Marshal(raw{Primary: m.Primary, Fallbacks: m.Fallbacks, Provider: m.Provider})
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
		out[i] = wire{Model: fb.Model, Provider: fb.Provider}
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
// Resolution order:
//  1. Exact match against any configured provider's ModelName → that
//     provider's Provider field.
//  2. Exact match against any configured provider's Model (the slug)
//     → that provider's Provider field.
//  3. Any configured provider is a passthrough (openrouter / vivgrid) →
//     that passthrough provider.
//  4. Otherwise empty string (apply-time resolver will error out).
//
// Step 3 cannot call providers.IsPassthroughProvider directly — pkg/providers
// already imports pkg/config, so the reverse direction would be a cycle. The
// 3-line check below mirrors that helper byte-for-byte; keep them in sync.
func resolveFallbackProvider(cfg *Config, slug string) string {
	if cfg == nil {
		return ""
	}
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return ""
	}

	// 1 & 2: match against provider entries.
	for _, p := range cfg.Providers {
		if p == nil {
			continue
		}
		if strings.TrimSpace(p.ModelName) == slug {
			return strings.TrimSpace(p.Provider)
		}
		if strings.TrimSpace(p.Model) == slug {
			return strings.TrimSpace(p.Provider)
		}
	}

	// 3: passthrough fallback (openrouter, vivgrid).
	for _, p := range cfg.Providers {
		if p == nil {
			continue
		}
		provName := strings.ToLower(strings.TrimSpace(p.Provider))
		if provName == "openrouter" || provName == "vivgrid" ||
			strings.Contains(strings.ToLower(p.APIBase), "openrouter.ai") {
			return strings.TrimSpace(p.Provider)
		}
	}

	return ""
}

type AgentConfig struct {
	ID          string            `json:"id"`
	Default     bool              `json:"default,omitempty"`
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Workspace   string            `json:"workspace,omitempty"`
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
	CanDelegateTo     []string         `json:"can_delegate_to,omitempty"`
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
	// DelegationPolicy is the canonical unified delegation policy.
	// When set, it takes precedence over CanDelegateTo and Subagents.AllowAgents.
	// Nil means "unset — fall back to legacy fields for backward compatibility".
	DelegationPolicy *DelegationPolicy `json:"delegation_policy,omitempty"`
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
	// Tools, when non-nil, overrides scope-based tool visibility for this agent.
	// Nil means all tools allowed by the agent's type are available.
	Tools *AgentToolsCfg `json:"tools,omitempty"`
	// SandboxProfile selects the kernel sandbox profile for this agent.
	// Empty means "use the global default" (OmnipusSandboxConfig.DefaultProfile,
	// which itself falls back to SandboxProfileWorkspace when also empty).
	SandboxProfile SandboxProfile `json:"sandbox_profile,omitempty"`
	// ShellPolicy configures per-agent shell command deny patterns.
	// When non-nil, its settings are merged with the global ShellDenyPatterns
	// at enforcement time.
	ShellPolicy *AgentShellPolicy `json:"shell_policy,omitempty"`
	// UpdatedAt is the timestamp of the last successful PUT /agents/{id} update.
	// It is returned in list and detail responses and used for optimistic concurrency.
	// Pointer so omitempty works; nil = never updated. A non-pointer time.Time
	// is a struct type and always serializes (writing "0001-01-01T00:00:00Z"
	// for agents that were never PUT-updated), defeating omitempty.
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
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
type AgentBuiltinToolsCfg struct {
	// DefaultPolicy applies to tools not listed in Policies.
	// Default: "allow" (all scope-appropriate tools available).
	DefaultPolicy ToolPolicy `json:"default_policy,omitempty"`
	// Policies is a per-tool override map. Keys are tool names from the catalog.
	Policies map[string]ToolPolicy `json:"policies,omitempty"`
}

// ResolvePolicy returns the effective policy for a tool name.
// Checks per-tool overrides first, then falls back to DefaultPolicy.
// If DefaultPolicy is empty, defaults to "allow".
func (c *AgentBuiltinToolsCfg) ResolvePolicy(toolName string) ToolPolicy {
	if c == nil {
		return ToolPolicyAllow
	}
	if p, ok := c.Policies[toolName]; ok {
		return p
	}
	if c.DefaultPolicy != "" {
		return c.DefaultPolicy
	}
	return ToolPolicyAllow
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

// IsChatTarget reports whether this agent may receive inbound channel messages
// and be resolved as the default/routing agent. Every agent kind is a chat
// target EXCEPT a worker. Routing (resolveDefaultAgentID, first-enabled
// fallback) and the default-agent setter/repair use this to exclude workers.
func (a AgentConfig) IsChatTarget() bool {
	return !a.IsWorker()
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

// AgentRefKind enumerates the legal values for AgentRef.Kind. A non-empty value
// outside this set is REJECTED at config-load time (see AgentRef.Validate) so a
// typo fails loudly instead of silently mis-resolving a delegation target.
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
// An empty Kind is accepted for back-compat: callers (ResolveDelegationTo)
// default an absent kind to "local". Only a present-but-unknown value (a typo)
// is an error, so it fails loudly rather than silently downgrading routing.
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

// DelegationPolicy is the unified delegation policy for an agent.
// It unifies the three legacy allowlists:
//   - AgentConfig.CanDelegateTo   (per-agent task delegation)
//   - AgentDefaults.CanDelegateTo (global-default task delegation)
//   - SubagentsConfig.AllowAgents (spawn/subagent tool allowlist)
//
// Precedence: agent-level To > defaults-level To; SubagentsConfig.AllowAgents
// serves as a backward-compat fallback when To is nil.
//
// AcceptFrom and Budget are present but NOT enforced in v0.1.0. A startup WARN
// is emitted when either is non-empty to prevent them being mistaken for an
// active authorization boundary.
type DelegationPolicy struct {
	// To is the list of agent references this agent may delegate work to.
	// Nil means "unset — fall back to legacy fields".
	// An explicitly empty slice means "no delegation allowed".
	To []AgentRef `json:"to,omitempty"`

	// AcceptFrom is INERT in v0.1.0. Listed here so configs that set it
	// trigger a startup WARN rather than being silently ignored.
	AcceptFrom []AgentRef `json:"accept_from,omitempty"`

	// Modes lists allowed delegation modes. Enforced in v0.1.0.
	// An empty/nil Modes list means all modes are allowed (when To permits).
	Modes []DelegationMode `json:"modes,omitempty"`

	// Depth is the maximum delegation chain depth. Enforced as a safety cap.
	// Zero means uncapped (nil pointer = uncapped).
	Depth *int `json:"depth,omitempty"`

	// Budget is INERT in v0.1.0. Present to trigger a startup WARN when set.
	Budget *DelegationBudget `json:"budget,omitempty"`
}

// DelegationBudget is present in the schema but not enforced until a later release.
type DelegationBudget struct {
	MaxCostUSD float64 `json:"max_cost_usd,omitempty"`
	MaxTokens  int     `json:"max_tokens,omitempty"`
}

// WarnIfInertFieldsSet logs a startup WARN if accept_from or budget are non-empty.
// This prevents operators from mistaking them for active authorization boundaries.
func (dp *DelegationPolicy) WarnIfInertFieldsSet(agentID string) {
	if dp == nil {
		return
	}
	if len(dp.AcceptFrom) > 0 {
		slog.Warn(
			"delegation policy: accept_from is present but NOT enforced in v0.1.0 — do not rely on it as an authorization boundary",
			"agent_id",
			agentID,
		)
	}
	if dp.Budget != nil {
		slog.Warn(
			"delegation policy: budget is present but NOT enforced in v0.1.0 — do not rely on it as an authorization boundary",
			"agent_id",
			agentID,
		)
	}
}

// ResolveDelegationTo returns the canonical unified "to" allowlist for an agent,
// implementing the three-path precedence rules without silent authz widening.
//
// Precedence (first non-nil wins):
//  1. agentCfg.DelegationPolicy.To  (canonical; set by Spec-3 code paths)
//  2. agentCfg.CanDelegateTo        (legacy task delegation allowlist)
//  3. defaults.DelegationPolicy.To  (global default canonical policy)
//  4. defaults.CanDelegateTo        (global default legacy allowlist)
//
// Unset behavior per path:
//   - If the agent has DelegationPolicy set (non-nil), its To list is used even
//     if that list is empty — an empty explicit To means "deny all delegation".
//   - If the agent has no DelegationPolicy and has CanDelegateTo, those string IDs
//     are used as {kind:local, id:X} references (backward compat).
//   - If neither agent field is set, the defaults chain is consulted the same way.
//   - If nothing is set at any level, nil is returned (deny-by-default).
//
// SubagentsConfig.AllowAgents is NOT consulted here; it is used only as a
// backward-compat fallback in CanSpawnSubagent when DelegationPolicy.To is nil.
func ResolveDelegationTo(agentCfg *AgentConfig, defaults AgentDefaults) []AgentRef {
	// 1. Agent-level canonical policy (DelegationPolicy.To takes precedence;
	//    even an empty slice is authoritative — it means deny all).
	if agentCfg != nil && agentCfg.DelegationPolicy != nil {
		return agentCfg.DelegationPolicy.To
	}

	// 2. Agent-level legacy CanDelegateTo (backward compat: treat as local refs).
	if agentCfg != nil && len(agentCfg.CanDelegateTo) > 0 {
		refs := make([]AgentRef, len(agentCfg.CanDelegateTo))
		for i, id := range agentCfg.CanDelegateTo {
			refs[i] = AgentRef{Kind: "local", ID: id}
		}
		return refs
	}

	// 3. Global default canonical policy.
	if defaults.DelegationPolicy != nil {
		return defaults.DelegationPolicy.To
	}

	// 4. Global default legacy CanDelegateTo.
	if len(defaults.CanDelegateTo) > 0 {
		refs := make([]AgentRef, len(defaults.CanDelegateTo))
		for i, id := range defaults.CanDelegateTo {
			refs[i] = AgentRef{Kind: "local", ID: id}
		}
		return refs
	}

	// Nothing set at any level — deny-by-default.
	return nil
}

// IsDelegationAllowed checks whether delegation from an agent to targetAgentID
// is permitted by the unified to-allowlist. Returns false when toList is nil
// (deny-by-default). Wildcards ("*") match any target.
func IsDelegationAllowed(toList []AgentRef, targetAgentID string) bool {
	for _, ref := range toList {
		if ref.Kind != "local" && ref.Kind != "" {
			// remote-a2a and unknown kinds are not enforced locally in v0.1.0.
			continue
		}
		if ref.ID == "*" || ref.ID == targetAgentID {
			return true
		}
	}
	return false
}

// IsDelegationAllowedAny checks whether any delegation is permitted (used when
// no specific target is known, e.g. the sync subagent tool that has no agent_id).
// Returns true only if the toList contains at least one entry.
func IsDelegationAllowedAny(toList []AgentRef) bool {
	return len(toList) > 0
}

// ResolveDelegationPolicy returns the effective DelegationPolicy for an agent,
// applying the same agent>defaults precedence used by ResolveDelegationTo. It is
// used by the dispatch layer to enforce the policy's Modes and Depth fields
// (FR-6.2) — which ResolveDelegationTo (which only resolves the To allowlist)
// does not surface.
//
// Precedence (first non-nil wins):
//  1. agentCfg.DelegationPolicy  (canonical per-agent policy)
//  2. defaults.DelegationPolicy  (global default canonical policy)
//
// Returns nil when neither level sets a canonical DelegationPolicy. A nil result
// means "no canonical Modes/Depth constraints" — callers treat that as
// "all modes allowed / depth uncapped" so the legacy To-only paths are unaffected
// (no silent behavior change for configs that never adopted DelegationPolicy).
func ResolveDelegationPolicy(agentCfg *AgentConfig, defaults AgentDefaults) *DelegationPolicy {
	if agentCfg != nil && agentCfg.DelegationPolicy != nil {
		return agentCfg.DelegationPolicy
	}
	if defaults.DelegationPolicy != nil {
		return defaults.DelegationPolicy
	}
	return nil
}

// IsDelegationModeAllowed reports whether the given delegation mode is permitted
// by a DelegationPolicy (FR-6.2). The contract:
//   - nil policy            → allowed (no canonical policy ⇒ no mode constraint).
//   - non-nil, empty Modes  → allowed (empty/nil Modes means "all modes allowed").
//   - non-nil, Modes set    → allowed only if mode is present in the list.
func IsDelegationModeAllowed(dp *DelegationPolicy, mode DelegationMode) bool {
	if dp == nil || len(dp.Modes) == 0 {
		return true
	}
	for _, m := range dp.Modes {
		if m == mode {
			return true
		}
	}
	return false
}

// ResolveDelegationDepth returns the effective max delegation-chain depth cap
// from a DelegationPolicy (FR-6.2). The contract:
//   - nil policy or nil/<=0 Depth → 0, meaning "uncapped by policy" (the global
//     SubTurn.MaxDepth safety ceiling still applies independently).
//   - positive Depth              → that value, an additional per-agent cap.
func ResolveDelegationDepth(dp *DelegationPolicy) int {
	if dp == nil || dp.Depth == nil || *dp.Depth <= 0 {
		return 0
	}
	return *dp.Depth
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
	MaxDepth              int `json:"max_depth"               env:"OMNIPUS_AGENTS_DEFAULTS_SUBTURN_MAX_DEPTH"`
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
	Workspace string `json:"workspace" env:"OMNIPUS_AGENTS_DEFAULTS_WORKSPACE"`
	// RestrictToWorkspace and AllowReadOutsideWorkspace are removed from the v1
	// schema (FR-001). Tags use json:"-" so SaveConfig never serializes them;
	// validateRemovedKeys rejects any v1 config that still carries these keys.
	RestrictToWorkspace       bool               `json:"-"                               env:"OMNIPUS_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE"`
	AllowReadOutsideWorkspace bool               `json:"-"                               env:"OMNIPUS_AGENTS_DEFAULTS_ALLOW_READ_OUTSIDE_WORKSPACE"`
	Provider                  string             `json:"provider"                        env:"OMNIPUS_AGENTS_DEFAULTS_PROVIDER"`
	ModelName                 string             `json:"model_name"                      env:"OMNIPUS_AGENTS_DEFAULTS_MODEL_NAME"`
	ModelFallbacks            []string           `json:"model_fallbacks,omitempty"`
	ImageModel                string             `json:"image_model,omitempty"           env:"OMNIPUS_AGENTS_DEFAULTS_IMAGE_MODEL"`
	ImageModelFallbacks       []string           `json:"image_model_fallbacks,omitempty"`
	MaxTokens                 int                `json:"max_tokens"                      env:"OMNIPUS_AGENTS_DEFAULTS_MAX_TOKENS"`
	ContextWindow             int                `json:"context_window,omitempty"        env:"OMNIPUS_AGENTS_DEFAULTS_CONTEXT_WINDOW"`
	Temperature               *float64           `json:"temperature,omitempty"           env:"OMNIPUS_AGENTS_DEFAULTS_TEMPERATURE"`
	MaxToolIterations         int                `json:"max_tool_iterations"             env:"OMNIPUS_AGENTS_DEFAULTS_MAX_TOOL_ITERATIONS"`
	SummarizeMessageThreshold int                `json:"summarize_message_threshold"     env:"OMNIPUS_AGENTS_DEFAULTS_SUMMARIZE_MESSAGE_THRESHOLD"`
	SummarizeTokenPercent     int                `json:"summarize_token_percent"         env:"OMNIPUS_AGENTS_DEFAULTS_SUMMARIZE_TOKEN_PERCENT"`
	MaxMediaSize              int                `json:"max_media_size,omitempty"        env:"OMNIPUS_AGENTS_DEFAULTS_MAX_MEDIA_SIZE"`
	Routing                   *RoutingConfig     `json:"routing,omitempty"`
	SteeringMode              string             `json:"steering_mode,omitempty"         env:"OMNIPUS_AGENTS_DEFAULTS_STEERING_MODE"` // "one-at-a-time" (default) or "all"
	SubTurn                   SubTurnConfig      `json:"subturn"                                                                                    envPrefix:"OMNIPUS_AGENTS_DEFAULTS_SUBTURN_"`
	ToolFeedback              ToolFeedbackConfig `json:"tool_feedback,omitempty"`
	SplitOnMarker             bool               `json:"split_on_marker"                 env:"OMNIPUS_AGENTS_DEFAULTS_SPLIT_ON_MARKER"` // split messages on <|[SPLIT]|> marker
	TimeoutSeconds            int                `json:"timeout_seconds"                 env:"OMNIPUS_AGENTS_DEFAULTS_TIMEOUT_SECONDS"` // per-turn timeout in seconds; 0 = disabled
	CanDelegateTo             []string           `json:"can_delegate_to,omitempty"`
	// DelegationPolicy is the canonical unified delegation policy for the global
	// default. When set, it takes precedence over CanDelegateTo.
	// Nil means "unset — fall back to CanDelegateTo for backward compatibility".
	DelegationPolicy *DelegationPolicy `json:"delegation_policy,omitempty"`
	DefaultAgentID   string            `json:"default_agent_id,omitempty"  env:"OMNIPUS_DEFAULT_AGENT_ID"`

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
	// behaviour as an agent's FallbackModels field. Each entry carries its
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

// GetModelName returns the effective model name for the agent defaults.
// It prefers the new "model_name" field but falls back to "model" for backward compatibility.
func (d *AgentDefaults) GetModelName() string {
	return d.ModelName
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
var ErrHalfBoundChannelInstance = errors.New("channels: half-bound workspace instance (workspace_id set but agent identity missing or invalid)")

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
			if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
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
				ErrHalfBoundChannelInstance, key, inst.WorkspaceID,
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

// validateIdentityAndAgentRefKinds rejects any non-empty ChannelIdentity.Kind or
// AgentRef.Kind that is outside its known set. This runs at config load so a typo
// fails loudly with a clear error instead of silently downgrading routing (a
// mis-spelled channel identity kind would otherwise fall through to user routing
// in route.go) or mis-resolving a delegation target. Empty/absent kinds keep
// their documented defaults and are NOT rejected (back-compat).
//
// On success it also normalizes the stored Kind in place (lowercase + trim) so a
// value the API accepted in mixed case (e.g. {"kind":"Agent"}) is persisted in
// canonical form. This keeps the on-disk config self-consistent with the
// case-tolerant API/route paths and prevents drift between what was accepted at
// write time and what is loaded later. Validate already normalizes before
// comparing, so this is belt-and-suspenders: even an un-normalized stored value
// would load, but we canonicalize it here so it does not stay mixed-case forever.
func validateIdentityAndAgentRefKinds(cfg *Config) error {
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

	// Agent delegation-policy refs (per-agent and global defaults). Both the
	// active To list and the inert AcceptFrom list are validated so a typo'd kind
	// is caught regardless of where it sits.
	validatePolicy := func(scope string, dp *DelegationPolicy) error {
		if dp == nil {
			return nil
		}
		// dp.To / dp.AcceptFrom are slices of value type AgentRef; index to mutate
		// the stored element rather than the loop copy so normalization persists.
		for i := range dp.To {
			if err := dp.To[i].Validate(); err != nil {
				return fmt.Errorf("%s delegation_policy.to: %w", scope, err)
			}
			dp.To[i].Kind = canonicalizeKind(dp.To[i].Kind)
		}
		for i := range dp.AcceptFrom {
			if err := dp.AcceptFrom[i].Validate(); err != nil {
				return fmt.Errorf("%s delegation_policy.accept_from: %w", scope, err)
			}
			dp.AcceptFrom[i].Kind = canonicalizeKind(dp.AcceptFrom[i].Kind)
		}
		return nil
	}
	if err := validatePolicy("agents.defaults", cfg.Agents.Defaults.DelegationPolicy); err != nil {
		return err
	}
	for _, ag := range cfg.Agents.List {
		scope := fmt.Sprintf("agent %q", ag.ID)
		if err := validatePolicy(scope, ag.DelegationPolicy); err != nil {
			return err
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

// InstanceToEmail returns the EmailConfig for a ChannelInstanceConfig of type "email".
// The password credential is stored encrypted via PasswordRef; only non-secret fields
// are copied directly from the instance. The PasswordRef field in ChannelInstanceConfig
// (shared with IRC) carries the email credential-store key.
func InstanceToEmail(inst ChannelInstanceConfig) EmailConfig {
	return EmailConfig{
		Enabled:     inst.Enabled,
		IMAPHost:    inst.IMAPHost,
		IMAPPort:    inst.IMAPPort,
		SMTPHost:    inst.SMTPHost,
		SMTPPort:    inst.SMTPPort,
		Username:    inst.EmailUsername,
		PasswordRef: inst.PasswordRef,
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

// ModelConfig represents a model-centric provider configuration.
// It allows adding new providers (especially OpenAI-compatible ones) via configuration only.
// The model field uses protocol prefix format: [protocol/]model-identifier
// Supported protocols include openai, anthropic, antigravity, claude-cli,
// codex-cli, github-copilot, and named OpenAI-compatible protocols such as
// groq, deepseek, modelscope, and novita.
// Default protocol is "openai" if no prefix is specified.
type ModelConfig struct {
	// Required fields
	ModelName string `json:"model_name"`         // User-facing alias for the model
	Model     string `json:"model"`              // Protocol/model-identifier (e.g., "openai/gpt-4o", "anthropic/claude-sonnet-4.6")
	Provider  string `json:"provider,omitempty"` // Routing key — determines which API endpoint to use (e.g. "openrouter", "anthropic")

	// HTTP-based providers
	APIBase   string   `json:"api_base,omitempty"`  // API endpoint URL
	Proxy     string   `json:"proxy,omitempty"`     // HTTP proxy URL
	Fallbacks []string `json:"fallbacks,omitempty"` // Fallback model names for failover

	// Special providers (CLI-based, OAuth, etc.)
	AuthMethod  string `json:"auth_method,omitempty"`  // Authentication method: oauth, token
	ConnectMode string `json:"connect_mode,omitempty"` // Connection mode: stdio, grpc
	Workspace   string `json:"workspace,omitempty"`    // Workspace path for CLI-based providers

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

	// Name is an alias for ModelName used in some display contexts.
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

// Validate checks if the ModelConfig has all required fields.
func (c *ModelConfig) Validate() error {
	if c.ModelName == "" {
		return fmt.Errorf("model_name is required")
	}
	if c.Model == "" {
		return fmt.Errorf("model is required")
	}
	return nil
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

	// Preview listener fields (FR-001..FR-005, FR-027, FR-028).
	// PreviewPort is the port for the preview listener that serves /serve/ and /dev/.
	// When zero, defaults to Port+1 at boot. Must differ from Port when preview is enabled.
	PreviewPort int32 `json:"preview_port,omitempty" env:"OMNIPUS_GATEWAY_PREVIEW_PORT"`
	// PreviewHost is the bind host for the preview listener.
	// When empty, defaults to Host at boot. Operators set to 127.0.0.1 to keep
	// the preview listener private behind a reverse proxy.
	PreviewHost string `json:"preview_host,omitempty" env:"OMNIPUS_GATEWAY_PREVIEW_HOST"`
	// PreviewOrigin is the browser-facing origin of the preview listener, used when
	// a reverse proxy is in front (e.g. "https://preview.omnipus.acme.com").
	// When set, PreviewListenerEnabled must be true. Must parse as a URL with scheme.
	PreviewOrigin string `json:"preview_origin,omitempty" env:"OMNIPUS_GATEWAY_PREVIEW_ORIGIN"`
	// PublicURL is the browser-facing origin of the main gateway listener, used when
	// a reverse proxy is in front (e.g. "https://omnipus.acme.com").
	// When set, it overrides the Host:Port-derived origin in frame-ancestors CSP directives.
	PublicURL string `json:"public_url,omitempty" env:"OMNIPUS_GATEWAY_PUBLIC_URL"`
	// PreviewListenerEnabled controls whether the preview listener is started.
	// Defaults to true on Linux/macOS/Windows, false on Android/Termux.
	// Setting false disables the preview listener entirely and forces link-only
	// rendering in tool UIs (emergency rollback knob).
	PreviewListenerEnabled *bool `json:"preview_listener_enabled,omitempty" env:"OMNIPUS_GATEWAY_PREVIEW_LISTENER_ENABLED"`

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
	// tool call before auto-denying. 0 or negative uses the default (300 s).
	ToolApprovalTimeout int `json:"tool_approval_timeout,omitempty" env:"OMNIPUS_TOOL_APPROVAL_TIMEOUT"`
	// ToolApprovalMaxPending is the maximum number of concurrently-pending tool
	// approvals before new requests are auto-denied (FR-016, MAJ-009).
	// 0 uses the spec default (64). Negative values are rejected at startup.
	ToolApprovalMaxPending int `json:"tool_approval_max_pending,omitempty" env:"OMNIPUS_TOOL_APPROVAL_MAX_PENDING"`
	// TurnSyntheticErrorFloor is the number of consecutive synthetic-deny tool
	// results in a single turn that triggers a turn abort (FR-084). Default: 8.
	// Set to 0 to disable. Negative values are treated as the default (8).
	TurnSyntheticErrorFloor int `json:"turn_synthetic_error_floor,omitempty" env:"OMNIPUS_TURN_SYNTHETIC_ERROR_FLOOR"`

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
	PreferNative bool `json:"prefer_native" yaml:"-" env:"OMNIPUS_TOOLS_WEB_PREFER_NATIVE"`
	// Proxy is an optional proxy URL for web tools (http/https/socks5/socks5h).
	// For authenticated proxies, prefer HTTP_PROXY/HTTPS_PROXY env vars instead of embedding credentials in config.
	Proxy                string              `json:"proxy,omitempty"                  yaml:"-" env:"OMNIPUS_TOOLS_WEB_PROXY"`
	FetchLimitBytes      int64               `json:"fetch_limit_bytes,omitempty"      yaml:"-" env:"OMNIPUS_TOOLS_WEB_FETCH_LIMIT_BYTES"`
	Format               string              `json:"format,omitempty"                 yaml:"-" env:"OMNIPUS_TOOLS_WEB_FORMAT"`
	PrivateHostWhitelist FlexibleStringSlice `json:"private_host_whitelist,omitempty" yaml:"-" env:"OMNIPUS_TOOLS_WEB_PRIVATE_HOST_WHITELIST"`
}

type CronToolsConfig struct {
	ToolConfig         `     envPrefix:"OMNIPUS_TOOLS_CRON_"`
	ExecTimeoutMinutes int  `                                json:"exec_timeout_minutes" env:"OMNIPUS_TOOLS_CRON_EXEC_TIMEOUT_MINUTES"` // 0 means no timeout
	AllowCommand       bool `                                json:"allow_command"        env:"OMNIPUS_TOOLS_CRON_ALLOW_COMMAND"`
}

type ExecConfig struct {
	ToolConfig          `         envPrefix:"OMNIPUS_TOOLS_EXEC_"`
	EnableDenyPatterns  bool     `                                json:"enable_deny_patterns"  env:"OMNIPUS_TOOLS_EXEC_ENABLE_DENY_PATTERNS"`
	CustomDenyPatterns  []string `                                json:"custom_deny_patterns"  env:"OMNIPUS_TOOLS_EXEC_CUSTOM_DENY_PATTERNS"`
	CustomAllowPatterns []string `                                json:"custom_allow_patterns" env:"OMNIPUS_TOOLS_EXEC_CUSTOM_ALLOW_PATTERNS"`
	TimeoutSeconds      int      `                                json:"timeout_seconds"       env:"OMNIPUS_TOOLS_EXEC_TIMEOUT_SECONDS"` // 0 means use default (60s)

	// US-7: Interactive approval before exec commands.
	// "ask" (default) prompts the user; "off" skips the prompt.
	Approval string `json:"approval,omitempty" env:"OMNIPUS_TOOLS_EXEC_APPROVAL"`

	// US-7/US-5: Glob patterns for binaries the exec tool is allowed to run.
	// Non-empty list acts as an allowlist; all other commands are denied.
	AllowedBinaries []string `json:"allowed_binaries,omitempty" env:"OMNIPUS_TOOLS_EXEC_ALLOWED_BINARIES"`

	// US-14: Route exec child process HTTP traffic through the local SSRF proxy.
	// When true (default), HTTP_PROXY and HTTPS_PROXY are set on child processes.
	EnableProxy bool `json:"enable_proxy,omitempty" env:"OMNIPUS_TOOLS_EXEC_ENABLE_PROXY"`

	// MaxBackgroundSeconds is the hard-kill timeout for background sessions.
	// After this duration, the process receives SIGTERM, then SIGKILL 5s later.
	// 0 = disabled (no timeout enforced).
	MaxBackgroundSeconds int `json:"max_background_seconds" env:"OMNIPUS_TOOLS_EXEC_MAX_BACKGROUND_SECONDS"`
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

type ReadFileToolConfig struct {
	Enabled         bool `json:"enabled"`
	MaxReadFileSize int  `json:"max_read_file_size"`
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
	Spawn           ToolConfig         `json:"spawn"             yaml:"-"                                                      envPrefix:"OMNIPUS_TOOLS_SPAWN_"`
	SpawnStatus     ToolConfig         `json:"spawn_status"      yaml:"-"                                                      envPrefix:"OMNIPUS_TOOLS_SPAWN_STATUS_"`
	Subagent        ToolConfig         `json:"subagent"          yaml:"-"                                                      envPrefix:"OMNIPUS_TOOLS_SUBAGENT_"`
	WebFetch        ToolConfig         `json:"web_fetch"         yaml:"-"                                                      envPrefix:"OMNIPUS_TOOLS_WEB_FETCH_"`
	WriteFile       ToolConfig         `json:"write_file"        yaml:"-"                                                      envPrefix:"OMNIPUS_TOOLS_WRITE_FILE_"`
	Browser         BrowserToolConfig  `json:"browser"           yaml:"-"                                                      envPrefix:"OMNIPUS_TOOLS_BROWSER_"`
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
	// plus load_tool/search are sent as callable defs each turn; all other
	// allowed tools appear in a compact manifest block in the system context
	// and are made callable on demand via load_tool. When false, every tool is
	// sent as a full callable def every turn (legacy behavior; backward-compat
	// kill-switch).
	Manifest ManifestConfig `json:"manifest" yaml:"manifest,omitempty"`
}

// ManifestConfig holds settings for the tool-manifest optimization.
// Maps to config.json: tools.manifest.*
type ManifestConfig struct {
	// Compressed controls whether the manifest optimization is active.
	// Default: true (enabled). When false, every tool is sent full (legacy behavior).
	// Controlled at runtime via PUT /api/v1/performance (tools_on_demand field).
	Compressed bool `json:"compressed" yaml:"compressed,omitempty"`
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
type BrowserToolConfig struct {
	ToolConfig     `       envPrefix:"OMNIPUS_TOOLS_BROWSER_"`
	Headless       bool   `                                   json:"headless"        env:"OMNIPUS_TOOLS_BROWSER_HEADLESS"`
	CDPURL         string `                                   json:"cdp_url"         env:"OMNIPUS_TOOLS_BROWSER_CDP_URL"`
	PageTimeoutSec int    `                                   json:"page_timeout"    env:"OMNIPUS_TOOLS_BROWSER_PAGE_TIMEOUT"`
	MaxTabs        int    `                                   json:"max_tabs"        env:"OMNIPUS_TOOLS_BROWSER_MAX_TABS"`
	PersistSession bool   `                                   json:"persist_session" env:"OMNIPUS_TOOLS_BROWSER_PERSIST_SESSION"`
	ProfileDir     string `                                   json:"profile_dir"     env:"OMNIPUS_TOOLS_BROWSER_PROFILE_DIR"`
	// EvaluateEnabled gates browser.evaluate (arbitrary JS execution).
	// Defaults to false (deny-by-default per SEC-04/SEC-06). Must be explicitly
	// opted in by the operator since evaluate runs arbitrary JavaScript.
	EvaluateEnabled bool `json:"evaluate_enabled" env:"OMNIPUS_TOOLS_BROWSER_EVALUATE_ENABLED"`
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
	// Env are environment variables to set for the server process (stdio only)
	Env map[string]string `json:"env,omitempty"`
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

// LoadConfigWithStore loads a config and, for v0 configs, migrates legacy
// plaintext secrets into the provided store. If store is nil and a v0 config
// with plaintext secrets is found, an error is returned.
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
func LoadConfigWithStoreAndSelfHealHook(path string, store CredentialStore, onSelfHeal SelfHealWriteHook) (*Config, error) {
	return loadConfigInternal(path, store, onSelfHeal)
}

func loadConfigInternal(path string, store CredentialStore, onSelfHeal SelfHealWriteHook) (*Config, error) {
	logger.Debugf("loading config from %s", path)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			logger.WarnF("config file not found, using default config", map[string]any{"path": path})
			return DefaultConfig(), nil
		}
		logger.Errorf("failed to read config file: %v", err)
		return nil, err
	}

	// First, try to detect config version by reading the version field
	var versionInfo struct {
		Version int `json:"version"`
	}
	if e := json.Unmarshal(data, &versionInfo); e != nil {
		return nil, fmt.Errorf("failed to detect config version: %w", e)
	}

	// Reject removed keys only for v1+ configs (FR-001). Legacy v0 configs may
	// legitimately contain restrict_to_workspace; we migrate them instead.
	if versionInfo.Version >= CurrentVersion {
		if validateErr := validateRemovedKeys(data); validateErr != nil {
			return nil, validateErr
		}
	}
	if len(data) <= 10 {
		logger.Warn(fmt.Sprintf("content is [%s]", string(data)))
		return DefaultConfig(), nil
	}

	// Load config based on detected version
	var cfg *Config
	switch versionInfo.Version {
	case 0:
		logger.InfoF("config migrate start", map[string]any{"from": versionInfo.Version, "to": CurrentVersion})
		// Legacy config (no version field)
		v, e := loadConfigV0(data)
		if e != nil {
			return nil, e
		}
		// Use MigrateWithStore when a store is available so legacy plaintext
		// secrets are moved to the encrypted credential store. When store is nil,
		// refuse to migrate if the v0 config contains any plaintext secrets to
		// prevent silent data loss. If there are no secrets, plain Migrate is safe.
		if store != nil {
			cfg, e = v.(*configV0).MigrateWithStore(store)
		} else if v.(*configV0).hasLegacySecrets() {
			return nil, fmt.Errorf(
				"config migration: v0 config contains plaintext secrets but no credential store was provided; " +
					"use LoadConfigWithStore and ensure OMNIPUS_MASTER_KEY is set",
			)
		} else {
			cfg, e = v.Migrate()
		}
		if e != nil {
			logger.ErrorF("config migrate fail", map[string]any{"from": versionInfo.Version, "to": CurrentVersion})
			return nil, e
		}
		logger.InfoF("config migrate success", map[string]any{"from": versionInfo.Version, "to": CurrentVersion})
		err = makeBackup(path)
		if err != nil {
			return nil, err
		}
		defer func(cfg *Config) {
			_ = SaveConfig(path, cfg)
		}(cfg)
	case CurrentVersion:
		// Current version
		cfg, err = loadConfig(data)
		if err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("unsupported config version: %d", versionInfo.Version)
	}

	if err := env.Parse(cfg); err != nil {
		return nil, err
	}

	// Expand multi-key configs into separate entries for key-level failover
	cfg.Providers = expandMultiKeyModels(cfg.Providers)

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

	// Reject typo'd identity / agent-ref Kind values so a mis-spelled kind fails
	// loudly here instead of silently downgrading routing (route.go treats any
	// non-"agent" identity kind as the user fallback) or mis-resolving a
	// delegation target. Empty/absent kinds keep their documented defaults.
	if err := validateIdentityAndAgentRefKinds(cfg); err != nil {
		return nil, err
	}

	// Merge Omnipus channel_policies routing rules into Bindings.
	cfg.MergeChannelPoliciesIntoBindings()

	// Validate model_list for uniqueness and required fields
	if err := cfg.ValidateProviders(); err != nil {
		return nil, err
	}

	// Ensure Workspace has a default if not set
	if cfg.Agents.Defaults.Workspace == "" {
		homePath, homeErr := os.UserHomeDir()
		if homeErr != nil {
			logger.WarnCF("config", "UserHomeDir failed; workspace path may be incomplete",
				map[string]any{"error": homeErr.Error()})
		}
		if omnipusHome := os.Getenv(EnvHome); omnipusHome != "" {
			homePath = omnipusHome
		} else if homePath != "" {
			homePath = filepath.Join(homePath, pkg.DefaultOmnipusHome)
		}
		cfg.Agents.Defaults.Workspace = filepath.Join(homePath, pkg.WorkspaceName)
	}

	migrateProviderFields(cfg)
	// O3 two-field model: split an existing combined primary slug
	// ("openrouter/google/gemini-2.5-flash") into {primary, provider} so routing
	// uses the explicit provider. Idempotent; runs after migrateProviderFields so
	// the provider protocol set is consistent across model_list and agents.
	migrateAgentPrimaryProvider(cfg)
	// O13: per-agent sandbox_profile=off is retired. Migrate any persisted
	// "off" to "host" so legacy configs load cleanly — "no sandbox" is now
	// reachable only via the global god-mode switch (sandbox.god_mode).
	// Idempotent; runs before any per-agent profile is consumed.
	migrateAgentSandboxOff(cfg)
	// Post-refactor: tools.<name>.enabled is deprecated. If the loaded config
	// carries explicit false values, translate them idempotently into
	// security.tool_policies deny entries so operator intent is enforced
	// by the policy engine rather than silently ignored (issue #237). The
	// returned legacy flags drive a one-time on-disk self-heal further down
	// (gated the same way as the CLI-token relocation, CurrentVersion only)
	// that removes the legacy keys and persists the derived policy — see
	// stripDeprecatedToolEnableFlagsOnDisk (tool_enable_migration.go) for why:
	// without it this migration re-derives the same deny entry on every
	// hot-reload and an operator's "allow" choice never sticks.
	legacyToolEnableFlags := migrateDeprecatedToolEnableFlags(cfg, data)

	// Migrate the pre-FR-10.1 skills config shape (typed ClawHub singleton +
	// separate Github block) into the unified Marketplaces list. Idempotent:
	// a no-op when the raw config already carries a "marketplaces" key. Runs
	// BEFORE restoreSkillDiscoveryDefaults so the healer sees the migrated
	// list (and so operator values from the old shape are preserved).
	migrateMarketplaces(cfg, data)

	// Restore skill-discovery tool defaults (find_skills/install_skill enabled,
	// ClawHub {enabled:true, base_url:"https://clawhub.ai"}) for configs that
	// persisted them as disabled/empty zero values. Onboarded/legacy configs
	// otherwise leave the agent loop's skills RegistryManager with ZERO
	// registries, so the agent's find_skills/install_skill silently return
	// nothing while the UI (which constructs ClawHub directly) works. Operator
	// intent (an explicit enabled key) is preserved.
	restoreSkillDiscoveryDefaults(cfg, data)

	// Enforce at-most-one Default=true invariant across cfg.Agents.List.
	// Hand-edited configs may contain multiple defaults; repair them now so the
	// registry's GetDefaultAgent sees a clean canonical state (F11).
	RepairMultipleDefaults(cfg)

	// ADR-029 FR-029/OBS-001: enforce the two-representation rule. A channel
	// instance that carries BOTH a bound representation (WorkspaceID + Identity)
	// AND a stale channel-wildcard AgentBinding in cfg.Bindings is inconsistent —
	// the bound representation wins. Drop the stale wildcard binding so the
	// on-disk state is self-consistent after this load (mirrors RepairMultipleDefaults).
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
	//
	// Only for CurrentVersion configs, mirroring the retired
	// selfHealUserRolesOnDisk's own gating: a v0 config already gets its
	// fully migrated (CurrentVersion-shaped, "cli"-Users-free) state written
	// by the existing v0-migration SaveConfig call further up in this
	// function — a v0-format raw-JSON patch attempt here would be redundant
	// and would be probing a raw JSON shape (the legacy configV0 layout) this
	// migration was never designed against.
	if versionInfo.Version == CurrentVersion {
		migrateCLITokenOutOfUsers(cfg, path, onSelfHeal)

		// Persist the tools.<name>.enabled=false → security.tool_policies deny
		// migration to disk (issue: an operator's "allow" set via the Security
		// UI bounced back to "deny" on the next reload). Same CurrentVersion-
		// only gating as the CLI-token relocation above, and for the same
		// reason: a v0 config's fully migrated state is already written by the
		// v0-migration SaveConfig call further up in this function, so a raw-
		// JSON patch here would be redundant. Best-effort — see
		// stripDeprecatedToolEnableFlagsOnDisk (tool_enable_migration.go).
		if len(legacyToolEnableFlags) > 0 {
			written, healErr := stripDeprecatedToolEnableFlagsOnDisk(cfg, path, legacyToolEnableFlags)
			if healErr != nil {
				logger.WarnF("failed to persist tools.<name>.enabled removal to config.json; "+
					"runtime behavior is still correct (in-memory state already reflects the "+
					"derived tool policy), but the on-disk file could not be self-healed", map[string]any{
					"path":  path,
					"error": healErr.Error(),
				})
			} else if written != nil && onSelfHeal != nil {
				onSelfHeal(written)
			}
		}
	}

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

	// Apply defaults and validate bounds for all security-relevant fields
	// (FR-001, FR-002a, numeric sandbox fields, AuthMismatchLogLevel).
	if err := validateBootConfig(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// knownProviderProtocols is the set of leading slug segments treated as an
// explicit provider/routing protocol when splitting a combined "provider/model"
// slug. Shared by migrateProviderFields (model_list) and
// migrateAgentPrimaryProvider (per-agent primary model, O3).
var knownProviderProtocols = map[string]bool{
	"openai": true, "openrouter": true, "anthropic": true, "anthropic-messages": true,
	"google": true, "gemini": true, "groq": true, "deepseek": true, "mistral": true,
	"minimax": true, "moonshot": true, "zhipu": true, "nvidia": true, "qwen": true,
	"qwen-intl": true, "qwen-international": true, "dashscope-intl": true,
	"qwen-us": true, "dashscope-us": true,
	"ollama": true, "cerebras": true, "azure": true, "azure-openai": true,
	"litellm": true, "vllm": true, "bedrock": true,
	"coding-plan": true, "alibaba-coding": true, "qwen-coding": true, "mimo": true,
	"novita": true, "vivgrid": true, "volcengine": true, "modelscope": true,
	"longcat": true, "avian": true, "shengsuanyun": true,
}

// migrateProviderFields splits old-format Model fields (e.g. "openrouter/anthropic/claude-opus-4")
// into separate Provider and Model fields for backward compatibility.
func migrateProviderFields(cfg *Config) {
	for _, p := range cfg.Providers {
		if p.Provider != "" {
			continue
		}
		protocol, modelID, found := strings.Cut(p.Model, "/")
		if found && knownProviderProtocols[protocol] {
			p.Provider = protocol
			p.Model = modelID
		}
	}
}

// migrateAgentPrimaryProvider implements the O3 two-field model migration for the
// PRIMARY agent model. It splits an existing combined primary slug — e.g.
// "openrouter/google/gemini-2.5-flash" — into Primary="google/gemini-2.5-flash"
// plus Provider="openrouter", so resolution can key off the explicit provider
// (never inferred). It is idempotent and conservative:
//
//   - skips agents whose Model is nil or whose Primary is empty;
//   - skips when Provider is already set (already migrated, or operator-supplied);
//   - only splits when the FIRST slug segment is a known provider protocol AND
//     there is a remaining model path after it (so a bare "gpt-4o", or a
//     vendor-prefixed "meta-llama/llama-3-70b" — where "meta-llama" is a model
//     vendor that is NOT in knownProviderProtocols — is left untouched, because
//     its leading segment is not a configured provider protocol).
//
// NOTE: a leading segment like "google" or "openai" IS a known protocol, so
// "google/gemini-2.5-flash" DOES split (Provider="google",
// Primary="gemini-2.5-flash"). Use a non-protocol vendor prefix as the
// "untouched" example, not a real provider name.
//
// Mirrors migrateProviderFields (model_list) using the same protocol set so the
// two stay consistent.
func migrateAgentPrimaryProvider(cfg *Config) {
	for i := range cfg.Agents.List {
		mc := cfg.Agents.List[i].Model
		if mc == nil || mc.Primary == "" || mc.Provider != "" {
			continue
		}
		protocol, rest, found := strings.Cut(mc.Primary, "/")
		if found && rest != "" && knownProviderProtocols[protocol] {
			mc.Provider = protocol
			mc.Primary = rest
		}
	}
}

// migrateAgentSandboxOff migrates any per-agent SandboxProfile set to the
// retired "off" value to "host" (O13). Per-agent "off" is dropped from the
// wire contract — "no sandbox" is reachable ONLY via the global god-mode
// switch (sandbox.god_mode). "host" is the most-permissive per-agent profile
// that remains (full host filesystem + network), so it is the closest
// non-breaking replacement for a config that previously requested "off".
//
// Idempotent: a no-op once no agent carries "off". Conservative: only the
// exact "off" value is rewritten; every other profile is left untouched.
func migrateAgentSandboxOff(cfg *Config) {
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].SandboxProfile == SandboxProfileOff {
			logger.WarnF("agent sandbox_profile=off is retired; migrating to host", map[string]any{
				"agent_id": cfg.Agents.List[i].ID,
			})
			cfg.Agents.List[i].SandboxProfile = SandboxProfileHost
		}
	}
}

func makeBackup(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	// Create backup of the config file before migration
	bakPath := path + ".bak"
	if err := fileutil.CopyFile(path, bakPath, 0o600); err != nil {
		logger.ErrorF("failed to create config backup", map[string]any{"error": err})
		return fmt.Errorf("failed to create config backup: %w", err)
	}
	return nil
}

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

func (c *Config) WorkspacePath() string {
	return expandHome(c.Agents.Defaults.Workspace)
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

// GetModelConfig returns the ModelConfig for the given model name.
// If multiple configs exist with the same model_name, it uses round-robin
// selection for load balancing. Returns an error if the model is not found.
func (c *Config) GetModelConfig(modelName string) (*ModelConfig, error) {
	matches := c.findMatches(modelName)
	if len(matches) == 0 {
		return nil, fmt.Errorf("model %q not found in model_list or providers", modelName)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}

	// Multiple configs - use round-robin for load balancing
	idx := (rrCounter.Add(1) - 1) % uint64(len(matches))
	return matches[idx], nil
}

// findMatches finds all ModelConfig entries with the given model_name.
func (c *Config) findMatches(modelName string) []*ModelConfig {
	var matches []*ModelConfig
	for i := range c.Providers {
		if c.Providers[i].ModelName == modelName {
			matches = append(matches, c.Providers[i])
		}
	}
	return matches
}

// ValidateProviders validates all ModelConfig entries in the providers config.
// It checks that each model config is valid.
// Note: Multiple entries with the same model_name are allowed for load balancing.
func (c *Config) ValidateProviders() error {
	for i := range c.Providers {
		if err := c.Providers[i].Validate(); err != nil {
			return fmt.Errorf("providers[%d]: %w", i, err)
		}
	}
	return nil
}

// previewListenerEnabledDefault returns the platform-appropriate default for
// gateway.preview_listener_enabled: true on Linux/macOS/Windows, false on Android.
// Per FR-027 / MR-09.
func previewListenerEnabledDefault() bool {
	return runtime.GOOS != "android"
}

// normaliseIPv6Host strips surrounding brackets from an IPv6 address so that
// "[::]" and "::" compare as equal. Per FR-028.
func normaliseIPv6Host(h string) string {
	h = strings.TrimPrefix(h, "[")
	h = strings.TrimSuffix(h, "]")
	return h
}

// IsPreviewListenerEnabled resolves the effective value of gateway.preview_listener_enabled.
// Returns the configured value when set; falls back to the platform default
// (true on Linux/macOS/Windows, false on Android/Termux — FR-027/MR-09).
func (g *GatewayConfig) IsPreviewListenerEnabled() bool {
	if g.PreviewListenerEnabled != nil {
		return *g.PreviewListenerEnabled
	}
	return previewListenerEnabledDefault()
}

// ValidateAndApplyPreviewDefaults validates gateway preview fields and applies
// computed defaults (preview port derivation, preview host defaulting,
// warmup timeout default). It MUTATES g in place.
//
// Validation order (FR-027c, MR-06, MR-08, MN-01):
//  1. Main port ∈ [1, 65535]
//  2. Resolve preview_listener_enabled (default: platform-specific)
//  3. If preview disabled → skip remaining preview checks
//  4. Resolve preview_port (field if set, else port+1; overflow → error)
//  5. preview_port ∈ [1, 65535]
//  6. preview_port != port (only when preview enabled)
//  7. Resolve preview_host (default to host); normalise IPv6 brackets
//  8. If preview_origin set → must parse with scheme
//  9. If public_url set → must parse with scheme
//  10. If preview_origin set but preview_listener_enabled=false → error
func (g *GatewayConfig) ValidateAndApplyPreviewDefaults() error {
	// 1. Main port range check.
	if g.Port < 1 || g.Port > 65535 {
		return fmt.Errorf("gateway.port %d is out of range [1, 65535]", g.Port)
	}

	// 2. Resolve preview_listener_enabled.
	enabled := g.IsPreviewListenerEnabled()

	// 3. Skip remaining checks when preview is disabled.
	if !enabled {
		// 10. preview_origin requires enabled=true.
		if g.PreviewOrigin != "" {
			return fmt.Errorf("preview_origin requires preview_listener_enabled = true")
		}
		return nil
	}

	// 4. Resolve preview_port.
	if g.PreviewPort == 0 {
		derived := int64(g.Port) + 1
		if derived > 65535 {
			return fmt.Errorf(
				"auto-derived preview port %s is out of range; set gateway.preview_port explicitly",
				strconv.FormatInt(derived, 10),
			)
		}
		g.PreviewPort = int32(derived)
	}

	// 5. Preview port range check.
	if g.PreviewPort < 1 || g.PreviewPort > 65535 {
		return fmt.Errorf("gateway.preview_port %d is out of range [1, 65535]", g.PreviewPort)
	}

	// 6. Preview port must differ from main port.
	if int(g.PreviewPort) == g.Port {
		return fmt.Errorf("gateway.preview_port must differ from gateway.port")
	}

	// 7. Resolve preview_host (default to host); normalise IPv6 brackets.
	if g.PreviewHost == "" {
		g.PreviewHost = g.Host
	}
	// Normalise both sides for the comparison to avoid spurious bind-twice failures
	// (e.g. "[::] == ::").
	if normaliseIPv6Host(g.PreviewHost) == normaliseIPv6Host(g.Host) && g.PreviewHost != g.Host {
		g.PreviewHost = g.Host
	}

	// 8. preview_origin must parse with scheme if set.
	if g.PreviewOrigin != "" {
		u, err := url.Parse(g.PreviewOrigin)
		if err != nil || u.Scheme == "" {
			return fmt.Errorf(
				"gateway.preview_origin %q must be a URL with a scheme (e.g. https://preview.example.com)",
				g.PreviewOrigin,
			)
		}
	}

	// 9. public_url must parse with scheme if set.
	if g.PublicURL != "" {
		u, err := url.Parse(g.PublicURL)
		if err != nil || u.Scheme == "" {
			return fmt.Errorf(
				"gateway.public_url %q must be a URL with a scheme (e.g. https://omnipus.example.com)",
				g.PublicURL,
			)
		}
	}

	return nil
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

// expandMultiKeyModels is retained for call-site compatibility.
// Multi-key failover via APIKeys was removed; APIKeyRef is the credential pattern.
func expandMultiKeyModels(models []*ModelConfig) []*ModelConfig {
	return models
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
// security.tool_policies to "deny". Configs that still carry
// tools.<name>.enabled=false are migrated automatically at load time by
// migrateDeprecatedToolEnableFlags (migration.go); the operator-disabled
// tool is translated to a "deny" policy entry so intent is honored.
//
// Sub-structs in ToolsConfig (e.g. Browser.MaxTabs, Exec.TimeoutSeconds)
// are retained — they carry non-enable configuration like timeouts and
// limits that the tools still read at runtime.

// deprecatedEnableFlagsWarnOnce is retained for tests that exercise
// warnDeprecatedEnableFlags directly. The authoritative migration path is
// migrateDeprecatedToolEnableFlags in migration.go, wired by loadConfigInternal.
var deprecatedEnableFlagsWarnOnce sync.Once

// warnDeprecatedEnableFlags is a legacy helper retained for tests; it is no
// longer called by loadConfigInternal. The replacement is
// migrateDeprecatedToolEnableFlags (migration.go), which also translates the
// flags into security.tool_policies deny entries rather than merely warning.
//
// Callers outside this package should use migrateDeprecatedToolEnableFlags
// instead. This method will be removed in a future release.
func (t *ToolsConfig) warnDeprecatedEnableFlags() {
	if t == nil {
		return
	}
	deprecated := []struct {
		name    string
		enabled bool
	}{
		{"web", t.Web.Enabled},
		{"fetch_url", t.WebFetch.Enabled},
		{"browser", t.Browser.Enabled},
		{"mcp", t.MCP.Enabled},
		{"exec", t.Exec.Enabled},
		{"cron", t.Cron.Enabled},
		{"spawn", t.Spawn.Enabled},
		{"check_spawn_status", t.SpawnStatus.Enabled},
		{"run_subagent", t.Subagent.Enabled},
		{"write_file", t.WriteFile.Enabled},
		{"edit_file", t.EditFile.Enabled},
		{"append_file", t.AppendFile.Enabled},
		{"send_file", t.SendFile.Enabled},
		{"list_tasks", t.TaskList.Enabled},
		{"create_task", t.TaskCreate.Enabled},
		{"update_task", t.TaskUpdate.Enabled},
	}
	var disabled []string
	for _, d := range deprecated {
		if !d.enabled {
			disabled = append(disabled, d.name)
		}
	}
	if len(disabled) > 0 {
		deprecatedEnableFlagsWarnOnce.Do(func() {
			slog.Warn("tools.<name>.enabled is deprecated and has no effect; "+
				"use security.tool_policies to disable tools (set value to \"deny\")",
				"component", "config",
				"disabled_fields_ignored", disabled)
		})
	}
}
