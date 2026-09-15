// config_channels_instance.go: The channels instance model - instance-key grammar, identity and kind validation, and extraction of the typed legacy per-channel sub-configs (InstanceTo*)

package config

import (
	"errors"
	"fmt"
	"log/slog"
	mathrand "math/rand"
	"sort"
	"strings"
)

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

type WhatsAppConfig struct {
	Enabled            bool                `json:"enabled"                 yaml:"-" env:"OMNIPUS_CHANNELS_WHATSAPP_ENABLED"`
	SessionStorePath   string              `json:"session_store_path"      yaml:"-" env:"OMNIPUS_CHANNELS_WHATSAPP_SESSION_STORE_PATH"`
	AllowFrom          FlexibleStringSlice `json:"allow_from"              yaml:"-" env:"OMNIPUS_CHANNELS_WHATSAPP_ALLOW_FROM"`
	ReasoningChannelID string              `json:"reasoning_channel_id"    yaml:"-" env:"OMNIPUS_CHANNELS_WHATSAPP_REASONING_CHANNEL_ID"`
	GroupTrigger       GroupTriggerConfig  `json:"group_trigger,omitempty" yaml:"-"`
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

type DingTalkConfig struct {
	Enabled            bool                `json:"enabled"                     yaml:"-" env:"OMNIPUS_CHANNELS_DINGTALK_ENABLED"`
	ClientID           string              `json:"client_id"                   yaml:"-" env:"OMNIPUS_CHANNELS_DINGTALK_CLIENT_ID"`
	ClientSecretRef    string              `json:"client_secret_ref,omitempty" yaml:"-" env:"OMNIPUS_CHANNELS_DINGTALK_CLIENT_SECRET_REF"`
	AllowFrom          FlexibleStringSlice `json:"allow_from"                  yaml:"-" env:"OMNIPUS_CHANNELS_DINGTALK_ALLOW_FROM"`
	GroupTrigger       GroupTriggerConfig  `json:"group_trigger,omitempty"     yaml:"-"`
	ReasoningChannelID string              `json:"reasoning_channel_id"        yaml:"-" env:"OMNIPUS_CHANNELS_DINGTALK_REASONING_CHANNEL_ID"`
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

type WeComConfig struct {
	Enabled             bool                `json:"enabled"                 yaml:"-" env:"ENABLED"`
	BotID               string              `json:"bot_id"                  yaml:"-" env:"BOT_ID"`
	SecretRef           string              `json:"secret_ref,omitempty"    yaml:"-" env:"SECRET_REF"`
	WebSocketURL        string              `json:"websocket_url,omitempty" yaml:"-" env:"WEBSOCKET_URL"`
	SendThinkingMessage bool                `json:"send_thinking_message"   yaml:"-" env:"SEND_THINKING_MESSAGE"`
	AllowFrom           FlexibleStringSlice `json:"allow_from"              yaml:"-" env:"ALLOW_FROM"`
	ReasoningChannelID  string              `json:"reasoning_channel_id"    yaml:"-" env:"REASONING_CHANNEL_ID"`
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
