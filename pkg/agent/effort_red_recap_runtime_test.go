package agent

// CHECK-13 / S-79..S-84: execute the recap pipeline, not just GET/disk.
// The config-tools variant writes through the production registry and reopens
// disk before the next recap. Provider fakes are at the paid process edge.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recapRuntimeCall struct {
	messages       []providers.Message
	options        map[string]any
	deadline, time time.Time
}
type recapRuntimeProvider struct {
	mu     sync.Mutex
	calls  []recapRuntimeCall
	fail   bool
	resets int
}

func (*recapRuntimeProvider) GetDefaultModel() string { return "recap-runtime-model" }
func (p *recapRuntimeProvider) Chat(ctx context.Context, m []providers.Message, _ []providers.ToolDefinition, _ string, o map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	dl, _ := ctx.Deadline()
	p.calls = append(p.calls, recapRuntimeCall{m, o, dl, time.Now()})
	if len(p.calls) <= p.resets {
		return nil, errors.New("streaming read error: http2: response body closed")
	}
	if p.fail {
		return nil, errors.New("scripted summary failure")
	}
	return &providers.LLMResponse{Content: recapJSONResponse, FinishReason: "stop"}, nil
}
func (p *recapRuntimeProvider) snapshot() []recapRuntimeCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]recapRuntimeCall(nil), p.calls...)
}

func recapRuntimeConfig(t *testing.T, source string, values map[string]any) (*config.Config, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	path := filepath.Join(home, "config.json")
	defaults := map[string]any{"recap_model": "recap-runtime-model", "auto_recap_enabled": true, "bootstrap_recap_enabled": true, "bootstrap_recap_max_per_minute": 60}
	if source == "settings" {
		for k, v := range values {
			defaults[k] = v
		}
	}
	raw, err := json.Marshal(map[string]any{"version": 1, "agents": map[string]any{"defaults": defaults}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0600))
	cfg, err := config.LoadConfig(path)
	require.NoError(t, err)
	if source == "config-tool" {
		deps := &systools.Deps{Home: home, ConfigPath: path, GetCfg: func() *config.Config { return cfg }, MutateConfig: func(fn func(*config.Config) error) error { return fn(cfg) }, SaveConfigLocked: func(c *config.Config) error { return config.SaveConfig(path, c) }}
		reg := tools.NewToolRegistry()
		reg.Register(systools.NewConfigSetTool(deps))
		reg.Register(systools.NewConfigGetTool(deps))
		for k, v := range values {
			result := reg.Execute(context.Background(), "set_config", map[string]any{"key": "agents.defaults." + k, "value": v})
			require.False(t, result.IsError, "S-83 real registry write %s: %s", k, result.ForLLM)
		}
		// S-83 observes both public read forms before the next recap. Compare
		// JSON-normalized values so an integer's wire representation is exact.
		for k, v := range values {
			for _, key := range []string{"agents.defaults." + k, "agents.defaults"} {
				res := reg.Execute(context.Background(), "get_config", map[string]any{"key": key})
				require.False(t, res.IsError, "S-83 read %s: %s", key, res.ForLLM)
				var payload map[string]any
				require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &payload))
				got := payload["value"]
				if key == "agents.defaults" {
					section, ok := got.(map[string]any)
					require.True(t, ok)
					got = section[k]
				}
				encoded, err := json.Marshal(v)
				require.NoError(t, err)
				var want any
				require.NoError(t, json.Unmarshal(encoded, &want))
				require.Equal(t, want, got, "S-83 exact unredacted read %s/%s", key, k)
			}
		}
		cfg, err = config.LoadConfig(path)
		require.NoError(t, err)
	}
	return cfg, path
}

func runRuntimeRecap(t *testing.T, cfg *config.Config, p *recapRuntimeProvider, input string) (*agentLoopRunRecap, []recapRuntimeCall) {
	t.Helper()
	al, sid, ag, _ := buildEffortRecapLoop(t, cfg, p)
	rr := &agentLoopRunRecap{al: al, sessionID: sid, trigger: "test", agentInst: ag, userTurns: []string{input}}
	rr.callCandidates()
	return rr, p.snapshot()
}

func TestT_L4_S79_S80_S83_RecapPromptInputDeadlineAndRetries(t *testing.T) {
	rows := []struct {
		key          string
		value        any
		tail         int
		words, items int
		deadline     time.Duration
		calls        int
		backoffs     []string
	}{
		{key: "defaults", tail: 8000, words: 150, items: 5, deadline: 60 * time.Second, calls: 3, backoffs: []string{"500", "1000"}},
		{key: "recap_max_output_tokens", value: 4096},
		{key: "recap_input_tokens", value: 500, tail: 2000},
		{key: "recap_summary_words", value: 80, words: 80},
		{key: "recap_list_items", value: 3, items: 3},
		{key: "recap_timeout_seconds", value: 15, deadline: 15 * time.Second},
		{key: "recap_stream_retries", value: 0, calls: 1},
		{key: "recap_retry_pause_ms", value: 200, calls: 3, backoffs: []string{"200", "400"}},
		{key: "recap_chars_per_token", value: 3, tail: 6000},
	}
	for _, source := range []string{"settings", "config-tool"} {
		for _, row := range rows {
			// S-80 uses 80; S-83's independently specified config-tool row
			// uses 100. Both must affect the actual prompt.
			if source == "config-tool" && row.key == "recap_summary_words" {
				row.value, row.words = 100, 100
			}
			t.Run(source+"/"+row.key, func(t *testing.T) {
				values := map[string]any{}
				if row.value != nil {
					values[row.key] = row.value
				}
				cfg, _ := recapRuntimeConfig(t, source, values)
				captureFileLog(t)
				p := &recapRuntimeProvider{}
				if row.calls > 0 {
					p.resets = 2
				}
				input := "OLD-MUST-TRIM-" + strings.Repeat("x", 10000) + "-NEWEST-7731"
				start := time.Now()
				_, calls := runRuntimeRecap(t, cfg, p, input)
				require.NotEmpty(t, calls, "real recap called the provider")
				first := calls[0]
				if row.tail > 0 {
					parts := strings.SplitN(first.messages[0].Content, "\n\nSummarize this conversation", 2)
					require.Len(t, parts, 2)
					want := "[history truncated for summarisation]\n\n" + input[len(input)-row.tail:]
					assert.Equal(t, want, parts[0], "settings change the actual history tail")
				}
				if row.words > 0 {
					assert.Contains(t, first.messages[0].Content, fmt.Sprintf("≤ %d words", row.words))
				}
				if row.items > 0 {
					assert.Equal(t, 3, strings.Count(first.messages[0].Content, fmt.Sprintf("up to %d", row.items)), "each of the three lists uses the configured limit")
				}
				cap := 2048
				if row.key == "recap_max_output_tokens" {
					cap = 4096
				}
				assert.EqualValues(t, cap, first.options["max_tokens"])
				if row.deadline > 0 {
					assert.InDelta(t, start.Add(row.deadline).UnixMilli(), first.deadline.UnixMilli(), 1000, "actual received deadline is the whole configured budget")
				}
				if row.calls > 0 {
					assert.Len(t, calls, row.calls, "zero stays zero; defaults allow exactly two reset retries")
				}
				if row.backoffs != nil {
					matches := regexp.MustCompile(`backoff_ms=([0-9]+)`).FindAllStringSubmatch(slogBufForTest.String(), -1)
					got := []string{}
					for _, m := range matches {
						got = append(got, m[1])
					}
					assert.Equal(t, row.backoffs, got, "assert computed pause schedule, not elapsed-time guesses")
				}
			})
		}
	}
}

func TestT_L4_S79_S80_S83_FallbackKeepsConfiguredTail(t *testing.T) {
	for _, source := range []string{"settings", "config-tool"} {
		for _, limit := range []int{1500, 300} {
			t.Run(fmt.Sprintf("%s/chars=%d", source, limit), func(t *testing.T) {
				values := map[string]any{}
				if limit != 1500 {
					values["recap_fallback_context_chars"] = limit
				}
				cfg, _ := recapRuntimeConfig(t, source, values)
				p := &recapRuntimeProvider{fail: true}
				al, sid, ag, store := buildEffortRecapLoop(t, cfg, p)
				input := strings.Repeat("A", 2200) + "BEGIN-TAIL-" + strings.Repeat("B", 289)
				require.NoError(t, store.AppendTranscriptStrict(sid, session.TranscriptEntry{Role: "user", Content: input, Timestamp: time.Now().UTC()}))
				runRecapAndWait(t, al, sid, wu21ReaimHome(ag), ag.ID)
				data, err := os.ReadFile(wu21ReaimPairPath(wu21ReaimHome(ag), ag.ID))
				require.NoError(t, err)
				parts := strings.SplitN(string(data), "Recent context (verbatim — automatic summary unavailable):\n", 2)
				require.Len(t, parts, 2)
				assert.Equal(t, input[len(input)-limit:], strings.TrimSpace(parts[1]), "fallback setting affects kept context, not only stored settings")
			})
		}
	}
}

func rewriteRecapAge(t *testing.T, store *session.UnifiedStore, sid string, age time.Duration) {
	t.Helper()
	file := filepath.Join(store.BaseDir(), sid, "transcript.jsonl")
	raw, err := os.ReadFile(file)
	require.NoError(t, err)
	out := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &m))
		m["timestamp"] = time.Now().Add(-age).UTC().Format(time.RFC3339Nano)
		b, err := json.Marshal(m)
		require.NoError(t, err)
		out = append(out, string(b))
	}
	require.NoError(t, os.WriteFile(file, []byte(strings.Join(out, "\n")+"\n"), 0600))
}

func TestT_L4_S84_BootstrapUsesConfiguredIdleAge(t *testing.T) {
	for _, row := range []struct {
		idle       int
		due, young time.Duration
	}{{10, 12 * time.Minute, 8 * time.Minute}, {30, 31 * time.Minute, 29 * time.Minute}} {
		t.Run(fmt.Sprintf("idle=%d", row.idle), func(t *testing.T) {
			values := map[string]any{}
			if row.idle != 30 {
				values["idle_timeout_minutes"] = row.idle
			}
			cfg, _ := recapRuntimeConfig(t, "settings", values)
			p := &recapRuntimeProvider{}
			al, due, ag, store := buildEffortRecapLoop(t, cfg, p)
			rewriteRecapAge(t, store, due, row.due)
			young, err := store.NewSession(session.SessionTypeChat, "web", ag.ID)
			require.NoError(t, err)
			wu21ReaimStampWorkspace(t, store, young.ID)
			require.NoError(t, store.AppendTranscriptStrict(young.ID, session.TranscriptEntry{Role: "user", Content: "YOUNG-DO-NOT-RECAP-7731", Timestamp: time.Now().Add(-row.young).UTC()}))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			al.BootstrapRecapPass(ctx)
			al.recapWG.Wait()
			calls := p.snapshot()
			require.Len(t, calls, 1, "only the due session is recapped")
			assert.Contains(t, calls[0].messages[0].Content, "hello recap")
			assert.NotContains(t, calls[0].messages[0].Content, "YOUNG-DO-NOT-RECAP-7731")
		})
	}
}

func TestT_L4_S79_S80_S83_BootstrapSpacing(t *testing.T) {
	for _, source := range []string{"settings", "config-tool"} {
		for _, gap := range []int{1, 20} {
			t.Run(fmt.Sprintf("%s/gap=%d", source, gap), func(t *testing.T) {
				values := map[string]any{}
				if gap != 1 {
					values["bootstrap_recap_min_spacing_seconds"] = gap
				}
				cfg, _ := recapRuntimeConfig(t, source, values)
				p := &recapRuntimeProvider{}
				al, first, ag, store := buildEffortRecapLoop(t, cfg, p)
				rewriteRecapAge(t, store, first, 31*time.Minute)
				second, err := store.NewSession(session.SessionTypeChat, "web", ag.ID)
				require.NoError(t, err)
				wu21ReaimStampWorkspace(t, store, second.ID)
				require.NoError(t, store.AppendTranscriptStrict(second.ID, session.TranscriptEntry{Role: "user", Content: "second due", Timestamp: time.Now().Add(-31 * time.Minute).UTC()}))
				ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
				defer cancel()
				al.BootstrapRecapPass(ctx)
				al.recapWG.Wait()
				calls := p.snapshot()
				require.Len(t, calls, 2, "positive control: both due recaps really start")
				assert.GreaterOrEqual(t, calls[1].time.Sub(calls[0].time), time.Duration(gap)*time.Second-50*time.Millisecond, "spacing setting controls actual catch-up starts")
			})
		}
	}
}

// Discover only whether the specified config key exists. Its effective value
// is observed below at the actual recap boundary, never through a guessed
// getter name (the spec defines behavior, not a Go method spelling).
func requireRecapConfigKey(t *testing.T, d *config.AgentDefaults, key string) {
	t.Helper()
	typ := reflect.TypeOf(*d)
	for i := 0; i < typ.NumField(); i++ {
		if strings.Split(typ.Field(i).Tag.Get("json"), ",")[0] == key {
			return
		}
	}
	t.Fatalf("BLOCKED: config key agents.defaults.%s not implemented — required by S-81/FR-074", key)
}

func exerciseClampedRecap(t *testing.T, cfg *config.Config, key string, maximum int) {
	t.Helper()
	p := &recapRuntimeProvider{}
	if key == "bootstrap_recap_min_spacing_seconds" || key == "idle_timeout_minutes" || key == "bootstrap_recap_max_per_minute" {
		al, sid, _, store := buildEffortRecapLoop(t, cfg, p)
		age := 31 * time.Minute
		if key == "idle_timeout_minutes" {
			age = 1440*time.Minute + 30*time.Second
		}
		rewriteRecapAge(t, store, sid, age)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		al.BootstrapRecapPass(ctx)
		al.recapWG.Wait()
		require.Len(t, p.snapshot(), 1, "actual bootstrap executes the clamped setting path")
		return
	}
	if key == "recap_fallback_context_chars" {
		p.fail = true
		al, sid, ag, store := buildEffortRecapLoop(t, cfg, p)
		input := strings.Repeat("x", maximum+100) + "TAIL-7731"
		require.NoError(t, store.AppendTranscriptStrict(sid, session.TranscriptEntry{Role: "user", Content: input, Timestamp: time.Now().UTC()}))
		runRecapAndWait(t, al, sid, wu21ReaimHome(ag), ag.ID)
		raw, err := os.ReadFile(wu21ReaimPairPath(wu21ReaimHome(ag), ag.ID))
		require.NoError(t, err)
		parts := strings.SplitN(string(raw), "Recent context (verbatim — automatic summary unavailable):\n", 2)
		require.Len(t, parts, 2)
		assert.Equal(t, input[len(input)-maximum:], strings.TrimSpace(parts[1]))
		return
	}
	if key == "recap_stream_retries" {
		p.resets = maximum + 1
	}
	if key == "recap_retry_pause_ms" {
		p.resets = 1
	}
	input := strings.Repeat("x", 140000) + "NEWEST-7731"
	start := time.Now()
	_, calls := runRuntimeRecap(t, cfg, p, input)
	require.NotEmpty(t, calls)
	first := calls[0]
	switch key {
	case "recap_max_output_tokens":
		assert.EqualValues(t, maximum, first.options["max_tokens"])
	case "recap_summary_words":
		assert.Contains(t, first.messages[0].Content, fmt.Sprintf("≤ %d words", maximum))
	case "recap_list_items":
		assert.Equal(t, 3, strings.Count(first.messages[0].Content, fmt.Sprintf("up to %d", maximum)))
	case "recap_timeout_seconds":
		assert.InDelta(t, start.Add(time.Duration(maximum)*time.Second).UnixMilli(), first.deadline.UnixMilli(), 1000)
	case "recap_stream_retries":
		assert.Len(t, calls, maximum+1)
	case "recap_retry_pause_ms":
		assert.Contains(t, slogBufForTest.String(), fmt.Sprintf("backoff_ms=%d", maximum))
	case "recap_input_tokens", "recap_chars_per_token":
		tail := maximum * 4
		if key == "recap_chars_per_token" {
			tail = maximum * 2000
		}
		parts := strings.SplitN(first.messages[0].Content, "\n\nSummarize this conversation", 2)
		require.Len(t, parts, 2)
		assert.Equal(t, "[history truncated for summarisation]\n\n"+input[len(input)-tail:], parts[0])
	}
}

func TestT_L4_S81_S83_ClampOnlyInUseWarnsOnceAndPreservesDisk(t *testing.T) {
	ranges := []struct {
		key string
		max int
	}{{"recap_max_output_tokens", 16384}, {"recap_input_tokens", 32000}, {"recap_summary_words", 500}, {"recap_list_items", 10}, {"recap_fallback_context_chars", 8000}, {"recap_timeout_seconds", 300}, {"recap_stream_retries", 5}, {"recap_retry_pause_ms", 5000}, {"recap_chars_per_token", 8}, {"bootstrap_recap_min_spacing_seconds", 60}, {"idle_timeout_minutes", 1440}, {"bootstrap_recap_max_per_minute", 60}}
	for _, row := range ranges {
		t.Run(row.key, func(t *testing.T) {
			captureFileLog(t)
			cfg, path := recapRuntimeConfig(t, "settings", map[string]any{row.key: row.max + 1})
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			requireRecapConfigKey(t, &cfg.Agents.Defaults, row.key)
			exerciseClampedRecap(t, cfg, row.key, row.max)
			exerciseClampedRecap(t, cfg, row.key, row.max)
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after, "in-use clamp must not rewrite saved config")
			lines := []string{}
			for _, line := range strings.Split(slogBufForTest.String(), "\n") {
				if strings.Contains(line, row.key) && strings.Contains(strings.ToLower(line), "warn") {
					lines = append(lines, line)
				}
			}
			require.Len(t, lines, 1, "exactly one warning per clamped key despite repeated use")
			assert.Contains(t, lines[0], fmt.Sprint(row.max+1))
			assert.Contains(t, lines[0], fmt.Sprint(row.max))
		})
	}
	t.Run("tool1000_stored_used500", func(t *testing.T) {
		captureFileLog(t)
		cfg, path := recapRuntimeConfig(t, "config-tool", map[string]any{"recap_summary_words": 1000})
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"recap_summary_words": 1000`)
		reg := tools.NewToolRegistry()
		reg.Register(systools.NewConfigGetTool(&systools.Deps{Home: filepath.Dir(path), ConfigPath: path, GetCfg: func() *config.Config { return cfg }}))
		for _, key := range []string{"agents.defaults.recap_summary_words", "agents.defaults"} {
			res := reg.Execute(context.Background(), "get_config", map[string]any{"key": key})
			require.False(t, res.IsError, "%s: %s", key, res.ForLLM)
			var payload map[string]any
			require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &payload))
			value := payload["value"]
			if key == "agents.defaults" {
				value = value.(map[string]any)["recap_summary_words"]
			}
			assert.Equal(t, float64(1000), value, "both config-tool reads expose written 1000, not the effective clamp")
		}
		_, calls := runRuntimeRecap(t, cfg, &recapRuntimeProvider{}, "hello")
		require.NotEmpty(t, calls)
		assert.Contains(t, calls[0].messages[0].Content, "≤ 500 words")
		matches := 0
		for _, line := range strings.Split(slogBufForTest.String(), "\n") {
			if strings.Contains(line, "recap_summary_words") && strings.Contains(strings.ToLower(line), "warn") {
				matches++
				assert.Contains(t, line, "1000")
				assert.Contains(t, line, "500")
			}
		}
		assert.Equal(t, 1, matches)
	})
}
