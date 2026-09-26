// max_tool_iterations_gaps_test.go — #904 test gaps from the 8-reviewer gate
// and CHECK, runtime side.
//
// Spec: docs/internal/specs/tool-iteration-limit-spec.md (Approved) —
// FR-004 / SC-002 (no hidden literal), FR-019 + scenario "Running turn keeps
// the limit it started with" (D18), D4 (external-CLI workers follow the same
// resolved limit). Expected values come from the spec, never from running
// the code.

package agent

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/routing"
)

// ---------------------------------------------------------------------------
// Item 5 — D18 / FR-019: a running turn keeps the limit it started with when
// the registry is rebuilt mid-turn; the next turn uses the new limit.
// ---------------------------------------------------------------------------

// midTurnReloadProvider always asks for a tool call; on its onCallN-th call
// it runs onCall (the mid-turn registry rebuild) before answering.
type midTurnReloadProvider struct {
	mu      sync.Mutex
	calls   int
	taken   int
	onCallN int
	onCall  func()
}

func (p *midTurnReloadProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	fire := p.calls == p.onCallN && p.onCall != nil
	p.mu.Unlock()
	if fire {
		p.onCall()
	}
	return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
		ID: "call_d18", Type: "function", Name: "tool_limit_test_tool", Arguments: map[string]any{"value": "x"},
	}}}, nil
}

func (p *midTurnReloadProvider) GetDefaultModel() string { return "d18-model" }

// takeCalls returns the model calls since the last take. Each tool round of
// a turn is exactly one model call (the final limit message is canned, not a
// model call — toolLimitResponse), so calls per turn = tool rounds per turn.
func (p *midTurnReloadProvider) takeCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := p.calls - p.taken
	p.taken = p.calls
	return n
}

func d18Config(home string, global int) *config.Config {
	return &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              home,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: global,
			},
			List: []config.AgentConfig{{ID: "mia", Home: home}},
		},
	}
}

// Scenario "Running turn keeps the limit it started with" at small numbers:
// global 3, agent with no own value; during the turn (2nd model call) the
// global is lowered to 1 and the registry is rebuilt exactly as a reload
// does (ReloadProviderAndConfig). The running turn still gets its 3 tool
// rounds; the rebuilt instance carries 1 and the next turn gets 1 round.
func TestLoop_RunningTurnKeepsStartLimit(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	prov := &midTurnReloadProvider{onCallN: 2}
	al := mustNewAgentLoop(t, d18Config(home, 3), bus.NewMessageBus(), prov)
	al.RegisterTool(&toolLimitTestTool{})

	started := al.GetRegistry().GetDefaultAgent()
	if started == nil || started.MaxIterations != 3 {
		t.Fatalf("instrument: the turn must start with limit 3, got %+v", started)
	}
	var reloadErr error
	prov.onCall = func() {
		reloadErr = al.ReloadProviderAndConfig(context.Background(), prov, d18Config(home, 1))
	}

	if _, err := al.ProcessDirectWithChannel(context.Background(), "hello", "d18-turn-1", "test", "chat1"); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if reloadErr != nil {
		t.Fatalf("mid-turn reload failed: %v", reloadErr)
	}
	rebuilt := al.GetRegistry().GetDefaultAgent()
	if rebuilt == nil || rebuilt == started {
		t.Fatal("instrument: the registry was not rebuilt mid-turn")
	}
	if rebuilt.MaxIterations != 1 {
		t.Fatalf("rebuilt instance MaxIterations = %d, want 1 (the new global)", rebuilt.MaxIterations)
	}
	if got := prov.takeCalls(); got != 3 {
		t.Fatalf("turn 1 ran %d tool rounds, want 3: a running turn keeps the limit in force when it started (D18, FR-019)", got)
	}
	// Second witness for turn 1, from the instance the turn ran on: history
	// = user + 3x(assistant, tool) + final = 8 (cf. TestLoop_StopsAtEffectiveLimit).
	route := al.GetRegistry().ResolveRoute(routing.RouteInput{
		Channel: "test",
		Peer:    &routing.RoutePeer{Kind: string(bus.PeerDirect), ID: "cron"},
	})
	if h := started.Sessions.GetHistory(route.SessionKey); len(h) != 8 {
		t.Fatalf("turn 1 history len = %d, want 8 (3 tool rounds)", len(h))
	}

	al.RegisterTool(&toolLimitTestTool{})
	if _, err := al.ProcessDirectWithChannel(context.Background(), "again", "d18-turn-2", "test", "chat1"); err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	if got := prov.takeCalls(); got != 1 {
		t.Fatalf("turn 2 ran %d tool rounds, want 1: the next turn uses the new limit (D18)", got)
	}
}

// ---------------------------------------------------------------------------
// Item 6 — D4: prepareRunOptions' fallback (instance MaxIterations <= 0)
// resolves from the LIVE config through the one resolver — never a literal.
// ---------------------------------------------------------------------------

func TestExternalDispatch_ZeroInstanceLimit_ResolvesFromLiveConfig(t *testing.T) {
	cases := []struct {
		name   string
		global int
		want   int
	}{
		// In range: the live global is the turn cap.
		{"live global 37", 37, 37},
		// Dataset "Saved global" rows 3 / 6: 0 → shipped default 200,
		// 1001 → 1000 (D13 in-memory correction).
		{"live global 0 runs as the shipped default", 0, config.DefaultMaxToolIterations},
		{"live global 1001 runs as 1000", 1001, 1000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(config.EnvHome, t.TempDir())
			al, ts := newExternalTestLoop(t, "claude-code", "")
			al.GetConfig().Agents.Defaults.MaxToolIterations = tc.global
			ts.agent.MaxIterations = 0 // an instance built without NewAgentInstance
			fr, restore := withFakeDriver(t)
			defer restore()
			go func() {
				fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
				fr.Cancel()
			}()
			if _, err := runExternalCLISubTurn(context.Background(), al, ts, "task", 30*time.Second); err != nil {
				t.Fatalf("runExternalCLISubTurn: %v", err)
			}
			opts := fr.RecordedRunOpts()
			if len(opts) != 1 {
				t.Fatalf("driver Run called %d times, want 1", len(opts))
			}
			if opts[0].MaxTurns != tc.want {
				t.Fatalf("MaxTurns = %d, want %d (resolved from the live global %d)", opts[0].MaxTurns, tc.want, tc.global)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Item 12 — FR-004 / SC-002 guard, broadened beyond the literal
// "maxIter = 200": ANY positive integer literal assigned to a limit-named
// identifier (max iterations / max tool iterations / max turns) in
// production Go code, outside the one shipped-default constant file.
// ---------------------------------------------------------------------------

// limitLiteralRe matches `<ident containing a limit name> (=|:=|:) <positive int>`.
var limitLiteralRe = regexp.MustCompile(
	`(?i)\b(\w*(?:maxiter|maxtooliteration|max_tool_iteration|maxturn|max_turn)\w*)\s*(?::=|=|:)\s*([1-9][0-9]*)\b`)

// limitLiteralAllowed lists the only permitted matches, each with its reason.
var limitLiteralAllowed = map[string]string{
	"pkg/config/max_tool_iterations.go:DefaultMaxToolIterations": "the one shipped-default constant FR-004 allows",
	"pkg/config/max_tool_iterations.go:MinMaxToolIterations":     "the 1-1000 bound (D2), not a default",
	"pkg/config/max_tool_iterations.go:MaxMaxToolIterations":     "the 1-1000 bound (D2), not a default",
	"pkg/gateway/rest_executor_smoketest.go:smokeTestMaxTurns":   "fixed cap of the CLI smoke-test probe, not an agent's limit",
}

func TestNoHiddenLimitLiteral_BroadGuard(t *testing.T) {
	repo := filepath.Join("..", "..")
	var hits []string
	seenAllowed := map[string]bool{}
	for _, top := range []string{"pkg", "cmd", "internal"} {
		root := filepath.Join(repo, top)
		if _, err := os.Stat(root); err != nil {
			continue
		}
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if info.Name() == "generated" || info.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			rel, _ := filepath.Rel(repo, path)
			rel = filepath.ToSlash(rel)
			for i, line := range strings.Split(string(raw), "\n") {
				if c := strings.Index(line, "//"); c >= 0 {
					line = line[:c]
				}
				for _, m := range limitLiteralRe.FindAllStringSubmatch(line, -1) {
					key := rel + ":" + m[1]
					if _, ok := limitLiteralAllowed[key]; ok {
						seenAllowed[key] = true
						continue
					}
					hits = append(hits, rel+":"+itoaLine(i+1)+": "+strings.TrimSpace(m[0]))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	// Instrument: the scan must SEE the one allowed constant, or an empty
	// hit list proves nothing.
	if !seenAllowed["pkg/config/max_tool_iterations.go:DefaultMaxToolIterations"] {
		t.Fatal("instrument check failed: the scan did not find DefaultMaxToolIterations = 200 — the walk or the pattern is broken")
	}
	for _, h := range hits {
		t.Errorf("hidden tool-iteration/turn-cap literal (FR-004: only the shipped-default constant may exist): %s", h)
	}
}

func itoaLine(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
