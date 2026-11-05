package agent

// T-25 integration: real constructor wiring, ContextBuilder, transcript/meta/
// lifecycle stores and audience resolver. No test closure substitutes for the
// production Channels injector; only external adapters/HTTP are controlled.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

type t25Adapter struct {
	*channels.BaseChannel
	running    bool
	probes     int
	onProbe    func()
	cached     map[string]bool
	cacheReads []string
	startErr   error
}

func (c *t25Adapter) IsRunning() bool {
	c.probes++
	if c.onProbe != nil {
		c.onProbe()
	}
	return c.running
}
func (c *t25Adapter) Start(context.Context) error { return c.startErr }
func (c *t25Adapter) Stop(context.Context) error  { return nil }
func (c *t25Adapter) Send(context.Context, bus.OutboundMessage) error {
	return errors.New("QA: renderer must not send")
}
func (c *t25Adapter) CachedPostableChannel(id string) bool {
	c.cacheReads = append(c.cacheReads, id)
	return c.cached[id]
}

func t25NewAdapter(platform string) *t25Adapter {
	return &t25Adapter{BaseChannel: channels.NewBaseChannel(platform, nil, nil, nil), running: true, cached: map[string]bool{}}
}

var _ channels.Channel = (*t25Adapter)(nil)
var _ channels.LocalChannelCache = (*t25Adapter)(nil)

type t25NoHTTP struct{ calls atomic.Int32 }

func (n *t25NoHTTP) RoundTrip(*http.Request) (*http.Response, error) {
	n.calls.Add(1)
	return nil, errors.New("QA forbidden external HTTP")
}

func TestT25_Block_RenderMakesNoNetworkCall_S06(t *testing.T) {
	network := &t25NoHTTP{}
	old := http.DefaultTransport
	http.DefaultTransport = network
	t.Cleanup(func() { http.DefaultTransport = old })
	// Instrument control: a real HTTP request reaches this transport, not DNS.
	resp, err := http.Get("https://qa-t25.invalid/control")
	if resp != nil {
		require.NoError(t, resp.Body.Close())
	}
	require.ErrorContains(t, err, "QA forbidden external HTTP")
	require.Equal(t, int32(1), network.calls.Load())
	network.calls.Store(0)
	manager := channels.NewManagerForTesting(nil)
	adapter := t25NewAdapter("discord")
	adapter.cached["111"] = true
	manager.RegisterChannel("discord", adapter)
	in := t25Input(map[string]config.ChannelInstanceConfig{"discord": t25Instance("discord", "agent_may_start", "discord:111", "discord:222")})
	in.Availability, in.CachedGroup = manager.Availability, manager.CachedPostableChannel
	require.Equal(t, t25Header+t25MayStart("discord", "Discord", "  - 111 (group)\n  - 222 (person or group)"), channels.RenderChannelsBlock(in))
	require.Equal(t, []string{"111", "222"}, adapter.cacheReads, "real Manager delegates only to the local-cache capability")
	require.Equal(t, 1, adapter.probes)
	require.Equal(t, int32(0), network.calls.Load(), "FR-006/S-06: exactly zero REST calls")
}

func TestT25_Block_RealManagerConstructionAndStartFailure_S04(t *testing.T) {
	for _, name := range []string{"construction", "start"} {
		t.Run(name, func(t *testing.T) {
			var manager *channels.Manager
			wantReason, wantState := "token not resolved", channels.AvailabilityNotLoaded
			if name == "construction" {
				manager = channels.NewManagerForTesting([]channels.ChannelInitError{{Name: "discord", Err: errors.New(wantReason)}})
			} else {
				wantReason, wantState = "401", channels.AvailabilityNotRunning
				manager = channels.NewManagerForTesting(nil)
				adapter := t25NewAdapter("discord")
				adapter.running, adapter.startErr = false, errors.New(wantReason)
				manager.RegisterChannel("discord", adapter)
				require.ErrorContains(t, manager.StartAll(context.Background()), "all 1 configured channels failed to start")
			}
			availability := manager.Availability("discord")
			require.Equal(t, wantState, availability.State)
			require.Contains(t, availability.Reason, wantReason, "S-04 specifies the failure cause, not an adapter-display-name prefix")
			in := t25Input(map[string]config.ChannelInstanceConfig{"discord": t25Instance("discord", "agent_may_start", "discord:111")})
			in.Availability, in.CachedGroup = manager.Availability, manager.CachedPostableChannel
			block := channels.RenderChannelsBlock(in)
			// T-5's <reason> is this separately checked Manager input, not text
			// copied from the renderer's result.
			require.Equal(t, t25Header+"- discord (Discord): configured but not connected right now ("+availability.Reason+"); you cannot post here until it reconnects.", block)
			require.Equal(t, map[string]bool{}, t25PrintedTargets(t, block), "S-04: no target on construction/start failure")
		})
	}
}

func t25ContextHarness(t *testing.T) (*AgentLoop, *AgentInstance, *channels.Manager, string) {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t) // existing real-loop fixture registers Close via t.Cleanup
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok, "instrument: the real registry contains the named agent")
	require.NotNil(t, inst.ContextBuilder)
	require.NotNil(t, inst.ContextBuilder.channelsInjector, "FR-001: constructor must install the production wireChannelsInjectors callback")
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{GlobalPolicies: map[string]config.ToolPolicy{"send_message": "allow"}})
	manager := channels.NewManagerForTesting(nil)
	manager.RegisterChannel("discord", t25NewAdapter("discord"))
	manager.SetPolicySource(al.GetConfig)
	al.SetChannelManager(manager)
	require.NoError(t, al.MutateConfig(func(cfg *config.Config) error {
		ch := t25Instance("discord", "")
		ch.Identity.ID = testDefaultAgentID
		cfg.Channels = map[string]config.ChannelInstanceConfig{"discord": ch}
		return nil
	}))
	transcript := newTestSteeringSession(t, al, "ws-a")
	resolver := NewSteerAudienceResolver(NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore()))
	al.SetSteerAudienceDeps(resolver, steer.NopBoundaryObserver{}, nil)
	return al, inst, manager, transcript
}

func t25ContextBlock(t *testing.T, cb *ContextBuilder, transcript, workspace string, depth int) string {
	t.Helper()
	messages := cb.BuildMessagesForSession(transcript, depth, nil, "QA current turn", nil, workspace, "webchat", "qa-chat", "", "", "", nil)
	require.NotEmpty(t, messages)
	require.Equal(t, "system", messages[0].Role)
	text := messages[0].Content
	require.Equal(t, 1, strings.Count(text, "## Channels\n"), "FR-001: exactly one actual dynamic block in the provider system message")
	block := text[strings.Index(text, "## Channels\n"):]
	if end := strings.Index(block, "\n\n"); end >= 0 {
		block = block[:end]
	}
	return block
}

func TestT25_Context_NextTurnUsesLivePolicyAndWorkspace_S01_S03(t *testing.T) {
	al, inst, _, transcript := t25ContextHarness(t)
	want := t25Header + t25ReplyLine("discord", "Discord")
	require.Equal(t, want, t25ContextBlock(t, inst.ContextBuilder, transcript, "ws-a", 0))
	require.Equal(t, t25None, t25ContextBlock(t, inst.ContextBuilder, transcript, "ws-other", 0), "effective workspace, not a cached agent-wide block")
	require.NoError(t, al.MutateConfig(func(cfg *config.Config) error {
		ch := cfg.Channels["discord"]
		ch.Enabled = false
		cfg.Channels["discord"] = ch
		return nil
	}))
	require.Equal(t, t25None, t25ContextBlock(t, inst.ContextBuilder, transcript, "ws-a", 0), "S-03: same builder's next turn sees the disabled live snapshot without a cache reset")
}

func TestT25_Context_CurrentToolPoliciesResolveDeny_S02(t *testing.T) {
	_, inst, _, transcript := t25ContextHarness(t)
	for _, tc := range []struct{ name, global, agent, want string }{
		{"ceiling allow sparse agent", "allow", "", t25Header + t25ReplyLine("discord", "Discord")},
		{"agent tighten", "allow", "deny", t25Denied},
		{"ceiling deny cannot loosen", "deny", "allow", t25Denied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := &tools.ToolPolicyCfg{GlobalPolicies: map[string]config.ToolPolicy{"send_message": config.ToolPolicy(tc.global)}, Policies: map[string]config.ToolPolicy{}}
			if tc.agent != "" {
				policy.Policies["send_message"] = config.ToolPolicy(tc.agent)
			}
			inst.StoreToolPolicy(policy)
			require.Equal(t, tc.want, t25ContextBlock(t, inst.ContextBuilder, transcript, "ws-a", 0))
		})
	}
}

func TestT25_Context_RealSessionAudienceNotNumericDepth_S02(t *testing.T) {
	al, inst, _, root := t25ContextHarness(t)
	lifecycle, store := al.GetSessionLifecycleStore(), al.GetSessionStore()
	mustPersistOrdinaryRoot(t, lifecycle, root, session.OriginKindChat)
	// A steered TASK is deliberately tested at depth0. It is excluded from
	// withSteeredDelegationDepth; FR-004 instead follows the session audience.
	child, err := store.NewSession(session.SessionTypeTask, "webchat", testDefaultAgentID)
	require.NoError(t, err)
	require.NoError(t, store.SetMeta(child.ID, session.MetaPatch{ParentSessionID: &root}))
	require.NoError(t, lifecycle.Persist(&session.LifecycleRecord{
		SessionID: child.ID, State: session.LifecycleRunning, Generation: 1,
		OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: root, WorkspaceID: "ws-a", AgentID: testDefaultAgentID,
		Origin:    &session.Origin{Kind: session.OriginKindTask, TaskID: "qa-t25-task"},
		SteeredBy: &session.SteeredBy{SteeringSessionID: root, RootSessionID: root},
	}))
	resolver := NewSteerAudienceResolver(NewSteerRecordClassifier(lifecycle, store))
	al.SetSteerAudienceDeps(resolver, steer.NopBoundaryObserver{}, nil)
	audience, class, err := resolver.Audience(context.Background(), child.ID)
	require.NoError(t, err)
	require.Equal(t, steer.AudienceSteeringSession, audience, "instrument: actual stores/classifier establish a steered session")
	require.Equal(t, steer.ClassSteered, class)
	require.Equal(t, t25Steered, t25ContextBlock(t, inst.ContextBuilder, child.ID, "ws-a", 0))
	require.Equal(t, t25Header+t25ReplyLine("discord", "Discord"), t25ContextBlock(t, inst.ContextBuilder, root, "ws-a", 7), "an ordinary root is not steered merely because a caller passes a nonzero depth")
	// No record on a parented delegate -> real AudienceNone, not a fake flag.
	damaged, err := store.NewSession(session.SessionTypeDelegate, "webchat", testDefaultAgentID)
	require.NoError(t, err)
	require.NoError(t, store.SetMeta(damaged.ID, session.MetaPatch{ParentSessionID: &root}))
	audience, _, err = resolver.Audience(context.Background(), damaged.ID)
	require.NoError(t, err)
	require.Equal(t, steer.AudienceNone, audience)
	require.Equal(t, t25Steered, t25ContextBlock(t, inst.ContextBuilder, damaged.ID, "ws-a", 0))
	// The actual resolver's missing-classifier error must also restrict a REAL
	// transcript session. It is not silently treated as an ordinary root.
	broken := NewSteerAudienceResolver(nil)
	al.SetSteerAudienceDeps(broken, steer.NopBoundaryObserver{}, nil)
	_, _, err = broken.Audience(context.Background(), root)
	require.ErrorContains(t, err, "no classifier configured")
	require.Equal(t, t25Steered, t25ContextBlock(t, inst.ContextBuilder, root, "ws-a", 0))
}

func TestT25_Context_OneSnapshotAndAvailabilityPerConnection_FR005(t *testing.T) {
	al, inst, manager, transcript := t25ContextHarness(t)
	require.NoError(t, al.MutateConfig(func(cfg *config.Config) error {
		d := t25Instance("discord", "agent_may_start", "discord:111")
		d.Identity.ID = testDefaultAgentID
		g := t25Instance("telegram", "agent_may_start", "telegram:222")
		g.Identity.ID = testDefaultAgentID
		hidden := t25Instance("discord", "agent_may_start", "discord:999")
		hidden.Enabled = false
		cfg.Channels = map[string]config.ChannelInstanceConfig{"discord.a": d, "telegram.z": g, "discord.hidden": hidden}
		return nil
	}))
	d, g, hidden := t25NewAdapter("discord"), t25NewAdapter("telegram"), t25NewAdapter("discord")
	manager.RegisterChannel("discord.a", d)
	manager.RegisterChannel("telegram.z", g)
	manager.RegisterChannel("discord.hidden", hidden)
	// Publish a NEW config during the first availability read. The running
	// block must not mix old and new grants or call availability twice.
	d.onProbe = func() {
		d.onProbe = nil
		require.NoError(t, al.MutateConfig(func(cfg *config.Config) error {
			for _, id := range []string{"discord.a", "telegram.z"} {
				ch := cfg.Channels[id]
				ch.Outbound.Targets = []string{}
				cfg.Channels[id] = ch
			}
			return nil
		}))
	}
	wantOld := t25Header + t25MayStart("discord.a", "Discord", "  - 111 (person or group)") + "\n" + t25MayStart("telegram.z", "Telegram", "  - 222 (person, direct message)")
	require.Equal(t, wantOld, t25ContextBlock(t, inst.ContextBuilder, transcript, "ws-a", 0), "FR-005: one coherent old snapshot despite mid-render publication")
	require.Empty(t, al.GetConfig().Channels["telegram.z"].Outbound.Targets, "instrument: the new live policy really was published")
	require.Equal(t, []int{1, 1, 0}, []int{d.probes, g.probes, hidden.probes}, "FR-005: exactly one real Manager availability per listed connection, zero for excluded")
	wantNew := t25Header + "- discord.a (Discord): only answer (the list of people and groups is empty).\n- telegram.z (Telegram): only answer (the list of people and groups is empty)."
	require.Equal(t, wantNew, t25ContextBlock(t, inst.ContextBuilder, transcript, "ws-a", 0), "the next turn sees only the new snapshot")
	require.Equal(t, []int{2, 2, 0}, []int{d.probes, g.probes, hidden.probes})
}
