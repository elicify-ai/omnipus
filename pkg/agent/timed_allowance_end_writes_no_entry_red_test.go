//go:build goolm && stdjson

package agent

// Part K (2026-11-26): C-26 "Audit" — an allowance that reaches its end time writes no entry.
//
// Oracle (skills-tools-redesign-spec.md at b27c64109, C-26 "Audit"): "`shell.grant_recorded` ... gains `duration` and
// `expires_at` ... An allowance that reaches its end time writes no entry (the record entry already states the end
// time). An allowance ended by a permission change writes `approval.allowance_ended` with agent, subject, the changed
// key, old and new value, the actor of the change and reason `permission_changed`." Read with C-26 "A permission
// change": a change "ends that tool's or skill's running allowances ... each ended allowance writes one audit entry",
// and with C-26 "End"/"Key" (the subject of each kind: the tool name, the command or command family, the path with its
// access, the address set, the skill key). Dataset D17 has no row for a permission change that arrives after an
// allowance already ended; K2 derives it from the three sentences above (a running allowance is ended by the change; one
// that reached its end time already is not running, and the end time writes no entry). That derivation is the one place
// this file reads between the lines of the spec; it is named so CHECK can rule on it.
//
// What is pinned, with the real loop, store and audit logger, and in K1 the real egress proxy as well (nothing is
// mocked; the audit file is read back from disk):
//
//   - K1 the sweep: when the clock reaches the end, SweepEndedAllowances (called by hand) and RunAllowanceExpirySweep
//     (ticking by itself) write no audit line at all, neither approval.allowance_ended nor any other event, and looking
//     the ended allowances up writes none either. The comparison is the file's line count before and after, so an end
//     entry under a NEW event name is caught as well as one under approval.allowance_ended.
//   - K2 the later change: a permission change that arrives once Mia's allowance has reached its end time (exactly at the
//     end) and while Jim's is still running writes one approval.allowance_ended entry, for Jim only. One sub-test per kind
//     of allowance and per phase (seven kinds, two phases), because every kind has its own end-time guard in the store
//     (an ordinary tool, a skill, and the five bash kinds: exact command, command family, path, named address, any
//     address).
//
// Controls (a clean "no entry" could otherwise be a blind reader or an allowance that never ended):
//
//   - the logger is wired to the loop AND to the store, the way production wires them (loop_construct.go hands the
//     loop's logger to the store), and the store's own record entries must reach the reader, each stating the end time
//     ("the record entry already states the end time");
//   - every allowance is running one nanosecond before its end and ended at it (the lookups say so), and the sweep's
//     real effect is seen (Mia's egress token is refused with 403, the status the sibling sweep tests use);
//   - the same reader shows an approval.allowance_ended entry when a permission change ends a RUNNING allowance (K1's
//     closing control; K2's "one nanosecond before" half writes two entries, Mia's and Jim's).
//
// Failability: no baseline commit fails these tests. The store has guarded every kind with its end time since it landed
// (c9f6fb09c), and the sweep (5d34cf6ba) arrived with no audit write, so there is nothing RED to run them against.
// Probes in a scratch checkout (an entry written by the loop's sweep, an entry under another event name written by the
// store's sweep, an entry written when a lookup finds an allowance past its end, the end-time guards removed, EndFor
// reporting nothing, the store's record entry silenced) are recorded in the Part K pack; they are the author's probes,
// not CHECK's mutation run. GREEN and mutation proof are deferred to CHECK.
// Limits: an entry written by a goroutine that outlives the sweep call and lands after the read would be missed (a
// synchronous write is caught). The fields of an ending entry are not pinned here; the gateway tests pin them, in
// pkg/gateway/timed_allowance_ends_on_permission_change_red_test.go (D17#10, through permissionChangeAudit: key, old,
// new and actor; D17#16: key, new, actor and that old is present; D17#14 pins only the count, two entries) and in
// skill_consent_table_red_test.go (applyNativeD10Before, case write-own-ask, D10#31: agent, subject, key, new, actor
// and reason permission_changed).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Event names are the spec's literals, not the audit package's constants, so a renamed constant cannot hide a change.
const (
	endNoEntryRecordEvent = "shell.grant_recorded"
	endNoEntryEndedEvent  = "approval.allowance_ended"
	endNoEntrySession     = "end-no-entry-session"
)

// endNoEntryKind is one kind of allowance the store holds, the stored permission whose change ends it (C-26 "A
// permission change": for bash that includes its command, command-family, path and address allowances) and the part of
// its subject (C-26 "Key") that the inputs below put into it. fragment is built from those inputs, never read back from
// the store; it is "" for the kind whose subject the spec does not spell out.
type endNoEntryKind struct {
	name     string
	key      string
	fragment string
	record   func(t *testing.T, s *security.ApprovalGrantStore, agent string, d time.Duration)
	running  func(s *security.ApprovalGrantStore, agent string) bool
}

func endNoEntryKinds() []endNoEntryKind {
	toolArgs := map[string]any{"task_id": "t1"}
	bashArgs := map[string]any{"command": "make deploy-k"}
	family := security.ShellPrefixGrant{Binary: "/usr/bin/npm", ArgPrefix: "run k-family"}
	pathGrant := fspolicy.PathGrant{Path: "/home/p/Desktop/k-out.pdf", Access: fspolicy.PathGrantAccessWrite}
	return []endNoEntryKind{
		{
			name: "ordinary tool", key: "delete_task", fragment: "delete_task",
			record: func(t *testing.T, s *security.ApprovalGrantStore, agent string, d time.Duration) {
				t.Helper()
				_, ok := s.RecordFor(agent, "delete_task", toolArgs, d)
				require.True(t, ok, "instrument: the store takes a timed answer for an ordinary tool")
			},
			running: func(s *security.ApprovalGrantStore, agent string) bool {
				return s.IsAllowedForAgent(agent, "delete_task", toolArgs)
			},
		},
		{
			name: "skill", key: security.AllowanceSubjectSkillPrefix + "weather", fragment: "weather",
			record: func(t *testing.T, s *security.ApprovalGrantStore, agent string, d time.Duration) {
				t.Helper()
				_, ok := s.RecordSkillAllowance(agent, "weather", d)
				require.True(t, ok, "instrument: the store takes a timed answer for a skill")
			},
			running: func(s *security.ApprovalGrantStore, agent string) bool {
				return s.IsSkillAllowed(agent, "weather")
			},
		},
		{
			name: "bash exact command", key: "bash", fragment: "make deploy-k",
			record: func(t *testing.T, s *security.ApprovalGrantStore, agent string, d time.Duration) {
				t.Helper()
				_, ok := s.RecordFor(agent, "bash", bashArgs, d)
				require.True(t, ok, "instrument: the store takes a timed answer for an exact bash command")
			},
			running: func(s *security.ApprovalGrantStore, agent string) bool {
				return s.IsAllowedForAgent(agent, "bash", bashArgs)
			},
		},
		{
			name: "bash command family", key: "bash", fragment: "run k-family",
			record: func(t *testing.T, s *security.ApprovalGrantStore, agent string, d time.Duration) {
				t.Helper()
				_, ok := s.RecordPrefixGrantFor(agent, "bash", family, d)
				require.True(t, ok, "instrument: the store takes a timed answer for a command family")
			},
			running: func(s *security.ApprovalGrantStore, agent string) bool {
				return s.IsPrefixAllowed(endNoEntrySession, agent, "bash", family.Binary, []string{"run", "k-family"}, false)
			},
		},
		{
			name: "bash path", key: "bash", fragment: "/home/p/Desktop/k-out.pdf",
			record: func(t *testing.T, s *security.ApprovalGrantStore, agent string, d time.Duration) {
				t.Helper()
				_, ok := s.RecordPathGrantFor(agent, pathGrant, d)
				require.True(t, ok, "instrument: the store takes a timed answer for a path")
			},
			running: func(s *security.ApprovalGrantStore, agent string) bool {
				return slices.ContainsFunc(s.PathGrantsFor(endNoEntrySession, agent), func(g fspolicy.PathGrant) bool {
					return g.Path == pathGrant.Path
				})
			},
		},
		{
			name: "bash named address", key: "bash", fragment: "k-host.example",
			record: func(t *testing.T, s *security.ApprovalGrantStore, agent string, d time.Duration) {
				t.Helper()
				_, ok := s.RecordNetworkGrantFor(agent, []string{"k-host.example"}, d)
				require.True(t, ok, "instrument: the store takes a timed answer for a named address")
			},
			running: func(s *security.ApprovalGrantStore, agent string) bool {
				return slices.Contains(s.NetworkHostsFor(endNoEntrySession, agent), "k-host.example")
			},
		},
		{
			name: "bash any address", key: "bash", fragment: "",
			record: func(t *testing.T, s *security.ApprovalGrantStore, agent string, d time.Duration) {
				t.Helper()
				_, ok := s.RecordNetworkGrantFor(agent, nil, d)
				require.True(t, ok, "instrument: the store takes a timed answer for the usual ports to any address")
			},
			running: func(s *security.ApprovalGrantStore, agent string) bool {
				return s.HasNetworkGrant(endNoEntrySession, agent)
			},
		},
	}
}

// wireEndNoEntryAudit gives the loop and its allowance store ONE real audit logger, as production does
// (pkg/agent/loop_construct.go hands the loop's logger to the store), and returns a reader of the file it writes. The
// logger flushes every line as it writes it, so the reader can be called any number of times without closing the logger.
func wireEndNoEntryAudit(t *testing.T, al *AgentLoop) func() []map[string]any {
	t.Helper()
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 90})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, logger.Close()) })
	al.auditLogger = logger
	al.ApprovalGrants().SetAuditLogger(logger)
	return func() []map[string]any { return readEndNoEntryRows(t, filepath.Join(dir, "audit.jsonl")) }
}

func readEndNoEntryRows(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	var rows []map[string]any
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &row), "an audit line is one JSON object: %q", line)
		rows = append(rows, row)
	}
	return rows
}

func endNoEntryRowsFor(rows []map[string]any, event string) []map[string]any {
	var out []map[string]any
	for _, r := range rows {
		if r["event"] == event {
			out = append(out, r)
		}
	}
	return out
}

// describeEndNoEntryRows names rows for a failure message, so a stray entry is identified rather than counted.
func describeEndNoEntryRows(rows []map[string]any) string {
	parts := make([]string, 0, len(rows))
	for _, r := range rows {
		parts = append(parts, fmt.Sprintf("%v[agent=%v kind=%v subject=%v]", r["event"], r["agent_id"], r["kind"], r["subject"]))
	}
	return strings.Join(parts, "; ")
}

// requireRecordRowsStateTheEnd is the control that the store's record entries reach the reader and state the end time
// (C-26 Audit: "`shell.grant_recorded` ... gains `duration` and `expires_at`" and "the record entry already states the
// end time").
func requireRecordRowsStateTheEnd(t *testing.T, rows []map[string]any, end time.Time) {
	t.Helper()
	recorded := endNoEntryRowsFor(rows, endNoEntryRecordEvent)
	require.NotEmpty(t, recorded, "instrument: the store's record entries reach this reader (logger wired to the store)")
	for _, row := range recorded {
		details, _ := row["details"].(map[string]any)
		text, _ := details["expires_at"].(string)
		got, err := time.Parse(time.RFC3339, text)
		require.NoError(t, err, "C-26 Audit: a timed record entry carries expires_at as a time; row %v", row)
		assert.True(t, got.Equal(end), "C-26 Audit: the record entry states the end time %s, got %s", end, got)
	}
}

// endNoEntrySweepRig is the real sweep rig (loop, egress proxy, upstream, injected clock; Mia holds a one-hour allowance
// of the loopback address and her token is refused only after it ends) plus the audit wiring, with one more one-hour
// allowance of every kind recorded for Mia AFTER the logger was wired, so each of their record entries is in the file.
type endNoEntrySweepRig struct {
	*sweepRig
	rows func() []map[string]any
}

func newEndNoEntrySweepRig(t *testing.T) *endNoEntrySweepRig {
	t.Helper()
	rig := newSweepRig(t)
	rows := wireEndNoEntryAudit(t, rig.al)
	store := rig.al.ApprovalGrants()
	for _, k := range endNoEntryKinds() {
		k.record(t, store, "mia", time.Hour)
		require.True(t, k.running(store, "mia"), "instrument: Mia's %s allowance runs once recorded", k.name)
	}
	return &endNoEntrySweepRig{sweepRig: rig, rows: rows}
}

// requireKindsRunning reads every one of Mia's allowances through the store's own lookups and requires them all to be
// running (want true) or all ended (want false); when names the moment, for the failure message.
func (r *endNoEntrySweepRig) requireKindsRunning(t *testing.T, want bool, when string) {
	t.Helper()
	store := r.al.ApprovalGrants()
	for _, k := range endNoEntryKinds() {
		require.Equal(t, want, k.running(store, "mia"), "instrument: Mia's %s allowance, %s", k.name, when)
	}
}

// requireEndingEntryForRunningAllowance is K1's closing control: the same logger, store and reader DO show an
// approval.allowance_ended entry when a permission change ends a running allowance, which is the contrast C-26 Audit
// draws with the end time.
func (r *endNoEntrySweepRig) requireEndingEntryForRunningAllowance(t *testing.T) {
	t.Helper()
	store := r.al.ApprovalGrants()
	_, ok := store.RecordSkillAllowance("mia", "k-control-skill", 3*time.Hour)
	require.True(t, ok, "instrument: a new three-hour skill allowance for the control")
	require.NoError(t, r.al.EndAllowancesFor(context.Background(), r.al.auditLogger, PermissionChange{
		Key: security.AllowanceSubjectSkillPrefix + "k-control-skill", Old: "ask", New: "deny", AgentIDs: []string{"mia"},
	}))
	ended := endNoEntryRowsFor(r.rows(), endNoEntryEndedEvent)
	require.Len(t, ended, 1, "control: a permission change that ends a running allowance writes exactly one entry; rows: %s",
		describeEndNoEntryRows(ended))
	assert.Equal(t, "mia", ended[0]["agent_id"], "control: the entry names the agent")
	assert.Contains(t, fmt.Sprint(ended[0]["subject"]), "k-control-skill", "control: the entry names the subject")
}

func TestTimedAllowance_SweepAtEndWritesNoAuditEntry(t *testing.T) {
	t.Run("K1 SweepEndedAllowances at the end time writes no entry", func(t *testing.T) {
		rig := newEndNoEntrySweepRig(t)
		baseline := rig.rows()
		requireRecordRowsStateTheEnd(t, baseline, rig.end)
		require.Empty(t, endNoEntryRowsFor(baseline, endNoEntryEndedEvent), "instrument: no ending entry exists before anything ended")
		rig.requireKindsRunning(t, true, "before the end")

		rig.clock.advance(time.Hour - time.Nanosecond)
		rig.al.SweepEndedAllowances()
		rig.requireKindsRunning(t, true, "one nanosecond before the end")
		beforeEnd := rig.rows()
		require.Len(t, beforeEnd, len(baseline), "one nanosecond before the end the sweep writes nothing; new rows: %s",
			describeEndNoEntryRows(beforeEnd[min(len(baseline), len(beforeEnd)):]))

		rig.clock.advance(time.Nanosecond)
		rig.al.SweepEndedAllowances()
		afterSweep := rig.rows()
		rig.requireKindsRunning(t, false, "at the end time")
		afterLookups := rig.rows()
		status, _ := rig.request(t, rig.miaToken)
		require.Equal(t, http.StatusForbidden, status, "instrument: the sweep ran and refused Mia's token at the end time")

		assert.Empty(t, endNoEntryRowsFor(afterSweep, endNoEntryEndedEvent),
			"C-26 Audit: an allowance that reaches its end time writes no approval.allowance_ended entry")
		assert.Len(t, afterSweep, len(baseline),
			"C-26 Audit: the sweep at the end time writes no entry of any kind; new rows: %s",
			describeEndNoEntryRows(afterSweep[min(len(baseline), len(afterSweep)):]))
		assert.Len(t, afterLookups, len(baseline),
			"C-26 Audit: looking an ended allowance up writes no entry either; new rows: %s",
			describeEndNoEntryRows(afterLookups[min(len(baseline), len(afterLookups)):]))

		rig.requireEndingEntryForRunningAllowance(t)
	})

	t.Run("K1 the running expiry sweep at the end time writes no entry", func(t *testing.T) {
		rig := newEndNoEntrySweepRig(t)
		baseline := rig.rows()
		requireRecordRowsStateTheEnd(t, baseline, rig.end)
		rig.requireKindsRunning(t, true, "before the end")

		ctx, cancel := context.WithCancel(context.Background())
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			rig.al.RunAllowanceExpirySweep(ctx)
		}()
		t.Cleanup(func() {
			cancel()
			<-stopped
		})
		rig.clock.advance(time.Hour)

		// The sweep ticks on its own, so the effect is awaited by polling a real new connection; the deadline is ten
		// times the one-second tick and a miss fails the test, as in the sibling sweep test.
		deadline := time.Now().Add(10 * time.Second)
		var status int
		for {
			status, _ = rig.request(t, rig.miaToken)
			if status == http.StatusForbidden || time.Now().After(deadline) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		require.Equal(t, http.StatusForbidden, status, "instrument: the running sweep refused Mia's token after the hour")
		// Stop the sweep and wait for any tick in flight, so no write can land after the read below.
		cancel()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Fatal("RunAllowanceExpirySweep did not return after its context ended")
		}
		after := rig.rows()
		rig.requireKindsRunning(t, false, "after the hour")
		afterLookups := rig.rows()

		assert.Empty(t, endNoEntryRowsFor(after, endNoEntryEndedEvent),
			"C-26 Audit: an allowance that reaches its end time writes no approval.allowance_ended entry")
		assert.Len(t, after, len(baseline),
			"C-26 Audit: the running sweep writes no entry of any kind at the end time; new rows: %s",
			describeEndNoEntryRows(after[min(len(baseline), len(after)):]))
		assert.Len(t, afterLookups, len(baseline),
			"C-26 Audit: looking an ended allowance up writes no entry either; new rows: %s",
			describeEndNoEntryRows(afterLookups[min(len(baseline), len(afterLookups)):]))

		rig.requireEndingEntryForRunningAllowance(t)
	})
}

// newEndNoEntryLoop is a real loop with Mia and Jim, the injected allowance clock and the audit wiring, and no egress
// proxy: nothing here mints an egress token, so a bash ending revokes none.
func newEndNoEntryLoop(t *testing.T) (*AgentLoop, *allowanceTestClock, func() []map[string]any) {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmp,
				DefaultModel:      config.DefaultModel{Model: "allowance-end-entry-test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 2,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmp}, {ID: "jim", Home: tmp}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &capturingProvider{})
	t.Cleanup(al.Close)
	clock := &allowanceTestClock{start: time.Date(2026, time.November, 22, 12, 0, 0, 0, time.UTC)}
	al.SetAllowanceClock(clock.wallNow, clock.elapsedNow)
	return al, clock, wireEndNoEntryAudit(t, al)
}

func TestTimedAllowance_PermissionChangeAfterEndWritesNoEntryForTheEndedAllowance(t *testing.T) {
	phases := []struct {
		name       string
		elapsed    time.Duration
		miaRunning bool
		wantAgents []string
	}{
		{"one nanosecond before Mia's end both allowances run and both end", time.Hour - time.Nanosecond, true, []string{"jim", "mia"}},
		{"at Mia's end time only Jim's running allowance ends", time.Hour, false, []string{"jim"}},
	}
	for _, kind := range endNoEntryKinds() {
		for _, phase := range phases {
			t.Run(kind.name+": "+phase.name, func(t *testing.T) {
				al, clock, rows := newEndNoEntryLoop(t)
				store := al.ApprovalGrants()
				kind.record(t, store, "mia", time.Hour)   // Mia's answer "Allow for 1 hour"
				kind.record(t, store, "jim", 3*time.Hour) // Jim's answer "Allow for 3 hours"
				clock.advance(phase.elapsed)
				require.True(t, kind.running(store, "jim"), "instrument: Jim's three-hour allowance runs")
				require.Equal(t, phase.miaRunning, kind.running(store, "mia"),
					"instrument: Mia's one-hour allowance runs one nanosecond before its end and has ended at it")
				require.Empty(t, endNoEntryRowsFor(rows(), endNoEntryEndedEvent), "instrument: nothing is ended yet")

				require.NoError(t, al.EndAllowancesFor(context.Background(), al.auditLogger,
					PermissionChange{Key: kind.key, Old: "ask", New: "deny"}), "a global change, so every agent is affected")

				ended := endNoEntryRowsFor(rows(), endNoEntryEndedEvent)
				gotAgents := make([]string, 0, len(ended))
				for _, row := range ended {
					agent, _ := row["agent_id"].(string)
					gotAgents = append(gotAgents, agent)
					if kind.fragment != "" {
						assert.Contains(t, fmt.Sprint(row["subject"]), kind.fragment,
							"C-26 Audit: the entry names the subject of the allowance it ended")
					}
				}
				sort.Strings(gotAgents)
				assert.Equal(t, phase.wantAgents, gotAgents,
					"C-26 Audit and A permission change: one entry per allowance that was RUNNING when the change was saved, "+
						"none for an allowance that had already reached its end time; entries: %s", describeEndNoEntryRows(ended))
			})
		}
	}
}
