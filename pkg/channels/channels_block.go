package channels

// The per-turn "## Channels" block (channels-spec-r8 T-5, FR-001–FR-006).

import (
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// The whole-block texts of T-5 (normative).
const (
	channelsBlockHeader    = "## Channels"
	channelsBlockIntro     = "Connections bound to you in this workspace (live, this turn):"
	channelsBlockNone      = "No chat connection is bound to you in this workspace. You cannot post to any chat platform; do not ask the user for channel ids."
	channelsBlockSendDeny  = "You cannot send messages (send_message is not allowed for you)."
	channelsBlockDelegated = "You are in a delegated session: you may message only your own conversation."
)

// Target kind labels (T-5).
const (
	KindLabelPerson        = "person, direct message"
	KindLabelGroup         = "group"
	KindLabelPersonOrGroup = "person or group"
)

// ChannelsBlockInput is everything one render reads. Config is ONE live
// policy snapshot (FR-005); Availability is called once per listed
// connection; CachedGroup answers from an adapter's local cache only and
// never makes a network call (FR-006) — nil means "nothing cached".
type ChannelsBlockInput struct {
	Config            *config.Config
	WorkspaceID       string
	AgentID           string
	SendMessageDenied bool // send_message resolves to deny for this agent (FR-004)
	Delegated         bool // the turn runs in a delegated (steered) session (FR-004)
	Availability      func(instanceID string) Availability
	CachedGroup       func(instanceID, bareID string) bool
}

// RenderChannelsBlock renders the "## Channels" block of one turn exactly as
// T-5 specifies: the whole-block texts for a delegated session, a denied
// send_message and no bound connection; otherwise one entry per enabled
// connection bound to (WorkspaceID, AgentID), sorted by instance id. Every
// printed target passes StartAllowed on the same snapshot and availability,
// and no allowed target is missing (FR-005).
func RenderChannelsBlock(in ChannelsBlockInput) string {
	switch {
	case in.Delegated:
		return channelsBlockHeader + "\n" + channelsBlockDelegated
	case in.SendMessageDenied:
		return channelsBlockHeader + "\n" + channelsBlockSendDeny
	}
	var ids []string
	if in.Config != nil {
		for id, inst := range in.Config.Channels {
			if boundTo(inst, in.WorkspaceID, in.AgentID) {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return channelsBlockHeader + "\n" + channelsBlockNone
	}
	sort.Strings(ids)
	var sb strings.Builder
	sb.WriteString(channelsBlockHeader + "\n" + channelsBlockIntro)
	for _, id := range ids {
		sb.WriteString("\n")
		sb.WriteString(in.connectionEntry(id, in.Config.Channels[id]))
	}
	return sb.String()
}

func (in ChannelsBlockInput) availability(id string) Availability {
	if in.Availability == nil {
		return Availability{State: AvailabilityNotLoaded, Reason: "not loaded"}
	}
	return in.Availability(id)
}

// connectionEntry renders the T-5 line(s) of one bound enabled connection.
func (in ChannelsBlockInput) connectionEntry(id string, inst config.ChannelInstanceConfig) string {
	platform := InstancePlatform(id, inst)
	rule := PlatformStartRuleFor(platform)
	prefix := "- " + id + " (" + rule.DisplayName + "): "
	avail := in.availability(id)
	if avail.State != AvailabilityRunning {
		return prefix + "configured but not connected right now (" + availabilityReason(avail) +
			"); you cannot post here until it reconnects."
	}
	if !rule.StartSupported {
		return prefix + "only answer; starting messages is not supported yet on " + rule.DisplayName + "."
	}
	if OutboundPolicy(inst, in.WorkspaceID, in.AgentID) != StartPolicyAgentMayStart {
		return prefix + "only answer. Answer messages that arrive here (in your main conversation: " +
			"send_message reply_to=<request id>). You cannot start messages here."
	}
	targets := in.startableTargets(id, platform, inst, avail)
	if len(targets) == 0 {
		return prefix + "only answer (the list of people and groups is empty)."
	}
	var sb strings.Builder
	sb.WriteString(prefix + "you may also start messages to:")
	for _, bare := range targets {
		sb.WriteString("\n  - " + bare + " (" + in.kindLabel(id, platform, bare) + ")")
	}
	sb.WriteString("\n  To start one: send_message channel=\"" + id + "\" chat_id=\"<one id above>\" content=\"…\". " +
		"The result says \"accepted for sending (delivery not confirmed)\".")
	return sb.String()
}

// startableTargets returns the stored targets' bare ids, in stored order and
// without duplicates, that StartAllowed accepts on the same snapshot.
func (in ChannelsBlockInput) startableTargets(id, platform string, inst config.ChannelInstanceConfig, avail Availability) []string {
	if inst.Outbound == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, stored := range inst.Outbound.Targets {
		canonical, code := CanonicalizeTarget(platform, stored)
		if code != "" || seen[canonical] {
			continue
		}
		seen[canonical] = true
		bare := BareTargetID(canonical)
		if StartAllowed(in.Config, avail, id, bare, in.WorkspaceID, in.AgentID).Allowed {
			out = append(out, bare)
		}
	}
	return out
}

// kindLabel is the T-5 kind hint, with no network call: Telegram by sign;
// Discord "group" only when the adapter's local cache holds the id as a
// postable channel, otherwise "person or group" (never "person").
func (in ChannelsBlockInput) kindLabel(id, platform, bare string) string {
	switch platform {
	case "telegram":
		if strings.HasPrefix(bare, "-") {
			return KindLabelGroup
		}
		return KindLabelPerson
	case "discord":
		if in.CachedGroup != nil && in.CachedGroup(id, bare) {
			return KindLabelGroup
		}
		return KindLabelPersonOrGroup
	default:
		return KindLabelPersonOrGroup
	}
}
