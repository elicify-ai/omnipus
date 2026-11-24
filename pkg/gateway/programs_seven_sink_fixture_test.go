// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// S48: one real checked connection/runtime/export campaign. Only the paid model
// and external account-check executable are controlled process edges.
import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/programs"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/steer"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

const (
	programsSinkSentinel = "sentinel_9f3c2a7e5b1d" // S48/S130: 21 bytes.
	programsSinkRotated  = "tok_new_654321"        // S51: 14 bytes.
	programsSinkSoul     = "S48 real exported Dev prompt.\n"
)

type programsSentinelFixture struct {
	*programsPeerFixture
	api *restAPI
	svc *programs.Service
}

func newProgramsSentinelFixture(t *testing.T, god bool, configure ...func(*config.Config)) *programsSentinelFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Fatal("BLOCKED: S48/S106's combined campaign needs native Windows check and command fixtures; the POSIX child scripts do not certify Windows")
	}
	if god {
		if !sandbox.GodModeAvailable {
			t.Fatal("BLOCKED: this build has no God Mode support — required by S106's actual God Mode child inventory")
		}
		// With the switch on in this disposable config, the public active query
		// reads the actual availability gate, not the fixture's desired mode.
		probe := config.DefaultConfig()
		probe.Sandbox.GodMode = true
		previousAvailability := agent.GodModeActive(probe)
		agent.SetGodModeAvailable(true) // Actual boot gate, before constructing tools.
		t.Cleanup(func() { agent.SetGodModeAvailable(previousAvailability) })
	}
	home := t.TempDir()
	_, source, _, callerOK := runtime.Caller(0)
	require.True(t, callerOK)
	for _, token := range []string{programsSinkSentinel, programsSinkRotated} {
		require.False(t, pgSinkHasProtectedWindow([]byte(home+source+t.Name()), token), "instrument: fixture paths, log caller and test name must not themselves contain protected token windows")
	}
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", packageTestBearer)
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		t.Setenv(name, "")
		require.NoError(t, os.Unsetenv(name))
	}
	// The same real file sink/bridge as gateway boot, installed before the loop.
	oldLevel, oldSlog := logger.GetLevel(), slog.Default()
	restoreConsole := logger.DisableConsole()
	t.Cleanup(func() {
		logger.DisableFileLogging()
		slog.SetDefault(oldSlog)
		logger.SetLevel(oldLevel)
		restoreConsole()
	})
	logger.SetLevel(logger.DEBUG)
	require.NoError(t, logger.EnableFileLogging(filepath.Join(home, "logs", "gateway.log")))
	installSlogBridge()
	logger.InfoC("s48-instrument", "S48_NATIVE_LOG_READER_CONTROL")
	slog.Info("S48_SLOG_READER_CONTROL")

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.Defaults.MaxToolIterations = 8
	cfg.Agents.List = []config.AgentConfig{{ID: "dev", Name: "Dev", Type: config.AgentTypeCustom, Tools: coreagent.NewCustomAgentToolsCfg(), Programs: []string{"github"}}}
	cfg.Sandbox.ToolPolicies["bash"] = "allow"
	cfg.Agents.List[0].Tools.Builtin.Policies["bash"] = config.ToolPolicyAllow
	cfg.Sandbox.AuditLog = true
	cfg.Sandbox.GodMode = god
	cfg.Tools.FilterSensitiveData = false
	cfg.Programs.WebChatAllowed = true
	for _, apply := range configure {
		apply(cfg) // Test-only configuration, before constructing the real tools.
	}
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{ID: "sales", Name: "Sales", Status: "active", CoreTeam: []string{"dev"}}))
	_, err := agentstore.New(home).CreateState("dev", &cfg.Agents.List[0], programsSinkSoul)
	require.NoError(t, err)
	p := &programsPeerProvider{wu5GatewayProvider: newWU5GatewayProvider(), origins: map[string][]bus.Provenance{}}
	b := bus.NewMessageBus()
	t.Cleanup(b.Close)
	al := mustAgentLoop(t, cfg, b, p)
	closeLoop := sync.OnceFunc(al.Close)
	t.Cleanup(closeLoop)
	require.Equal(t, god, agent.GodModeActive(al.GetConfig()), "instrument: actual runtime God Mode activation, not merely a config flag")
	dev, found := al.GetRegistry().GetAgent("dev")
	require.True(t, found)
	registered, found := dev.Tools.Get("bash")
	require.True(t, found)
	shell, ok := registered.(*tools.ExecTool)
	require.True(t, ok, "instrument: inspect the actual registered child tool, not a replacement")
	require.Equal(t, god, shell.GodModeForTest(), "instrument: actual registered shell was constructed in the requested mode")
	t.Logf("actual child mode: runtime_god=%t registered_shell_god=%t", agent.GodModeActive(al.GetConfig()), shell.GodModeForTest())
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x39}, 32)))
	t.Cleanup(store.Close)
	al.WireSysagentDeps(&systools.Deps{Home: home, GetCfg: al.GetConfig, CredStore: store})
	al.SetSteerAudienceDeps(agent.NewSteerAudienceResolver(agent.NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())), steer.NopBoundaryObserver{}, agent.NewSteerUpwardDeliverer())
	require.NotNil(t, al.AuditLogger(), "instrument: the real runtime owns the actual audit logger")

	bin := pgFakeProgramDir(t) // Deliberately outside the agent-writable data home and the temp roots.
	check := "#!/bin/sh\n[ \"$#\" -eq 4 ] && [ \"$1\" = api ] && [ \"$2\" = user ] && [ \"$3\" = --jq ] && [ \"$4\" = .login ] || exit 81\ncase \"$GH_TOKEN\" in\n sentinel_9f3c2a7e5b1d) printf 'octocat\\n';;\n tok_new_654321) printf 'hubot\\n';;\n *) exit 82;;\nesac\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "gh"), []byte(check), 0700))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	api := &restAPI{agentLoop: al, homePath: home, credStore: store, auditor: al.AuditLogger()}
	svc := api.programService()
	t.Cleanup(svc.Wait)
	_, err = svc.Publish("github", programsSinkSentinel, nil, nil)
	require.NoError(t, err)
	svc.Wait()
	require.Equal(t, "connected", svc.Connection("github").State)
	require.Equal(t, "octocat", svc.Connection("github").AccountLabel)
	require.Equal(t, int64(1), svc.Connection("github").Generation)
	main, err := al.GetSessionStore().GetOrCreateMainSession("sales", "dev")
	require.NoError(t, err)
	f := &programsPeerFixture{al: al, bus: b, provider: p, home: home, work: filepath.Join(home, "workspaces", "sales", "work"), dev: main.ID, events: al.SubscribeEvents(512)}
	require.NoError(t, os.MkdirAll(f.work, 0700))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- al.Run(ctx) }()
	var shutdown sync.Once
	f.shutdown = func() {
		shutdown.Do(func() {
			cancel()
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(10 * time.Second):
				t.Fatal("S48 actual loop did not stop/join")
			}
			al.UnsubscribeEvents(f.events.ID)
			closeLoop()
			store.Close()
			// The outer cleanup owns bus.Close: it drains pending publications.
			// Keep those actual messages available until the sink reader finishes.
		})
	}
	t.Cleanup(f.shutdown)
	return &programsSentinelFixture{programsPeerFixture: f, api: api, svc: svc}
}
