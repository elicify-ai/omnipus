package config

import (
	"bytes"
	"encoding/json"
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
	Version   int             `json:"version"            yaml:"-"` // Config schema version for migration
	Agents    AgentsConfig    `json:"agents"             yaml:"-"`
	Bindings  []AgentBinding  `json:"bindings,omitempty" yaml:"-"`
	Session   SessionConfig   `json:"session,omitempty"  yaml:"-"`
	Channels  ChannelsConfig  `json:"channels"           yaml:"channels"`
	Providers []*ModelConfig  `json:"providers"          yaml:"providers"` // Configured providers with credentials
	Gateway   GatewayConfig   `json:"gateway"            yaml:"-"`
	Hooks     HooksConfig     `json:"hooks,omitempty"    yaml:"-"`
	Tools     ToolsConfig     `json:"tools"              yaml:",inline"`
	Heartbeat HeartbeatConfig `json:"heartbeat"          yaml:"-"`
	Devices   DevicesConfig   `json:"devices"            yaml:"-"`
	Voice     VoiceConfig     `json:"voice"              yaml:"-"`
	// BuildInfo contains build-time version information
	BuildInfo BuildInfo `json:"build_info,omitempty" yaml:"-"`

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
// to 30. Spec v7 FR-034 — used by MemoryStore.SweepRetros and the recall search
// window for retrospectives.
func (r OmnipusRetentionConfig) RetentionMemoryRetrosDays() int {
	if r.MemoryRetrosDays <= 0 {
		return 30
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
}

func (m *AgentModelConfig) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		m.Primary = s
		m.Fallbacks = nil
		return nil
	}
	type raw struct {
		Primary   string   `json:"primary"`
		Fallbacks []string `json:"fallbacks"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	m.Primary = r.Primary
	m.Fallbacks = r.Fallbacks
	return nil
}

func (m AgentModelConfig) MarshalJSON() ([]byte, error) {
	if len(m.Fallbacks) == 0 && m.Primary != "" {
		return json.Marshal(m.Primary)
	}
	type raw struct {
		Primary   string   `json:"primary,omitempty"`
		Fallbacks []string `json:"fallbacks,omitempty"`
	}
	return json.Marshal(raw{Primary: m.Primary, Fallbacks: m.Fallbacks})
}

type AgentConfig struct {
	ID            string            `json:"id"`
	Default       bool              `json:"default,omitempty"`
	Name          string            `json:"name,omitempty"`
	Description   string            `json:"description,omitempty"`
	Workspace     string            `json:"workspace,omitempty"`
	Model         *AgentModelConfig `json:"model,omitempty"`
	Skills        []string          `json:"skills,omitempty"`
	Subagents     *SubagentsConfig  `json:"subagents,omitempty"`
	CanDelegateTo []string          `json:"can_delegate_to,omitempty"`
	// Enabled controls whether the agent is active. A nil pointer means
	// "treat as active" for backward compatibility with configs that predate
	// this field. Agents with Enabled=false are inactive; Enabled=true are
	// explicitly active. Use IsActive() to read the effective state.
	Enabled *bool `json:"enabled,omitempty"`
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
	// OwnerUsername is the username of the user who created this agent.
	// Set at creation time; only the owner or an admin may access the agent's
	// resources. Empty string for system/core agents.
	OwnerUsername string `json:"owner_username,omitempty"`
	// SandboxProfile selects the kernel sandbox profile for this agent.
	// Empty means "use the global default" (OmnipusSandboxConfig.DefaultProfile,
	// which itself falls back to SandboxProfileWorkspace when also empty).
	SandboxProfile SandboxProfile `json:"sandbox_profile,omitempty"`
	// ShellPolicy configures per-agent shell command deny patterns.
	// When non-nil, its settings are merged with the global ShellDenyPatterns
	// at enforcement time.
	ShellPolicy *AgentShellPolicy `json:"shell_policy,omitempty"`
}

// IsActive returns the effective active state of this agent.
// Agents without an explicit Enabled field (nil) are treated as active for
// backward compatibility with configs created before the Enabled field existed.
// Agents with Enabled=false are inactive; Enabled=true are explicitly active.
func (a AgentConfig) IsActive() bool {
	return a.Enabled == nil || *a.Enabled
}

// AgentType classifies an agent for scope-based tool visibility filtering.
type AgentType string

const (
	AgentTypeSystem AgentType = "system"
	AgentTypeCore   AgentType = "core"
	AgentTypeCustom AgentType = "custom"
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

type SubagentsConfig struct {
	AllowAgents []string          `json:"allow_agents,omitempty"`
	Model       *AgentModelConfig `json:"model,omitempty"`
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
	DefaultAgentID            string             `json:"default_agent_id,omitempty"      env:"OMNIPUS_DEFAULT_AGENT_ID"`

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

	// BootstrapRecapDailyBudgetUSD caps total estimated spend across a single
	// bootstrap pass. Units: USD. Default 1.00. Per-process-boot, not calendar-day.
	BootstrapRecapDailyBudgetUSD float64 `json:"bootstrap_recap_daily_budget_usd" env:"OMNIPUS_AGENTS_DEFAULTS_BOOTSTRAP_RECAP_DAILY_BUDGET_USD"`

	// RecapModelAllowList overrides the compiled-in cheap-model allow-list used
	// by FR-029a to fail-closed at boot if recap_model is too expensive. Empty
	// slice → use the package-level default.
	//
	// Entries are PREFIX-matched against the resolved recap model name
	// (strings.HasPrefix). An operator adding "claude-sonnet-" matches every
	// claude-sonnet release; writing "claude-sonnet-*" literally will not
	// match anything because the asterisk is treated as part of the prefix.
	RecapModelAllowList []string `json:"recap_model_allow_list,omitempty"`
}

// DefaultRecapModelAllowList is the compiled-in set of models considered cheap
// enough for session-end recaps under SC-010b. Spec v7 FR-029a. If you add a
// model family here, confirm its input pricing is ≤ $1/Mtok and it does not
// charge for thinking/reasoning tokens by default.
var DefaultRecapModelAllowList = []string{
	"claude-sonnet-",
	"claude-haiku-",
	"gpt-4o-mini",
	"gpt-4.1-mini",
	"z-ai/glm-",
	"gemini-flash-",
}

// ResolveRecapModelAllowList returns the configured allow-list if non-empty,
// else the compiled default.
func (d *AgentDefaults) ResolveRecapModelAllowList() []string {
	if len(d.RecapModelAllowList) > 0 {
		return d.RecapModelAllowList
	}
	return DefaultRecapModelAllowList
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

// GetBootstrapRecapDailyBudgetUSD returns the cost cap, defaulting to $1.00.
func (d *AgentDefaults) GetBootstrapRecapDailyBudgetUSD() float64 {
	if d.BootstrapRecapDailyBudgetUSD <= 0 {
		return 1.00
	}
	return d.BootstrapRecapDailyBudgetUSD
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

type ChannelsConfig struct {
	WhatsApp WhatsAppConfig `json:"whatsapp" yaml:"-"`
	Telegram TelegramConfig `json:"telegram" yaml:"telegram,omitempty"`
	Feishu   FeishuConfig   `json:"feishu"   yaml:"feishu,omitempty"`
	Discord  DiscordConfig  `json:"discord"  yaml:"discord,omitempty"`

	QQ         QQConfig         `json:"qq"          yaml:"qq,omitempty"`
	DingTalk   DingTalkConfig   `json:"dingtalk"    yaml:"dingtalk,omitempty"`
	Slack      SlackConfig      `json:"slack"       yaml:"slack,omitempty"`
	Matrix     MatrixConfig     `json:"matrix"      yaml:"matrix,omitempty"`
	LINE       LINEConfig       `json:"line"        yaml:"line,omitempty"`
	OneBot     OneBotConfig     `json:"onebot"      yaml:"onebot,omitempty"`
	WeCom      WeComConfig      `json:"wecom"       yaml:"wecom,omitempty"       envPrefix:"OMNIPUS_CHANNELS_WECOM_"`
	Weixin     WeixinConfig     `json:"weixin"      yaml:"weixin,omitempty"`
	IRC        IRCConfig        `json:"irc"         yaml:"irc,omitempty"`
	GoogleChat GoogleChatConfig `json:"google-chat" yaml:"google-chat,omitempty"`
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
	BridgeURL          string              `json:"bridge_url"              yaml:"-" env:"OMNIPUS_CHANNELS_WHATSAPP_BRIDGE_URL"`
	UseNative          bool                `json:"use_native"              yaml:"-" env:"OMNIPUS_CHANNELS_WHATSAPP_USE_NATIVE"`
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

type OneBotConfig struct {
	Enabled            bool                `json:"enabled"                    yaml:"-" env:"OMNIPUS_CHANNELS_ONEBOT_ENABLED"`
	WSUrl              string              `json:"ws_url"                     yaml:"-" env:"OMNIPUS_CHANNELS_ONEBOT_WS_URL"`
	AccessTokenRef     string              `json:"access_token_ref,omitempty" yaml:"-" env:"OMNIPUS_CHANNELS_ONEBOT_ACCESS_TOKEN_REF"`
	ReconnectInterval  int                 `json:"reconnect_interval"         yaml:"-" env:"OMNIPUS_CHANNELS_ONEBOT_RECONNECT_INTERVAL"`
	GroupTriggerPrefix []string            `json:"group_trigger_prefix"       yaml:"-" env:"OMNIPUS_CHANNELS_ONEBOT_GROUP_TRIGGER_PREFIX"`
	AllowFrom          FlexibleStringSlice `json:"allow_from"                 yaml:"-" env:"OMNIPUS_CHANNELS_ONEBOT_ALLOW_FROM"`
	GroupTrigger       GroupTriggerConfig  `json:"group_trigger,omitempty"    yaml:"-"`
	Typing             TypingConfig        `json:"typing,omitempty"           yaml:"-"`
	Placeholder        PlaceholderConfig   `json:"placeholder,omitempty"      yaml:"-"`
	ReasoningChannelID string              `json:"reasoning_channel_id"       yaml:"-"`
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
	Enabled            bool                `json:"enabled"                        yaml:"-"                              env:"OMNIPUS_CHANNELS_GOOGLECHAT_ENABLED"`
	Mode               string              `json:"mode"                           yaml:"-"                              env:"OMNIPUS_CHANNELS_GOOGLECHAT_MODE"` // "webhook" | "bot"
	WebhookURL         SecureString        `json:"webhook_url,omitzero"           yaml:"webhook_url,omitempty"          env:"OMNIPUS_CHANNELS_GOOGLECHAT_WEBHOOK_URL"`
	ServiceAccountFile string              `json:"service_account_file,omitempty" yaml:"-"                              env:"OMNIPUS_CHANNELS_GOOGLECHAT_SERVICE_ACCOUNT_FILE"`
	ServiceAccountJSON SecureString        `json:"service_account_json,omitzero"  yaml:"service_account_json,omitempty" env:"OMNIPUS_CHANNELS_GOOGLECHAT_SERVICE_ACCOUNT_JSON"`
	Space              string              `json:"space"                          yaml:"-"                              env:"OMNIPUS_CHANNELS_GOOGLECHAT_SPACE"`
	BotUser            string              `json:"bot_user"                       yaml:"-"                              env:"OMNIPUS_CHANNELS_GOOGLECHAT_BOT_USER"`
	AllowFrom          FlexibleStringSlice `json:"allow_from"                     yaml:"-"                              env:"OMNIPUS_CHANNELS_GOOGLECHAT_ALLOW_FROM"`
	GroupTrigger       GroupTriggerConfig  `json:"group_trigger,omitempty"        yaml:"-"`
	Typing             TypingConfig        `json:"typing,omitempty"               yaml:"-"`
	Placeholder        PlaceholderConfig   `json:"placeholder,omitempty"          yaml:"-"`
	ReasoningChannelID string              `json:"reasoning_channel_id"           yaml:"-"                              env:"OMNIPUS_CHANNELS_GOOGLECHAT_REASONING_CHANNEL_ID"`
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

type HeartbeatConfig struct {
	Enabled  bool `json:"enabled"  env:"OMNIPUS_HEARTBEAT_ENABLED"`
	Interval int  `json:"interval" env:"OMNIPUS_HEARTBEAT_INTERVAL"` // minutes, min 5
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

// UserRole represents a human user's role in the system.
type UserRole string

const (
	UserRoleAdmin UserRole = "admin"
	UserRoleUser  UserRole = "user"
)

// MarshalJSON serializes a UserRole to JSON.
func (r UserRole) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(r))
}

// UnmarshalJSON validates and deserializes a UserRole from JSON.
func (r *UserRole) UnmarshalJSON(data []byte) error {
	switch string(data) {
	case `"admin"`:
		*r = UserRoleAdmin
	case `"user"`:
		*r = UserRoleUser
	default:
		return fmt.Errorf("invalid role: %s", string(data))
	}
	return nil
}

// UserConfig holds per-user authentication and authorization settings.
type UserConfig struct {
	Username         string     `json:"username,omitempty"`
	PasswordHash     string     `json:"password_hash,omitempty"`      // bcrypt hash
	TokenHash        BcryptHash `json:"token_hash,omitempty"`         // bcrypt hash of bearer token
	SessionTokenHash BcryptHash `json:"session_token_hash,omitempty"` // bcrypt hash of session cookie token
	Role             UserRole   `json:"role"`
	Name             string     `json:"name,omitempty"`
}

type GatewayConfig struct {
	Host          string       `json:"host"                      env:"OMNIPUS_GATEWAY_HOST"`
	Port          int          `json:"port"                      env:"OMNIPUS_GATEWAY_PORT"`
	HotReload     bool         `json:"hot_reload"                env:"OMNIPUS_GATEWAY_HOT_RELOAD"`
	LogLevel      string       `json:"log_level,omitempty"       env:"OMNIPUS_LOG_LEVEL"`
	Token         string       `json:"token,omitempty"           env:"-"` // Bearer token stored for reference; runtime auth uses OMNIPUS_BEARER_TOKEN env var
	Users         []UserConfig `json:"users,omitempty"           env:"-"` // Per-user RBAC user list
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
	ToolConfig            `                       yaml:"-"                 envPrefix:"OMNIPUS_TOOLS_SKILLS_"`
	Registries            SkillsRegistriesConfig `yaml:",inline,omitempty"                                   json:"registries"`
	Github                SkillsGithubConfig     `yaml:"github,omitempty"                                    json:"github"`
	MaxConcurrentSearches int                    `yaml:"-"                                                   json:"max_concurrent_searches" env:"OMNIPUS_TOOLS_SKILLS_MAX_CONCURRENT_SEARCHES"`
	SearchCache           SearchCacheConfig      `yaml:"-"                                                   json:"search_cache"`
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

type SkillsRegistriesConfig struct {
	ClawHub ClawHubRegistryConfig `json:"clawhub" yaml:"clawhub,omitempty"`
}

type SkillsGithubConfig struct {
	// TokenRef is the env-var name whose value holds the GitHub personal access token.
	// Resolved at boot via credentials.InjectFromConfig; never store the token value here.
	TokenRef string `json:"token_ref,omitempty" yaml:"-" env:"OMNIPUS_TOOLS_SKILLS_GITHUB_TOKEN_REF"`
	Proxy    string `json:"proxy,omitempty"     yaml:"-" env:"OMNIPUS_TOOLS_SKILLS_GITHUB_PROXY"`
}

type ClawHubRegistryConfig struct {
	Enabled bool   `json:"enabled"  yaml:"-" env:"OMNIPUS_SKILLS_REGISTRIES_CLAWHUB_ENABLED"`
	BaseURL string `json:"base_url" yaml:"-" env:"OMNIPUS_SKILLS_REGISTRIES_CLAWHUB_BASE_URL"`
	// AuthTokenRef is the env-var name whose value holds the ClawHub authentication token.
	// Resolved at boot via credentials.InjectFromConfig; never store the token value here.
	AuthTokenRef    string `json:"auth_token_ref,omitempty" yaml:"-" env:"OMNIPUS_SKILLS_REGISTRIES_CLAWHUB_AUTH_TOKEN_REF"`
	SearchPath      string `json:"search_path"              yaml:"-" env:"OMNIPUS_SKILLS_REGISTRIES_CLAWHUB_SEARCH_PATH"`
	SkillsPath      string `json:"skills_path"              yaml:"-" env:"OMNIPUS_SKILLS_REGISTRIES_CLAWHUB_SKILLS_PATH"`
	DownloadPath    string `json:"download_path"            yaml:"-" env:"OMNIPUS_SKILLS_REGISTRIES_CLAWHUB_DOWNLOAD_PATH"`
	Timeout         int    `json:"timeout"                  yaml:"-" env:"OMNIPUS_SKILLS_REGISTRIES_CLAWHUB_TIMEOUT"`
	MaxZipSize      int    `json:"max_zip_size"             yaml:"-" env:"OMNIPUS_SKILLS_REGISTRIES_CLAWHUB_MAX_ZIP_SIZE"`
	MaxResponseSize int    `json:"max_response_size"        yaml:"-" env:"OMNIPUS_SKILLS_REGISTRIES_CLAWHUB_MAX_RESPONSE_SIZE"`
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
	// RequiresAdminAsk lists tool names from this server that require admin approval
	// before execution (FR-064). Tools not in this list default to RequiresAdminAsk()==false.
	// Example: ["dangerous_tool", "drop_database"]
	RequiresAdminAsk []string `json:"requires_admin_ask,omitempty"`
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
	return loadConfigInternal(path, store)
}

func LoadConfig(path string) (*Config, error) {
	return loadConfigInternal(path, nil)
}

func loadConfigInternal(path string, store CredentialStore) (*Config, error) {
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

	// Migrate legacy channel config fields to new unified structures
	cfg.migrateChannelConfigs()

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
	// Post-refactor: tools.<name>.enabled is deprecated. If the loaded config
	// carries explicit false values, translate them idempotently into
	// security.tool_policies deny entries so operator intent is enforced
	// by the policy engine rather than silently ignored (issue #237).
	migrateDeprecatedToolEnableFlags(cfg, data)

	// Apply defaults and validate bounds for all security-relevant fields
	// (FR-001, FR-002a, numeric sandbox fields, AuthMismatchLogLevel).
	if err := validateBootConfig(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// migrateProviderFields splits old-format Model fields (e.g. "openrouter/anthropic/claude-opus-4")
// into separate Provider and Model fields for backward compatibility.
func migrateProviderFields(cfg *Config) {
	knownProtocols := map[string]bool{
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
	for _, p := range cfg.Providers {
		if p.Provider != "" {
			continue
		}
		protocol, modelID, found := strings.Cut(p.Model, "/")
		if found && knownProtocols[protocol] {
			p.Provider = protocol
			p.Model = modelID
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
	// Discord: mention_only -> group_trigger.mention_only
	if c.Channels.Discord.MentionOnly && !c.Channels.Discord.GroupTrigger.MentionOnly {
		c.Channels.Discord.GroupTrigger.MentionOnly = true
	}

	// OneBot: group_trigger_prefix -> group_trigger.prefixes
	if len(c.Channels.OneBot.GroupTriggerPrefix) > 0 &&
		len(c.Channels.OneBot.GroupTrigger.Prefixes) == 0 {
		c.Channels.OneBot.GroupTrigger.Prefixes = c.Channels.OneBot.GroupTriggerPrefix
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
		{"web_fetch", t.WebFetch.Enabled},
		{"browser", t.Browser.Enabled},
		{"mcp", t.MCP.Enabled},
		{"exec", t.Exec.Enabled},
		{"cron", t.Cron.Enabled},
		{"spawn", t.Spawn.Enabled},
		{"spawn_status", t.SpawnStatus.Enabled},
		{"subagent", t.Subagent.Enabled},
		{"write_file", t.WriteFile.Enabled},
		{"edit_file", t.EditFile.Enabled},
		{"append_file", t.AppendFile.Enabled},
		{"send_file", t.SendFile.Enabled},
		{"task_list", t.TaskList.Enabled},
		{"task_create", t.TaskCreate.Enabled},
		{"task_update", t.TaskUpdate.Enabled},
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
