package channels

// start_allowed_test.go — T-11, StartAllowed over dataset D2 (channels-spec-r8
// T-3 / FR-041; S-41, S-70; SC-003 "all 14 rows of D2 return the stated
// result").
//
// Oracle: dataset D2 row by row, with the T-3 refusal order a, b, e, c, d, f
// — the FIRST failing row decides, and the normative refusal texts come from
// the T-3 table, never from running code. Success (rows 9, 12, 13) means
// Allowed with empty Code and Text (the success text itself is composed by
// the tool, T-13's surface, not the pure check).
//
// Notes on underivable detail (reported in the RED report, not invented):
//   - the parenthetical of the b text ("(<yours here: a, b | you have no
//     connection here>)") is not pinned per binding variant, so rows 2–4
//     assert the fixed prefix and the fixed suffix around it;
//   - the non-empty "Listed: <ids>" rendering (bare ids vs canonical values)
//     is not pinned outside the block's bare-id contract (FR-002), so rows 8
//     and 11 assert the fixed prefix, the fixed suffix and that the stored
//     target value appears; row 7's "none" variant IS pinned (D2 row 7) and
//     asserted exactly.
//
// RED expectation: the WP-0 shell returns the zero StartDecision (Allowed
// false, empty Code), so every refusal row fails on the empty code and every
// allowed row fails on Allowed=false.

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// t11Instance builds one configured channel instance for a D2 row. Binding is
// the instance's identity (an agent-kind identity naming agentID) plus its
// workspace; policy/targets land in the outbound setting.
func t11Instance(t *testing.T, name, workspace, agent string, enabled bool, policy string, targets ...string) config.ChannelInstanceConfig {
	t.Helper()
	inst := config.ChannelInstanceConfig{
		Type:        "discord",
		Enabled:     enabled,
		WorkspaceID: workspace,
	}
	if agent != "" {
		inst.Identity = &config.ChannelIdentity{Kind: "agent", ID: agent}
	}
	if policy != "" {
		inst.Outbound = &config.ChannelOutboundConfig{Policy: policy, Targets: targets}
	}
	return inst
}

func t11Cfg(instances map[string]config.ChannelInstanceConfig) *config.Config {
	return &config.Config{Channels: instances}
}

const t11Agent = "mia"
const t11Workspace = "ws-t11"

type d2Row struct {
	n       int
	name    string                              // the connection name the caller supplies
	inst    func() config.ChannelInstanceConfig // zero Type = the instance is absent from config (D2 row 1)
	avail   Availability
	chat    string
	allowed bool
	code    StartRefusalCode
	textHas []string // substrings the normative text must contain
	textNot []string // substrings it must not contain
}

func t11DatasetD2(t *testing.T) []d2Row {
	const d2DiscordName = "discord"
	const d2TelegramName = "telegram"

	return []d2Row{
		{
			n: 1,
			// D2 row 1: the instance exists neither in config nor in the
			// manager, so the snapshot is empty of it (zero Type = absent).
			name:  "bogus",
			inst:  func() config.ChannelInstanceConfig { return config.ChannelInstanceConfig{} },
			avail: Availability{State: AvailabilityNotRunning, Reason: "unused"},
			chat:  "x",
			code:  StartRefusedUnknownChannel,
			textHas: []string{
				`no connection named "bogus": check your Channels block or list_channels. Nothing was sent.`,
			},
		},
		{
			// Bound to (W,B): the workspace is right, the agent is not.
			n:    2,
			name: d2DiscordName,
			inst: func() config.ChannelInstanceConfig {
				return t11Instance(t, d2DiscordName, t11Workspace, "other-agent", true, "agent_may_start", "222")
			},
			avail: Availability{State: AvailabilityRunning},
			chat:  "222",
			code:  StartRefusedNotBoundToYou,
			textHas: []string{
				`cannot start a message on "discord": it is not bound to you in this workspace (`,
				`Nothing was sent.`,
			},
			textNot: []string{`Only answer`},
		},
		{
			// Unbound instance (no workspace, no agent identity).
			n:    3,
			name: d2DiscordName,
			inst: func() config.ChannelInstanceConfig {
				return t11Instance(t, d2DiscordName, "", "", true, "agent_may_start", "222")
			},
			avail: Availability{State: AvailabilityRunning},
			chat:  "222",
			code:  StartRefusedNotBoundToYou,
			textHas: []string{
				`cannot start a message on "discord": it is not bound to you in this workspace (`,
				`Nothing was sent.`,
			},
		},
		{
			// Bound to (W,A) but disabled: still row b, and row b beats row f
			// (availability is not_loaded).
			n:    4,
			name: d2DiscordName,
			inst: func() config.ChannelInstanceConfig {
				return t11Instance(t, d2DiscordName, t11Workspace, t11Agent, false, "agent_may_start", "222")
			},
			avail: Availability{State: AvailabilityNotLoaded, Reason: "not loaded"},
			chat:  "222",
			code:  StartRefusedNotBoundToYou,
			textHas: []string{
				`cannot start a message on "discord": it is not bound to you in this workspace (`,
			},
			textNot: []string{"not connected right now"},
		},
		{
			// Slack with a hand-edited agent_may_start: row e beats row c —
			// an unsupported platform never points the agent at a setting
			// that cannot help.
			n:    5,
			name: "slack",
			inst: func() config.ChannelInstanceConfig {
				inst := t11Instance(t, "slack", t11Workspace, t11Agent, true, "agent_may_start", "C1")
				inst.Type = "slack"
				return inst
			},
			avail: Availability{State: AvailabilityRunning},
			chat:  "C1",
			code:  StartRefusedPlatformCannotStart,
			textHas: []string{
				`starting messages is not supported yet on Slack ("slack"). Nothing was sent.`,
			},
			textNot: []string{`Only answer`},
		},
		{
			// No outbound setting at all: reply_only by default (FR-010).
			n:    6,
			name: d2DiscordName,
			inst: func() config.ChannelInstanceConfig {
				inst := t11Instance(t, d2DiscordName, t11Workspace, t11Agent, true, "")
				inst.Type = "discord"
				return inst
			},
			avail: Availability{State: AvailabilityRunning},
			chat:  "222",
			code:  StartRefusedOnlyAnswer,
			textHas: []string{
				`cannot start a message on "discord": this connection is set to "Only answer". ` +
					`The operator can change it in Connectors > Discord > Configure ` +
					`("Answer and also start messages"). Nothing was sent.`,
			},
		},
		{
			// Empty target list: row d with "Listed: none" (pinned by D2).
			n:    7,
			name: d2DiscordName,
			inst: func() config.ChannelInstanceConfig {
				return t11Instance(t, d2DiscordName, t11Workspace, t11Agent, true, "agent_may_start")
			},
			avail: Availability{State: AvailabilityRunning},
			chat:  "222",
			code:  StartRefusedTargetNotListed,
			textHas: []string{
				`cannot start a message to "222" on "discord": it is not on this connection's list. Listed: none. Nothing was sent.`,
			},
		},
		{
			// Listed 222, asked 555.
			n:    8,
			name: d2DiscordName,
			inst: func() config.ChannelInstanceConfig {
				return t11Instance(t, d2DiscordName, t11Workspace, t11Agent, true, "agent_may_start", "222")
			},
			avail: Availability{State: AvailabilityRunning},
			chat:  "555",
			code:  StartRefusedTargetNotListed,
			textHas: []string{
				`cannot start a message to "555" on "discord": it is not on this connection's list. Listed: `,
				"222", // the listed target appears in the hint (bare or canonical — see header note)
				`Nothing was sent.`,
			},
		},
		{
			// The agent supplies the prefixed form; the check canonicalizes
			// inside and the answer is allowed (bare 222 stored).
			n:    9,
			name: d2DiscordName,
			inst: func() config.ChannelInstanceConfig {
				return t11Instance(t, d2DiscordName, t11Workspace, t11Agent, true, "agent_may_start", "222")
			},
			avail:   Availability{State: AvailabilityRunning},
			chat:    "discord:222",
			allowed: true,
		},
		{
			// Everything passed except availability: row f fires last.
			n:    10,
			name: d2DiscordName,
			inst: func() config.ChannelInstanceConfig {
				return t11Instance(t, d2DiscordName, t11Workspace, t11Agent, true, "agent_may_start", "222")
			},
			avail: Availability{State: AvailabilityNotRunning, Reason: "connection is offline"},
			chat:  "222",
			code:  StartRefusedNotRunning,
			textHas: []string{
				`"discord" is configured but not connected right now (connection is offline). Nothing was sent.`,
			},
		},
		{
			// Telegram: stored target -100/45, asked bare -100 (the group id
			// without its topic) — not on the list; the hint shows -100/45.
			n:    11,
			name: d2TelegramName,
			inst: func() config.ChannelInstanceConfig {
				inst := t11Instance(t, d2TelegramName, t11Workspace, t11Agent, true, "agent_may_start", "-100/45")
				inst.Type = "telegram"
				return inst
			},
			avail: Availability{State: AvailabilityRunning},
			chat:  "-100",
			code:  StartRefusedTargetNotListed,
			textHas: []string{
				`cannot start a message to "-100" on "telegram": it is not on this connection's list. Listed: `,
				"-100/45",
				`Nothing was sent.`,
			},
		},
		{
			// The allowed path, bare chat form.
			n:    12,
			name: d2DiscordName,
			inst: func() config.ChannelInstanceConfig {
				return t11Instance(t, d2DiscordName, t11Workspace, t11Agent, true, "agent_may_start", "222")
			},
			avail:   Availability{State: AvailabilityRunning},
			chat:    "222",
			allowed: true,
		},
		{
			// Telegram topic form allowed end to end; the serialization split
			// (chat -100, topic 45) is T-10b's round-trip surface.
			n:    13,
			name: d2TelegramName,
			inst: func() config.ChannelInstanceConfig {
				inst := t11Instance(t, d2TelegramName, t11Workspace, t11Agent, true, "agent_may_start", "-100/45")
				inst.Type = "telegram"
				return inst
			},
			avail:   Availability{State: AvailabilityRunning},
			chat:    "-100/45",
			allowed: true,
		},
		{
			// Row 14 (S-24): the manager-side availability input — the
			// caller passes IsRunning=false without a manager look-up, and
			// the refusal text is identical. The "log only, no chat notice"
			// consequence lives at the dispatch site (S-24/S-41 caller
			// coverage), not in the pure function.
			n:    14,
			name: d2DiscordName,
			inst: func() config.ChannelInstanceConfig {
				return t11Instance(t, d2DiscordName, t11Workspace, t11Agent, true, "agent_may_start", "222")
			},
			avail: Availability{State: AvailabilityNotRunning},
			chat:  "222",
			code:  StartRefusedNotRunning,
			textHas: []string{
				`"discord" is configured but not connected right now (`,
				`Nothing was sent.`,
			},
		},
	}
}

func TestT11_StartAllowed_DatasetD2(t *testing.T) {
	rows := t11DatasetD2(t)

	for _, r := range rows {
		instances := map[string]config.ChannelInstanceConfig{}
		if fixture := r.inst(); fixture.Type != "" {
			instances[r.name] = fixture
		}
		cfg := t11Cfg(instances)
		d := StartAllowed(cfg, r.avail, r.name, r.chat, t11Workspace, t11Agent)
		if r.allowed {
			require.True(t, d.Allowed, "D2 row %d (%s %q): must be allowed; got refusal %q %q",
				r.n, r.name, r.chat, d.Code, d.Text)
			require.Empty(t, d.Code, "D2 row %d: an allowed start has an empty code", r.n)
			require.Empty(t, d.Text, "D2 row %d: an allowed start has empty text", r.n)
			continue
		}
		require.False(t, d.Allowed, "D2 row %d (%s %q): must refuse", r.n, r.name, r.chat)
		require.Equal(t, r.code, d.Code,
			"D2 row %d (%s %q): refusal code (first failing T-3 row decides)", r.n, r.name, r.chat)
		for _, want := range r.textHas {
			require.Contains(t, d.Text, want,
				"D2 row %d (%s %q): normative T-3 text", r.n, r.name, r.chat)
		}
		for _, banned := range r.textNot {
			require.NotContains(t, d.Text, banned,
				"D2 row %d (%s %q): text must not contain %q", r.n, r.name, r.chat, banned)
		}
		// Every refusal text ends with the normative tail.
		require.True(t, strings.HasSuffix(d.Text, "Nothing was sent."),
			"D2 row %d: refusal ends with the normative tail; got %q", r.n, d.Text)
	}
}

// TestT11_StartAllowed_RefusalOrderPinned pins the T-3 order itself with
// pairs of rows that share every input except the deciding one (SC-003 / the
// order note under the T-3 table): a wrongly ordered implementation fails one
// of these even if each row individually looks right.
func TestT11_StartAllowed_RefusalOrderPinned(t *testing.T) {
	bound := func(typ, policy string, targets ...string) *config.Config {
		inst := config.ChannelInstanceConfig{
			Type:        typ,
			Enabled:     true,
			WorkspaceID: t11Workspace,
			Identity:    &config.ChannelIdentity{Kind: "agent", ID: t11Agent},
		}
		if policy != "" {
			inst.Outbound = &config.ChannelOutboundConfig{Policy: policy, Targets: targets}
		}
		name := "discord"
		if typ == "slack" {
			name = "slack"
		}
		return t11Cfg(map[string]config.ChannelInstanceConfig{name: inst})
	}
	running := Availability{State: AvailabilityRunning}

	// b before e: bound-but-unsupported platform (slack, right pair) is
	// not_bound_to_you when the pair is wrong even though the platform is
	// unsupported — the agent fix (binding) is checked first.
	slack := bound("slack", "agent_may_start", "C1")
	got := StartAllowed(slack, running, "slack", "C1", t11Workspace, "other-agent")
	require.Equal(t, StartRefusedNotBoundToYou, got.Code,
		"order: row b fires before row e for a wrong pair on an unsupported platform")

	// e before c: slack with agent_may_start and the right pair is
	// platform_cannot_start, never only_answer (the T-3 order note).
	got = StartAllowed(slack, running, "slack", "C1", t11Workspace, t11Agent)
	require.Equal(t, StartRefusedPlatformCannotStart, got.Code,
		"order: row e fires before row c — an unsupported platform never blames the policy")

	// c before d: right pair, reply_only, unlisted target — only_answer,
	// never target_not_listed.
	replyOnly := bound("discord", "")
	got = StartAllowed(replyOnly, running, "discord", "999", t11Workspace, t11Agent)
	require.Equal(t, StartRefusedOnlyAnswer, got.Code,
		"order: row c fires before row d")

	// d before f: right pair, may-start, listed 222, asked 555,
	// not-running — target_not_listed, never not_running.
	listed := bound("discord", "agent_may_start", "222")
	notRunning := Availability{State: AvailabilityNotRunning, Reason: "offline"}
	got = StartAllowed(listed, notRunning, "discord", "555", t11Workspace, t11Agent)
	require.Equal(t, StartRefusedTargetNotListed, got.Code,
		"order: row d fires before row f")

	// f last: identical row with the listed target is not_running.
	got = StartAllowed(listed, notRunning, "discord", "222", t11Workspace, t11Agent)
	require.Equal(t, StartRefusedNotRunning, got.Code,
		"order: row f is last — an unlisted target and a down connection still report the list first")
}

// TestT11_OutboundPolicy_FR011 pins the effective-policy rule the start check
// rides on (FR-011): agent_may_start only when the instance is enabled, bound
// to exactly that (workspace, agent) pair, and its stored policy is
// agent_may_start. Absent outbound means reply_only (FR-010).
func TestT11_OutboundPolicy_FR011(t *testing.T) {
	may := func() config.ChannelInstanceConfig {
		return config.ChannelInstanceConfig{
			Type:        "discord",
			Enabled:     true,
			WorkspaceID: t11Workspace,
			Identity:    &config.ChannelIdentity{Kind: "agent", ID: t11Agent},
			Outbound:    &config.ChannelOutboundConfig{Policy: "agent_may_start", Targets: []string{"discord:222"}},
		}
	}

	require.Equal(t, StartPolicyAgentMayStart, OutboundPolicy(may(), t11Workspace, t11Agent),
		"FR-011: enabled, exactly bound, agent_may_start")

	replyOnly := may()
	replyOnly.Outbound = nil
	require.Equal(t, StartPolicyReplyOnly, OutboundPolicy(replyOnly, t11Workspace, t11Agent),
		"FR-010: absent outbound means reply_only")

	storedReply := may()
	storedReply.Outbound.Policy = "reply_only"
	require.Equal(t, StartPolicyReplyOnly, OutboundPolicy(storedReply, t11Workspace, t11Agent),
		"FR-011: a stored reply_only stays reply_only")

	otherAgent := may()
	otherAgent.Identity = &config.ChannelIdentity{Kind: "agent", ID: "other-agent"}
	require.Equal(t, StartPolicyReplyOnly, OutboundPolicy(otherAgent, t11Workspace, t11Agent),
		"FR-011: bound to a different agent is reply_only for this one")

	otherWorkspace := may()
	otherWorkspace.WorkspaceID = "ws-other"
	require.Equal(t, StartPolicyReplyOnly, OutboundPolicy(otherWorkspace, t11Workspace, t11Agent),
		"FR-011: bound to a different workspace is reply_only here")

	unbound := may()
	unbound.WorkspaceID = ""
	unbound.Identity = nil
	require.Equal(t, StartPolicyReplyOnly, OutboundPolicy(unbound, t11Workspace, t11Agent),
		"FR-011: an unbound instance is reply_only")

	disabled := may()
	disabled.Enabled = false
	require.Equal(t, StartPolicyReplyOnly, OutboundPolicy(disabled, t11Workspace, t11Agent),
		"FR-011: a disabled instance is reply_only")
}
