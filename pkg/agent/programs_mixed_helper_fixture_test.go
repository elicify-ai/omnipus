package agent

// Owned runtime fixture for spec rev8 S123. The observer sits solely at the
// model-provider edge; restart reloads real config/agent/credential/session data.
import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/steer"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

type programsMixedHelperProvider struct {
	*programsHelperProvider
	origins map[string][]bus.Provenance
}

func (p *programsMixedHelperProvider) Chat(ctx context.Context, messages []providers.Message, definitions []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	sid := tools.ToolTranscriptSessionID(ctx)
	p.origins[sid] = append(p.origins[sid], tools.ToolTurnProvenance(ctx))
	p.mu.Unlock()
	return p.programsHelperProvider.Chat(ctx, messages, definitions, model, options)
}

func (p *programsMixedHelperProvider) observed(sid string) []bus.Provenance {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]bus.Provenance(nil), p.origins[sid]...)
}

type programsMixedHelperFixture struct {
	f            *wu5Fixture
	parent, work string
	closeLoop    func()
	observer     *programsMixedHelperProvider
}

func newProgramsMixedHelperFixture(t *testing.T) *programsMixedHelperFixture {
	t.Helper()
	stubMemory(t, false, true)
	f, closeLoop := newProgramsTurnFixtureOwned(t, "tok_valid_12345", func(cfg *config.Config) {
		cfg.Sandbox.ToolPolicies["delegate"] = "allow"
		cfg.Sandbox.ToolPolicies["message_parent"] = "allow"
		cfg.Performance = config.DefaultConfig().Performance
		cfg.Channels = map[string]config.ChannelInstanceConfig{"telegram": {Type: "telegram", WorkspaceID: "sales", Identity: &config.ChannelIdentity{Kind: "agent", ID: "dev"}, ProgramsAllowed: true}}
	})
	require.NoError(t, workspace.SaveRecord(f.home, workspace.Workspace{ID: "sales", Name: "Sales", Status: "active", CoreTeam: []string{"dev", "mia"}}))
	require.NoError(t, workspace.SaveDelegation(f.home, "sales", []workspace.DelegationEdge{{FromAgent: "dev", ToAgent: "dev"}}))
	x := &programsMixedHelperFixture{f: f, parent: f.mainSession("sales", "dev"), work: filepath.Join(f.home, "workspaces", "sales", "work"), closeLoop: closeLoop}
	require.NoError(t, os.MkdirAll(x.work, 0700))
	x.wire(t)
	return x
}

func (x *programsMixedHelperFixture) wire(t *testing.T) {
	t.Helper()
	f := x.f
	programsWireMessaging(t, f)
	f.al.SetSteerAudienceDeps(NewSteerAudienceResolver(NewSteerRecordClassifier(f.al.GetSessionLifecycleStore(), f.al.GetSessionStore())), steer.NopBoundaryObserver{}, NewSteerUpwardDeliverer())
	f.al.SetSteerSessionLauncher(NewSteerLauncher(f.al))
	x.observer = &programsMixedHelperProvider{programsHelperProvider: &programsHelperProvider{wu5ScriptedProvider: f.provider, parent: x.parent, work: x.work}, origins: map[string][]bus.Provenance{}}
	inst, ok := f.al.GetRegistry().GetAgent("dev")
	require.True(t, ok)
	inst.Provider = x.observer
}

func (x *programsMixedHelperFixture) restart(t *testing.T) {
	t.Helper()
	old := x.f
	joinGoalFixtureRuns(t, old.al)
	path := filepath.Join(old.home, "config.json")
	entities := agentstore.New(old.home)
	for _, actor := range old.al.GetConfig().Agents.List {
		require.NoError(t, entities.Create(actor.ID, &actor))
	}
	require.NoError(t, config.SaveConfig(path, old.al.GetConfig()))
	x.closeLoop()
	old.bus.Close()
	old.al.sysagentDeps.CredStore.Close()
	store := credentials.NewStore(filepath.Join(old.home, "credentials.json"))
	require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x39}, 32)))
	t.Cleanup(store.Close)
	cfg, err := config.LoadConfigWithStore(path, store)
	require.NoError(t, err)
	actors, skipped, err := entities.List()
	require.NoError(t, err)
	require.Empty(t, skipped, "instrument: the actual persisted actor roster is fully readable")
	require.Len(t, actors, 2)
	cfg.Agents.List = actors
	provider := newWU5ScriptedProvider()
	messages := bus.NewMessageBus()
	t.Cleanup(messages.Close)
	al := mustNewAgentLoop(t, cfg, messages, provider)
	x.closeLoop = sync.OnceFunc(al.Close)
	t.Cleanup(x.closeLoop)
	al.WireSysagentDeps(&systools.Deps{Home: old.home, GetCfg: al.GetConfig, CredStore: store})
	x.f = &wu5Fixture{t: t, al: al, bus: messages, provider: provider, home: old.home}
	x.wire(t)
	require.NotSame(t, old.al, al)
	require.NotSame(t, old.provider, provider)
	require.NotSame(t, old.bus, messages)
	require.NotSame(t, old.al.GetConfig(), cfg)
	require.False(t, cfg.Programs.WebChatAllowed, "S123: actual web-off setting persisted across runtime restart")
	require.True(t, cfg.Channels["telegram"].ProgramsAllowed, "S123: actual Telegram-on setting persisted across runtime restart")
	t.Log("actual helper runtime restart: closed old loop/bus/store; distinct loop/provider/bus, reloaded config/actor entities, real reopened credentials/session/lifecycle stores and new boot epoch")
}
