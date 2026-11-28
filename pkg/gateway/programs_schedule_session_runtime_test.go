package gateway

// Spec rev8 S144 / FR-041 / FR-050 / I-2 / S-111 / S-124: the isolated scheduled
// kind of S144 through the real schedule writers, the real cron lane and the real
// runner. A command in each fired session counts the program token. The test
// writes no origin, record or session: the fired session, its lifecycle record,
// its turn origin and the count are what production wrote.
// Producers: a person's schedule is created through the real REST handler
// (HandleSchedules with run_isolated), which stores {independent}. The catalog
// has no tool that creates a schedule (/loop and task triggers make other kinds of
// job), so the Telegram-created schedule is written through the real cron writer
// (AddJobFull) with the provenance a Telegram-origin turn's creation stores,
// {connector:telegram}. The last row is a twin: a job with neither stored
// provenance nor a creating user, which no current writer produces.
// Oracle (spec S-144, row 5 of the classification table, the notice table): the
// session is lineage; a Telegram-created schedule prints 0 with the Telegram
// notice, a person's schedule is {independent} and follows the web-chat setting
// (A-1), and a record with no origin is unresolved and prints 0.
// The connected identity is Ops (this fixture assigns GitHub to Ops, not Dev).
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	programsScheduleCall = "s144-scheduled-count"
	// A day: a schedule only ever runs when the test fires it.
	programsScheduleEveryMS = int64(24 * 60 * 60 * 1000)
	// Spec notice table, "unresolved" and "withheld by web chat" rows.
	programsScheduleUnresolvedNotice = "Connected programs were not used for this command: where this work came from could not be confirmed."
	programsScheduleWebOffNotice     = "Connected programs were not used for this command: programs are off for the web chat. A person can switch them on in Settings → Programs."
)

// programsScheduleProvider is the model edge for a session whose id does not
// exist until its schedule fires: the first request of a scheduled session gets
// the counting command scripted. Any other unscripted session is a fixture
// error, never a silent default.
type programsScheduleProvider struct {
	*programsPeerProvider
	al   *agent.AgentLoop
	work string
}

func (p *programsScheduleProvider) Chat(ctx context.Context, messages []providers.Message, definitions []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error) {
	sid := tools.ToolTranscriptSessionID(ctx)
	p.mu.Lock()
	_, scripted := p.scripts[sid]
	p.mu.Unlock()
	if !scripted {
		meta, err := p.al.GetSessionStore().GetMeta(sid)
		if err != nil {
			return nil, fmt.Errorf("schedule provider fixture: read the meta of unscripted session %q: %w", sid, err)
		}
		if meta == nil || meta.Type != session.SessionTypeScheduled {
			return nil, fmt.Errorf("schedule provider fixture: session %q is neither scripted nor a scheduled session", sid)
		}
		command := fmt.Sprintf("printenv GH_TOKEN | wc -c > %q; printf 'PROGRAM_SCHEDULE_CHILD_RAN'", filepath.Join(p.work, sid+"-schedule-count"))
		p.setScript(sid, []providers.LLMResponse{
			{ToolCalls: []providers.ToolCall{{ID: programsScheduleCall, Type: "function", Name: "bash", Arguments: map[string]any{"command": command}}}},
			wu5StopGW("scheduled count completed"),
		})
	}
	return p.programsPeerProvider.Chat(ctx, messages, definitions, model, options)
}

type programsScheduleFixture struct {
	*programsPeerFixture
	api *restAPI
	cs  *cron.CronService
}

// newProgramsScheduleFixture builds the real cron service, runner and REST
// handlers over the real loop, after a web control turn has proven that Ops's
// connected identity receives the token before any schedule runs.
func newProgramsScheduleFixture(t *testing.T) *programsScheduleFixture {
	t.Helper()
	f := newProgramsPeerFixture(t)
	p := &programsScheduleProvider{programsPeerProvider: f.provider, al: f.al, work: f.work}
	for _, id := range []string{"dev", "ops"} {
		inst, ok := f.al.GetRegistry().GetAgent(id)
		require.True(t, ok)
		inst.Provider = p
	}
	controlPath := f.scriptCount(f.ops, "s144-schedule-control")
	require.NoError(t, f.bus.PublishInbound(context.Background(), bus.InboundMessage{Channel: "webchat", SessionID: f.ops, ChatID: f.ops, Content: "connected Ops control", Provenance: bus.NewProvenance(bus.OriginWeb)}))
	f.waitEnds(t, f.ops)
	control, result := programsPeerCount(t, f, controlPath, "s144-schedule-control")
	require.Equal(t, "16", control, "instrument: this actual Ops identity receives the real checked program on trusted web input before any schedule fires")
	require.NotContains(t, result, "Connected programs were not used for this command:")
	cs := cron.NewCronService(filepath.Join(f.home, "cron", "s144-schedules.json"))
	t.Cleanup(cs.Stop)
	runner := newScheduledRunner(f.al, agentCheckerFunc(func(id string) bool {
		inst, ok := f.al.GetRegistry().GetAgent(id)
		return ok && inst != nil
	}), f.bus, nil, f.al.GetConfig)
	runner.home = f.home
	cs.SetRunner(runner)
	api := &restAPI{agentLoop: f.al, homePath: f.home}
	api.cronService.Store(cs)
	return &programsScheduleFixture{programsPeerFixture: f, api: api, cs: cs}
}

// writeJob adds an isolated Ops schedule through the real cron writer.
func (x *programsScheduleFixture) writeJob(t *testing.T, name string, spec cron.JobSpec) cron.CronJob {
	t.Helper()
	every := programsScheduleEveryMS
	spec.Name, spec.Message, spec.AgentID = name, "Count the connected token.", "ops"
	spec.Schedule = cron.CronSchedule{Kind: "every", EveryMS: &every}
	spec.SessionMode, spec.RunIsolated = cron.SessionModeIsolated, true
	job, err := x.cs.AddJobFull(spec)
	require.NoError(t, err)
	return *job
}

// personCreate is the UI's real handler with its already-authenticated person
// context; authentication itself is outside this writer/dispatch test's boundary.
func (x *programsScheduleFixture) personCreate(t *testing.T) cron.CronJob {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"name": "S144 person schedule", "message": "Count the connected token.", "owner_agent_id": "ops",
		"run_isolated": true, "trigger": map[string]any{"kind": "every", "every_ms": programsScheduleEveryMS},
	})
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/schedules", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey{}, &config.UserConfig{Username: "s1-person"}))
	w := httptest.NewRecorder()
	x.api.HandleSchedules(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "actual person's create: %s", w.Body.String())
	var created gen.Schedule
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	job, ok := x.cs.GetJob(created.Id)
	require.True(t, ok)
	require.Equal(t, cron.SessionModeIsolated, job.SessionMode, "instrument: the real create made an isolated schedule")
	require.Equal(t, "s1-person", job.CreatedBy, "instrument: the real create is attributed to the person")
	return job
}

// programsScheduleRun is what one fired schedule produced: its session, the
// command's count and result, the provenance of the turn's first two model
// requests (the command and its consumed result) and the session's real record.
type programsScheduleRun struct {
	sid, count, result string
	origins            []bus.Provenance
	record             *session.LifecycleRecord
}

func (x *programsScheduleFixture) fire(t *testing.T, job cron.CronJob) programsScheduleRun {
	t.Helper()
	status, sid, err := x.cs.RunNow(job.ID)
	require.NoError(t, err, "instrument: the real isolated schedule completed, not a setup or admission failure")
	require.Equal(t, "ok", status)
	require.NotEmpty(t, sid)
	require.NotEqual(t, x.ops, sid, "instrument: an isolated schedule runs in a fresh session, never Ops's main")
	x.waitEnds(t, sid)
	result := x.provider.result(sid, programsScheduleCall)
	require.Contains(t, result, "PROGRAM_SCHEDULE_CHILD_RAN", "instrument: the actual scheduled command ran; a refused or missed request cannot establish zero")
	raw, err := os.ReadFile(filepath.Join(x.work, sid+"-schedule-count"))
	require.NoError(t, err)
	origins := x.provider.observedOrigins(sid)
	require.GreaterOrEqual(t, len(origins), 2, "instrument: the turn requested the command and consumed its real result")
	record, err := x.al.GetSessionLifecycleStore().Load(sid)
	require.NoError(t, err)
	require.NotNil(t, record.Origin, "instrument: the session's real record names its origin")
	return programsScheduleRun{sid: sid, count: strings.TrimSpace(string(raw)), result: result, origins: origins[:2], record: record}
}

func TestProgramProvenance_ActualIsolatedScheduledSession(t *testing.T) {
	t.Run("S144_telegram_created_schedule_session_is_lineage_with_telegram_provenance_and_prints_0", func(t *testing.T) {
		x := newProgramsScheduleFixture(t)
		run := x.fire(t, x.writeJob(t, "S144 telegram schedule", cron.JobSpec{Provenance: []string{"connector:telegram"}}))
		assert.Equal(t, session.ProvenanceModeLineage, run.record.ProvenanceMode, "S144: a scheduled session is lineage")
		assert.Equal(t, session.OriginKindScheduled, run.record.Origin.Kind)
		assert.Equal(t, []string{"connector:telegram"}, run.record.Provenance, "S144/I-2: the session's record stores its schedule's provenance")
		for _, actual := range run.origins {
			assert.Equal(t, []bus.Origin{bus.ConnectorOrigin("telegram")}, actual.Origins(), "S144: the scheduled turn is {connector:telegram}, never web or independent")
		}
		assert.Equal(t, "0", run.count, "S144: the Telegram-created schedule's session prints 0")
		assert.Contains(t, run.result, programsPeerTelegramNotice, "S144: the Telegram-off notice, not unresolved")
	})

	for _, tc := range []struct {
		name       string
		webAllowed bool
		count      string
		notice     string
	}{
		{"S144_person_created_schedule_session_is_independent_with_web_on_prints_16", true, "16", ""},
		{"S144_person_created_schedule_session_is_independent_with_web_off_prints_0", false, "0", programsScheduleWebOffNotice},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := newProgramsScheduleFixture(t)
			x.al.GetConfig().Programs.WebChatAllowed = tc.webAllowed
			run := x.fire(t, x.personCreate(t))
			assert.Equal(t, session.ProvenanceModeLineage, run.record.ProvenanceMode, "S144: a scheduled session is lineage")
			assert.Equal(t, []string{"independent"}, run.record.Provenance, "S144/I-2: a person's schedule stores {independent}")
			for _, actual := range run.origins {
				assert.Equal(t, []bus.Origin{bus.OriginIndependent}, actual.Origins(), "S144: a person's schedule's turn is {independent}, never web or a connector")
			}
			assert.Equal(t, tc.count, run.count, "S144/A-1: the person's schedule follows the web-chat setting")
			if tc.notice == "" {
				assert.NotContains(t, run.result, "Connected programs were not used for this command:", "S144/A-1: allowed, so nothing is withheld")
			} else {
				assert.Contains(t, run.result, tc.notice, "S144/A-1: independent is governed by the web setting, not unresolved")
			}
		})
	}

	t.Run("S144_schedule_without_stored_provenance_or_creator_is_unresolved_and_prints_0", func(t *testing.T) {
		x := newProgramsScheduleFixture(t)
		job := x.writeJob(t, "S144 legacy schedule", cron.JobSpec{})
		require.Empty(t, job.Provenance, "instrument: the twin stores no origin set")
		require.Empty(t, job.CreatedBy, "instrument: and no creating user, so nothing makes it independent")
		run := x.fire(t, job)
		assert.Equal(t, session.ProvenanceModeLineage, run.record.ProvenanceMode, "S144: still a lineage session")
		assert.Empty(t, run.record.Provenance, "S144: nothing to store, so the record has no origin set")
		for _, actual := range run.origins {
			assert.True(t, actual.IsUnresolved(), "S144: the turn of a schedule with no recorded origin is unresolved, never a permissive fallback")
		}
		assert.Equal(t, "0", run.count, "S144: an unresolved schedule session prints 0")
		assert.Contains(t, run.result, programsScheduleUnresolvedNotice, "S144: unresolved notice, not the Telegram or web one")
	})
}
