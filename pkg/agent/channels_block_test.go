package agent

// T-25: exact independent T-5/S-01..S-06 oracles from channels-spec-r8.
// Reconciles the original eight loud missing-renderer rows to the committed
// renderer. Expected copy is deliberately not imported from production.

import (
	"fmt"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

const (
	t25Header  = "## Channels\nConnections bound to you in this workspace (live, this turn):\n"
	t25None    = "## Channels\nNo chat connection is bound to you in this workspace. You cannot post to any chat platform; do not ask the user for channel ids."
	t25Denied  = "## Channels\nYou cannot send messages (send_message is not allowed for you)."
	t25Steered = "## Channels\nYou are in a delegated session: you may message only your own conversation."
)

func t25Instance(platform, policy string, targets ...string) config.ChannelInstanceConfig {
	inst := config.ChannelInstanceConfig{
		Type: platform, Enabled: true, WorkspaceID: "ws-a",
		Identity: &config.ChannelIdentity{Kind: "agent", ID: "agent-a"},
	}
	if policy != "" {
		inst.Outbound = &config.ChannelOutboundConfig{Policy: policy, Targets: targets}
	}
	return inst
}

func t25Input(instances map[string]config.ChannelInstanceConfig) channels.ChannelsBlockInput {
	return channels.ChannelsBlockInput{
		Config: &config.Config{Channels: instances}, WorkspaceID: "ws-a", AgentID: "agent-a",
		Availability: func(string) channels.Availability { return channels.Availability{State: "running"} },
	}
}

func t25ReplyLine(id, platform string) string {
	return fmt.Sprintf("- %s (%s): only answer. Answer messages that arrive here (in your main conversation: send_message reply_to=<request id>). You cannot start messages here.", id, platform)
}

func t25MayStart(id, platform, targets string) string {
	return fmt.Sprintf("- %s (%s): you may also start messages to:\n%s\n  To start one: send_message channel=\"%s\" chat_id=\"<one id above>\" content=\"…\". The result says \"accepted for sending (delivery not confirmed)\".", id, platform, targets, id)
}

func TestT25_Block_RunningReplyOnlyLine(t *testing.T) {
	for _, policy := range []string{"", "reply_only"} {
		t.Run("policy="+policy, func(t *testing.T) {
			in := t25Input(map[string]config.ChannelInstanceConfig{"discord": t25Instance("discord", policy)})
			calls := 0
			in.Availability = func(id string) channels.Availability {
				require.Equal(t, "discord", id)
				calls++
				return channels.Availability{State: "running"}
			}
			require.Equal(t, t25Header+t25ReplyLine("discord", "Discord"), channels.RenderChannelsBlock(in))
			require.Equal(t, 1, calls, "FR-005: exactly one availability answer")
		})
	}
}

func TestT25_Block_RunningMayStartWithTargets_LinesAndKindLabels(t *testing.T) {
	in := t25Input(map[string]config.ChannelInstanceConfig{
		"telegram.z": t25Instance("telegram", "agent_may_start", "telegram:333", "telegram:-100", "telegram:-100/7"),
		"discord.a":  t25Instance("discord", "agent_may_start", "discord:111", "discord:222"),
	})
	cacheCalls := []string{}
	availabilityCalls := map[string]int{}
	in.Availability = func(id string) channels.Availability {
		availabilityCalls[id]++
		return channels.Availability{State: "running"}
	}
	in.CachedGroup = func(id, bare string) bool {
		cacheCalls = append(cacheCalls, id+":"+bare)
		return id == "discord.a" && bare == "111"
	}
	want := t25Header + t25MayStart("discord.a", "Discord", "  - 111 (group)\n  - 222 (person or group)") + "\n" +
		t25MayStart("telegram.z", "Telegram", "  - 333 (person, direct message)\n  - -100 (group)\n  - -100/7 (group)")
	require.Equal(t, want, channels.RenderChannelsBlock(in), "T-5: complete sorted block, bare ids, cache-miss ambiguity and Telegram sign/topic labels")
	require.Equal(t, []string{"discord.a:111", "discord.a:222"}, cacheCalls)
	require.Equal(t, map[string]int{"discord.a": 1, "telegram.z": 1}, availabilityCalls)
}

func TestT25_Block_RunningMayStartEmptyList(t *testing.T) {
	in := t25Input(map[string]config.ChannelInstanceConfig{"discord": t25Instance("discord", "agent_may_start")})
	require.Equal(t, t25Header+"- discord (Discord): only answer (the list of people and groups is empty).", channels.RenderChannelsBlock(in))
}

func TestT25_Block_UnsupportedPlatformLine(t *testing.T) {
	in := t25Input(map[string]config.ChannelInstanceConfig{"slack": t25Instance("slack", "agent_may_start", "slack:111")})
	in.CachedGroup = func(string, string) bool {
		t.Fatal("unsupported platform must not inspect a target cache")
		return false
	}
	require.Equal(t, t25Header+"- slack (Slack): only answer; starting messages is not supported yet on Slack.", channels.RenderChannelsBlock(in))
}

func TestT25_Block_NotRunningAndNotLoadedLines_NoTargets(t *testing.T) {
	for _, tc := range []struct{ state, reason string }{{"not_running", "401"}, {"not_loaded", "token not resolved"}} {
		t.Run(tc.state, func(t *testing.T) {
			in := t25Input(map[string]config.ChannelInstanceConfig{"discord": t25Instance("discord", "agent_may_start", "discord:111")})
			calls := 0
			in.Availability = func(string) channels.Availability {
				calls++
				return channels.Availability{State: channels.AvailabilityState(tc.state), Reason: tc.reason}
			}
			in.CachedGroup = func(string, string) bool {
				t.Fatal("unavailable connection must not inspect target cache")
				return false
			}
			require.Equal(t, t25Header+"- discord (Discord): configured but not connected right now ("+tc.reason+"); you cannot post here until it reconnects.", channels.RenderChannelsBlock(in))
			require.Equal(t, 1, calls)
		})
	}
}

func TestT25_Block_WholeBlockCases(t *testing.T) {
	t.Run("S02 sorted discord may-start and telegram reply-only", func(t *testing.T) {
		in := t25Input(map[string]config.ChannelInstanceConfig{
			"telegram": t25Instance("telegram", "reply_only"),
			"discord":  t25Instance("discord", "agent_may_start", "discord:111", "discord:222"),
		})
		want := t25Header + t25MayStart("discord", "Discord", "  - 111 (person or group)\n  - 222 (person or group)") + "\n" + t25ReplyLine("telegram", "Telegram")
		require.Equal(t, want, channels.RenderChannelsBlock(in))
	})
	base := t25Instance("discord", "agent_may_start", "discord:111")
	for _, name := range []string{"no connections", "disabled", "another agent", "another workspace", "unbound workspace", "unbound identity"} {
		t.Run(name, func(t *testing.T) {
			inst := base
			inst.Identity = &config.ChannelIdentity{Kind: "agent", ID: "agent-a"}
			instances := map[string]config.ChannelInstanceConfig{"discord": inst}
			switch name {
			case "no connections":
				instances = map[string]config.ChannelInstanceConfig{}
			case "disabled":
				inst.Enabled = false
			case "another agent":
				inst.Identity.ID = "agent-b"
			case "another workspace":
				inst.WorkspaceID = "ws-b"
			case "unbound workspace":
				inst.WorkspaceID = ""
			case "unbound identity":
				inst.Identity = nil
			}
			if name != "no connections" {
				instances["discord"] = inst
			}
			in := t25Input(instances)
			in.Availability = func(string) channels.Availability {
				t.Fatal("FR-005: excluded connections are not probed")
				return channels.Availability{}
			}
			require.Equal(t, t25None, channels.RenderChannelsBlock(in))
		})
	}
	for _, tc := range []struct {
		name            string
		denied, steered bool
		want            string
	}{
		{"denied", true, false, t25Denied}, {"steered", false, true, t25Steered},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := t25Input(map[string]config.ChannelInstanceConfig{"discord": base})
			in.SendMessageDenied, in.Delegated = tc.denied, tc.steered
			in.Availability = func(string) channels.Availability {
				t.Fatal("whole-block refusal must list/probe no connection")
				return channels.Availability{}
			}
			require.Equal(t, tc.want, channels.RenderChannelsBlock(in))
		})
	}
}
