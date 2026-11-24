package agent

// S78/H49v2: only the external model/account-check edges are scripted. A real
// helper reaches a real question then blocks at its next model call until Stop.
import (
	"bytes"
	"context"
	"fmt"
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

const programsStoppedCorrelation = "s78-real-helper-question"

type programsStoppedHelperProvider struct {
	*wu5ScriptedProvider
	parent, child, work string
	resumed             bool
	held                chan string
	release             chan struct{}
	origins             map[string][]bus.Provenance
}

func (p *programsStoppedHelperProvider) Chat(ctx context.Context, messages []providers.Message, definitions []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error) {
	sid := tools.ToolTranscriptSessionID(ctx)
	if sid == "" {
		return nil, fmt.Errorf("S78 provider: actual request lacks transcript session")
	}
	p.mu.Lock()
	p.origins[sid] = append(p.origins[sid], tools.ToolTurnProvenance(ctx))
	if sid != p.parent && p.child != "" && p.child != sid {
		p.mu.Unlock()
		return nil, fmt.Errorf("S78 provider: a different helper was started")
	}
	if sid != p.parent {
		p.child = sid
		if _, scripted := p.scripts[sid]; !scripted {
			phase := "before"
			if p.resumed {
				phase = "after"
			}
			command := fmt.Sprintf("printenv GH_TOKEN | wc -c > %q; printf 'S78_HELPER_CHILD_RAN'", filepath.Join(p.work, sid+"-"+phase))
			p.scripts[sid] = []providers.LLMResponse{wu5BashCall("s78-count-"+phase, command)}
			if !p.resumed {
				p.scripts[sid] = append(p.scripts[sid], providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: "s78-question", Type: "function", Name: "message_parent", Arguments: map[string]any{"kind": "question", "text": "May I run the next byte count?", "correlation_id": programsStoppedCorrelation}}}})
			}
			p.scripts[sid] = append(p.scripts[sid], wu5Stop("S78 helper command completed"))
		}
	}
	n := p.calls[sid]
	p.mu.Unlock()
	response, err := p.wu5ScriptedProvider.Chat(ctx, messages, definitions, model, options)
	if sid != p.parent && !p.resumed && n == 2 {
		p.held <- sid // The real question result was already included in this prompt.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.release: // Cleanup only; normal execution is released by real Stop.
		}
	}
	return response, err
}

func (p *programsStoppedHelperProvider) observed(sid string) []bus.Provenance {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]bus.Provenance(nil), p.origins[sid]...)
}

type programsStoppedHelperFixture struct {
	f            *wu5Fixture
	parent, work string
	closeLoop    func()
	provider     *programsStoppedHelperProvider
}

func newProgramsStoppedHelperFixture(t *testing.T) *programsStoppedHelperFixture {
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
	x := &programsStoppedHelperFixture{f: f, parent: f.mainSession("sales", "dev"), work: filepath.Join(f.home, "workspaces", "sales", "work"), closeLoop: closeLoop}
	require.NoError(t, os.MkdirAll(x.work, 0700))
	x.wire(t, "", false)
	return x
}

func (x *programsStoppedHelperFixture) wire(t *testing.T, child string, resumed bool) {
	t.Helper()
	f := x.f
	programsWireMessaging(t, f)
	f.al.SetSteerAudienceDeps(NewSteerAudienceResolver(NewSteerRecordClassifier(f.al.GetSessionLifecycleStore(), f.al.GetSessionStore())), steer.NopBoundaryObserver{}, NewSteerUpwardDeliverer())
	f.al.SetSteerSessionLauncher(NewSteerLauncher(f.al))
	x.provider = &programsStoppedHelperProvider{wu5ScriptedProvider: f.provider, parent: x.parent, child: child, work: x.work, resumed: resumed, held: make(chan string, 1), release: make(chan struct{}), origins: map[string][]bus.Provenance{}}
	release := x.provider.release // Capture this runtime, not x's later replacement.
	t.Cleanup(sync.OnceFunc(func() { close(release) }))
	inst, ok := f.al.GetRegistry().GetAgent("dev")
	require.True(t, ok)
	inst.Provider = x.provider
}

func (x *programsStoppedHelperFixture) restart(t *testing.T, child string) {
	t.Helper()
	old := x.f
	joinGoalFixtureRuns(t, old.al)
	priorEpoch := old.al.bootEpochFor()
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
	require.Empty(t, skipped)
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
	x.wire(t, child, true)
	require.NotSame(t, old.al, al)
	require.NotSame(t, old.provider, provider)
	require.NotSame(t, old.bus, messages)
	require.NotSame(t, old.al.GetConfig(), cfg)
	require.NotSame(t, old.al.GetSessionStore(), al.GetSessionStore())
	require.NotSame(t, old.al.GetSessionLifecycleStore(), al.GetSessionLifecycleStore())
	require.NotSame(t, old.al.GetMessageInboxStore(), al.GetMessageInboxStore())
	require.Equal(t, priorEpoch+1, al.bootEpochFor(), "one genuine next boot epoch over the same durable counter")
	require.False(t, cfg.Channels["telegram"].ProgramsAllowed, "S78: actual off setting persisted, not overwritten in the new runtime")
}
