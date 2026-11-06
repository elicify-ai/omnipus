package channels

// Channels outbound policy core (channels-spec-r8, T-1, T-2, T-3, FR-011,
// FR-012, FR-041): the one target canonicalizer, the platform table and the
// pure start check shared by the send_message tool, the Channels block and
// the dispatch/dequeue checks.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/identity"
)

// StartPolicy is a connection's outbound policy (FR-010). The values are the
// contract enum ChannelOutbound.policy.
type StartPolicy string

const (
	// StartPolicyReplyOnly is "Only answer", the default when config.json
	// has no outbound setting.
	StartPolicyReplyOnly StartPolicy = "reply_only"
	// StartPolicyAgentMayStart is "Answer and also start messages".
	StartPolicyAgentMayStart StartPolicy = "agent_may_start"
)

// TargetKind says what a canonical target is (T-1, T-5 kind labels).
type TargetKind string

const (
	// TargetKindPerson is a person: a start opens a direct message.
	TargetKindPerson TargetKind = "person"
	// TargetKindGroup is a group, channel or thread.
	TargetKindGroup TargetKind = "group"
	// TargetKindPersonOrGroup is a Discord id the local cache cannot place;
	// never labelled "person" (T-5, M-07).
	TargetKindPersonOrGroup TargetKind = "person_or_group"
)

// TargetErrorCode is why one target entry was refused. The values are the
// contract enum ChannelOutboundRejection.target_errors[].code.
type TargetErrorCode string

const (
	TargetErrInvalidID              TargetErrorCode = "invalid_id"
	TargetErrWildcardNotAllowed     TargetErrorCode = "wildcard_not_allowed"
	TargetErrUsernameNotAllowed     TargetErrorCode = "username_not_allowed"
	TargetErrWrongPlatform          TargetErrorCode = "wrong_platform"
	TargetErrNotFound               TargetErrorCode = "not_found"
	TargetErrUnsupportedChannelKind TargetErrorCode = "unsupported_channel_kind"
	TargetErrCannotMessagePeople    TargetErrorCode = "cannot_message_people"
	TargetErrLookupUnavailable      TargetErrorCode = "lookup_unavailable"
)

// TargetError is one refused entry of a raw target list. Index is the
// entry's 0-based position in the raw input (the contract's
// target_errors[].index); Raw is the entry as received (unsanitized — the
// REST layer sanitizes it into the contract's target hint).
type TargetError struct {
	Raw   string
	Code  TargetErrorCode
	Index int
	// Reason, when set, is a refusal-specific reason chosen by the check
	// that refused the entry (T-9: a DM channel, a channel the bot cannot
	// see); empty means the code's catalog reason.
	Reason string
}

// maxCanonicalTargetLen is the contract bound on a stored canonical value,
// prefix included (ChannelOutbound.targets[].maxLength). A value that passes
// the Discord or Telegram numeric rules is at most 40 characters.
const maxCanonicalTargetLen = 64

// CanonicalizeTarget is the one canonicalizer of T-2 (FR-012) for one raw
// entry on a connection of the given platform ("discord", "telegram", ...,
// lower case). On success it returns the canonical "platform:id" value and
// an empty code; on refusal it returns "" and the refusal code. It decides
// syntax and the T-2 numeric rules only — never a network look-up and never
// the platform-support refusals of T-1 (PlatformStartRuleFor).
//
// Order: trim; an optional "platform:" prefix (any letter case) must name
// this connection's platform (wrong_platform otherwise); then a wildcard
// (wildcard_not_allowed), an @name (username_not_allowed), and finally the
// platform's id rule (invalid_id). Discord and Telegram apply the numeric
// rules of T-2; other platforms accept any non-empty id without whitespace
// or "|" (the writer refuses them at the platform level, T-1).
func CanonicalizeTarget(platform, raw string) (canonical string, code TargetErrorCode) {
	platform = strings.ToLower(strings.TrimSpace(platform))
	id := strings.TrimSpace(raw)
	if prefix, rest, found := strings.Cut(id, ":"); found && isPlatformPrefix(prefix) {
		if strings.ToLower(prefix) != platform {
			return "", TargetErrWrongPlatform
		}
		id = strings.TrimSpace(rest)
	}
	switch {
	case id == "":
		return "", TargetErrInvalidID
	case id == "*":
		return "", TargetErrWildcardNotAllowed
	case strings.HasPrefix(id, "@"):
		return "", TargetErrUsernameNotAllowed
	}
	var ok bool
	switch platform {
	case "discord":
		ok = validDiscordID(id)
	case "telegram":
		ok = validTelegramTarget(id)
	default:
		ok = platform != "" && !strings.ContainsAny(id, "| \t\r\n")
	}
	canonical = identity.BuildCanonicalID(platform, id)
	if !ok || len(canonical) > maxCanonicalTargetLen {
		return "", TargetErrInvalidID
	}
	return canonical, ""
}

// isPlatformPrefix reports whether s can be a "platform:" prefix: a
// non-empty run of ASCII letters, digits, "-" or "_" (platform keys such as
// "google-chat" or "whatsapp_native").
func isPlatformPrefix(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// plainPositiveDecimal reports whether s is ASCII digits with a first digit
// 1–9 (no sign, no leading zero, no full-width digits).
func plainPositiveDecimal(s string) bool {
	if s == "" || s[0] < '1' || s[0] > '9' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// validDiscordID applies T-2's Discord rule: 1 … 2^64−1, plain decimal.
func validDiscordID(s string) bool {
	if !plainPositiveDecimal(s) {
		return false
	}
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}

// validTelegramTarget applies T-2's Telegram rules: a non-zero signed 64-bit
// chat id in plain decimal form, optionally followed by "/topic" with a
// topic of 1 … 2^31−1, and a topic only after a negative chat id.
func validTelegramTarget(s string) bool {
	chat, topic, hasTopic := strings.Cut(s, "/")
	negative := strings.HasPrefix(chat, "-")
	if !plainPositiveDecimal(strings.TrimPrefix(chat, "-")) {
		return false
	}
	if _, err := strconv.ParseInt(chat, 10, 64); err != nil {
		return false
	}
	if !hasTopic {
		return true
	}
	if !negative || !plainPositiveDecimal(topic) {
		return false
	}
	_, err := strconv.ParseInt(topic, 10, 32)
	return err == nil
}

// CanonicalizeTargets canonicalizes a whole raw list (FR-012): each entry
// through CanonicalizeTarget, duplicates (in any input form) collapsed to
// their first occurrence, request order kept. errs lists every refused entry
// in request order with its raw index. The limit of 20 is NOT applied here
// (the caller refuses too_many_targets on len(canonical) > 20 when errs is
// empty).
func CanonicalizeTargets(platform string, raw []string) (canonical []string, errs []TargetError) {
	seen := make(map[string]bool, len(raw))
	for i, entry := range raw {
		value, code := CanonicalizeTarget(platform, entry)
		if code != "" {
			errs = append(errs, TargetError{Raw: entry, Code: code, Index: i})
			continue
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		canonical = append(canonical, value)
	}
	return canonical, errs
}

// TelegramTargetKind derives a Telegram target's kind locally from the sign
// of its chat id (T-1): positive = person, negative = group (with or without
// a "/topic" suffix). ok is false for a value that is not a canonical
// Telegram target.
func TelegramTargetKind(canonical string) (kind TargetKind, ok bool) {
	if !strings.HasPrefix(canonical, "telegram:") {
		return "", false
	}
	if again, code := CanonicalizeTarget("telegram", canonical); code != "" || again != canonical {
		return "", false
	}
	if strings.HasPrefix(strings.TrimPrefix(canonical, "telegram:"), "-") {
		return TargetKindGroup, true
	}
	return TargetKindPerson, true
}

// BareTargetID returns a canonical value without its "platform:" prefix —
// the form the Channels block prints and the adapters send to.
func BareTargetID(canonical string) string {
	if prefix, rest, found := strings.Cut(canonical, ":"); found && isPlatformPrefix(prefix) {
		return rest
	}
	return canonical
}

// PlatformStartRule is one row of the T-1 platform table.
type PlatformStartRule struct {
	// Platform is the lower-case platform key, e.g. "telegram".
	Platform string
	// DisplayName is the name used in user and agent texts, e.g. "Telegram".
	DisplayName string
	// PersonRefusal is the contract ChannelOutboundRejection.code for a
	// person target on this platform ("" when allowed):
	// "platform_cannot_message_people" or "platform_cannot_start".
	PersonRefusal string
	// GroupRefusal is the same for a group target ("" when allowed).
	GroupRefusal string
	// StartSupported is true for Discord and Telegram only.
	StartSupported bool
}

const (
	refusalCannotStart         = "platform_cannot_start"
	refusalCannotMessagePeople = "platform_cannot_message_people"
)

// platformStartRules is the T-1 table, keyed by lower-case platform key
// (config channel types plus the adapter spellings "googlechat" and
// "whatsapp_native").
var platformStartRules = func() map[string]PlatformStartRule {
	rules := map[string]PlatformStartRule{
		"telegram": {DisplayName: "Telegram", StartSupported: true},
		"discord":  {DisplayName: "Discord", StartSupported: true},
	}
	// DingTalk, Weixin and Google Chat can only answer a person (their send
	// needs state from an inbound message, T-1 evidence).
	for key, name := range map[string]string{
		"dingtalk": "DingTalk", "weixin": "Weixin", "googlechat": "Google Chat", "google-chat": "Google Chat",
	} {
		rules[key] = PlatformStartRule{DisplayName: name, PersonRefusal: refusalCannotMessagePeople, GroupRefusal: refusalCannotStart}
	}
	for key, name := range map[string]string{
		"whatsapp": "WhatsApp", "whatsapp_native": "WhatsApp", "slack": "Slack", "matrix": "Matrix",
		"feishu": "Feishu", "line": "LINE", "wecom": "WeCom", "irc": "IRC", "qq": "QQ",
	} {
		rules[key] = PlatformStartRule{DisplayName: name, PersonRefusal: refusalCannotStart, GroupRefusal: refusalCannotStart}
	}
	for key, rule := range rules {
		rule.Platform = key
		rules[key] = rule
	}
	return rules
}()

// PlatformStartRuleFor returns the T-1 row for a platform key. An unknown
// key returns a row with StartSupported false that refuses both target kinds
// with platform_cannot_start and displays the key itself.
func PlatformStartRuleFor(platform string) PlatformStartRule {
	key := strings.ToLower(strings.TrimSpace(platform))
	if rule, ok := platformStartRules[key]; ok {
		return rule
	}
	return PlatformStartRule{
		Platform: key, DisplayName: key,
		PersonRefusal: refusalCannotStart, GroupRefusal: refusalCannotStart,
	}
}

// InstancePlatform returns the platform key of a configured instance: its
// explicit type, else the type part of its instance key ("discord.b" →
// "discord"), lower-cased.
func InstancePlatform(instanceID string, inst config.ChannelInstanceConfig) string {
	platform := strings.TrimSpace(inst.Type)
	if platform == "" {
		platform, _ = config.ParseInstanceKey(instanceID)
	}
	return strings.ToLower(platform)
}

// boundTo reports whether inst is enabled and bound to exactly (workspace,
// agent) — the FR-011 binding test.
func boundTo(inst config.ChannelInstanceConfig, workspaceID, agentID string) bool {
	return inst.Enabled && inst.IsWorkspaceBound() &&
		strings.TrimSpace(inst.WorkspaceID) == strings.TrimSpace(workspaceID) &&
		strings.TrimSpace(inst.Identity.ID) == strings.TrimSpace(agentID)
}

// OutboundPolicy returns the effective policy of one channel instance for one
// (workspace, agent) pair (FR-011): StartPolicyReplyOnly unless the instance
// is enabled, bound to exactly that pair, and its stored policy is
// agent_may_start.
func OutboundPolicy(inst config.ChannelInstanceConfig, workspaceID, agentID string) StartPolicy {
	if !boundTo(inst, workspaceID, agentID) || inst.Outbound == nil ||
		StartPolicy(inst.Outbound.Policy) != StartPolicyAgentMayStart {
		return StartPolicyReplyOnly
	}
	return StartPolicyAgentMayStart
}

// AvailabilityState is the contract enum ChannelAvailability.state (T-7).
type AvailabilityState string

const (
	AvailabilityRunning    AvailabilityState = "running"
	AvailabilityNotRunning AvailabilityState = "not_running"
	AvailabilityNotLoaded  AvailabilityState = "not_loaded"
)

// Availability is the answer of Manager.Availability (T-7) and the
// availability input of StartAllowed. Reason is empty when State is running.
type Availability struct {
	State  AvailabilityState
	Reason string
}

// StartRefusalCode is a T-3 refusal code.
type StartRefusalCode string

const (
	StartRefusedUnknownChannel      StartRefusalCode = "unknown_channel"
	StartRefusedNotBoundToYou       StartRefusalCode = "not_bound_to_you"
	StartRefusedPlatformCannotStart StartRefusalCode = "platform_cannot_start"
	StartRefusedOnlyAnswer          StartRefusalCode = "only_answer"
	StartRefusedTargetNotListed     StartRefusalCode = "target_not_listed"
	StartRefusedNotRunning          StartRefusalCode = "not_running"
)

// StartDecision is the result of StartAllowed. When Allowed is false, Code is
// the first failing T-3 row and Text its exact normative tool text with the
// placeholders filled in. When Allowed is true, Code and Text are empty.
type StartDecision struct {
	Code    StartRefusalCode
	Text    string
	Allowed bool
}

func refuse(code StartRefusalCode, format string, args ...any) StartDecision {
	return StartDecision{Code: code, Text: fmt.Sprintf(format, args...)}
}

// StartAllowed is the one pure start check of T-3 (FR-041), shared by the
// send_message tool, the Channels block, the dispatch hook and the dequeue
// check. It reads only its arguments: cfg is the policy snapshot (the live
// policy source), avail the caller-supplied availability answer for
// instanceID (Manager.Availability at the tool and block; the worker's
// IsRunning at dispatch and dequeue — a caller that only has the running
// flag passes AvailabilityRunning or AvailabilityNotRunning), chatID the
// target as the agent supplied it (canonicalized inside with
// CanonicalizeTarget), workspaceID and agentID the caller. Rows are
// evaluated in the T-3 order a, b, e, c, d, f; the first failing row decides.
// It takes no lock and makes no network call.
func StartAllowed(
	cfg *config.Config,
	avail Availability,
	instanceID, chatID, workspaceID, agentID string,
) StartDecision {
	// a — the instance exists in the policy snapshot.
	var inst config.ChannelInstanceConfig
	var exists bool
	if cfg != nil {
		inst, exists = cfg.Channels[instanceID]
	}
	if !exists {
		return refuse(StartRefusedUnknownChannel,
			"no connection named %q: check your Channels block or list_channels. Nothing was sent.", instanceID)
	}
	// b — enabled and bound to exactly (workspace, agent).
	if !boundTo(inst, workspaceID, agentID) {
		return refuse(StartRefusedNotBoundToYou,
			"cannot start a message on %q: it is not bound to you in this workspace (%s). Nothing was sent.",
			instanceID, yoursHere(cfg, workspaceID, agentID))
	}
	// e — the platform supports starting (T-1), checked before c.
	rule := PlatformStartRuleFor(InstancePlatform(instanceID, inst))
	if !rule.StartSupported {
		return refuse(StartRefusedPlatformCannotStart,
			"starting messages is not supported yet on %s (%q). Nothing was sent.", rule.DisplayName, instanceID)
	}
	// c — the stored policy is agent_may_start.
	if OutboundPolicy(inst, workspaceID, agentID) != StartPolicyAgentMayStart {
		return refuse(StartRefusedOnlyAnswer,
			"cannot start a message on %q: this connection is set to \"Only answer\". The operator can change it "+
				"in Connectors > %s > Configure (\"Answer and also start messages\"). Nothing was sent.",
			instanceID, rule.DisplayName)
	}
	// d — the canonical chat id is exactly on the stored list.
	if !targetListed(rule.Platform, inst.Outbound.Targets, chatID) {
		return refuse(StartRefusedTargetNotListed,
			"cannot start a message to %q on %q: it is not on this connection's list. Listed: %s. Nothing was sent.",
			strings.TrimSpace(chatID), instanceID, listedIDs(rule.Platform, inst.Outbound.Targets))
	}
	// f — the connection is running.
	if avail.State != AvailabilityRunning {
		return refuse(StartRefusedNotRunning,
			"%q is configured but not connected right now (%s). Nothing was sent.", instanceID, availabilityReason(avail))
	}
	return StartDecision{Allowed: true}
}

// targetListed reports whether chatID's canonical value is in targets (each
// stored value canonicalized the same way, so a bare stored id matches). A
// chat id that does not canonicalize is never listed.
func targetListed(platform string, targets []string, chatID string) bool {
	want, code := CanonicalizeTarget(platform, chatID)
	if code != "" {
		return false
	}
	for _, stored := range targets {
		if got, c := CanonicalizeTarget(platform, stored); c == "" && got == want {
			return true
		}
	}
	return false
}

// listedIDs renders T-3 row d's "Listed:" part: the stored targets as bare
// ids in stored order, or "none".
func listedIDs(platform string, targets []string) string {
	ids := make([]string, 0, len(targets))
	for _, stored := range targets {
		if canonical, code := CanonicalizeTarget(platform, stored); code == "" {
			ids = append(ids, BareTargetID(canonical))
		}
	}
	if len(ids) == 0 {
		return "none"
	}
	return strings.Join(ids, ", ")
}

// yoursHere renders T-3 row b's parenthetical: the caller's enabled
// connections bound to (workspace, agent), sorted, or "you have no
// connection here".
func yoursHere(cfg *config.Config, workspaceID, agentID string) string {
	var ids []string
	for id, inst := range cfg.Channels {
		if boundTo(inst, workspaceID, agentID) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return "you have no connection here"
	}
	sort.Strings(ids)
	return "yours here: " + strings.Join(ids, ", ")
}

// availabilityReason is T-3 row f's reason: the supplied reason, else the
// T-7 default for the state.
func availabilityReason(avail Availability) string {
	if reason := strings.TrimSpace(avail.Reason); reason != "" {
		return reason
	}
	if avail.State == AvailabilityNotLoaded {
		return "not loaded"
	}
	return "not started"
}
