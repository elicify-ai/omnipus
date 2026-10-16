// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"fmt"
	"sort"

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
	// Revision is the FR-088 token the channel write tools' expected_revision
	// must carry ("absent" for a channel with no config entry).
	Revision string `json:"revision,omitempty"`
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

// findChannelInstance resolves a channel INSTANCE id — a bare type
// ("telegram", the default instance) or a namespaced instance key
// ("telegram.eu", ADR-029) — to its type's catalog entry. The shared-resource
// token and every write are scoped to the instance, never to the type
// (FR-085/FR-088).
func findChannelInstance(id string) (channelEntry, bool) {
	if config.ValidateInstanceKey(id) != nil {
		return channelEntry{}, false
	}
	chType, _ := config.ParseInstanceKey(id)
	return findChannel(chType)
}

func findChannel(id string) (channelEntry, bool) {
	for _, c := range knownChannels {
		if c.ID == id {
			return c, true
		}
	}
	return channelEntry{}, false
}

// channelRevisionProperty is the expected_revision parameter of the channel
// write tools (FR-085).
func channelRevisionProperty() map[string]any {
	return map[string]any{
		"type":        "string",
		"description": "The channel's revision from list_channels; \"absent\" for a channel that is not configured yet.",
	}
}

// channelLiveNote is what a channel write does not do: the tool path changes
// config only; the connection changes on the next config reload.
const channelLiveNote = "takes effect on the next config reload; the live connection was not changed or tested"

// ---- system.channel.enable ----

type ChannelEnableTool struct{ deps *Deps }

func NewChannelEnableTool(d *Deps) *ChannelEnableTool { return &ChannelEnableTool{deps: d} }
func (t *ChannelEnableTool) Name() string             { return "enable_channel" }
func (t *ChannelEnableTool) Scope() tools.ToolScope   { return tools.ScopeCore }
func (t *ChannelEnableTool) Description() string {
	return "Enable a channel connection. The channel will start on the next config reload. Call list_channels for valid ids. Enabling a channel with no credentials configured is allowed but the channel will fail to connect — configure it with configure_channel first. " +
		"expected_revision is the channel's revision from list_channels; the write is refused if the channel changed since.\nParameters: id (required, the channel type e.g. 'telegram', 'discord'), expected_revision (required)."
}

func (t *ChannelEnableTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":                  map[string]any{"type": "string"},
			expectedRevisionParam: channelRevisionProperty(),
		},
		"required": []string{"id", expectedRevisionParam},
	}
}

func (t *ChannelEnableTool) Execute(_ context.Context, args map[string]any) (res *tools.ToolResult) {
	defer func() { res = finalizeChangeResult(t.Name(), res) }()
	id, _ := args["id"].(string)
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}
	if _, ok := findChannelInstance(id); !ok {
		return tools.ErrorResult(errorJSON("CHANNEL_NOT_FOUND", fmt.Sprintf("Unknown channel %q", id), ""))
	}
	expected, missing := expectedRevisionArg(args, "list_channels")
	if missing != nil {
		return missing
	}
	if err := t.deps.withSharedResourceWrite(sharedChannel, id, expected, func() error {
		return t.deps.WithConfig(func(cfg *config.Config) error {
			if cfg.Channels == nil {
				cfg.Channels = map[string]config.ChannelInstanceConfig{}
			}
			ch, ok := cfg.Channels[id]
			if !ok {
				chType, _ := config.ParseInstanceKey(id)
				ch = config.ChannelInstanceConfig{Type: chType}
			}
			ch.Enabled = true
			cfg.Channels[id] = ch
			return nil
		})
	}); err != nil {
		if r, ok := revisionConflictResult(err, "list_channels"); ok {
			return r
		}
		return tools.ErrorResult(errorJSON("ENABLE_FAILED", err.Error(), ""))
	}
	return channelWriteResult(t.deps.GetCfg(), id, map[string]any{
		"enabled": true,
		"message": "Channel enabled. It will connect on the next config reload.",
	}, []string{"enabled"}, func(ch config.ChannelInstanceConfig) bool { return ch.Enabled })
}

// channelWriteResult reads the saved entry back and renders a channel write's
// FR-030 verdict. A read-back mismatch is "saved but not working"; a matching
// read-back is still "saved but not working" with the reload reason, because
// the tool path never applies a channel change live (channelLiveNote).
func channelWriteResult(cfg *config.Config, id string, fields map[string]any, changed []string,
	readBack func(config.ChannelInstanceConfig) bool,
) *tools.ToolResult {
	const scope = "saved configuration read back"
	fields["id"] = id
	fields["revision"] = channelRevision(cfg, id)
	fields["changed_fields"] = changed
	notTested := []string{"authentication with the platform", "the live connection"}
	var ch config.ChannelInstanceConfig
	ok := false
	if cfg != nil {
		ch, ok = cfg.Channels[id]
	}
	if !ok || !readBack(ch) {
		return changeSuccess(fields, savedNotWorking("the saved channel entry did not read back as written", scope,
			notTested...))
	}
	return changeSuccess(fields, savedNotWorking(channelLiveNote, scope, notTested...))
}

// ---- system.channel.configure ----

type ChannelConfigureTool struct{ deps *Deps }

func NewChannelConfigureTool(d *Deps) *ChannelConfigureTool { return &ChannelConfigureTool{deps: d} }
func (t *ChannelConfigureTool) Name() string                { return "configure_channel" }
func (t *ChannelConfigureTool) Scope() tools.ToolScope      { return tools.ScopeCore }
func (t *ChannelConfigureTool) Description() string {
	return "Store a channel's credentials and settings. Secrets (token, app_secret) go into the encrypted credential store and are referenced from config.json by a *_ref — they are never written to config.json in plaintext. Non-secret fields (bot_id, app_id, mode) are written to config.json directly. This does NOT enable the channel and does NOT check whether it is enabled: call enable_channel separately, and note the channel only connects on the next config reload. WhatsApp does not take credentials here — it pairs by QR code in the Channels screen. expected_revision is the channel's revision from list_channels; the write is refused if the channel changed since.\nParameters: id (required, from list_channels), expected_revision (required), token, app_secret, bot_id, app_id, mode."
}

func (t *ChannelConfigureTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":                  map[string]any{"type": "string"},
			"token":               map[string]any{"type": "string"},
			"bot_id":              map[string]any{"type": "string"},
			"app_id":              map[string]any{"type": "string"},
			"app_secret":          map[string]any{"type": "string"},
			"mode":                map[string]any{"type": "string"},
			expectedRevisionParam: channelRevisionProperty(),
		},
		"required": []string{"id", expectedRevisionParam},
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

func (t *ChannelConfigureTool) Execute(_ context.Context, args map[string]any) (res *tools.ToolResult) {
	defer func() { res = finalizeChangeResult(t.Name(), res) }()
	id, _ := args["id"].(string)
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}
	if _, ok := findChannelInstance(id); !ok {
		return tools.ErrorResult(errorJSON("CHANNEL_NOT_FOUND",
			fmt.Sprintf("Unknown channel %q", id), ""))
	}
	expected, missing := expectedRevisionArg(args, "list_channels")
	if missing != nil {
		return missing
	}

	// credRefs tracks field → credKey for secrets that were successfully
	// stored, so the config write can set the _ref fields; storedRefs keeps
	// them in order for a truthful partial result.
	credRefs := make(map[string]string)
	var storedRefs []string
	var refusal *tools.ToolResult
	writeErr := t.deps.withSharedResourceWrite(sharedChannel, id, expected, func() error {
		// Phase 1: store each provided secret in the credential store BEFORE
		// the config write, so a config failure never leaves a dangling _ref.
		for _, field := range channelSensitiveParams {
			v, ok := args[field].(string)
			if !ok || v == "" {
				continue
			}
			if t.deps.CredStore == nil {
				refusal = tools.ErrorResult(errorJSON("CREDENTIAL_SAVE_FAILED",
					"credential store is not available",
					"Ensure the credential store is unlocked before configuring channel secrets",
				))
				return nil
			}
			credKey := channelCredKey(id, field)
			if err := t.deps.CredStore.Set(credKey, v); err != nil {
				refusal = channelCredentialFailure(field, err, storedRefs)
				return nil
			}
			credRefs[field] = credKey
			storedRefs = append(storedRefs, credKey)
		}
		// Phase 2: write the _ref fields and plain config values into the
		// channel config inside a single WithConfig transaction.
		return t.deps.WithConfig(func(cfg *config.Config) error {
			if cfg.Channels == nil {
				cfg.Channels = map[string]config.ChannelInstanceConfig{}
			}
			ch, ok := cfg.Channels[id]
			if !ok {
				chType, _ := config.ParseInstanceKey(id)
				ch = config.ChannelInstanceConfig{Type: chType}
			}
			for field, credKey := range credRefs {
				applyChannelRef(&ch, field, credKey)
			}
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
		})
	})
	if r, ok := revisionConflictResult(writeErr, "list_channels"); ok {
		return r
	}
	if refusal != nil {
		return refusal
	}
	if writeErr != nil {
		if len(storedRefs) > 0 {
			return changeErrorWithOutcome(map[string]any{
				"success": false,
				"error": map[string]any{
					"code":    "CONFIG_SAVE_FAILED",
					"message": "Failed to persist channel config: " + writeErr.Error(),
				},
				"id":             id,
				"changed_fields": storedRefs,
			}, partlySaved("credentials were stored but the channel configuration was not written: "+writeErr.Error(),
				"", []string{"channel configuration entry"}))
		}
		return tools.ErrorResult(errorJSON("CONFIG_SAVE_FAILED",
			"Failed to persist channel config: "+writeErr.Error(), ""))
	}

	changed := append([]string{}, storedRefs...)
	for _, f := range []string{"bot_id", "app_id", "mode"} {
		if v, ok := args[f].(string); ok && v != "" {
			changed = append(changed, f)
		}
	}
	return channelWriteResult(t.deps.GetCfg(), id, map[string]any{"status": "configured"}, changed,
		func(ch config.ChannelInstanceConfig) bool {
			for field, credKey := range credRefs {
				probe := config.ChannelInstanceConfig{}
				applyChannelRef(&probe, field, credKey)
				if (field == "token" && ch.TokenRef != probe.TokenRef) ||
					(field == "app_secret" && ch.AppSecretRef != probe.AppSecretRef) {
					return false
				}
			}
			return true
		})
}

// channelCredentialFailure reports a failed secret write. Secrets stored
// earlier in the same call are durable (the store write cannot be undone
// without destroying a value it overwrote), so they make the result
// "partly saved" rather than "failed: nothing saved".
func channelCredentialFailure(field string, err error, stored []string) *tools.ToolResult {
	body := map[string]any{
		"success": false,
		"error": map[string]any{
			"code":       "CREDENTIAL_SAVE_FAILED",
			"message":    fmt.Sprintf("Failed to store %s credential: %s", field, err.Error()),
			"suggestion": "Check that the credential store is unlocked",
		},
	}
	if len(stored) == 0 {
		return tools.ErrorResult(successJSON(body))
	}
	body["changed_fields"] = stored
	return changeErrorWithOutcome(body, partlySaved(
		"some credentials were stored but the "+field+" credential and the channel configuration were not",
		"", []string{field + " credential", "channel configuration entry"}))
}

// ---- system.channel.disable ----

type ChannelDisableTool struct{ deps *Deps }

func NewChannelDisableTool(d *Deps) *ChannelDisableTool { return &ChannelDisableTool{deps: d} }
func (t *ChannelDisableTool) Name() string              { return "disable_channel" }
func (t *ChannelDisableTool) Scope() tools.ToolScope    { return tools.ScopeCore }
func (t *ChannelDisableTool) Description() string {
	return "Disable a channel connection. The channel will stop on the next config reload. A channel that has never been configured cannot be disabled — it is already inactive. " +
		"expected_revision is the channel's revision from list_channels; the write is refused if the channel changed since.\nParameters: id (required, the channel type e.g. 'telegram', 'discord'), expected_revision (required)."
}

func (t *ChannelDisableTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":                  map[string]any{"type": "string"},
			expectedRevisionParam: channelRevisionProperty(),
		},
		"required": []string{"id", expectedRevisionParam},
	}
}

func (t *ChannelDisableTool) Execute(_ context.Context, args map[string]any) (res *tools.ToolResult) {
	defer func() { res = finalizeChangeResult(t.Name(), res) }()
	id, _ := args["id"].(string)
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}
	if _, ok := findChannelInstance(id); !ok {
		return tools.ErrorResult(errorJSON("CHANNEL_NOT_FOUND", fmt.Sprintf("Unknown channel %q", id), ""))
	}
	expected, missing := expectedRevisionArg(args, "list_channels")
	if missing != nil {
		return missing
	}
	if err := t.deps.withSharedResourceWrite(sharedChannel, id, expected, func() error {
		return t.deps.WithConfig(func(cfg *config.Config) error {
			ch, ok := cfg.Channels[id]
			if !ok {
				return fmt.Errorf("channel %q is not configured", id)
			}
			ch.Enabled = false
			cfg.Channels[id] = ch
			return nil
		})
	}); err != nil {
		if r, ok := revisionConflictResult(err, "list_channels"); ok {
			return r
		}
		return tools.ErrorResult(errorJSON("DISABLE_FAILED", err.Error(), ""))
	}
	return channelWriteResult(t.deps.GetCfg(), id, map[string]any{
		"enabled": false,
		"message": "Channel disabled. It will disconnect on the next config reload.",
	}, []string{"enabled"}, func(ch config.ChannelInstanceConfig) bool { return !ch.Enabled })
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
		e.Revision = channelRevision(cfg, c.ID)
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
	// Configured instances beyond each type's default one ("telegram.eu"):
	// each is its own shared resource with its own revision (FR-088).
	if cfg != nil {
		var extra []string
		for key := range cfg.Channels {
			if _, isType := findChannel(key); !isType {
				extra = append(extra, key)
			}
		}
		sort.Strings(extra)
		for _, key := range extra {
			meta, ok := findChannelInstance(key)
			if !ok {
				continue
			}
			ch := cfg.Channels[key]
			e := channelEntry{ID: key, Name: meta.Name, Tier: meta.Tier, Implementation: meta.Implementation,
				Enabled: ch.Enabled, Status: "configured", Revision: channelRevision(cfg, key)}
			if ch.TokenRef == "" && ch.AppSecretRef == "" && (ch.Identity == nil || ch.Identity.ID == "") {
				e.Status = "needs_credentials"
			}
			out = append(out, e)
		}
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
	if _, ok := findChannelInstance(id); !ok {
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
