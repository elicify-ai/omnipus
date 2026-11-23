package agent

// S11/FR-011: a real turn and its actual loop-wired bash child, not a direct
// Attach call. The scripted provider replaces only the paid model boundary.
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/programs"
	"github.com/elicify-ai/omnipus/pkg/providers"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/require"
)

func newProgramsTurnFixture(t *testing.T) *wu5Fixture {
	t.Helper()
	return newProgramsTurnFixtureFor(t, nil)
}

func newProgramsTurnFixtureFor(t *testing.T, configure func(*config.Config)) *wu5Fixture {
	t.Helper()
	return newProgramsTurnFixtureToken(t, "tok_valid_12345", configure)
}

func newProgramsTurnFixtureToken(t *testing.T, token string, configure func(*config.Config)) *wu5Fixture {
	t.Helper()
	fixture, _ := newProgramsTurnFixtureOwned(t, token, configure)
	return fixture
}

// The restart fixture owns an idempotent close receipt; its explicit restart
// and t.Cleanup must not close the same AgentLoop twice.
func newProgramsTurnFixtureOwned(t *testing.T, token string, configure func(*config.Config)) (*wu5Fixture, func()) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	// Setenv registers restoration; Unsetenv then makes absence real (an
	// empty-but-present variable would make printenv emit a one-byte newline).
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "FLY_API_TOKEN", "ACME_TOKEN"} {
		t.Setenv(name, "")
		require.NoError(t, os.Unsetenv(name))
	}
	seedWU5WorkspaceMembership(t, home, "sales", "dev", "mia")
	cfg := &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{Home: filepath.Join(home, "agents"), DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096, MaxToolIterations: 24},
		List:     []config.AgentConfig{{ID: "dev", Name: "Dev", Type: config.AgentTypeCustom, Programs: []string{"github"}}, {ID: "mia", Name: "Mia", Type: config.AgentTypeCustom}},
	}}
	cfg.Sandbox.ToolPolicies = map[string]string{"bash": "allow", "environment_setup": "allow"}
	// Explicitly allow this fixture's web origin. Default-value behavior is a
	// different row; neither a withheld origin nor an approval may explain RED.
	cfg.Programs.WebChatAllowed = true
	if configure != nil {
		configure(cfg)
	}
	provider := newWU5ScriptedProvider()
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	closeLoop := sync.OnceFunc(al.Close)
	t.Cleanup(closeLoop)
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x39}, 32)))
	t.Cleanup(store.Close)
	al.WireSysagentDeps(&systools.Deps{Home: home, GetCfg: al.GetConfig, CredStore: store})
	bin := t.TempDir()
	name, body := "gh", fmt.Sprintf("#!/bin/sh\n[ \"$#\" -eq 4 ] && [ \"$1\" = api ] && [ \"$2\" = user ] && [ \"$3\" = --jq ] && [ \"$4\" = .login ] || exit 8\n[ \"$GH_TOKEN\" = %q ] || exit 9\nprintf 'octocat\\n'\n", token)
	if runtime.GOOS == "windows" {
		name, body = "gh.cmd", "@echo off\r\nif not \"%~1\"==\"api\" exit /b 8\r\nif not \"%~2\"==\"user\" exit /b 8\r\nif not \"%~3\"==\"--jq\" exit /b 8\r\nif not \"%~4\"==\".login\" exit /b 8\r\nif not \"%~5\"==\"\" exit /b 8\r\nif not \"%GH_TOKEN%\"==\""+token+"\" exit /b 9\r\necho octocat\r\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(body), 0700))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	svc := programs.NewService(al.GetConfig, func() (*credentials.Store, error) { return store, nil }, filepath.Join(home, "programs", "connections.json"))
	_, err := svc.Publish("github", token, nil, nil)
	require.NoError(t, err)
	svc.Wait()
	require.Equal(t, "connected", svc.Connection("github").State, "Given: a real saved/checked GitHub token, not a pretend reference")
	return &wu5Fixture{t: t, al: al, bus: msgBus, provider: provider, home: home}, closeLoop
}

// programsWebTurn supplies the trusted server-side web input carrier, as the
// real WS handler does. It does not infer origin from model text or silently
// treat an omitted carrier as web. Omitted-producer tests deliberately do not
// call this helper. The WS producer itself is the separate source-guard/UAT axis.
func programsWebTurn(t *testing.T, f *wu5Fixture, sessionID, text string) {
	t.Helper()
	msg := bus.InboundMessage{Channel: "webchat", ChatID: sessionID, SessionID: sessionID, Content: text,
		Provenance: bus.NewProvenance(bus.OriginWeb)}
	_, _, err := f.al.processMessage(context.Background(), msg)
	require.NoError(t, err)
}

func TestProgramEnv_PerModeAndTool(t *testing.T) {
	t.Run("S18_gh_uses_the_token", func(t *testing.T) {
		f := newProgramsTurnFixture(t)
		sid := f.mainSession("sales", "dev")
		f.provider.setScript(sid, []providers.LLMResponse{wu5BashCall("s18-gh", "gh api user --jq .login"), wu5Stop("probe completed")})
		programsWebTurn(t, f, sid, "read the GitHub account name")
		f.waitForCalls(sid, 2, "Dev's actual gh call")
		require.Equal(t, "octocat", strings.TrimSpace(f.provider.resultForCall(sid, "s18-gh")), "S18: the real Dev child uses the saved token")
	})
	t.Run("S11_only_switched_on_agent_gets_the_token", func(t *testing.T) {
		f := newProgramsTurnFixture(t)
		work := filepath.Join(f.home, "workspaces", "sales", "work")
		require.NoError(t, os.MkdirAll(work, 0700))
		for _, row := range []struct{ agent, expected string }{{"mia", "0"}, {"dev", "16"}} {
			sid := f.mainSession("sales", row.agent)
			report := filepath.Join(work, row.agent+"-program-token-count")
			command := fmt.Sprintf("printenv GH_TOKEN | wc -c > %q; printf 'S11_CHILD_RAN'", report)
			f.provider.setScript(sid, []providers.LLMResponse{wu5Bash(command), wu5Stop("probe completed")})
			programsWebTurn(t, f, sid, "count the program token bytes without printing its value")
			f.waitForCalls(sid, 2, row.agent+" actual bash child")
			results, err := json.Marshal(f.provider.uniqueToolResultsFor(sid))
			require.NoError(t, err)
			require.Contains(t, string(results), "S11_CHILD_RAN", "the real bash child ran; a denied/missing tool cannot satisfy the zero control")
			body, err := os.ReadFile(report)
			require.NoError(t, err)
			// S11's exact outputs: printenv adds one newline to this 15-byte
			// token (16); an absent variable produces no stdout bytes (0).
			require.Equal(t, row.expected, strings.TrimSpace(string(body)), "S11: %s's real child environment", row.agent)
		}
	})
}
