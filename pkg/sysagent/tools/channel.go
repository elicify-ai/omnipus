// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// channelEntry describes a channel's runtime state (not persisted — read from config).
type channelEntry struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Tier           string `json:"tier"`
	Implementation string `json:"implementation"`
	Enabled        bool   `json:"enabled"`
	Status         string `json:"status"`
}

// channelDisplayMeta carries presentational-only metadata (Name/Tier/
// Implementation) for each canonical supported channel type. Tier is not
// consulted by any other code in the repo (no other package or test reads
// it) — it is purely an informational grouping for list_channels output.
//
// Deliberately keyed by the SAME ids as config.KnownChannelTypes() (the
// canonical, single-source-of-truth set pkg/config uses to validate/drop
// channel config entries): TestKnownChannels_MatchesCanonicalChannelTypes
// asserts the two id sets are identical, and buildKnownChannels below panics
// at package-init time if a canonical id has no entry here — so a channel
// type added to pkg/config's canonical list without a matching display-meta
// entry fails the build loudly instead of silently listing it with an empty
// Name/Tier/Implementation.
var channelDisplayMeta = map[string]struct {
	Name           string
	Tier           string
	Implementation string
}{
	"telegram":    {"Telegram", "tier1", "go"},
	"discord":     {"Discord", "tier1", "go"},
	"whatsapp":    {"WhatsApp", "tier1", "go"},
	"slack":       {"Slack", "tier2", "go"},
	"matrix":      {"Matrix", "tier2", "go"},
	"google-chat": {"Google Chat", "tier2", "go"},
	"irc":         {"IRC", "tier3", "go"},
	"line":        {"LINE", "tier3", "go"},
	"feishu":      {"Feishu", "tier3", "go"},
	"qq":          {"QQ", "tier3", "go"},
	"dingtalk":    {"DingTalk", "tier3", "go"},
	"wecom":       {"WeCom", "tier3", "go"},
	"weixin":      {"Weixin/WeChat", "tier3", "go"},
}

// knownChannels lists the channels Omnipus knows about with their metadata.
// Its ID membership is DERIVED from config.KnownChannelTypes() rather than
// hand-maintained as a second, driftable copy — this eliminates the exact
// bug class this list used to have: "signal" was in the old hand-written
// list here but NOT in pkg/config's canonical list, so
// enable_channel{"id":"signal"} returned success and the entry was then
// silently dropped on the next config reload (config.normalizeChannelMap
// drops any key not in knownChannelTypes); meanwhile several real,
// supported channels (feishu, qq, dingtalk, matrix, wecom, weixin,
// google-chat) were in the canonical list but absent here, so
// enable_channel/configure_channel/etc. on any of them returned
// CHANNEL_NOT_FOUND for channels Omnipus genuinely supports.
var knownChannels = buildKnownChannels()

// buildKnownChannels derives the []channelEntry slice from
// config.KnownChannelTypes(), looking up presentational metadata from
// channelDisplayMeta. Panics if a canonical channel type has no matching
// metadata entry — this is a static, compile-time-known set (not a runtime
// condition), so a gap here is an authoring mistake that must fail loudly
// rather than silently render an entry with empty fields.
func buildKnownChannels() []channelEntry {
	ids := config.KnownChannelTypes()
	out := make([]channelEntry, 0, len(ids))
	for _, id := range ids {
		meta, ok := channelDisplayMeta[id]
		if !ok {
			panic(fmt.Sprintf(
				"pkg/sysagent/tools/channel.go: canonical channel type %q (from config.KnownChannelTypes) "+
					"has no channelDisplayMeta entry — add one so knownChannels stays in sync", id))
		}
		out = append(out, channelEntry{
			ID:             id,
			Name:           meta.Name,
			Tier:           meta.Tier,
			Implementation: meta.Implementation,
		})
	}
	return out
}

func findChannel(id string) (channelEntry, bool) {
	for _, c := range knownChannels {
		if c.ID == id {
			return c, true
		}
	}
	return channelEntry{}, false
}

// ---- system.channel.enable ----

type ChannelEnableTool struct{ deps *Deps }

func NewChannelEnableTool(d *Deps) *ChannelEnableTool { return &ChannelEnableTool{deps: d} }
func (t *ChannelEnableTool) Name() string             { return "enable_channel" }
func (t *ChannelEnableTool) Scope() tools.ToolScope   { return tools.ScopeCore }
func (t *ChannelEnableTool) Description() string {
	return "Enable a channel connection. The channel will start on the next config reload. Call list_channels for valid ids. Enabling a channel with no credentials configured is allowed but the channel will fail to connect — configure it with configure_channel first.\nParameters: id (required, the channel type e.g. 'telegram', 'discord')."
}

func (t *ChannelEnableTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"id": map[string]any{"type": "string"}},
		"required":   []string{"id"},
	}
}

func (t *ChannelEnableTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}
	if _, ok := findChannel(id); !ok {
		return tools.ErrorResult(errorJSON("CHANNEL_NOT_FOUND", fmt.Sprintf("Unknown channel %q", id), ""))
	}
	if err := t.deps.WithConfig(func(cfg *config.Config) error {
		ch, ok := cfg.Channels[id]
		if !ok {
			ch = config.ChannelInstanceConfig{Type: id}
		}
		ch.Enabled = true
		cfg.Channels[id] = ch
		return nil
	}); err != nil {
		return tools.ErrorResult(errorJSON("ENABLE_FAILED", err.Error(), ""))
	}
	return tools.NewToolResult(successJSON(map[string]any{
		"id":      id,
		"enabled": true,
		"message": "Channel enabled. It will connect on the next config reload.",
	}))
}

// ---- system.channel.configure ----

type ChannelConfigureTool struct{ deps *Deps }

func NewChannelConfigureTool(d *Deps) *ChannelConfigureTool { return &ChannelConfigureTool{deps: d} }
func (t *ChannelConfigureTool) Name() string                { return "configure_channel" }
func (t *ChannelConfigureTool) Scope() tools.ToolScope      { return tools.ScopeCore }
func (t *ChannelConfigureTool) Description() string {
	return "Store a channel's credentials and settings. Secrets (token, app_secret) go into the encrypted credential store and are referenced from config.json by a *_ref — they are never written to config.json in plaintext. Non-secret fields (bot_id, app_id, mode) are written to config.json directly. This does NOT enable the channel and does NOT check whether it is enabled: call enable_channel separately, and note the channel only connects on the next config reload. WhatsApp does not take credentials here — it pairs by QR code in the Channels screen.\nParameters: id (required, from list_channels), token, app_secret, bot_id, app_id, mode."
}

func (t *ChannelConfigureTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":         map[string]any{"type": "string"},
			"token":      map[string]any{"type": "string"},
			"bot_id":     map[string]any{"type": "string"},
			"app_id":     map[string]any{"type": "string"},
			"app_secret": map[string]any{"type": "string"},
			"mode":       map[string]any{"type": "string"},
		},
		"required": []string{"id"},
	}
}

// channelSensitiveParams is the ordered list of sensitive parameter names that
// configure_channel accepts. These are stored in the encrypted credential store
// (never in config.json) and referenced via <field>_ref on ChannelInstanceConfig.
// The mapping from field name to the *_ref struct field is handled by
// applyChannelRef below. Must stay in sync with the Parameters() schema.
var channelSensitiveParams = []string{"token", "app_secret"}

// channelCredKey returns the canonical credential-store key for a channel secret.
// Format mirrors the gateway's channelCredKey: "channel_<id>_<field>".
func channelCredKey(channelID, field string) string {
	return "channel_" + channelID + "_" + field
}

// applyChannelRef sets the appropriate *_ref field on ch for the given secret
// field name and credential reference key. This mirrors the gateway's
// configureChannel, which writes updates[field+"_ref"] = refName into the raw
// config map. Here we set the matching typed struct field instead.
func applyChannelRef(ch *config.ChannelInstanceConfig, field, refName string) {
	switch field {
	case "token":
		ch.TokenRef = refName
	case "app_secret":
		ch.AppSecretRef = refName
	}
}

func (t *ChannelConfigureTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}
	if _, ok := findChannel(id); !ok {
		return tools.ErrorResult(errorJSON("CHANNEL_NOT_FOUND",
			fmt.Sprintf("Unknown channel %q", id), ""))
	}

	// Phase 1: store each provided secret in the credential store BEFORE the
	// config write. The gateway follows the same order so that a partial failure
	// (cred stored, config not written) leaves no dangling _ref — the config is
	// only updated after all secrets are safely persisted.
	sensitive := make(map[string]bool, len(channelSensitiveParams))
	for _, f := range channelSensitiveParams {
		sensitive[f] = true
	}
	// credRefs tracks field → credKey for secrets that were successfully stored,
	// so the WithConfig closure can write the _ref fields atomically.
	credRefs := make(map[string]string)
	for _, field := range channelSensitiveParams {
		v, ok := args[field].(string)
		if !ok || v == "" {
			continue
		}
		if t.deps.CredStore == nil {
			return tools.ErrorResult(errorJSON("CREDENTIAL_SAVE_FAILED",
				"credential store is not available",
				"Ensure the credential store is unlocked before configuring channel secrets",
			))
		}
		credKey := channelCredKey(id, field)
		if err := t.deps.CredStore.Set(credKey, v); err != nil {
			return tools.ErrorResult(errorJSON("CREDENTIAL_SAVE_FAILED",
				fmt.Sprintf("Failed to store %s credential: %s", field, err.Error()),
				"Check that the credential store is unlocked",
			))
		}
		credRefs[field] = credKey
	}

	// Phase 2: write the _ref fields and plain config values into the channel
	// config inside a single WithConfig transaction. This is the mutation the
	// original code was missing entirely: without it the ChannelInstanceConfig
	// never had TokenRef / AppSecretRef set, so prerequisites.go always reported
	// "no credentials" and test_channel returned success=false.
	if err := t.deps.WithConfig(func(cfg *config.Config) error {
		if cfg.Channels == nil {
			cfg.Channels = map[string]config.ChannelInstanceConfig{}
		}
		ch, ok := cfg.Channels[id]
		if !ok {
			ch = config.ChannelInstanceConfig{Type: id}
		}
		// Apply credential refs for every secret field we stored above.
		for field, credKey := range credRefs {
			applyChannelRef(&ch, field, credKey)
		}
		// Apply non-sensitive plain fields supported by this tool.
		if v, ok := args["bot_id"].(string); ok && v != "" {
			ch.BotID = v
		}
		if v, ok := args["app_id"].(string); ok && v != "" {
			ch.AppID = v
		}
		if v, ok := args["mode"].(string); ok && v != "" {
			ch.Mode = v
		}
		cfg.Channels[id] = ch
		return nil
	}); err != nil {
		return tools.ErrorResult(errorJSON("CONFIG_SAVE_FAILED",
			"Failed to persist channel config: "+err.Error(),
			"",
		))
	}

	return tools.NewToolResult(successJSON(map[string]any{
		"id":     id,
		"status": "configured",
	}))
}

// ---- system.channel.disable ----

type ChannelDisableTool struct{ deps *Deps }

func NewChannelDisableTool(d *Deps) *ChannelDisableTool { return &ChannelDisableTool{deps: d} }
func (t *ChannelDisableTool) Name() string              { return "disable_channel" }
func (t *ChannelDisableTool) Scope() tools.ToolScope    { return tools.ScopeCore }
func (t *ChannelDisableTool) Description() string {
	return "Disable a channel connection. The channel will stop on the next config reload. A channel that has never been configured cannot be disabled — it is already inactive.\nParameters: id (required, the channel type e.g. 'telegram', 'discord')."
}

func (t *ChannelDisableTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"id": map[string]any{"type": "string"}},
		"required":   []string{"id"},
	}
}

func (t *ChannelDisableTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}
	if _, ok := findChannel(id); !ok {
		return tools.ErrorResult(errorJSON("CHANNEL_NOT_FOUND", fmt.Sprintf("Unknown channel %q", id), ""))
	}
	if err := t.deps.WithConfig(func(cfg *config.Config) error {
		ch, ok := cfg.Channels[id]
		if !ok {
			return fmt.Errorf("channel %q is not configured", id)
		}
		ch.Enabled = false
		cfg.Channels[id] = ch
		return nil
	}); err != nil {
		return tools.ErrorResult(errorJSON("DISABLE_FAILED", err.Error(), ""))
	}
	return tools.NewToolResult(successJSON(map[string]any{
		"id":      id,
		"enabled": false,
		"message": "Channel disabled. It will disconnect on the next config reload.",
	}))
}

// ---- system.channel.list ----

type ChannelListTool struct{ deps *Deps }

func NewChannelListTool(d *Deps) *ChannelListTool { return &ChannelListTool{deps: d} }
func (t *ChannelListTool) Name() string           { return "list_channels" }
func (t *ChannelListTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *ChannelListTool) Description() string {
	return "List every messaging channel Omnipus supports — Telegram, Discord, WhatsApp, Slack, Matrix, Google Chat, IRC, LINE, Feishu, QQ, DingTalk, WeCom, Weixin — with each one's live state: not_configured, needs_credentials, or configured, plus whether it is currently enabled. Use the returned id with enable_channel, configure_channel, or test_channel. No parameters required."
}

func (t *ChannelListTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *ChannelListTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	cfg := t.deps.GetCfg()
	out := make([]channelEntry, 0, len(knownChannels))
	for _, c := range knownChannels {
		e := c // copy the static display meta
		if cfg != nil {
			if ch, ok := cfg.Channels[c.ID]; ok {
				e.Enabled = ch.Enabled
				e.Status = "configured"
				if ch.TokenRef == "" && ch.AppSecretRef == "" &&
					(ch.Identity == nil || ch.Identity.ID == "") {
					e.Status = "needs_credentials"
				}
			} else {
				e.Status = "not_configured"
			}
		}
		out = append(out, e)
	}
	return tools.NewToolResult(successJSON(map[string]any{"channels": out}))
}

// ---- system.channel.test ----

type ChannelTestTool struct{ deps *Deps }

func NewChannelTestTool(d *Deps) *ChannelTestTool { return &ChannelTestTool{deps: d} }
func (t *ChannelTestTool) Name() string           { return "test_channel" }
func (t *ChannelTestTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *ChannelTestTool) Description() string {
	return "Check a channel's stored configuration: that the channel type is known, that a config entry exists, whether it is enabled, and whether credentials are present. Does NOT contact the platform \u2014 this makes zero network calls, so a revoked or wrong token still reports success=true, and a channel that is configured but disabled also reports success=true (read the `enabled` field). Enable it with enable_channel and watch the gateway log for the real connection result.\nParameters: id (required, from list_channels)."
}

func (t *ChannelTestTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"id": map[string]any{"type": "string"}},
		"required":   []string{"id"},
	}
}

func (t *ChannelTestTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}
	if _, ok := findChannel(id); !ok {
		return tools.ErrorResult(errorJSON("CHANNEL_NOT_FOUND", fmt.Sprintf("Unknown channel %q", id), ""))
	}
	cfg := t.deps.GetCfg()
	if cfg == nil {
		return tools.ErrorResult(errorJSON("CONFIG_UNAVAILABLE", "config not available", ""))
	}
	ch, ok := cfg.Channels[id]
	if !ok {
		return tools.NewToolResult(successJSON(map[string]any{
			"id":      id,
			"success": false,
			"message": fmt.Sprintf("Channel %q is not configured. Use configure_channel to set it up.", id),
		}))
	}
	hasCreds := ch.TokenRef != "" || ch.AppSecretRef != "" ||
		(ch.Identity != nil && ch.Identity.ID != "")
	if !hasCreds {
		return tools.NewToolResult(successJSON(map[string]any{
			"id":      id,
			"success": false,
			"message": fmt.Sprintf(
				"Channel %q exists but has no credentials. Use configure_channel to set its token/credentials.",
				id,
			),
		}))
	}
	return tools.NewToolResult(successJSON(map[string]any{
		"id":      id,
		"success": true,
		"enabled": ch.Enabled,
		"message": fmt.Sprintf(
			"Channel %q is configured (type=%s, enabled=%v, credentials=set).",
			id,
			ch.Type,
			ch.Enabled,
		),
	}))
}
