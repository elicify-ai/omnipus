package gateway

// SCRATCH COMPOSITION DRAFT ONLY; not installed, formatted, compiled or run.
// Intended primary: pkg/gateway/goal_pause_b1_boot_contract_test.go.
// Actual boot handler + SAME exported PlanEngine.Tick; no future hook/API.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/notifications"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const b1CompOwner = "b1-fixture-owner"
const b1CompTitle = "B1 fixture chat"
const b1CompPrivateOutput = "B1_SYNTHETIC_OUTPUT_PRIVATE_68 question=fixture-only approval=fixture-only"
const b1CompOutputToolName = "b1_private_edge"

type b1CompOutputTool struct {
	tools.BaseTool
	calls atomic.Int32
}

func (*b1CompOutputTool) Name() string        { return b1CompOutputToolName }
func (*b1CompOutputTool) Description() string { return "B1 external output privacy fixture" }
func (*b1CompOutputTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (*b1CompOutputTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (p *b1CompOutputTool) Execute(context.Context, map[string]any) *tools.ToolResult {
	p.calls.Add(1)
	return &tools.ToolResult{ForLLM: b1CompPrivateOutput}
}

var b1CompFailure = errors.New("B1 controlled provider failure")

type b1CompCall struct {
	record *session.LifecycleRecord
	err    error
}
type b1CompProvider struct {
	f                *b1CompFixture
	mu               sync.Mutex
	mode, evidence   string
	twice            bool
	sawPrivateOutput bool
	round            int
	entered          chan b1CompCall
}

func (*b1CompProvider) GetDefaultModel() string { return "b1-controlled-provider" }
func (p *b1CompProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	r, err := p.f.al.GetSessionLifecycleStore().Load(p.f.sid)
	select {
	case p.entered <- b1CompCall{r, err}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	p.mu.Lock()
	mode, evidence, twice := p.mode, p.evidence, p.twice
	p.round++
	round := p.round
	for _, m := range messages {
		if strings.Contains(m.Content, b1CompPrivateOutput) {
			p.sawPrivateOutput = true
		}
	}
	p.mu.Unlock()
	if mode == "fail" {
		return nil, b1CompFailure
	}
	if mode == "output" && round == 1 {
		return &providers.LLMResponse{FinishReason: "tool_calls", ToolCalls: []providers.ToolCall{{ID: "b1-output-proof", Name: b1CompOutputToolName, Arguments: map[string]any{}}}}, nil
	}
	if (mode == "blocked" || mode == "waiting_on_user") && round == 1 {
		calls := []providers.ToolCall{{ID: fmt.Sprintf("b1-real-claim-%d", p.f.seq), Name: "goal_claim", Arguments: map[string]any{"status": mode, "evidence": evidence}}}
		if twice {
			calls = append(calls, providers.ToolCall{ID: fmt.Sprintf("b1-real-claim-repeat-%d", p.f.seq), Name: "goal_claim", Arguments: map[string]any{"status": mode, "evidence": evidence}})
		}
		return &providers.LLMResponse{FinishReason: "tool_calls", ToolCalls: calls}, nil
	}
	return &providers.LLMResponse{Content: "B1 controlled natural reply"}, nil
}

type b1CompFixture struct {
	al                            *agent.AgentLoop
	rs                            *services
	h                             *WSHandler
	mb                            *bus.MessageBus
	cfg                           *config.Config
	pe                            *agent.PlanEngine
	p                             *b1CompProvider
	outputTool                    *b1CompOutputTool
	sid, gid, home, chat, logPath string
	wc                            *wsConn
	runCancel                     context.CancelFunc
	runDone                       chan error
	seq                           int
	mu                            sync.Mutex
	events                        []agent.Event
	observeStop                   func()
	sub                           agent.EventSubscription
	shared                        bool
	liveOwn, liveForeign          <-chan []byte
	stopLive                      func()
	cleanupOnce                   sync.Once
}

func b1CompBoot(t *testing.T, shared, accountless bool) *b1CompFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	// Configure ONLY the existing gateway logging startup functions; restore after
	// all services/ordinary owners have joined. No test-created logger or filter.
	level := logger.GetLevel()
	previousSlog := slog.Default()
	nativePath := filepath.Join(t.TempDir(), "b1-native-private.jsonl")
	require.NoError(t, logger.EnableFileLogging(nativePath))
	t.Cleanup(func() { slog.SetDefault(previousSlog); logger.DisableFileLogging(); logger.SetLevel(level) })
	logger.SetLevel(logger.WARN)
	installSlogBridge()
	logger.WarnCF("agent", "B1 native join instrument control", map[string]any{"control": true})
	require.Equal(t, 1, b1CompLogCount(t, nativePath, "B1 native join instrument control"))
	controlErr := errors.New("B1 log instrument error control")
	slog.Error("B1 existing bridge instrument control", "error", controlErr)
	require.Equal(t, 1, b1CompActualErrorCount(t, nativePath, controlErr), "SETUP: actual error severity and structured error must survive the existing boot bridge")
	cfg := seededBootConfig(t)
	cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
	cfg.Gateway.Host = "127.0.0.1"
	cfg.Gateway.Port = 0
	cfg.Gateway.Users = []config.UserConfig{{Username: b1CompOwner}, {Username: "b1-foreign-viewer"}}
	if accountless {
		cfg.Gateway.Users = nil
	}
	cfg.Sandbox.ToolPolicies[b1CompOutputToolName] = "allow" // test-only external edge, no product policy changed
	mb := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, mb, &restMockProvider{})
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x41}, 32)))
	bootCtx, bootCancel := context.WithCancel(context.Background())
	t.Cleanup(bootCancel)
	rs, err := setupAndStartServices(bootCtx, cfg, credentials.SecretBundle{}, al, mb, home, store, &SandboxApplyResult{}, tools.NewBuiltinRegistry(), tools.NewMCPRegistry(), false)
	require.NoError(t, err, "SETUP: genuine top-level boot must succeed")
	t.Cleanup(func() { bootCancel(); stopAndCleanupServices(rs, 5*time.Second, false) })
	require.NotNil(t, rs.PlanEngine)
	require.Same(t, rs.PlanEngine, agent.GetPlanEngine(al))
	ch, ok := rs.ChannelManager.GetChannel("webchat")
	require.True(t, ok)
	web, ok := ch.(*webchatChannel)
	require.True(t, ok)
	require.NotNil(t, web.wsHandler)
	// Join boot's periodic loops, retaining THIS object for public manual Tick.
	rs.PlanEngine.Stop()
	f := &b1CompFixture{al: al, rs: rs, h: web.wsHandler, mb: mb, cfg: cfg, pe: rs.PlanEngine, home: home, chat: "b1-real-intake", shared: shared || accountless, logPath: nativePath}
	t.Cleanup(func() {
		f.cleanupOnce.Do(func() {
			defer f.pe.Stop()
			defer func() {
				if f.observeStop != nil {
					f.observeStop()
				}
			}()
			defer func() {
				if f.stopLive != nil {
					f.stopLive()
				}
			}()
			f.joinReceiver(t)
		})
	}) // services stop next; mustAgentLoop's registered Close precedes logger restoration.
	f.p = &b1CompProvider{f: f, mode: "text", entered: make(chan b1CompCall, 128)}
	inst, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	inst.Provider = f.p
	f.outputTool = &b1CompOutputTool{}
	al.RegisterTool(f.outputTool)
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	f.sid = meta.ID
	owner := b1CompOwner
	if shared || accountless {
		owner = ""
	}
	title := b1CompTitle
	require.NoError(t, al.GetSessionStore().SetMeta(f.sid, session.MetaPatch{Owner: &owner, Title: &title}))
	f.wc, _ = makeForwarderTestConn(4096)
	f.wc.userID = b1CompOwner
	if accountless {
		f.wc.userID = ""
	}
	bindTestConnToSession(f.h, f.chat, f.sid, f.wc)
	f.sub = al.SubscribeEvents(4096)
	var observerOnce sync.Once
	f.observeStop = func() { observerOnce.Do(func() { al.UnsubscribeEvents(f.sub.ID) }) }
	f.startLive(t)
	f.user(t, "text", "") // REAL handler stamps genuine UserInitiated; no manual bus input.
	return f
}
func (f *b1CompFixture) setMode(mode, evidence string) {
	f.p.mu.Lock()
	f.p.mode = mode
	f.p.evidence = evidence
	f.p.round = 0
	f.p.mu.Unlock()
	for len(f.p.entered) > 0 {
		<-f.p.entered
	}
}
func (f *b1CompFixture) startReceiver(t *testing.T) {
	t.Helper()
	require.Nil(t, f.runCancel)
	ctx, cancel := context.WithCancel(context.Background())
	f.runCancel = cancel
	f.runDone = make(chan error, 1)
	go func() { f.runDone <- f.al.Run(ctx) }()
}
func (f *b1CompFixture) joinReceiver(t *testing.T) {
	t.Helper()
	if f.runCancel == nil {
		return
	}
	f.runCancel()
	err := <-f.runDone
	f.runCancel = nil
	f.runDone = nil
	require.NoError(t, err)
	// Source Run waits actual worker.done; a bounded-drain warning INVALIDATES this proof.
	require.Equal(t, 0, b1CompLogCount(t, f.logPath, "Session worker did not drain within shutdown budget"), "INVALID fixture: full ordinary receiver/owner tail did not join")
}
func b1CompWait(t *testing.T, why string, condition func() bool) {
	t.Helper()
	until := time.NewTimer(10 * time.Second)
	defer until.Stop()
	poll := time.NewTicker(5 * time.Millisecond)
	defer poll.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-poll.C:
		case <-until.C:
			t.Fatal("INVALID fixture: " + why)
			return
		}
	}
}
func (f *b1CompFixture) finishProduced(t *testing.T) {
	t.Helper()
	var call b1CompCall
	select {
	case call = <-f.p.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("INVALID fixture: actual provider admission not reached")
	}
	require.NoError(t, call.err)
	require.NotNil(t, call.record)
	require.NotNil(t, call.record.ExecutionID)
	require.Equal(t, session.LifecycleRunning, call.record.State)
	runID := call.record.ExecutionID.RunID
	b1CompWait(t, "producing execution did not reach a canonical terminal state", func() bool {
		r, err := f.al.GetSessionLifecycleStore().Load(f.sid)
		return err == nil && r.Terminal() && r.ExecutionID != nil && r.ExecutionID.RunID == runID
	})
	// Terminal state is only a premise; the full normal receiver drain is the join.
	f.joinReceiver(t)
	r, err := session.NewLifecycleStore(f.al.GetSessionLifecycleStore().Dir()).Load(f.sid)
	require.NoError(t, err)
	require.True(t, r.Terminal())
	require.Equal(t, runID, r.ExecutionID.RunID)
}
func (f *b1CompFixture) user(t *testing.T, mode, evidence string) {
	t.Helper()
	f.setMode(mode, evidence)
	f.seq++
	f.h.handleChatMessageWithClientID(context.Background(), f.chat, f.sid, "B1 genuine fixture input", "mia", nil, "", "", fmt.Sprintf("b1-input-%d", f.seq), nil, f.wc)
	// Shared ownership is actual store data, not a metadata-read failure or fabricated principal.
	if f.shared {
		empty := ""
		require.NoError(t, f.al.GetSessionStore().SetMeta(f.sid, session.MetaPatch{Owner: &empty}))
	}
	f.startReceiver(t)
	f.finishProduced(t)
	if mode == "blocked" || mode == "waiting_on_user" {
		g, err := goal.NewStore(f.home).Get(f.gid)
		require.NoError(t, err)
		require.NotNil(t, g.LatestClaim)
		require.Equal(t, mode, string(g.LatestClaim.Status), "SETUP: actual successful goal_claim must persist before a missing bell is meaningful")
		require.Equal(t, "active", string(g.State))
		require.Equal(t, f.sid, g.ActiveSessionID)
		f.collectEvents()
		positive := false
		for _, e := range f.events {
			p, ok := e.Payload.(agent.GoalStatusChangedPayload)
			if e.Kind == agent.EventKindGoalStatusChanged && ok && p.SessionID == f.sid && p.GoalID == f.gid && p.State == mode {
				positive = true
			}
		}
		require.True(t, positive, "SETUP: actual existing claim-pause status must be positive before any missing-bell RED")
	}
}

func (f *b1CompFixture) arm(t *testing.T) {
	t.Helper()
	ac := task.AcceptanceCriterion{ID: "b1-ac", Kind: task.KindProse, Judgment: task.JudgmentBoolean, Text: "Fixture work complete", Status: task.CritPending, Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: b1CompOwner}}
	dod := ac
	dod.ID = "b1-dod"
	dod.Text = "Independent evidence verified"
	g, err := goal.New(gen.GoalOwnerKindSession, f.sid, gen.GoalSourceChatCompiled, "Fixture goal", "", []task.AcceptanceCriterion{ac}, []task.AcceptanceCriterion{dod}, 5, time.Now().UTC())
	require.NoError(t, err)
	gs := goal.NewStore(f.home)
	require.NoError(t, gs.Create(g))
	_, err = gs.Update(g.GoalID, func(cur *goal.Goal) error { return cur.Activate(f.sid, time.Now().UTC()) })
	require.NoError(t, err)
	f.gid = g.GoalID
	f.user(t, "text", "") // actual goal entry captures its real webchat routing.
}
func (f *b1CompFixture) age(t *testing.T) {
	t.Helper()
	past := time.Now().UTC().Add(-time.Hour)
	_, err := goal.NewStore(f.home).Update(f.gid, func(g *goal.Goal) error { g.LastActivityAt = past; g.StartedAt = &past; return nil })
	require.NoError(t, err)
}
func (f *b1CompFixture) tickProduced(t *testing.T, mode string) {
	t.Helper()
	f.setMode(mode, "")
	f.age(t)
	f.startReceiver(t)
	f.pe.Tick(context.Background())
	f.finishProduced(t)
}
func (f *b1CompFixture) pause(t *testing.T, failed bool) {
	t.Helper()
	n := 5
	mode := "text"
	if failed {
		n = 3
		mode = "fail"
	}
	for i := 0; i < n; i++ {
		f.tickProduced(t, mode)
	}
	f.age(t)
	f.pe.Tick(context.Background()) // FIRST retry gate assesses final settled producer.
	g, err := goal.NewStore(f.home).Get(f.gid)
	require.NoError(t, err)
	want := "No progress after 5 reminders — goal paused. Reply to continue."
	if failed {
		want = "3 reminder turns failed in a row — goal paused. Reply to continue."
	} else {
		require.Equal(t, 5, g.ZeroOutputPushes)
	}
	// Existing public pause line is preserved; exact cause source was read, no run-derived oracle.
	require.Equal(t, want, g.LatestReason, "SETUP: actual old pause, not empty bell alone, is the prerequisite")
	require.Equal(t, "active", string(g.State))
	require.Equal(t, f.sid, g.ActiveSessionID)
	entries, err := f.al.GetSessionStore().ReadTranscript(f.sid)
	require.NoError(t, err)
	notices := []session.TranscriptEntry{}
	for _, entry := range entries {
		if entry.Role == "system" && entry.GoalID == f.gid && entry.Content == want {
			notices = append(notices, entry)
		}
	}
	require.Len(t, notices, 1, "SETUP: genuine brake must have committed exactly one old public pause chat row")
	require.NotEmpty(t, notices[0].ID)
	f.collectEvents()
	matched := 0
	for _, event := range f.events {
		if event.Kind != agent.EventKindGoalStatusChanged {
			continue
		}
		p, ok := event.Payload.(agent.GoalStatusChangedPayload)
		if ok && p.SessionID == f.sid && p.GoalID == f.gid && p.State == "waiting_on_user" && p.LatestReason == want && p.ChatNotice != nil && p.ChatNotice.Id != nil && *p.ChatNotice.Id == notices[0].ID {
			matched++
		}
	}
	require.Equal(t, 1, matched, "SETUP: actual committed pause chat/status identity must be positive before any missing-bell RED")
}
func b1CompLogCount(t *testing.T, path, message string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &row))
		if row["message"] == message {
			n++
		}
	}
	return n
}

// Match actual fault text at Error severity, without freezing an unspecified
// producer message/attribute name. A fixture marker cannot satisfy this match.
func b1CompActualErrorCount(t *testing.T, path string, expected error) int {
	t.Helper()
	require.Error(t, expected)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var contains func(any) bool
	contains = func(v any) bool {
		switch x := v.(type) {
		case string:
			return strings.Contains(x, expected.Error())
		case map[string]any:
			for _, item := range x {
				if contains(item) {
					return true
				}
			}
		case []any:
			for _, item := range x {
				if contains(item) {
					return true
				}
			}
		}
		return false
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &row))
		if row["level"] == "error" && contains(row) {
			n++
		}
	}
	return n
}
func (f *b1CompFixture) collectEvents() {
	for {
		select {
		case e, ok := <-f.sub.C:
			if !ok {
				return
			}
			f.events = append(f.events, e)
		default:
			return
		}
	}
}
func (f *b1CompFixture) bells() []agent.NotificationPayload {
	f.collectEvents()
	var out []agent.NotificationPayload
	for _, e := range f.events {
		if p, ok := e.Payload.(agent.NotificationPayload); e.Kind == agent.EventKindNotification && ok && p.NotificationType == "goal_paused" {
			out = append(out, p)
		}
	}
	return out
}
func (f *b1CompFixture) saved(t *testing.T, user string) []notifications.Notification {
	t.Helper()
	rows, err := notifications.NewStore(filepath.Join(f.home, "notifications")).ListForUser(user)
	require.NoError(t, err)
	return rows
}

func TestGoalPauseB1BootContract(t *testing.T) {
	t.Run("GeneratedClosedEnum", func(t *testing.T) {
		require.True(t, gen.NotificationType("knowledge_drift").Valid())
		assert.True(t, gen.NotificationType("goal_paused").Valid())
	})
	for _, failed := range []bool{true, false} {
		name := "FiveCompleted"
		if failed {
			name = "ThreeFailed"
		}
		t.Run(name+"RealBootTickToBell", func(t *testing.T) {
			f := b1CompBoot(t, false, false)
			f.arm(t)
			f.pause(t, failed)
			b1CompAssertBell(t, f, b1CompOwner, 1)
			f.pe.Tick(context.Background())
			assert.Len(t, f.bells(), 1, "same accepted pause cannot stack/re-push a bell")
		})
	}
	for _, status := range []string{"blocked", "waiting_on_user"} {
		t.Run(status+"GenuineClaimFirstSet", func(t *testing.T) {
			f := b1CompBoot(t, false, false)
			f.arm(t)
			f.p.mu.Lock()
			f.p.twice = true
			f.p.mu.Unlock()
			f.user(t, status, "Fixture pause reason")
			g, err := goal.NewStore(f.home).Get(f.gid)
			require.NoError(t, err)
			require.NotNil(t, g.LatestClaim)
			assert.Equal(t, status, string(g.LatestClaim.Status))
			b1CompAssertBell(t, f, b1CompOwner, 1)
			for n := 0; n < 3; n++ {
				f.pe.Tick(context.Background())
			}
			assert.Len(t, f.bells(), 1)
		})
	}
	t.Run("SharedUsesConfiguredRecipients", func(t *testing.T) {
		f := b1CompBoot(t, true, false)
		f.arm(t)
		f.user(t, "blocked", "Fixture shared pause")
		own := b1CompAssertBell(t, f, b1CompOwner, 1)
		all := f.bells()
		require.Len(t, all, 2)
		recipients := []string{}
		for _, bell := range all {
			recipients = append(recipients, bell.Recipient)
		}
		assert.ElementsMatch(t, []string{b1CompOwner, "b1-foreign-viewer"}, recipients)
		other := f.saved(t, "b1-foreign-viewer")
		require.Len(t, other, 1)
		assert.NotEqual(t, own.ID, other[0].ID)
		assert.Equal(t, "goal_paused:"+f.gid, other[0].CoalesceKey)
		assert.Equal(t, f.sid, other[0].SessionID)
	})
	t.Run("AccountlessLiveOnlyNonemptyID", func(t *testing.T) {
		f := b1CompBoot(t, true, true)
		f.arm(t)
		f.user(t, "blocked", "Fixture accountless pause")
		bells := f.bells()
		require.Len(t, bells, 1)
		assert.Equal(t, agent.NotificationAdminBroadcast, bells[0].Recipient)
		assert.NotEmpty(t, bells[0].ID)
		assert.Empty(t, f.saved(t, agent.NotificationAdminBroadcast))
		f.assertLive(t, bells[0], true)
	})
	t.Run("ResumeDoesNotAutoClearThenReadAllowsNewPause", b1CompReadResume)
	t.Run("UnreadRePauseCoalescesPerRecipient", b1CompUnreadCoalesces)
	t.Run("RealStoreFailureIsLiveOnlyNotDurable", b1CompStoreFailure)
	t.Run("ExactChiefUnicodeReasonProjection", b1CompReasonCases)
	t.Run("PositiveRawOutputDoesNotEnterBell", func(t *testing.T) {
		f := b1CompBoot(t, false, false)
		f.arm(t)
		f.user(t, "output", "")
		require.Equal(t, int32(1), f.outputTool.calls.Load())
		f.p.mu.Lock()
		seen := f.p.sawPrivateOutput
		f.p.mu.Unlock()
		require.True(t, seen, "SETUP: actual registry output must reach the next real provider request before privacy absence can mean anything")
		f.user(t, "blocked", "Fixture safe pause reason")
		row := b1CompAssertBell(t, f, b1CompOwner, 1)
		assert.NotContains(t, row.Title+row.Body, b1CompPrivateOutput)
	})
}
func b1CompAssertBell(t *testing.T, f *b1CompFixture, recipient string, total int) notifications.Notification {
	t.Helper()
	all := f.bells()
	bells := []agent.NotificationPayload{}
	for _, bell := range all {
		if bell.Recipient == recipient {
			bells = append(bells, bell)
		}
	}
	require.Len(t, bells, total, "actual pause/claim already committed; boot-installed bell must now be observable")
	if !f.shared {
		require.Len(t, all, total, "known owner cannot additionally broadcast to configured accounts")
	}
	last := bells[len(bells)-1]
	require.NotEmpty(t, last.ID)
	assert.Equal(t, recipient, last.Recipient)
	assert.Equal(t, "goal_paused", last.NotificationType)
	assert.Equal(t, "warning", last.Severity)
	assert.Equal(t, f.sid, last.SessionID)
	assert.Equal(t, "mia", last.AgentID)
	assert.Contains(t, last.Title, b1CompTitle)
	rows := f.saved(t, recipient)
	require.NotEmpty(t, rows)
	if total == 1 {
		require.Len(t, rows, 1, "one initial accepted pause creates exactly one saved item for this recipient")
	}
	var found *notifications.Notification
	for i := range rows {
		if rows[i].ID == last.ID {
			n := rows[i]
			found = &n
		}
	}
	require.NotNil(t, found)
	assert.Equal(t, "goal_paused:"+f.gid, found.CoalesceKey)
	assert.Equal(t, last.Title, found.Title)
	assert.Equal(t, last.Body, found.Body)
	assert.Equal(t, last.Severity, found.Severity)
	assert.Equal(t, last.NotificationType, found.Type)
	assert.Equal(t, last.AgentID, found.AgentID)
	assert.Equal(t, f.sid, found.SessionID)
	if !f.shared {
		assert.Empty(t, f.saved(t, "b1-foreign-viewer"), "known owner cannot become a configured-foreign-user fallback")
	}
	f.assertLive(t, last, f.shared)
	return *found
}
func b1CompReadResume(t *testing.T) {
	f := b1CompBoot(t, false, false)
	f.arm(t)
	f.user(t, "blocked", "Fixture initial pause")
	first := b1CompAssertBell(t, f, b1CompOwner, 1)
	f.user(t, "text", "")
	rows := f.saved(t, b1CompOwner)
	require.Len(t, rows, 1)
	assert.Equal(t, first.ID, rows[0].ID)
	assert.False(t, rows[0].Read, "genuine human resume is NOT bell MarkRead")
	require.NoError(t, f.rs.notifStore.MarkRead(b1CompOwner, first.ID))
	f.user(t, "waiting_on_user", "Fixture later pause")
	later := b1CompAssertBell(t, f, b1CompOwner, 2)
	assert.NotEqual(t, first.ID, later.ID)
	rows = f.saved(t, b1CompOwner)
	require.Len(t, rows, 2)
}
func b1CompUnreadCoalesces(t *testing.T) {
	f := b1CompBoot(t, false, false)
	f.arm(t)
	f.user(t, "blocked", "Fixture first pause")
	first := b1CompAssertBell(t, f, b1CompOwner, 1)
	f.user(t, "text", "")
	f.user(t, "waiting_on_user", "Fixture second pause")
	later := b1CompAssertBell(t, f, b1CompOwner, 2)
	assert.Equal(t, first.ID, later.ID)
	assert.Len(t, f.saved(t, b1CompOwner), 1)
}
func b1CompStoreFailure(t *testing.T) {
	f := b1CompBoot(t, false, false)
	f.arm(t)
	dir := filepath.Join(f.home, "notifications")
	backup := dir + ".b1-original"
	hadDir := false
	if _, err := os.Stat(dir); err == nil {
		require.NoError(t, os.Rename(dir, backup))
		hadDir = true
	} else {
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	require.NoError(t, os.WriteFile(dir, []byte("B1 fixture non-directory notification store"), 0600))
	var once sync.Once
	restore := func() {
		once.Do(func() {
			require.NoError(t, os.Remove(dir))
			if hadDir {
				require.NoError(t, os.Rename(backup, dir))
			}
		})
	}
	t.Cleanup(restore)
	_, probeErr := f.rs.notifStore.Create(notifications.Notification{Recipient: b1CompOwner, Type: "goal_paused", Title: "Fault positive control", Severity: "warning"})
	require.Error(t, probeErr, "SETUP: fresh actual store write must demonstrate the isolated fault")
	before := b1CompActualErrorCount(t, f.logPath, probeErr)
	f.user(t, "blocked", "Fixture save-failure pause")
	bells := f.bells()
	require.Len(t, bells, 1, "actual persistence failure cannot erase the live warning")
	assert.NotEmpty(t, bells[0].ID)
	assert.Equal(t, b1CompOwner, bells[0].Recipient)
	assert.Greater(t, b1CompActualErrorCount(t, f.logPath, probeErr), before, "actual storage failure must be operationally logged at Error, not replaced by an unrelated fixture error or silent live-only success")
	_, err := f.rs.notifStore.ListForUser(b1CompOwner)
	require.Error(t, err, "SETUP: actual store must be unavailable, not a mocked save result")
	restore()
	assert.Empty(t, f.saved(t, b1CompOwner), "live delivery is NOT a claim that the failed write was durable")
	f.assertLive(t, bells[0], false)
	// Exact identifying failure-log shape is not frozen; do not use unrelated
	// existing fixture policy errors as its positive proof. Combined CHECK must
	// verify the actual storage-failure diagnostic separately.
}
func (f *b1CompFixture) startLive(t *testing.T) {
	t.Helper()
	own, ownCh := makeForwarderTestConn(4096)
	own.userID = f.wc.userID
	other, otherCh := makeForwarderTestConn(4096)
	other.userID = "b1-foreign-viewer"
	a, b := f.al.SubscribeEvents(4096), f.al.SubscribeEvents(4096)
	ad, bd := make(chan struct{}), make(chan struct{})
	go f.h.eventForwarder(own, "b1-own-live", a, ad)
	go f.h.eventForwarder(other, "b1-foreign-live", b, bd)
	var once sync.Once
	f.stopLive = func() { once.Do(func() { f.al.UnsubscribeEvents(a.ID); f.al.UnsubscribeEvents(b.ID); <-ad; <-bd }) }
	f.liveOwn = ownCh
	f.liveForeign = otherCh
}
func b1CompFrames(t *testing.T, ch <-chan []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	for {
		select {
		case raw := <-ch:
			var frame map[string]any
			require.NoError(t, json.Unmarshal(raw, &frame))
			if frame["type"] == "notification" && frame["notification_type"] == "goal_paused" {
				out = append(out, frame)
			}
		default:
			return out
		}
	}
}
func (f *b1CompFixture) assertLive(t *testing.T, p agent.NotificationPayload, broadcast bool) {
	t.Helper()
	f.stopLive()
	own, other := b1CompFrames(t, f.liveOwn), b1CompFrames(t, f.liveForeign)
	require.Len(t, own, 1, "real boot producer must reach existing live notification forwarder")
	assert.Equal(t, p.ID, own[0]["id"])
	assert.Equal(t, p.SessionID, own[0]["session_id"])
	assert.Equal(t, p.AgentID, own[0]["agent_id"])
	assert.Equal(t, p.Title, own[0]["title"])
	if broadcast {
		require.Len(t, other, 1)
		expected := p
		if p.Recipient != agent.NotificationAdminBroadcast {
			found := false
			for _, bell := range f.bells() {
				if bell.Recipient == "b1-foreign-viewer" {
					expected = bell
					found = true
				}
			}
			require.True(t, found, "SETUP: shared-chat second recipient requires its own actual positive producer event")
		}
		assert.Equal(t, expected.ID, other[0]["id"])
		assert.Equal(t, expected.SessionID, other[0]["session_id"])
		assert.Equal(t, expected.Title, other[0]["title"])
	} else {
		assert.Empty(t, other, "configured foreign recipient zero is paired with actual owner positive")
	}
	f.startLive(t)
}
func b1CompReasonCases(t *testing.T) {
	for _, tc := range b1DraftReasonCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := b1CompBoot(t, false, false)
			f.arm(t)
			f.user(t, "blocked", tc.input)
			row := b1CompAssertBell(t, f, b1CompOwner, 1)
			prefix := "Goal paused in " + b1CompTitle + ": "
			require.True(t, strings.HasPrefix(row.Title, prefix))
			actual := strings.TrimPrefix(row.Title, prefix)
			assert.Equal(t, tc.want, actual)
			assert.LessOrEqual(t, utf8.RuneCountInString(actual), 160)
		})
	}
}
