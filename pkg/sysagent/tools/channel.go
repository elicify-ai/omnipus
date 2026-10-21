// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/elicify-ai/omnipus/pkg/channels"
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
	// Turning a QR-paired connector on reserves its unowned pairing attempt
	// before anything is saved, like every other start (gate r4 R4-A02, M2).
	attemptID, refused := t.admitPairing(id)
	if refused != nil {
		return refused
	}
	committed := false
	defer func() {
		if attemptID != "" && !committed && t.deps.ReleasePairing != nil {
			t.deps.ReleasePairing(id, attemptID)
		}
	}()
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
	if attemptID != "" {
		committed = t.deps.CommitPairing != nil && t.deps.CommitPairing(id, attemptID)
		if !committed {
			fields := map[string]any{"id": id, "revision": channelRevision(t.deps.GetCfg(), id), "changed_fields": []string{"enabled"}}
			return changeSuccess(fields, savedNotWorking("the connection is turned on, but its pairing was cancelled before it started; "+
				"turn it off and on again to start a new one", "saved configuration read back",
				"authentication with the platform", "the live connection"))
		}
	}
	return channelWriteResult(t.deps.GetCfg(), id, map[string]any{
		"enabled": true,
		"message": "Channel enabled. It will connect on the next config reload.",
	}, []string{"enabled"}, func(ch config.ChannelInstanceConfig) bool { return ch.Enabled })
}

// admitPairing reserves the unowned pairing attempt of a QR-paired connector
// this enable turns on. An enable of a connector already on is idempotent and
// reserves nothing (M1). A refusal saves nothing.
func (t *ChannelEnableTool) admitPairing(id string) (string, *tools.ToolResult) {
	chType, _ := config.ParseInstanceKey(id)
	if !whatsappChannel(chType) || t.deps.AdmitEnablePairing == nil {
		return "", nil
	}
	if cfg := t.deps.GetCfg(); cfg != nil {
		if ch, ok := cfg.Channels[id]; ok && ch.Enabled {
			return "", nil
		}
	}
	attemptID, err := t.deps.AdmitEnablePairing(id)
	switch {
	case errors.Is(err, channels.ErrPairingDeleting):
		return "", tools.ErrorResult(errorJSON("CHANNEL_BEING_DELETED",
			"this WhatsApp connection is being deleted; nothing saved", "Add the connection again once the deletion has finished"))
	case err != nil:
		return "", tools.ErrorResult(errorJSON("PAIRING_IN_PROGRESS",
			"another WhatsApp pairing of this connection is starting, running or stopping; nothing saved",
			"Wait for it to finish, or stop it from the chat that started it, then enable it again"))
	}
	return attemptID, nil
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

type ChannelConfigureTool struct {
	deps *Deps
	secretSelectionAudit
}

func NewChannelConfigureTool(d *Deps) *ChannelConfigureTool { return &ChannelConfigureTool{deps: d} }
func (t *ChannelConfigureTool) Name() string                { return "configure_channel" }
func (t *ChannelConfigureTool) Scope() tools.ToolScope      { return tools.ScopeCore }
func (t *ChannelConfigureTool) Description() string {
	return "Save a channel instance's non-secret settings and select stored credential references; plaintext secrets are refused. " +
		"Establish its intended settings first, then call request_secret with purpose and exactly one recipient form: instance + field " +
		"for that existing connection. Wait for the person to submit the secure web card, then pass its returned references " +
		"through the matching reference parameters. Submit stores a version; this approved change selects it. " +
		"Each reference must match that exact instance, recipient settings and field; a different recipient is refused, " +
		"so request a new card for it. A settings change that would redirect retained credentials is refused: " +
		"change the destination in Settings first, then request a new card. Slack's paired card supplies bot_token and app_token; " +
		"pass both as bot_token_ref and app_token_ref, not token_ref. Omitted references stay unchanged. " +
		"Read list_channels and pass expected_revision (\"absent\" for a new entry); stale writes are refused. " +
		"This does not enable or live-test the channel: call enable_channel separately; changes take effect on the next config reload. " +
		"Report committed settings as \"saved but not working\" with the reason, never working or nothing saved. " +
		"WhatsApp pairs by QR code under Connectors > WhatsApp, not with credentials here."
}

func (t *ChannelConfigureTool) Parameters() map[string]any {
	properties := map[string]any{
		"id": map[string]any{
			"type":        "string",
			"description": "Channel instance id from list_channels, also instance=id for request_secret. Establish its intended settings before requesting the card.",
		},
		"bot_id": map[string]any{
			"type": "string",
			"description": "Non-secret bot identifier; save it before requesting a credential card. " +
				"If retained credentials block a recipient change, change the destination in Settings, then request a new card.",
		},
		"app_id": map[string]any{
			"type": "string",
			"description": "Non-secret application identifier; save it before requesting a credential card. " +
				"If retained credentials block a recipient change, change the destination in Settings, then request a new card.",
		},
		"mode": map[string]any{
			"type": "string",
			"description": "Non-secret channel mode; save it before requesting a credential card. " +
				"If retained credentials block a recipient change, change the destination in Settings, then request a new card.",
		},
		expectedRevisionParam: channelRevisionProperty(),
	}
	for _, binding := range channelReferenceBindings {
		properties[binding.parameter] = map[string]any{
			"type": "string",
			"description": "Stored reference for field=" + binding.field + "; first request_secret with purpose, instance=id and field=" + binding.field +
				", then wait for the person's secure-card Submit. Must match that exact instance, recipient settings and field; " +
				"otherwise request a new card for that recipient. Never pass a plaintext value.",
		}
	}
	return map[string]any{
		"type": "object", "properties": properties,
		"required": []string{"id", expectedRevisionParam},
	}
}

func (t *ChannelConfigureTool) Execute(ctx context.Context, args map[string]any) (res *tools.ToolResult) {
	defer func() { res = finalizeChangeResult(t.Name(), res) }()
	if refusal := tools.ToolArgumentRefusal(t, args); refusal != nil {
		return refusal
	}
	id, _ := args["id"].(string)
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}
	if _, ok := findChannelInstance(id); !ok {
		return tools.ErrorResult(errorJSON("CHANNEL_NOT_FOUND", fmt.Sprintf("Unknown channel %q", id), ""))
	}
	in, err := readChannelReferenceInput(args)
	if err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), "Use request_secret for channel credentials"))
	}
	expected, missing := expectedRevisionArg(args, "list_channels")
	if missing != nil {
		return missing
	}
	var selected config.ChannelInstanceConfig
	writeErr := t.deps.withSharedResourceWrite(sharedChannel, id, expected, func() error {
		return t.deps.WithConfig(func(cfg *config.Config) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			before, exists := cfg.Channels[id]
			if !exists {
				chType, _ := config.ParseInstanceKey(id)
				before = config.ChannelInstanceConfig{Type: chType}
			}
			selected = before
			in.apply(&selected)
			if err := t.validateChannelReferences(id, before, selected, in); err != nil {
				return err
			}
			if cfg.Channels == nil {
				cfg.Channels = make(map[string]config.ChannelInstanceConfig)
			}
			cfg.Channels[id] = selected
			return nil
		})
	})
	if r, ok := revisionConflictResult(writeErr, "list_channels"); ok {
		return r
	}
	if r, ok := referenceSelectionRefusal(writeErr); ok {
		return r
	}
	if writeErr != nil {
		return tools.ErrorResult(errorJSON("CONFIG_SAVE_FAILED", "Failed to persist channel config: "+writeErr.Error(), "Stored credential values were not changed"))
	}
	refs := make([]string, 0, len(in.references))
	changed := make([]string, 0, len(in.references)+len(in.settings))
	for _, binding := range channelReferenceBindings {
		if ref, supplied := in.references[binding.parameter]; supplied {
			refs = append(refs, ref)
			changed = append(changed, binding.parameter)
		}
	}
	for _, field := range []string{"bot_id", "app_id", "mode"} {
		if _, supplied := in.settings[field]; supplied {
			changed = append(changed, field)
		}
	}
	t.selected(ctx, t.Name(), id, refs)
	return channelWriteResult(t.deps.GetCfg(), id, map[string]any{"status": "configured"}, changed,
		func(ch config.ChannelInstanceConfig) bool { return reflect.DeepEqual(ch, selected) })
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
	// Only the accepted, saved disable revokes a not-yet-started pairing
	// admission, so it can never start late (gate r4 R4-A02, M2); a refused
	// or failed disable changes nothing (gate r5 R5-A03, T2-SF-18).
	if t.deps.RevokePairingForDisable != nil {
		t.deps.RevokePairingForDisable(id)
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
				if !channelHasCredentials(ch) {
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
			if !channelHasCredentials(ch) {
				e.Status = "needs_credentials"
			}
			out = append(out, e)
		}
	}
	return tools.NewToolResult(successJSON(map[string]any{"channels": out}))
}

// channelHasCredentials reports whether a channel instance has credentials
// present in its configuration: any reference field the configure_channel
// write path can set (the shared channelReferenceBindings inventory, e.g.
// Slack's bot_token_ref/app_token_ref pair) or a bound identity. Presence is
// not proof the credentials authenticate with the platform.
func channelHasCredentials(ch config.ChannelInstanceConfig) bool {
	if ch.Identity != nil && ch.Identity.ID != "" {
		return true
	}
	for _, binding := range channelReferenceBindings {
		if *binding.target(&ch) != "" {
			return true
		}
	}
	return false
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
	if !channelHasCredentials(ch) {
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
