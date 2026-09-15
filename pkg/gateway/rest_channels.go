// rest_channels.go: Connectors — enable, configure, route, and test channel instances

package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/channels"
	whatsappnative "github.com/elicify-ai/omnipus/pkg/channels/whatsapp_native"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/google/uuid"
)

// readChannelConfigRaw reads config.json from disk and returns the raw map for
// the given channel. Both getChannelConfig and testChannel use this to avoid
// reading stale in-memory config after async reloads.
func (a *restAPI) readChannelConfigRaw(channelID string) (map[string]any, error) {
	a.configMu.Lock()
	raw, err := os.ReadFile(a.configPath())
	a.configMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	channels, _ := m["channels"].(map[string]any)
	if channels == nil {
		return map[string]any{}, nil
	}
	chCfg, _ := channels[channelID].(map[string]any)
	if chCfg == nil {
		return map[string]any{}, nil
	}
	return chCfg, nil
}

// channelBaseTypeMeta is the static presentation metadata for a base channel
// type: the human-readable name, transport label, and description surfaced in
// each ChannelEntry. It is type-level (shared by every instance of the type).
type channelBaseTypeMeta struct {
	name        string
	transport   string
	description string
}

// channelBaseTypes maps base channel type → its presentation metadata. This is
// the single source of truth for the name/transport/description of every
// conversational channel row (both configured instances and the static
// "available but unconfigured" rows). Email is intentionally absent — it is a
// TOOL surface (per-agent mailbox), not a conversational channel.
var channelBaseTypes = map[string]channelBaseTypeMeta{
	"telegram":    {name: "Telegram", transport: "webhook", description: "Telegram Bot API"},
	"discord":     {name: "Discord", transport: "websocket", description: "Discord Gateway"},
	"slack":       {name: "Slack", transport: "websocket", description: "Slack Socket Mode"},
	"whatsapp":    {name: "WhatsApp", transport: "native", description: "WhatsApp (native, whatsmeow)"},
	"feishu":      {name: "Feishu / Lark", transport: "webhook", description: "Feishu (Lark) Bot"},
	"dingtalk":    {name: "DingTalk", transport: "webhook", description: "DingTalk Bot"},
	"wecom":       {name: "WeCom", transport: "webhook", description: "WeCom (WeChat Work) Bot"},
	"weixin":      {name: "Weixin", transport: "webhook", description: "Weixin (WeChat) Official Account"},
	"line":        {name: "LINE", transport: "webhook", description: "LINE Messaging API"},
	"qq":          {name: "QQ", transport: "websocket", description: "QQ via napcat"},
	"irc":         {name: "IRC", transport: "tcp", description: "Internet Relay Chat"},
	"matrix":      {name: "Matrix", transport: "http", description: "Matrix protocol"},
	"google-chat": {name: "Google Chat", transport: "webhook", description: "Google Chat (webhook or service account)"},
}

// channelBaseTypeOrder is the canonical display order for the static
// "available but unconfigured" rows (Go map iteration is randomized, so a fixed
// slice keeps the list stable across requests).
var channelBaseTypeOrder = []string{
	"telegram", "discord", "slack", "whatsapp", "feishu", "dingtalk",
	"wecom", "weixin", "line", "qq", "irc", "matrix", "google-chat",
}

// validChannelIDs is the set of base channel types that can be toggled via the
// API. "webchat" is always enabled and intentionally excluded.
//
// Keyed by plain string literals (base channel type) because ChannelId is now a
// validated pattern (^[a-z0-9-]+(\.[a-z0-9-]+)?$) rather than a closed enum.
// Per-instance IDs like "whatsapp.eu" are accepted by extracting the base type
// via config.ParseInstanceKey before the lookup (ADR-029 Gate 0).
var validChannelIDs = map[string]bool{
	"telegram": true, "discord": true, "slack": true, "whatsapp": true,
	"feishu": true, "dingtalk": true, "wecom": true, "weixin": true,
	"line": true, "qq": true, "irc": true,
	"matrix": true, "google-chat": true,
	// "email" is deliberately absent — email is NOT a channel (M11:
	// config.knownChannelTypes excludes it too). The SPA uses
	// /agents/{id}/mailbox exclusively; /channels/email/* is unknown and 404s.
}

// --- Channels ---

// HandleChannels handles GET /api/v1/channels, GET /api/v1/channels/{id},
// PUT /api/v1/channels/{id}/enable|disable|configure, POST /api/v1/channels/{id}/test,
// and GET/PUT /api/v1/channels/{id}/routing.
func (a *restAPI) HandleChannels(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	sub := strings.TrimPrefix(path, "/api/v1/channels")
	sub = strings.TrimPrefix(sub, "/")

	if sub != "" {
		parts := strings.SplitN(sub, "/", 2)
		channelID := parts[0]
		// Accept both bare type keys ("whatsapp") and per-instance keys
		// ("whatsapp.eu"): extract the base type and validate against known types.
		// Per-instance existence is validated per-endpoint (ADR-029 Gate 0).
		baseChannelType, _ := config.ParseInstanceKey(channelID)
		if !validChannelIDs[baseChannelType] {
			jsonErr(w, http.StatusNotFound, fmt.Sprintf("channel %q not found", channelID))
			return
		}
		// FR-024 / S-1+S-2: enforce the full ADR-029 instance-key grammar at the
		// write boundary BEFORE any credential or config write reaches disk.
		// ParseInstanceKey only extracts the base type; it does not validate the
		// slug. An attacker with a valid session could otherwise send
		// PUT /channels/whatsapp.BAD/configure with an uppercase or overlong slug,
		// causing safeUpdateConfigJSON to persist the malformed key — which makes
		// LoadConfig abort on next boot (boot-brick) and leaves an orphaned
		// credential under the attacker-chosen key.
		// Return 400 "malformed channel id" (distinct from the 404 "not found"
		// above) so callers can distinguish unknown-type from bad-grammar.
		if err := config.ValidateInstanceKey(channelID); err != nil {
			jsonErr(w, http.StatusBadRequest, "malformed channel id")
			return
		}

		if len(parts) == 1 {
			switch r.Method {
			case http.MethodGet:
				a.getChannelConfig(w, channelID)
			case http.MethodDelete:
				// FINAL-REVIEW MEDIUM: DELETE is a high-blast-radius destructive verb
				// (os.RemoveAll on the instance state dir + credential deletion), so it
				// is gated by the dev-mode-bypass guard. The /channels route is already
				// registered under withAuth, so apply only the authorization layer
				// (RequireNotBypass) here — re-running withAuth would double-authenticate.
				a.requireAdminAuthz(func(w http.ResponseWriter, r *http.Request) {
					a.deleteChannelInstance(w, channelID)
				})(w, r)
			default:
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			}
			return
		}

		action := parts[1]
		switch action {
		case "enable":
			if r.Method != http.MethodPut {
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			a.setChannelEnabled(w, channelID, true)
		case "disable":
			if r.Method != http.MethodPut {
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			a.setChannelEnabled(w, channelID, false)
		case "configure":
			if r.Method != http.MethodPut {
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			a.configureChannel(w, r, channelID)
		case "test":
			if r.Method != http.MethodPost {
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			a.testChannel(w, channelID)
		case "routing":
			switch r.Method {
			case http.MethodGet:
				a.getChannelRouting(w, channelID)
			case http.MethodPut:
				a.setChannelRouting(w, r, channelID)
			default:
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			}
		default:
			jsonErr(w, http.StatusNotFound, "unknown channel action")
		}
		return
	}

	switch r.Method {
	case http.MethodGet:
		// fall through to list logic below
	case http.MethodPost:
		// FINAL-REVIEW MEDIUM: POST creates a channel instance (a destructive,
		// config-mutating action). The /channels route is already registered
		// under withAuth, so apply only the authorization layer (RequireNotBypass),
		// mirroring the other high-blast-radius routes without re-running withAuth.
		a.requireAdminAuthz(a.createChannelInstance)(w, r)
		return
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	cfg := a.agentLoop.GetConfig()

	// FINAL-REVIEW MAJOR: emit ONE ChannelEntry per configured instance, not one
	// per base type. The previous design collapsed all instances of a type into a
	// single row (byType last-writer-wins), so a second instance (whatsapp.eu +
	// whatsapp.us) was invisible and unmanageable — defeating US-6/US-11.
	//
	// Shape now:
	//   1. webchat (built-in, always first, always enabled, no instance entry).
	//   2. One entry per key in cfg.Channels — id == instance_id == the map key,
	//      with base-type metadata (name/transport/description) looked up from
	//      channelBaseTypes, plus per-instance enabled + identity.
	//   3. One "available but unconfigured" entry per base type that has NO
	//      configured instance (so the operator can still discover + add it).
	channels := []gen.ChannelEntry{
		{Id: "webchat", Name: "Web Chat", Transport: "websocket", Enabled: true, Description: "Built-in browser chat"},
	}

	// Track which base types already have at least one configured instance so we
	// can append the static "unconfigured" rows for the rest.
	configuredTypes := make(map[string]bool, len(cfg.Channels))

	// Deterministic ordering: configured instances sorted by their map key so the
	// list is stable across requests (Go map iteration is randomized).
	instanceKeys := make([]string, 0, len(cfg.Channels))
	for key := range cfg.Channels {
		instanceKeys = append(instanceKeys, key)
	}
	sort.Strings(instanceKeys)

	for _, key := range instanceKeys {
		inst := cfg.Channels[key]
		baseType := strings.ToLower(strings.TrimSpace(inst.Type))
		if baseType == "" {
			baseType, _ = config.ParseInstanceKey(key)
		}
		meta, known := channelBaseTypes[baseType]
		if !known {
			// An unknown/legacy type persisted in config still gets a row so the
			// operator can see and delete it; fall back to sensible defaults.
			meta = channelBaseTypeMeta{name: baseType, transport: "webhook", description: baseType}
		}
		configuredTypes[baseType] = true

		instanceID := key
		entry := gen.ChannelEntry{
			Id:          key,
			InstanceId:  &instanceID,
			Name:        meta.name,
			Transport:   gen.ChannelEntryTransport(meta.transport),
			Enabled:     inst.Enabled,
			Description: meta.description,
		}
		if baseType == "whatsapp" {
			entry.NativeAvailable = boolPtr(whatsappnative.NativeAvailable)
		}
		if ident := identityForInstance(inst.Identity); ident != nil {
			entry.Identity = ident
		}
		channels = append(channels, entry)
	}

	// Append the static "available but unconfigured" rows in the canonical base-type
	// order for every type with no configured instance.
	for _, baseType := range channelBaseTypeOrder {
		if configuredTypes[baseType] {
			continue
		}
		meta := channelBaseTypes[baseType]
		entry := gen.ChannelEntry{
			Id:          baseType,
			Name:        meta.name,
			Transport:   gen.ChannelEntryTransport(meta.transport),
			Enabled:     false,
			Description: meta.description,
		}
		if baseType == "whatsapp" {
			entry.NativeAvailable = boolPtr(whatsappnative.NativeAvailable)
		}
		channels = append(channels, entry)
	}

	// Overlay degraded state from the runtime channel manager. Channels that
	// failed to construct at startup are marked degraded=true with the init
	// error as the reason. whatsapp_native failures map to the "whatsapp" entry
	// because both transports share one list entry.
	if mgr := a.agentLoop.GetChannelManager(); mgr != nil {
		failed := mgr.FailedChannels()
		applyDegradedOverlay(channels, failed)
		// Warn for any failed channel whose (normalised) base type has no matching
		// entry in the channels list — these are dead channels that would otherwise
		// be silently invisible to operators. Entry ids may now be per-instance
		// keys ("whatsapp.eu"), so match on the base type extracted from each id.
		if len(failed) > 0 {
			entryBaseTypes := make(map[string]struct{}, len(channels))
			for _, e := range channels {
				bt, _ := config.ParseInstanceKey(e.Id)
				entryBaseTypes[bt] = struct{}{}
			}
			for _, f := range failed {
				id := f.Name
				if id == "whatsapp_native" {
					id = "whatsapp"
				}
				if _, matched := entryBaseTypes[id]; !matched {
					slog.Warn(
						"channels: failed channel has no matching entry in channels list",
						"registry_id", f.Name,
						"channel", f.Channel,
						"error", f.Err.Error(),
					)
				}
			}
		}
	}

	jsonOK(w, channels)
}

// applyDegradedOverlay marks entries in channelList as degraded when the
// supplied failed list contains a matching registry id.  It is a pure function
// extracted from HandleChannels so that it can be unit-tested without a full
// REST stack.
//
// Normalisation rule: "whatsapp_native" maps to the "whatsapp" base type because
// both the bridge and native transports share the WhatsApp channel type.
//
// Matching is by BASE TYPE (config.ParseInstanceKey of the entry id) so that a
// per-instance entry ("whatsapp.eu") is correctly marked when its base type
// ("whatsapp") failed to construct, and every instance of a failing type is
// flagged. A base-type entry ("telegram") parses to itself, so the existing
// base-type overlay tests keep passing unchanged.
func applyDegradedOverlay(channelList []gen.ChannelEntry, failed []channels.ChannelInitError) {
	if len(failed) == 0 {
		return
	}
	// Build a map of normalised registry-id (base type) → error reason.
	degradedMap := make(map[string]string, len(failed))
	for _, f := range failed {
		id := f.Name
		if id == "whatsapp_native" {
			id = "whatsapp"
		}
		degradedMap[id] = f.Err.Error()
	}
	for i := range channelList {
		baseType, _ := config.ParseInstanceKey(channelList[i].Id)
		if reason, ok := degradedMap[baseType]; ok {
			r := reason
			channelList[i].Degraded = boolPtr(true)
			channelList[i].DegradedReason = &r
		}
	}
}

// identityForInstance converts a persisted config.ChannelIdentity into the
// generated ChannelEntry.Identity anonymous-struct pointer, or nil when the
// instance has no identity or a malformed one (a bad persisted identity is
// dropped rather than emitting a schema-invalid entry). Extracted so the
// per-instance list construction stays readable.
func identityForInstance(identity *config.ChannelIdentity) *struct {
	Id   *string                      `json:"id,omitempty"`
	Kind gen.ChannelEntryIdentityKind `json:"kind"`
} {
	if identity == nil {
		return nil
	}
	var entryKind gen.ChannelEntryIdentityKind
	switch strings.ToLower(strings.TrimSpace(identity.Kind)) {
	case "agent":
		entryKind = gen.ChannelEntryIdentityKindAgent
	case "user":
		entryKind = gen.ChannelEntryIdentityKindUser
	default:
		return nil
	}
	ident := &struct { // not-wire-format: pointer to the generated gen.ChannelEntry.Identity anonymous field type — not a parallel wire type
		Id   *string                      `json:"id,omitempty"`
		Kind gen.ChannelEntryIdentityKind `json:"kind"`
	}{
		Kind: entryKind,
	}
	if id := strings.TrimSpace(identity.ID); id != "" {
		idCopy := id
		ident.Id = &idCopy
	}
	return ident
}

// channelWildcardIdx returns the index of the channel-wildcard AgentBinding
// for the given channelID in the bindings slice, or -1 if not found.
// A channel-wildcard binding has Match.Channel equal to channelID (compared
// case-insensitively), Match.AccountID=="*", and no Peer/Guild/Team qualifiers.
func channelWildcardIdx(bindings []config.AgentBinding, channelID string) int {
	for i, b := range bindings {
		if strings.ToLower(b.Match.Channel) != channelID {
			continue
		}
		if b.Match.AccountID != "*" {
			continue
		}
		if b.Match.Peer != nil || b.Match.GuildID != "" || b.Match.TeamID != "" {
			continue
		}
		return i
	}
	return -1
}

// isChannelWildcardRaw reports whether a raw binding match-map represents the
// channel-wildcard entry for channelID. A match is:
//   - match["channel"] equal to channelID (compared case-insensitively)
//   - match["account_id"] == "*"
//   - no "peer", "guild_id", or "team_id" keys present
func isChannelWildcardRaw(matchMap map[string]any, channelID string) bool {
	ch, _ := matchMap["channel"].(string)
	acc, _ := matchMap["account_id"].(string)
	_, hasPeer := matchMap["peer"]
	_, hasGuild := matchMap["guild_id"]
	_, hasTeam := matchMap["team_id"]
	return strings.ToLower(ch) == channelID && acc == "*" && !hasPeer && !hasGuild && !hasTeam
}

// getChannelRouting handles GET /api/v1/channels/{id}/routing.
// ADR-029 FR-029 / MAJ-004: for a BOUND instance (has WorkspaceID + agent Identity)
// returns {workspace_id, default_agent_id} read from cfg.Channels[id]; for an
// UNBOUND instance returns the legacy wildcard binding. The two representations are
// mutually exclusive per instance. For routing endpoints, a well-formed instance id
// that is not in cfg.Channels returns 404 "unknown instance" (FR-024/US-11 AC-2).
func (a *restAPI) getChannelRouting(w http.ResponseWriter, channelID string) {
	cfg := a.agentLoop.GetConfig()

	// Per-instance existence check for routing endpoints (FR-024).
	// A namespaced id like "whatsapp.eu" passes the base-type gate above but may
	// not have an entry in cfg.Channels if it was never configured.
	_, hasSlug := func() (string, bool) {
		_, slug := config.ParseInstanceKey(channelID)
		return slug, slug != ""
	}()
	if hasSlug {
		// Namespaced key: require entry in cfg.Channels.
		if _, exists := cfg.Channels[channelID]; !exists {
			jsonErr(w, http.StatusNotFound, "unknown instance")
			return
		}
	}

	// Check for bound representation first (FR-029). Use the single-source-of-truth
	// predicate so this GET path cannot diverge from the config layer's definition
	// of "bound" (e.g. the inline check previously omitted the TrimSpace on
	// WorkspaceID that IsWorkspaceBound applies).
	if inst, ok := cfg.Channels[channelID]; ok && inst.IsWorkspaceBound() {
		wsID := inst.WorkspaceID
		agentID := inst.Identity.ID
		resp := gen.ChannelRouting{
			WorkspaceId:    &wsID,
			DefaultAgentId: &agentID,
		}
		jsonOK(w, resp)
		return
	}

	// Unbound: fall back to legacy wildcard binding.
	idx := channelWildcardIdx(cfg.Bindings, channelID)
	var resp gen.ChannelRouting
	if idx >= 0 {
		id := cfg.Bindings[idx].AgentID
		resp.DefaultAgentId = &id
	}
	jsonOK(w, resp)
}

// setChannelRouting handles PUT /api/v1/channels/{id}/routing.
// ADR-029 FR-029 / MAJ-004 REWRITE:
//   - When workspace_id is present (bound flow): writes cfg.Channels[id].WorkspaceID +
//     cfg.Channels[id].Identity and REMOVES any stale channel-wildcard binding for id.
//     Rejection set: empty default_agent_id → 422; agent ∉ workspace.CoreTeam → 422;
//     worker agent → 422 (MIN-002, standardized from 400); unknown/archived workspace → 404.
//   - When workspace_id is absent (unbound/legacy flow): keeps existing wildcard-binding
//     upsert behavior unchanged (FR-005/FR-008 for the worker check still apply).
//   - Emits a routing-change audit event in both flows (FR-030 / STRIDE repudiation).
func (a *restAPI) setChannelRouting(w http.ResponseWriter, r *http.Request, channelID string) {
	var req gen.SetChannelRoutingJSONRequestBody
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "ChannelRouting", &req, validateEnabled) {
		return
	}

	// Per-instance existence check for routing endpoints (FR-024).
	cfg := a.agentLoop.GetConfig()
	_, hasSlug := func() (string, bool) {
		_, slug := config.ParseInstanceKey(channelID)
		return slug, slug != ""
	}()
	if hasSlug {
		if _, exists := cfg.Channels[channelID]; !exists {
			jsonErr(w, http.StatusNotFound, "unknown instance")
			return
		}
	}

	// Determine flow: bound (workspace_id present) or legacy (unbound).
	workspaceID := ""
	if req.WorkspaceId != nil {
		workspaceID = strings.TrimSpace(*req.WorkspaceId)
	}

	if workspaceID != "" {
		// S-5: validate workspace_id grammar before building any filesystem path.
		// A workspace_id from the request body is only TrimSpace'd; without a
		// positive-allowlist check, a value such as "../../../etc/passwd" would
		// reach filepath.Join(home, "workspaces", id+".json") and probe outside
		// the data root. validateEntityID is the same traversal guard used by
		// handleWorkspaceGet and all other per-entity FS handlers in this file.
		if err := validateEntityID(workspaceID); err != nil {
			jsonErr(w, http.StatusBadRequest, "malformed workspace_id")
			return
		}

		// ── Bound flow ──────────────────────────────────────────────────────────
		// FR-005: empty default_agent_id → 422.
		agentID := ""
		if req.DefaultAgentId != nil {
			agentID = strings.TrimSpace(*req.DefaultAgentId)
		}
		if agentID == "" {
			jsonErr(w, http.StatusUnprocessableEntity, "default_agent_id is required for a bound instance")
			return
		}

		// FR-007: unknown or archived workspace → 404.
		ws, err := readWorkspaceFile(a.homePath, workspaceID)
		if err != nil {
			if errors.Is(err, errWorkspaceNotFound) {
				jsonErr(w, http.StatusNotFound, "workspace not found")
				return
			}
			slog.Error("rest: setChannelRouting: read workspace", "workspace_id", workspaceID, "error", err)
			jsonErr(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if ws.Status == string(gen.WorkspaceStatusArchived) {
			jsonErr(w, http.StatusNotFound, "workspace not found")
			return
		}

		// FR-008: worker agent → 422 (MIN-002: was 400, standardized to 422).
		var foundAgent *config.AgentConfig
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == agentID {
				ac := cfg.Agents.List[i]
				foundAgent = &ac
				break
			}
		}
		if foundAgent == nil {
			jsonErr(w, http.StatusUnprocessableEntity, fmt.Sprintf("agent %q not found", agentID))
			return
		}
		// ADR-049 D3: System Agents are not valid routing-binding targets (400).
		// Checked before the worker 422 because a System Agent is also a
		// non-chat-target — the spec requires the system-specific 400.
		if foundAgent.IsSystem() {
			jsonErr(w, http.StatusBadRequest,
				"system agents are not chat targets and cannot be a channel's default agent")
			return
		}
		if foundAgent.IsWorker() {
			jsonErr(
				w,
				http.StatusUnprocessableEntity,
				"workers are not chat targets and cannot be a channel's default agent",
			)
			return
		}

		// FR-006: agent must be in the workspace's CoreTeam.
		inTeam := false
		for _, memberID := range ws.CoreTeam {
			if memberID == agentID {
				inTeam = true
				break
			}
		}
		if !inTeam {
			jsonErr(w, http.StatusUnprocessableEntity,
				fmt.Sprintf("agent %q is not a member of workspace %q", agentID, workspaceID))
			return
		}

		// All validations passed — persist the bound representation.
		if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
			// Set cfg.Channels[id].workspace_id and identity.
			channels, _ := m["channels"].(map[string]any)
			if channels == nil {
				channels = map[string]any{}
				m["channels"] = channels
			}
			ch, _ := channels[channelID].(map[string]any)
			if ch == nil {
				ch = map[string]any{}
			}
			ch["workspace_id"] = workspaceID
			ch["identity"] = map[string]any{
				"kind": "agent",
				"id":   agentID,
			}
			// Persist the type field if not already set (normalizeChannelMap backfills, but
			// be explicit so the on-disk entry is self-describing).
			if _, ok := ch["type"]; !ok {
				baseType, _ := config.ParseInstanceKey(channelID)
				ch["type"] = baseType
			}
			channels[channelID] = ch
			m["channels"] = channels

			// FR-029: remove any stale channel-wildcard binding for this instance.
			rawBindings, _ := m["bindings"].([]any)
			filtered := make([]any, 0, len(rawBindings))
			for _, entry := range rawBindings {
				bMap, ok := entry.(map[string]any)
				if !ok {
					filtered = append(filtered, entry)
					continue
				}
				matchMap, _ := bMap["match"].(map[string]any)
				if matchMap == nil {
					filtered = append(filtered, entry)
					continue
				}
				if isChannelWildcardRaw(matchMap, channelID) {
					continue // drop the stale wildcard
				}
				filtered = append(filtered, entry)
			}
			if len(filtered) == 0 {
				delete(m, "bindings")
			} else {
				m["bindings"] = filtered
			}
			return nil
		}); err != nil {
			slog.Error("rest: save config for channel routing (bound)", "channel_id", channelID, "error", err)
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
			return
		}

		// FR-030: emit routing-change audit event (STRIDE repudiation).
		if a.auditor != nil {
			if err := a.auditor.Log(&audit.Entry{
				Event:    audit.EventChannelRoutingChanged,
				Decision: audit.DecisionAllow,
				Details: map[string]any{
					"channel_id":   channelID,
					"workspace_id": workspaceID,
					"agent_id":     agentID,
					"flow":         "bound",
				},
			}); err != nil {
				slog.Warn("audit write failed", "event", audit.EventChannelRoutingChanged,
					"channel_id", channelID, "flow", "bound", "error", err)
			}
		}

		// Re-stamp workspace_id onto every session that already existed for
		// this channel BEFORE the bind — resolveOrCreateChannelSession
		// (pkg/agent/loop.go) only stamps workspace_id at session CREATION
		// time and explicitly does not patch already-existing sessions.
		// Without this, an existing conversation keeps routing delegation
		// trust, memory rooms, and task-board placement against the stale
		// (or empty, silently-default-substituted) workspace forever, even
		// though new messages on the SAME conversation resolve the new
		// agent via routing. See restampChannelSessionsWorkspace's doc
		// comment for the full rationale.
		if store := a.agentLoop.GetSessionStore(); store != nil {
			if n, rsErr := restampChannelSessionsWorkspace(store, channelID, workspaceID); rsErr != nil {
				slog.Warn("rest: setChannelRouting: restamp existing sessions",
					"instance_id", channelID, "workspace_id", workspaceID, "error", rsErr)
			} else if n > 0 {
				slog.Info("rest: setChannelRouting: restamped existing sessions to bound workspace",
					"instance_id", channelID, "workspace_id", workspaceID, "count", n)
			}
		}

		// Return the bound representation.
		wsIDCopy := workspaceID
		agentIDCopy := agentID
		jsonOK(w, gen.ChannelRouting{
			WorkspaceId:    &wsIDCopy,
			DefaultAgentId: &agentIDCopy,
		})
		return
	}

	// ── Unbound / legacy flow ────────────────────────────────────────────────
	// Validate the agent ID when non-empty (FR-005/FR-008 still apply).
	if req.DefaultAgentId != nil && *req.DefaultAgentId != "" {
		agentID := *req.DefaultAgentId
		var found *config.AgentConfig
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == agentID {
				ac := cfg.Agents.List[i]
				found = &ac
				break
			}
		}
		if found == nil {
			jsonErr(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", agentID))
			return
		}
		// ADR-049 D3: System Agents are not valid routing-binding targets (400).
		if found.IsSystem() {
			jsonErr(w, http.StatusBadRequest,
				"system agents are not chat targets and cannot be a channel's default agent")
			return
		}
		// MIN-002: standardize to 422 (was 400).
		if found.IsWorker() {
			jsonErr(
				w,
				http.StatusUnprocessableEntity,
				"workers are not chat targets and cannot be a channel's default agent",
			)
			return
		}
	}

	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		// FR-029 (bound→unbound): clear workspace_id and identity from the
		// channel entry so that a previously-bound instance is fully unbound.
		// Without this, cfg.Channels[id].WorkspaceID persists across the PUT
		// and RepairStaleChannelWildcardBindings drops the new wildcard on
		// next config load — leaving the bound representation intact and
		// silently discarding the caller's unbind intent.
		if channels, _ := m["channels"].(map[string]any); channels != nil {
			if ch, _ := channels[channelID].(map[string]any); ch != nil {
				delete(ch, "workspace_id")
				delete(ch, "identity")
				channels[channelID] = ch
				m["channels"] = channels
			}
		}

		// Read the bindings array from the raw JSON map.
		rawBindings, _ := m["bindings"].([]any)

		if req.DefaultAgentId == nil || *req.DefaultAgentId == "" {
			// Remove the channel-wildcard binding for this channel if it exists.
			filtered := make([]any, 0, len(rawBindings))
			for _, entry := range rawBindings {
				bMap, ok := entry.(map[string]any)
				if !ok {
					filtered = append(filtered, entry)
					continue
				}
				matchMap, _ := bMap["match"].(map[string]any)
				if matchMap == nil {
					filtered = append(filtered, entry)
					continue
				}
				if isChannelWildcardRaw(matchMap, channelID) {
					continue
				}
				filtered = append(filtered, entry)
			}
			if len(filtered) == 0 {
				delete(m, "bindings")
			} else {
				m["bindings"] = filtered
			}
			return nil
		}

		// Upsert: replace or append the channel-wildcard binding.
		newBinding := map[string]any{
			"agent_id": *req.DefaultAgentId,
			"match": map[string]any{
				"channel":    channelID,
				"account_id": "*",
			},
		}
		replaced := false
		for i, entry := range rawBindings {
			bMap, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			matchMap, _ := bMap["match"].(map[string]any)
			if matchMap == nil {
				continue
			}
			if isChannelWildcardRaw(matchMap, channelID) {
				rawBindings[i] = newBinding
				replaced = true
				break
			}
		}
		if !replaced {
			rawBindings = append(rawBindings, newBinding)
		}
		m["bindings"] = rawBindings
		return nil
	}); err != nil {
		slog.Error("rest: save config for channel routing", "channel_id", channelID, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}

	// FR-030: emit routing-change audit event.
	agentIDForAudit := ""
	if req.DefaultAgentId != nil {
		agentIDForAudit = *req.DefaultAgentId
	}
	if a.auditor != nil {
		if err := a.auditor.Log(&audit.Entry{
			Event:    audit.EventChannelRoutingChanged,
			Decision: audit.DecisionAllow,
			Details: map[string]any{
				"channel_id": channelID,
				"agent_id":   agentIDForAudit,
				"flow":       "unbound",
			},
		}); err != nil {
			slog.Warn("audit write failed", "event", audit.EventChannelRoutingChanged,
				"channel_id", channelID, "flow", "unbound", "error", err)
		}
	}

	// Symmetric with the bound flow above: unbinding a previously-bound
	// channel clears cfg.Channels[id].WorkspaceID, so every existing
	// session that was stamped from that binding must be cleared too —
	// otherwise it stays pinned to a workspace the channel is no longer
	// routed through, forever. A channel that was never bound has no
	// sessions carrying a channel-derived workspace stamp, so this is a
	// harmless no-op for it (restampChannelSessionsWorkspace skips writes
	// where WorkspaceID already matches the target).
	if store := a.agentLoop.GetSessionStore(); store != nil {
		if n, rsErr := restampChannelSessionsWorkspace(store, channelID, ""); rsErr != nil {
			slog.Warn("rest: setChannelRouting: clear existing sessions on unbind",
				"instance_id", channelID, "error", rsErr)
		} else if n > 0 {
			slog.Info("rest: setChannelRouting: cleared stale workspace on existing sessions after unbind",
				"instance_id", channelID, "count", n)
		}
	}

	// Return the resulting routing state.
	liveCfg := a.agentLoop.GetConfig()
	idx := channelWildcardIdx(liveCfg.Bindings, channelID)
	var resp gen.ChannelRouting
	if idx >= 0 {
		id := liveCfg.Bindings[idx].AgentID
		resp.DefaultAgentId = &id
	}
	jsonOK(w, resp)
}

// restampChannelSessionsWorkspace re-stamps WorkspaceID on every existing
// session belonging to the given channel base type (e.g. "whatsapp" for
// both the bare "whatsapp" instance key and namespaced ones like
// "whatsapp.eu"). SessionMeta.Channel persists only the bare channel TYPE,
// never the instance slug (see pkg/agent/loop.go::resolveOrCreateChannelSession's
// NewChannelSession(channel, chatID, ...) call, which is handed msg.Channel —
// the bare type — not the instance ID) — so this is the finest granularity
// the session data model supports today. On an install with two instances
// of the same channel type (e.g. "whatsapp.eu" and "whatsapp.us"), binding
// one instance's workspace will also restamp the other instance's sessions,
// because they are indistinguishable at the session-meta level; that is a
// pre-existing limitation of SessionMeta (no InstanceID field), not
// something introduced here, and fixing it would require a session schema
// change outside this handler's scope.
//
// Closes the gap where setChannelRouting updated cfg.Channels[id].WorkspaceID
// but left every session created BEFORE the change pinned to its stale
// workspace_id forever: resolveOrCreateChannelSession only stamps
// workspace_id at session CREATION time and its own doc comment concedes
// "Already-existing sessions are NOT patched." A stale or empty workspace_id
// is not a safe no-op — resolveEffectiveWorkspaceID (pkg/agent/loop.go)
// silently substitutes the operator's default workspace, so a stale stamp
// routes delegation trust (ADR-037), memory rooms, and task-board placement
// against the WRONG workspace's graph.
//
// newWorkspaceID may be "" to clear the stamp (the unbind case). Every
// matching session's WorkspaceID is unconditionally overwritten with
// newWorkspaceID (skipping sessions that already match, to avoid a
// no-op SetMeta write) — this must be able to MOVE a session off a stale
// workspace onto a new one on re-bind, so it deliberately does not use the
// non-clobber-once-set policy pkg/gateway/schedules.go's
// stampScheduledSessionWorkspace uses for a different (single-anchor)
// purpose.
//
// Only sessions whose Channel field equals baseType are touched — webchat
// and other-channel sessions are never affected. A per-session SetMeta
// failure is logged and does not abort the rest of the batch; the returned
// count is the number of sessions successfully updated, and the returned
// error (if any) is the first SetMeta failure encountered, for the caller's
// own WARN log.
// restampChannelSessionsWorkspace moves the sessions of ONE channel instance
// onto a new workspace.
//
// It matches on the INSTANCE key, never the bare channel type. An install can
// hold many instances of one platform — a hundred WhatsApp numbers, each bound
// to its own (workspace, agent) pair under ADR-029 — and every one of their
// sessions records Channel=="whatsapp". Matching on type would relabel all
// hundred when an operator re-binds one of them: the very bug this restamp
// exists to fix, inflicted on the other ninety-nine.
//
// A session with NO instance key is left alone. Those predate the field, so
// "unknown" is the honest reading; guessing from the type is exactly the
// conflation above. Their workspace still resolves correctly at turn time via
// processMessage's channel-instance fallback whenever it is empty.
func restampChannelSessionsWorkspace(store *session.UnifiedStore, instanceID, newWorkspaceID string) (int, error) {
	if store == nil || instanceID == "" {
		return 0, nil
	}
	sessions, err := store.ListSessionsFiltered(func(m *session.UnifiedMeta) bool {
		return m != nil && m.InstanceID != "" && m.InstanceID == instanceID
	})
	if err != nil {
		return 0, fmt.Errorf("list sessions for channel instance %q: %w", instanceID, err)
	}
	updated := 0
	var firstErr error
	for _, m := range sessions {
		if m.WorkspaceID == newWorkspaceID {
			continue // already correct — skip the redundant write
		}
		wsCopy := newWorkspaceID
		if setErr := store.SetMeta(m.ID, session.MetaPatch{WorkspaceID: &wsCopy}); setErr != nil {
			slog.Warn("rest: restampChannelSessionsWorkspace: SetMeta failed",
				"session_id", m.ID, "instance_id", instanceID, "workspace_id", newWorkspaceID, "error", setErr)
			if firstErr == nil {
				firstErr = fmt.Errorf("session %q: %w", m.ID, setErr)
			}
			continue
		}
		updated++
	}
	return updated, firstErr
}

// createChannelInstance handles POST /api/v1/channels (ADR-029 FR-024/FR-025,
// US-6/US-10). Creates a new channel instance with key "<type>.<slug>".
// The instance starts disabled (Enabled=false) and its config entry is written
// via safeUpdateConfigJSON. Rejects on: unknown type (400), malformed slug (400),
// duplicate key (409).
func (a *restAPI) createChannelInstance(w http.ResponseWriter, r *http.Request) {
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	var req gen.CreateChannelInstanceJSONRequestBody
	if !decodeAndValidate(w, r, "ChannelCreateRequest", &req, validateEnabled) {
		return
	}

	chType := strings.ToLower(strings.TrimSpace(req.Type))
	slug := strings.TrimSpace(req.Slug)

	// Validate the base channel type.
	if !validChannelIDs[chType] {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("unknown channel type %q", chType))
		return
	}

	// Build the instance key and validate its full grammar (FR-017).
	instanceKey := chType + "." + slug
	if err := config.ValidateInstanceKey(instanceKey); err != nil {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("malformed slug: %v", err))
		return
	}

	// Reject if the instance key already exists (409).
	cfg := a.agentLoop.GetConfig()
	if _, exists := cfg.Channels[instanceKey]; exists {
		jsonErr(w, http.StatusConflict, fmt.Sprintf("channel instance %q already exists", instanceKey))
		return
	}

	// Persist the new entry (disabled by default).
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		channels, _ := m["channels"].(map[string]any)
		if channels == nil {
			channels = map[string]any{}
			m["channels"] = channels
		}
		// Double-check for races — another concurrent request may have inserted it.
		if _, exists := channels[instanceKey]; exists {
			return fmt.Errorf("channel instance %q already exists", instanceKey)
		}
		channels[instanceKey] = map[string]any{
			"type":    chType,
			"enabled": false,
		}
		m["channels"] = channels
		return nil
	}); err != nil {
		// surfaced as 409 when the race guard fires, 500 otherwise
		if strings.Contains(err.Error(), "already exists") {
			jsonErr(w, http.StatusConflict, fmt.Sprintf("channel instance %q already exists", instanceKey))
			return
		}
		slog.Error("rest: create channel instance: save config", "instance", instanceKey, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}

	slog.Info("rest: created channel instance", "id", instanceKey, "type", chType)
	writeJSON(w, http.StatusCreated, gen.ChannelCreateResponse{
		Id:      instanceKey,
		Type:    chType,
		Enabled: false,
	})
}

// channelSensitiveFields maps channel TYPE to their secret/credential field names.
// These are redacted in GET responses (replaced with "[configured]" if set).
// Keyed by base channel TYPE (not instance ID) because field sensitivity is
// type-level knowledge shared across all instances of that type
// (SEC-23 type-vs-instance boundary). Lookups use the base type extracted from
// the instance key via config.ParseInstanceKey (ADR-029 Gate 0).
var channelSensitiveFields = map[string][]string{
	"telegram":    {"token"},
	"discord":     {"token"},
	"slack":       {"bot_token", "app_token"},
	"feishu":      {"app_secret", "encrypt_key", "verification_token"},
	"matrix":      {"access_token", "crypto_passphrase"},
	"line":        {"channel_secret", "channel_access_token"},
	"dingtalk":    {"client_secret"},
	"qq":          {"app_secret"},
	"wecom":       {"secret"},
	"irc":         {"password", "nickserv_password", "sasl_password"},
	"weixin":      {"token"},
	"whatsapp":    {},
	"google-chat": {"webhook_url", "service_account_json"},
	// "email" is deliberately absent — see validChannelIDs.
}

// deleteChannelInstance handles DELETE /api/v1/channels/{id} (ADR-029 FR-025,
// US-10/US-11). Removes the channel instance's config entry, credential refs,
// any stale channel-wildcard binding for the id, and the per-instance state
// directory (e.g. WhatsApp SQLite store).
//
// Teardown order (config removal first so a partial failure never leaves a
// config pointing at a missing store):
//  1. Collect credential ref names from the live config BEFORE writing.
//  2. Remove config entry + stale wildcard binding via safeUpdateConfigJSON.
//  3. Delete credential store entries (best-effort; orphaned blobs not fatal).
//  4. Remove per-instance state directory (best-effort; stale dir not fatal).
func (a *restAPI) deleteChannelInstance(w http.ResponseWriter, channelID string) {
	// Grammar already validated by HandleChannels before we reach here.
	// Existence check: require the instance to be in cfg.Channels.
	cfg := a.agentLoop.GetConfig()
	inst, exists := cfg.Channels[channelID]
	if !exists {
		jsonErr(w, http.StatusNotFound, "unknown instance")
		return
	}

	// Snapshot credential ref names to delete AFTER the config write.
	chBaseType, _ := config.ParseInstanceKey(channelID)
	var credRefs []string
	for _, field := range channelSensitiveFields[chBaseType] {
		credRefs = append(credRefs, channelCredKey(channelID, field))
	}

	// Snapshot the WhatsApp store path (if applicable) before the config write.
	// We derive the default path the same way init.go does: AgentHomeBasePath()/whatsapp/<instanceID>.
	// If SessionStorePath is explicitly set, use that; otherwise use the derived default.
	var stateDir string
	if chBaseType == "whatsapp" {
		storePath := inst.SessionStorePath
		if storePath == "" {
			storePath = filepath.Join(cfg.AgentHomeBasePath(), "whatsapp", channelID)
		}
		stateDir = storePath
	}

	// Step 2: remove config entry and stale wildcard binding atomically.
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		channels, _ := m["channels"].(map[string]any)
		if channels != nil {
			delete(channels, channelID)
			if len(channels) == 0 {
				delete(m, "channels")
			} else {
				m["channels"] = channels
			}
		}

		// Remove any stale channel-wildcard binding for this instance (FR-029).
		rawBindings, _ := m["bindings"].([]any)
		if len(rawBindings) > 0 {
			filtered := make([]any, 0, len(rawBindings))
			for _, entry := range rawBindings {
				bMap, ok := entry.(map[string]any)
				if !ok {
					filtered = append(filtered, entry)
					continue
				}
				matchMap, _ := bMap["match"].(map[string]any)
				if matchMap == nil {
					filtered = append(filtered, entry)
					continue
				}
				if isChannelWildcardRaw(matchMap, channelID) {
					continue // drop stale wildcard
				}
				filtered = append(filtered, entry)
			}
			if len(filtered) == 0 {
				delete(m, "bindings")
			} else {
				m["bindings"] = filtered
			}
		}
		return nil
	}); err != nil {
		slog.Error("rest: delete channel instance: save config", "id", channelID, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}

	// Step 2b (FINAL-REVIEW Medium-High — leaked running channel): reload the
	// manager NOW, after the config entry is removed but BEFORE the credential and
	// state-dir teardown. ChannelManager.Reload stops any channel dropped from the
	// config, so a deleted-while-ENABLED instance's goroutine/connection is torn
	// down instead of continuing to process inbound with credentials we are about
	// to delete. Doing it before the cred/state teardown means Stop() still has the
	// live credentials and store files it may need to close cleanly. In the
	// unit-test environment TriggerReload is a no-op (ErrReloadNotConfigured), so
	// this is safe to call unconditionally.
	if a.agentLoop != nil {
		if confirmed, err := a.triggerReloadAndWaitOutcome(); err != nil {
			// A genuine reload failure means the (now config-less) channel may still
			// be running. Log it but continue the teardown — we must still remove the
			// credentials/state the operator asked to delete, and the config entry is
			// already gone so a subsequent reload will converge.
			slog.Error("rest: delete channel instance: reload after config removal failed",
				"id", channelID, "error", err)
		} else if !confirmed {
			slog.Warn("rest: delete channel instance: reload did not confirm within the poll window; "+
				"the now config-less channel instance may still be running", "id", channelID)
		}
	}

	// Step 3: delete credential store entries. An orphaned encrypted secret left
	// behind against the operator's explicit "delete this instance" intent is a
	// security-relevant outcome (the credential remains recoverable at rest), so a
	// real store fault is escalated to Error (with a correlatable errorId) and
	// surfaced in the audit event via cleanup_failed=true. removeStoredCredential
	// returns nil for not-found keys, so only a genuine I/O/store fault trips this.
	cleanupFailed := false
	for _, refName := range credRefs {
		if err := a.removeStoredCredential(refName); err != nil {
			cleanupFailed = true
			errorID := uuid.NewString()
			slog.Error("rest: delete channel instance: remove credential failed — orphaned encrypted secret",
				"id", channelID, "ref", refName, "error", err, "errorId", errorID)
		}
	}

	// Step 4: remove per-instance state directory (best-effort).
	if stateDir != "" {
		if err := os.RemoveAll(stateDir); err != nil {
			slog.Warn("rest: delete channel instance: remove state dir",
				"id", channelID, "dir", stateDir, "error", err)
		}
	}

	// FINAL-REVIEW MEDIUM: emit an audit event for the destructive delete (ADR-029
	// FR-025 / STRIDE repudiation). A channel-instance delete revokes stored
	// credentials and removes state; it MUST leave an audit trail even on the happy
	// path. cleanup_failed=true flags an orphaned encrypted blob for the operator.
	if a.auditor != nil {
		decision := audit.DecisionAllow
		if cleanupFailed {
			decision = audit.DecisionError
		}
		if err := a.auditor.Log(&audit.Entry{
			Event:    audit.EventChannelInstanceDeleted,
			Decision: decision,
			Details: map[string]any{
				"channel_id":     channelID,
				"type":           chBaseType,
				"cleanup_failed": cleanupFailed,
			},
		}); err != nil {
			slog.Warn("audit write failed", "event", audit.EventChannelInstanceDeleted,
				"channel_id", channelID, "error", err)
		}
	}

	slog.Info("rest: deleted channel instance", "id", channelID, "type", chBaseType, "cleanup_failed", cleanupFailed)
	w.WriteHeader(http.StatusNoContent)
}

func (a *restAPI) setChannelEnabled(w http.ResponseWriter, channelID string, enabled bool) {
	baseType, _ := config.ParseInstanceKey(channelID)
	if !validChannelIDs[baseType] {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("channel %q not found", channelID))
		return
	}
	if enabled {
		// Stage 1 of the channel-Test redesign: enabling a channel with an
		// incomplete config must be rejected, not silently persisted — an
		// enabled-but-incomplete channel would fail to construct on the next
		// reload/boot. Disabling never validates (turning a channel off is
		// always safe). readChannelConfigRaw returns {} for a channel with no
		// persisted section yet.
		stored, err := a.readChannelConfigRaw(channelID)
		if err != nil {
			slog.Error("rest: read config for channel enable validation", "channel", channelID, "error", err)
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not read config: %v", err))
			return
		}
		if msg := validateChannelConfigComplete(baseType, stored, nil, nil); msg != "" {
			jsonErr(w, http.StatusBadRequest, msg)
			return
		}
	}
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		channels, _ := m["channels"].(map[string]any)
		if channels == nil {
			channels = map[string]any{}
			m["channels"] = channels
		}
		ch, _ := channels[channelID].(map[string]any)
		if ch == nil {
			ch = map[string]any{}
			channels[channelID] = ch
		}
		// Persist the type discriminator. For bare type keys the base type equals
		// the key; for namespaced instance keys ("whatsapp.eu") the type is the
		// pre-dot segment (ADR-029 Gate 0 / FR-017).
		ch["type"] = baseType
		ch["enabled"] = enabled
		return nil
	}); err != nil {
		slog.Error("rest: set channel enabled", "channel", channelID, "enabled", enabled, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}
	// #358: persisting channels.<id>.enabled via safeUpdateConfigJSON only swaps the
	// in-memory config pointer (refreshConfigAndRewireServices → SwapConfig); it does
	// NOT start or stop the channel. Reload so ChannelManager.Reload applies the diff —
	// starting a newly-enabled channel (e.g. whatsapp_native, which then emits its
	// pairing QR over the whatsapp_pairing WS frame) or stopping a disabled one. The
	// Reload path is crash-safe and name-correct as of #313. triggerReloadAndWait treats an
	// unwired reload pipeline (the unit-test environment) as a no-op and only returns an
	// error on a genuine reload failure, which we surface rather than reporting a false
	// success (the flag persisted but the channel did not start).
	if a.agentLoop != nil {
		if confirmed, err := a.triggerReloadAndWaitOutcome(); err != nil {
			verb := "start"
			if !enabled {
				verb = "stop"
			}
			slog.Error("rest: channel reload after enable toggle failed",
				"channel", channelID, "enabled", enabled, "error", err)
			jsonErr(w, http.StatusInternalServerError,
				fmt.Sprintf("channel %s saved but failed to %s: %v", channelID, verb, err))
			return
		} else if !confirmed {
			verb := "start"
			if !enabled {
				verb = "stop"
			}
			slog.Warn("rest: channel reload after enable toggle did not confirm within the poll window; "+
				"channel may not yet have finished attempting to "+verb,
				"channel", channelID, "enabled", enabled)
		}
	}
	jsonOK(w, gen.ChannelEnabledResponse{Id: channelID, Enabled: enabled})
}

// redactChannelConfig returns a copy of cfg with sensitive fields replaced by a
// "[configured]" marker (never the real secret). Post-#289 the secret lives in
// the credential store and config holds only <field>_ref, so the marker is
// driven by the presence of the ref — this preserves the UI's
// secret-already-set indicator (the inline field is no longer present).
func redactChannelConfig(channelID string, cfg map[string]any) map[string]any {
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		out[k] = v
	}
	baseType, _ := config.ParseInstanceKey(channelID)
	for _, field := range channelSensitiveFields[baseType] {
		// A set <field>_ref means a credential is stored → mark configured.
		if ref, _ := out[field+"_ref"].(string); strings.TrimSpace(ref) != "" {
			out[field] = "[configured]"
			continue
		}
		// Legacy/pre-scrub inline value (should be transient): mask, never echo.
		if v, ok := out[field]; ok {
			if s, _ := v.(string); s != "" {
				out[field] = "[configured]"
			} else {
				out[field] = ""
			}
		}
	}
	return out
}

// getChannelConfig handles GET /api/v1/channels/{id}.
// Returns the channel's config with credential fields redacted.
func (a *restAPI) getChannelConfig(w http.ResponseWriter, channelID string) {
	chCfg, err := a.readChannelConfigRaw(channelID)
	if err != nil {
		slog.Error("rest: read config for channel get", "channel", channelID, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not read config: %v", err))
		return
	}
	jsonOK(w, redactChannelConfig(channelID, chCfg))
}

// channelRequiredFields maps channel base TYPE to fields that must be non-empty
// for the channel to work. Keyed by base TYPE for the same reason as
// channelSensitiveFields — required fields are type-level knowledge, not
// instance-specific. Lookups use config.ParseInstanceKey (ADR-029 Gate 0).
var channelRequiredFields = map[string][]string{
	"telegram":    {"token"},
	"discord":     {"token"},
	"slack":       {"bot_token", "app_token"},
	"feishu":      {"app_id", "app_secret"},
	"matrix":      {"homeserver", "user_id", "access_token"},
	"line":        {"channel_secret", "channel_access_token"},
	"dingtalk":    {"client_id", "client_secret"},
	"qq":          {"app_id", "app_secret"},
	"wecom":       {"bot_id", "secret"},
	"irc":         {"server", "nick"},
	"weixin":      {"token"},
	"whatsapp":    {},
	"google-chat": {},
	// "email" is deliberately absent — see validChannelIDs.
}

// validateChannelConfigComplete reports whether the channel config that would
// result from persisting updates — plus prospectiveRefs, the <field>_ref
// values this request's sensitive fields WOULD resolve to once committed — on
// top of existing (the channel's currently-persisted raw config) satisfies
// channelRequiredFields[baseType]. Returns "" when complete, otherwise a
// "missing required fields: ..." message in the same format testChannel uses
// below (the SPA's mapErrorsToHumanLabels parses it).
//
// Stage 1 of the channel-Test redesign: the backend must prevent persisting
// an incomplete channel config, so callers run this BEFORE any
// credential-store write or config write — a rejection must be a true no-op.
// It deliberately checks <field>_ref PRESENCE, not credentialRefResolves — a
// save must not depend on an unlocked credential store; that liveness concern
// belongs to Test/activation (testChannel), not to "did the operator fill in
// the form".
func validateChannelConfigComplete(
	baseType string,
	existing, updates map[string]any,
	prospectiveRefs map[string]string,
) string {
	merged := make(map[string]any, len(existing)+len(updates)+len(prospectiveRefs))
	for k, v := range existing {
		merged[k] = v
	}
	for k, v := range updates {
		merged[k] = v
	}
	for field, ref := range prospectiveRefs {
		merged[field+"_ref"] = ref
	}
	nonEmpty := func(key string) bool {
		s, _ := merged[key].(string)
		return strings.TrimSpace(s) != ""
	}

	// Google Chat's required config is either/or (mirrors testChannel's
	// special-case below): webhook mode needs webhook_url, bot mode needs
	// service_account_json or service_account_file. channelRequiredFields is a
	// flat AND-list and cannot express that.
	if baseType == "google-chat" {
		if nonEmpty("webhook_url_ref") || nonEmpty("service_account_json_ref") || nonEmpty("service_account_file") {
			return ""
		}
		return "missing required fields: configure webhook_url (webhook mode) or service_account_json / service_account_file (bot mode)"
	}

	sensitive := make(map[string]bool, len(channelSensitiveFields[baseType]))
	for _, f := range channelSensitiveFields[baseType] {
		sensitive[f] = true
	}
	var missing []string
	for _, field := range channelRequiredFields[baseType] {
		if sensitive[field] {
			// A secret required field is satisfied by its <field>_ref resolving
			// to a non-empty value — the secret never lives in config.json.
			if !nonEmpty(field + "_ref") {
				missing = append(missing, field)
			}
			continue
		}
		if !nonEmpty(field) {
			missing = append(missing, field)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return fmt.Sprintf("missing required fields: %s", strings.Join(missing, ", "))
}

// channelFilesystemPathFields is the set of ChannelInstanceConfig JSON keys that
// hold a filesystem path. They must never be settable through PUT
// /channels/{id}/configure: deleteChannelInstance does os.RemoveAll on the
// derived (or persisted) SessionStorePath, so an attacker-persisted path turns a
// later delete into arbitrary-directory deletion. These are deployment/operator
// concerns, not UI config — configureChannel strips them so the derived default
// is always used. Sourced from a grep of config.ChannelInstanceConfig for
// path-typed string fields (session_store_path, crypto_database_path,
// service_account_file). If a new path-typed field is added to that struct, add
// it here too.
var channelFilesystemPathFields = []string{
	"session_store_path",   // WhatsApp
	"crypto_database_path", // Matrix
	"service_account_file", // Google Chat
}

// configureChannel handles PUT /api/v1/channels/{id}/configure.
// Merges the request body fields into the channel's config section (does not overwrite absent fields).
// Returns the updated channel config with credential fields redacted.
func (a *restAPI) configureChannel(w http.ResponseWriter, r *http.Request, channelID string) {
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	var updates map[string]any
	if !decodeAndValidate(w, r, "ChannelConfigureRequest", &updates, validateEnabled) {
		return
	}
	// Remove reserved fields that must not be set here.
	delete(updates, "enabled")
	// instance_id is a URL/addressing hint in the request body; the {id} path
	// segment is the authoritative instance key in v0.1 (cap-1/type). Never
	// persist it as a config field (it is not part of ChannelInstanceConfig).
	delete(updates, "instance_id")

	// SECURITY (FINAL-REVIEW HIGH — membership-bypass): the request body schema is
	// additionalProperties:true and this handler blind-merges every remaining field
	// into the persisted ChannelInstanceConfig below. Two classes of field must
	// NEVER be settable through /configure or the merge becomes an authorization
	// and filesystem-integrity hole:
	//
	//   1. Workspace binding (workspace_id + identity). Persisting these here binds
	//      the instance to an arbitrary agent WITHOUT the FR-006 CoreTeam-membership
	//      check that lives only in setChannelRouting. An attacker could set
	//      workspace_id + identity{kind:agent,id:<non-member>} to route a
	//      workspace's inbound traffic at an agent that is not on its team
	//      (IsWorkspaceBound() would then be true and ResolveRoute would honor it).
	//      Binding MUST go through PUT /channels/{id}/routing, which validates the
	//      workspace, the agent, worker-ness, and CoreTeam membership. Reject
	//      (not silently strip) so a client using the wrong endpoint gets told.
	//
	//   2. Filesystem-path fields (session_store_path, crypto_database_path,
	//      service_account_file). deleteChannelInstance calls os.RemoveAll on the
	//      instance's SessionStorePath; a caller who persists an attacker-chosen
	//      path here turns a later delete into arbitrary-directory deletion. These
	//      paths are operator/deployment concerns, not UI-settable config — strip
	//      them so the derived-default path is always used.
	if _, present := updates["workspace_id"]; present {
		jsonErr(w, http.StatusBadRequest,
			"workspace_id cannot be set via configure; use PUT /api/v1/channels/{id}/routing")
		return
	}
	if _, present := updates["identity"]; present {
		jsonErr(w, http.StatusBadRequest,
			"identity cannot be set via configure; use PUT /api/v1/channels/{id}/routing")
		return
	}
	// Filesystem-path fields (grep of config.ChannelInstanceConfig for path-typed
	// fields: session_store_path [WhatsApp], crypto_database_path [Matrix],
	// service_account_file [Google Chat]). Stripped, not rejected — they are not
	// part of the UI configure surface, so a stray value is silently ignored
	// rather than failing an otherwise-valid save.
	for _, pathField := range channelFilesystemPathFields {
		delete(updates, pathField)
	}

	// SEC-23 / #289: route secret fields into the encrypted credential store and
	// persist only their <field>_ref in config.json. Every channel constructor
	// reads its secret via the *_ref (e.g. token_ref); an inline plaintext secret
	// is both a plaintext-at-rest violation AND unreadable by the constructor —
	// so a UI-configured token-based channel would never start.
	// Sensitive-field lookup uses the base type (ADR-029 Gate 0): per-instance
	// keys like "whatsapp.eu" use the same type-level field set as "whatsapp".
	chBaseType, _ := config.ParseInstanceKey(channelID)

	// Phase A — classify (zero I/O). Every present sensitive field is either a
	// "clear" (empty/whitespace value) or a "store" (non-empty value), recorded
	// as an action plus the prospective <field>_ref it would produce. Nothing
	// is written to the credential store or config.json here, so a rejection in
	// Phase B below leaves the save a true no-op.
	type sensitiveAction struct {
		field   string
		refName string // channelCredKey(channelID, field)
		secret  string // raw value to store; unused when clear
		clear   bool
	}
	var actions []sensitiveAction
	prospectiveRefs := make(map[string]string, len(channelSensitiveFields[chBaseType]))
	for _, field := range channelSensitiveFields[chBaseType] {
		raw, present := updates[field]
		if !present {
			continue
		}
		secret, isStr := raw.(string)
		if !isStr && raw != nil {
			// A non-string, non-null secret (e.g. {"token": 123}) would collapse to
			// "" and be misread as a clear — reject it instead of silently revoking.
			jsonErr(w, http.StatusBadRequest, fmt.Sprintf("%s must be a string", field))
			return
		}
		refName := channelCredKey(channelID, field)
		if strings.TrimSpace(secret) == "" {
			actions = append(actions, sensitiveAction{field: field, refName: refName, clear: true})
			prospectiveRefs[field] = ""
			continue
		}
		actions = append(actions, sensitiveAction{field: field, refName: refName, secret: secret})
		prospectiveRefs[field] = refName
	}

	// Phase B — validate BEFORE any I/O. The backend must prevent persisting an
	// incomplete channel config (Stage 1 of the channel-Test redesign): a save
	// that would leave the channel unable to construct is rejected here, before
	// any credential is stored or config.json is touched. existing is the
	// channel's currently-persisted raw config ({} for a not-yet-configured
	// instance); prospectiveRefs overlays what this request's sensitive fields
	// would resolve to if committed.
	existing, err := a.readChannelConfigRaw(channelID)
	if err != nil {
		slog.Error("rest: read config for channel configure validation", "channel", channelID, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not read config: %v", err))
		return
	}
	if msg := validateChannelConfigComplete(chBaseType, existing, updates, prospectiveRefs); msg != "" {
		jsonErr(w, http.StatusBadRequest, msg)
		return
	}

	// Phase C — execute. Validation passed: perform the credential-store writes
	// and the config-file merge (unchanged from the pre-restructure behavior).
	var clearedRefs []string // credentials to delete AFTER the config write commits
	for _, act := range actions {
		delete(updates, act.field) // never persist the inline plaintext
		refField := act.field + "_ref"
		if act.clear {
			// Clearing: drop the ref now, but delete the stored credential only
			// AFTER the config write commits (below). Deleting first would strand
			// the channel pointing at a missing credential if the config write fails.
			updates[refField] = ""
			clearedRefs = append(clearedRefs, act.refName)
			continue
		}
		if _, err := a.storeCredential(act.refName, act.secret); err != nil {
			slog.Error("rest: store channel credential", "channel", channelID, "field", act.field, "error", err)
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not store %s credential: %v", act.field, err))
			return
		}
		updates[refField] = act.refName
	}

	var updatedCh map[string]any
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		channels, _ := m["channels"].(map[string]any)
		if channels == nil {
			channels = map[string]any{}
			m["channels"] = channels
		}
		ch, _ := channels[channelID].(map[string]any)
		if ch == nil {
			ch = map[string]any{}
		}
		for k, v := range updates {
			ch[k] = v
		}
		// Persist the type discriminator: for a bare type key ("whatsapp") the
		// base type equals the key; for a namespaced instance key ("whatsapp.eu")
		// the type is the pre-dot segment (ADR-029 Gate 0 / FR-017).
		ch["type"] = chBaseType
		// Invariant (#289/SEC-23): a known secret field is never stored inline in
		// config.json — it lives only in the credential store via its <field>_ref.
		// This also scrubs any stale plaintext left by the pre-#289 blind merge.
		for _, field := range channelSensitiveFields[chBaseType] {
			delete(ch, field)
		}
		channels[channelID] = ch
		updatedCh = ch
		return nil
	}); err != nil {
		slog.Error("rest: configure channel", "channel", channelID, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}

	// Config (with cleared refs) is durable now — delete the now-unreferenced
	// credentials. A failure here only leaves an orphaned encrypted blob (the ref
	// is already gone), so log rather than fail the already-committed request.
	credentialCleanupFailed := false
	for _, refName := range clearedRefs {
		if err := a.removeStoredCredential(refName); err != nil {
			credentialCleanupFailed = true
			slog.Error("rest: delete cleared channel credential", "channel", channelID, "ref", refName, "error", err)
		}
	}

	// FINAL-REVIEW HIGH — configureChannel had no audit trail for a mutating,
	// credential-touching config write (unlike its sibling deleteChannelInstance,
	// which emits EventChannelInstanceDeleted). A configure call can create,
	// rotate, or clear stored encrypted secrets and change arbitrary instance
	// fields, so it MUST leave an audit trail even on the happy path.
	// updates' keys (never values) are logged — secrets have already been
	// stripped and replaced with *_ref entries by Phase C above.
	//
	// Cross-cutting interaction (intentional, fail-closed tradeoff — see the
	// matching note in pkg/gateway/gateway.go's executeReload, next to
	// "Cross-cutting interaction"): the safeUpdateConfigJSON write above
	// triggers an async config reload via the file-watcher (HotReload
	// defaults to true, pkg/config/defaults.go). executeReload's credential
	// check runs against ALL enabled channels, not just this one, so this
	// DecisionAllow audit entry (recorded because THIS handler's own write
	// succeeded) can be followed moments later by executeReload rejecting
	// the reload — and rolling back this channel's newly-saved config in
	// memory — because a DIFFERENT, unrelated enabled channel's credential
	// ref fails to resolve. There is no correlation ID between this audit
	// entry and that later rejection. This is desired behavior (fail closed
	// rather than silently run a channel with a broken credential), not a
	// bug: an operator who wants to know whether their save actually "stuck"
	// checks GET /health's reloadDegraded-driven 503 (surfaces
	// runningServices.reloadError) alongside the gateway logs, not this
	// audit event alone.
	if a.auditor != nil {
		decision := audit.DecisionAllow
		if credentialCleanupFailed {
			decision = audit.DecisionError
		}
		touchedFields := make([]string, 0, len(updates))
		for k := range updates {
			touchedFields = append(touchedFields, k)
		}
		sort.Strings(touchedFields)
		if err := a.auditor.Log(&audit.Entry{
			Event:    audit.EventChannelInstanceConfigured,
			Decision: decision,
			Details: map[string]any{
				"channel_id":     channelID,
				"type":           chBaseType,
				"fields":         touchedFields,
				"cleanup_failed": credentialCleanupFailed,
			},
		}); err != nil {
			slog.Warn("audit write failed", "event", audit.EventChannelInstanceConfigured, "error", err)
		}
	}

	jsonOK(w, redactChannelConfig(channelID, updatedCh))
}

// testChannel handles POST /api/v1/channels/{id}/test.
// For v1.0: verifies required credential fields are configured without starting the channel.
func (a *restAPI) testChannel(w http.ResponseWriter, channelID string) {
	chCfg, err := a.readChannelConfigRaw(channelID)
	if err != nil {
		slog.Error("rest: read config for channel test", "channel", channelID, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not read config: %v", err))
		return
	}

	// Use the base channel type for type-level field lookups (ADR-029 Gate 0).
	testBaseType, _ := config.ParseInstanceKey(channelID)

	// Google Chat's required config is either/or (see NewGoogleChatChannel):
	// webhook mode needs webhook_url, bot mode needs service_account_json or
	// service_account_file. channelRequiredFields is a flat AND-list and
	// cannot express that — leaving it {} let "Test" report success on a
	// completely blank instance. Give it its own branch instead of the
	// generic required-fields loop below.
	if testBaseType == "google-chat" {
		webhookRef, _ := chCfg["webhook_url_ref"].(string)
		hasWebhook, err := a.credentialRefResolves(webhookRef)
		if err != nil {
			slog.Error(
				"rest: channel test credential check",
				"channel",
				channelID,
				"field",
				"webhook_url",
				"error",
				err,
			)
			jsonOK(w, gen.ChannelTestResponse{
				Success: false,
				Message: "credential store unavailable — unlock it (set OMNIPUS_MASTER_KEY) and retry",
			})
			return
		}
		saJSONRef, _ := chCfg["service_account_json_ref"].(string)
		hasSAJSON, err := a.credentialRefResolves(saJSONRef)
		if err != nil {
			slog.Error(
				"rest: channel test credential check",
				"channel",
				channelID,
				"field",
				"service_account_json",
				"error",
				err,
			)
			jsonOK(w, gen.ChannelTestResponse{
				Success: false,
				Message: "credential store unavailable — unlock it (set OMNIPUS_MASTER_KEY) and retry",
			})
			return
		}
		saFile, _ := chCfg["service_account_file"].(string)
		hasSAFile := strings.TrimSpace(saFile) != ""

		if !hasWebhook && !hasSAJSON && !hasSAFile {
			jsonOK(w, gen.ChannelTestResponse{
				Success: false,
				Message: "missing required fields: configure webhook_url (webhook mode) or service_account_json / service_account_file (bot mode)",
			})
			return
		}
		jsonOK(w, gen.ChannelTestResponse{
			Success: true,
			Message: fmt.Sprintf("channel %q is configured", channelID),
		})
		return
	}

	required := channelRequiredFields[testBaseType]
	sensitive := make(map[string]bool, len(channelSensitiveFields[testBaseType]))
	for _, f := range channelSensitiveFields[testBaseType] {
		sensitive[f] = true
	}
	var missing []string
	for _, field := range required {
		if sensitive[field] {
			// #289: a secret required field is satisfied by its <field>_ref
			// resolving in the credential store, NOT by an inline value — the
			// secret never lives in config.json. Checking the inline field here
			// (the old behavior) made "Test" report success for channels that
			// could never activate.
			ref, _ := chCfg[field+"_ref"].(string)
			ok, err := a.credentialRefResolves(ref)
			if err != nil {
				// Store fault (locked / wrong key / I/O) — distinct from a missing
				// secret. Reporting it as "missing" would tell the user to re-enter
				// a credential that is already correct, so surface it as its own state.
				slog.Error("rest: channel test credential check", "channel", channelID, "field", field, "error", err)
				jsonOK(w, gen.ChannelTestResponse{
					Success: false,
					Message: "credential store unavailable — unlock it (set OMNIPUS_MASTER_KEY) and retry",
				})
				return
			}
			if !ok {
				missing = append(missing, field)
			}
			continue
		}
		if v, vOk := chCfg[field].(string); !vOk || v == "" {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		jsonOK(w, gen.ChannelTestResponse{
			Success: false,
			Message: fmt.Sprintf("missing required fields: %s", strings.Join(missing, ", ")),
		})
		return
	}
	jsonOK(w, gen.ChannelTestResponse{
		Success: true,
		Message: fmt.Sprintf("channel %q is configured", channelID),
	})
}

// countEnabledChannels returns the number of non-webchat channels currently enabled in cfg.
func countEnabledChannels(cfg *config.Config) int {
	count := 0
	for _, inst := range cfg.Channels {
		if inst.Enabled {
			count++
		}
	}
	return count
}
