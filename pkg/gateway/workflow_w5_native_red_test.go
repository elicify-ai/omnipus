//go:build goolm && stdjson

package gateway

// CHECK F17, S-53/S-98 and C-21: a scripted no-paid-call model drives the
// actual WS -> loop -> Skill -> question/card -> person answer -> write_file
// -> edit_skill path. No callback publishes an expected directory/result.
// The model's planned calls are inputs, not autonomous-model conformance.
// GREEN, mutations and proof-of-failability defer to independent CHECK.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const w5WrittenBody = "---\nname: trip-notes\ndescription: Notes for trips.\n---\n\nW5-TRIP-NOTES-LITERAL-8D\n"

// not-wire-format: owned native test resources, never a gateway payload.
type w5NativeHarness struct {
	home, sid string
	workflow  string // exact shipped fixture input after its frontmatter, not a returned result.
	loop      *agent.AgentLoop
	api       *restAPI
	bus       *bus.MessageBus
	questions *askuser.Registry
	model     *f25NativeModel // existing model-edge recorder; no product fake.
	conn      *websocket.Conn
	start     func() func()
}

func newW5NativeHarness(t *testing.T, publish bool) *w5NativeHarness {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvConfig, filepath.Join(home, "config.json"))
	t.Setenv(config.EnvBuiltinSkills, t.TempDir())
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	root := filepath.Join(home, "skills")
	_, err := skills.SyncBuiltins(root, nil)
	require.NoError(t, err, "native shipped workflow seed, not a handwritten workflow substitute")
	shipped, err := os.ReadFile(filepath.Join(root, "write-skill", "SKILL.md"))
	require.NoError(t, err)
	_, workflow, found := strings.Cut(string(shipped), "\n---\n")
	require.True(t, found, "the shipped fixture has its standard frontmatter terminator")
	workflow = strings.TrimSpace(workflow)
	require.Contains(t, workflow, "# Write or edit a skill (W5)")
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "native-consent-script"}
	cfg.Agents.Defaults.MaxToolIterations = 8 // actual load/question or write/category/final rounds.
	cfg.Agents.List = nil
	coreagent.SeedConfig(cfg)
	var omnipus config.AgentConfig
	for _, record := range cfg.Agents.List {
		if record.ID == "omnipus" {
			omnipus = record
		}
	}
	require.Equal(t, "omnipus", omnipus.ID, "official roster seed supplies the real role")
	store := agentstore.New(home)
	require.NoError(t, store.Create(omnipus.ID, &omnipus))
	persisted, err := store.Get(omnipus.ID)
	require.NoError(t, err)
	require.NotEmpty(t, persisted.SkillGrants["write-skill"], "real generation-bound workflow assignment")
	cfg.Agents.List = []config.AgentConfig{*persisted}
	cfg.Gateway.DevModeBypass = true                   // disposable gateway's existing WS authentication fixture.
	cfg.Tools.Manifest.Compressed = false              // discovery is not the W5 subject.
	cfg.Sandbox.SkillTrust = config.SkillTrustAllowAll // fixture input only; canonical C-3 migration is tested elsewhere.
	cfg.Sandbox.AutoApprove, cfg.Sandbox.GodMode = false, false
	for _, name := range []string{"Skill", "AskUserQuestion", "write_file", "edit_skill"} {
		cfg.Sandbox.ToolPolicies[name] = "allow"
	}
	require.NoError(t, config.SaveConfig(filepath.Join(home, "config.json"), cfg))
	call := func(id, name string, args map[string]any) providers.ToolCall {
		return providers.ToolCall{ID: id, Type: "function", Name: name, Arguments: args}
	}
	model := &f25NativeModel{steps: []providers.ToolCall{
		call("w5-load", "Skill", map[string]any{"name": "write-skill"}),
	}}
	if publish {
		model.steps = append(model.steps,
			call("w5-card", "AskUserQuestion", map[string]any{"questions": []any{
				map[string]any{"header": "Category", "question": "Which category for trip-notes?", "options": []any{map[string]any{"label": "Lifestyle"}, map[string]any{"label": "Research"}}},
				map[string]any{"header": "Publish", "question": "Create trip-notes on the global shelf, granted to no agent?", "context": w5WrittenBody, "options": []any{map[string]any{"label": "Go"}, map[string]any{"label": "Change"}, map[string]any{"label": "Cancel"}}},
			}}),
			call("w5-write", "write_file", map[string]any{"path": filepath.Join(root, "trip-notes", "SKILL.md"), "content": w5WrittenBody}),
			call("w5-category", "edit_skill", map[string]any{"action": "set_category", "skill": "trip-notes", "category": "lifestyle"}),
		)
	}
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	al := mustAgentLoop(t, cfg, mb, model)
	handler := newWSHandler(mb, al, "")
	t.Cleanup(handler.Wait)
	api := &restAPI{agentLoop: al, homePath: home, auditor: al.AuditLogger()}
	rc := &runContextWithOptions{ctx: context.Background(), cfg: cfg, homePath: home,
		configPath: filepath.Join(home, "config.json"), agentLoop: al,
		runningServices: &services{restAPIRef: api}, centralMCPReg: tools.NewMCPRegistry(),
		sysSkillsLoader: skills.NewSkillsLoader(cfg.AgentHomeBasePath(), root, os.Getenv(config.EnvBuiltinSkills)),
		sysSkillWriter:  skills.NewSkillWriter(root)}
	rc.wireSystemTools() // real gateway composition, not test-created edit_skill behavior.
	sink := &askUserCardSink{h: handler}
	reg := askuser.NewRegistry(al.GetSessionStore(), &askUserResumeDispatcher{msgBus: mb},
		askuser.Options{Sink: sink, Audit: &askUserAuditSink{al: al}})
	sink.delayFn = reg.EffectiveDefaultSafeDelay
	handler.askUserReg = reg
	al.SetAskUserRegistry(reg)
	t.Cleanup(reg.Quiesce)
	main, err := al.GetSessionStore().GetOrCreateMainSession(testHarnessWorkspaceMembershipID, "omnipus")
	require.NoError(t, err)
	inst, ok := al.GetRegistry().GetAgent("omnipus")
	require.True(t, ok)
	for _, name := range []string{"Skill", "AskUserQuestion", "write_file", "edit_skill"} {
		tool, found := inst.Tools.GetIncludingHidden(name)
		require.True(t, found, "native %s registration", name)
		require.Equal(t, "allow", tools.EffectiveToolPolicy(inst.LoadToolPolicy(), tool.Scope(), inst.AgentType, name), "fixture must not stop at unrelated ordinary-tool Ask/Deny")
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	conn := wu0aWSConn(t, srv)
	agentID := "omnipus"
	frame := gen.MessageFrame{Type: string(gen.WsFrameTypeMessage), AgentId: &agentID,
		SessionId: &main.ID, Content: "Write a new skill", WorkflowStart: &gen.WorkflowStart{Workflow: "W5"}}
	raw, err := json.Marshal(frame)
	require.NoError(t, err)
	require.Empty(t, wu0aSendAndCollect(t, conn, string(raw)), "S-53: real W5 entry without a target is admitted")
	require.Len(t, mb.InboundChan(), 1, "exactly one accepted native turn before the dispatcher starts")
	start := func() func() {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- al.Run(ctx) }()
		var once sync.Once
		stop := func() {
			once.Do(func() {
				cancel()
				select {
				case err := <-done:
					require.NoError(t, err, "owned native loop joins")
				case <-time.After(10 * time.Second):
					t.Error("fixture UNVERIFIED: native dispatcher did not join")
				}
			})
		}
		t.Cleanup(stop)
		return stop
	}
	return &w5NativeHarness{home: home, sid: main.ID, workflow: workflow, loop: al, api: api, bus: mb, questions: reg, model: model, conn: conn, start: start}
}

func assertW5NativeLoaded(t *testing.T, h *w5NativeHarness) []string {
	t.Helper()
	var loaded []string
	for _, request := range h.model.snapshot() {
		for _, message := range request {
			if message.Role == "tool" && message.ToolCallID == "w5-load" {
				loaded = append(loaded, message.Content)
			}
		}
	}
	require.NotEmpty(t, loaded, "S-53: actual Skill result must reach the real next model request")
	for _, result := range loaded {
		assert.Contains(t, result, h.workflow, "the complete shipped workflow input, not a fake load acknowledgement")
	}
	entries, err := h.loop.GetSessionStore().ReadTranscript(h.sid)
	require.NoError(t, err)
	var original []string
	for _, entry := range entries {
		if entry.Role == "user" && entry.Content == "Write a new skill" {
			original = append(original, entry.Content)
		}
	}
	assert.Equal(t, []string{"Write a new skill"}, original, "S-53 original person message retained exactly once")
	return loaded
}

func waitW5NativeFinal(t *testing.T, h *w5NativeHarness) {
	t.Helper()
	select {
	case reply := <-h.bus.OutboundChan():
		require.Equal(t, h.sid, reply.SessionID)
		require.Equal(t, "done", reply.Content, "a real loop completion is required; acknowledgement alone is not a publish oracle")
	case <-time.After(30 * time.Second):
		t.Fatal("fixture UNVERIFIED: W5 native turn did not complete; deadline is not feature refusal")
	}
}

func testWriteSkillWorkflowNative(t *testing.T) {
	t.Run("instrument W5 public entry loads actual shipped workflow", func(t *testing.T) {
		h := newW5NativeHarness(t, false)
		stop := h.start()
		waitW5NativeFinal(t, h)
		stop()
		assertW5NativeLoaded(t, h)
		assert.Empty(t, h.questions.PendingAll())
		assert.NoDirExists(t, filepath.Join(h.home, "skills", "trip-notes"))
	})
	t.Run("C-21 actual loaded workflow instructs edit_skill set_category", func(t *testing.T) {
		h := newW5NativeHarness(t, false)
		stop := h.start()
		waitW5NativeFinal(t, h)
		stop()
		for _, result := range assertW5NativeLoaded(t, h) {
			assert.Contains(t, result, "edit_skill", "C-21 names the native category writer in the actual model input")
			assert.Contains(t, result, "set_category", "C-21 requires the loaded workflow to tell Omnipus to set category outside the skill file")
		}
	})
	t.Run("S-98 real person category and Go lead to native bytes category permission and no grants", func(t *testing.T) {
		h := newW5NativeHarness(t, true)
		stop := h.start()
		var frame gen.AskUserQuestionFrame
		require.NoError(t, json.Unmarshal(wu15ReadFrame(t, h.conn, "ask_user_question"), &frame))
		require.Equal(t, h.sid, frame.Card.SessionId)
		require.Equal(t, "omnipus", frame.Card.AgentId)
		require.Equal(t, "pending", frame.Card.Status)
		require.Len(t, frame.Card.Questions, 2)
		assert.Equal(t, "Category", frame.Card.Questions[0].Header)
		require.Len(t, frame.Card.Questions[0].Options, 2)
		assert.Equal(t, []string{"Lifestyle", "Research"}, []string{frame.Card.Questions[0].Options[0].Label, frame.Card.Questions[0].Options[1].Label})
		assert.Equal(t, "Publish", frame.Card.Questions[1].Header)
		var publishChoices []string
		for _, option := range frame.Card.Questions[1].Options {
			publishChoices = append(publishChoices, option.Label)
		}
		assert.Equal(t, []string{"Go", "Change", "Cancel"}, publishChoices, "actual W5 card preserves the one approval question")
		require.NotNil(t, frame.Card.Questions[1].Context)
		assert.Equal(t, w5WrittenBody, *frame.Card.Questions[1].Context)
		assert.NoDirExists(t, filepath.Join(h.home, "skills", "trip-notes"), "no change before the person's Go")
		pending := h.questions.PendingAll()
		require.Len(t, pending, 1, "one actual durable question card, not a seeded approval")
		assert.Equal(t, frame.Card.CardId, pending[0].CardID)
		assertW5NativeLoaded(t, h)
		var answer gen.AskUserAnswerFrame
		require.NoError(t, json.Unmarshal([]byte(`{"type":"ask_user_answer","answers":[{"header":"Category","selected":["Lifestyle"]},{"header":"Publish","selected":["Go"]}]}`), &answer))
		answer.CardId, answer.SessionId = frame.Card.CardId, h.sid
		require.NoError(t, h.conn.WriteJSON(answer))
		var answered gen.AskUserQuestionFrame
		require.NoError(t, json.Unmarshal(wu15ReadFrame(t, h.conn, "ask_user_question"), &answered))
		assert.Equal(t, frame.Card.CardId, answered.Card.CardId)
		assert.Equal(t, "answered", answered.Card.Status, "native registry accepted the person's actual submission")
		require.Len(t, answered.Card.Answers, 2)
		assert.Equal(t, "Category", answered.Card.Answers[0].Header)
		assert.Equal(t, []string{"Lifestyle"}, answered.Card.Answers[0].Selected)
		assert.Equal(t, "Publish", answered.Card.Answers[1].Header)
		assert.Equal(t, []string{"Go"}, answered.Card.Answers[1].Selected)
		waitW5NativeFinal(t, h)
		stop()
		assert.Empty(t, h.questions.PendingAll())
		actual, err := os.ReadFile(filepath.Join(h.home, "skills", "trip-notes", "SKILL.md"))
		require.NoError(t, err, "actual write_file must create the final skill")
		assert.Equal(t, w5WrittenBody, string(actual), "C-21/S-98: category never inserted into skill content")
		w := httptest.NewRecorder()
		h.api.HandleSkills(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil))
		require.Equal(t, http.StatusOK, w.Code, "real inventory: %s", w.Body.String())
		var listed []gen.Skill
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listed))
		var named []gen.Skill
		for _, skill := range listed {
			if skill.Id == "trip-notes" {
				named = append(named, skill)
			}
		}
		require.Len(t, named, 1, "S-98 named skill listed exactly once")
		assert.Equal(t, gen.SkillCategory("lifestyle"), named[0].Category)
		agents, skipped, err := agentstore.New(h.home).List()
		require.NoError(t, err)
		require.Empty(t, skipped)
		require.NotEmpty(t, agents, "no-grant observer must inspect real protected entities")
		for _, record := range agents {
			assert.NotContains(t, record.Skills, "trip-notes", record.ID)
			assert.NotContains(t, record.SkillGrants, "trip-notes", record.ID)
		}
		t.Setenv("OMNIPUS_BEARER_TOKEN", permissionsTestBearer)
		routes := routeTable{}
		h.api.registerAdditionalEndpoints(routes)
		readback := getPermissions(t, h.api, routes)
		require.Equal(t, http.StatusOK, readback.Code, "real permissions read model: %s", readback.Body.String())
		var settings gen.PermissionSettings
		require.NoError(t, json.Unmarshal(readback.Body.Bytes(), &settings))
		assert.Equal(t, gen.ToolPolicyAsk, settings.SkillPolicies["trip-notes"], "S-98 new unverified skill starts Ask")
	})
}
