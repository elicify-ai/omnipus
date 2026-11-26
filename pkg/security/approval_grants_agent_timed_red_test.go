//go:build goolm && stdjson

package security_test

// Order 57 / CHECK F17 and F21: C-26, S-120/S-122, D17#1–8/#12–13.
// Plan: real store Record*/lookup calls, independent wall/monotonic clock,
// exact 1h/3h/24h ends, before/at/after boundaries and later-end-wins.
// Home is the actual process OMNIPUS_HOME, with protected entity fixtures;
// enumerate ALL persisted paths/bytes/modes, not three nominated files.
// A real same-home fresh subprocess proves the legacy restart instrument.
// It is NOT a gateway restart or a duration-bearing card-answer acceptance.
//
// Only clock injection is a callback (injectTimedStoreClock, bound by the
// backend to security.SetApprovalGrantStoreClock). Part H (2026-11-26): the
// thin adapters no longer stand in for absent code. recordActualTimedStore
// calls the real duration-bearing method of the subject's kind (RecordFor,
// RecordPrefixGrantFor, RecordPathGrantFor, RecordNetworkGrantFor) and
// returns the end the store reports, and the ClearAgent row calls the real
// ClearAgent. No adapter manufactures an end, a coverage verdict or a
// lifecycle: every expected end below is a literal derived from C-26.
// Lookups below are always direct real-store calls. Native enforcement and
// the two real agent-delete entry paths are not certified by this unit test;
// gateway restart/delete integration is pinned in pkg/gateway and the
// real-kernel rows stay UNVERIFIED (ST-29/ST-30).
// Mutations for independent CHECK: 55/65 minutes, wall-clock expiry, wrong
// 24h end, shortening a second answer, new persisted file, restored grants,
// or clearing Jim along with Mia. GREEN/mutation proof remains deferred.

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test-only clock boundary, NOT a store/action/lookup replacement: the
// store's wall and monotonic clock input. Every other store call is direct.
var injectTimedStoreClock func(*security.ApprovalGrantStore, func() time.Time, func() time.Duration)

type allowanceStoreClock struct {
	wall    time.Time
	elapsed time.Duration
}

func (c *allowanceStoreClock) advance(d time.Duration) {
	c.wall = c.wall.Add(d)
	c.elapsed += d
}

func allowanceStoreStart() time.Time {
	return time.Date(2026, time.November, 22, 12, 0, 0, 0, time.UTC)
}

func newTimedStoreWithClock(t *testing.T, c *allowanceStoreClock) *security.ApprovalGrantStore {
	t.Helper()
	store := security.NewApprovalGrantStore()
	nativebinding.Require(t, "pkg/security", injectTimedStoreClock,
		"C-26 / S-120/S-122: ApprovalGrantStore's actual wall/monotonic clock input (duration-taking Record* remains absent)")
	injectTimedStoreClock(store, func() time.Time { return c.wall }, func() time.Duration { return c.elapsed })
	return store
}

type allowanceStoreSubject struct {
	kind, tool           string
	recordArgs, callArgs map[string]any
	family               security.ShellPrefixGrant
	path                 fspolicy.PathGrant
	hosts                []string
}

func allowanceStoreSubjects(home string) []allowanceStoreSubject {
	return []allowanceStoreSubject{
		{kind: "tool", tool: "delete_task", recordArgs: map[string]any{"task_id": "argument-A"}, callArgs: map[string]any{"task_id": "argument-B"}},
		{kind: "shell_exact", tool: "bash", recordArgs: map[string]any{"command": "npm run test"}, callArgs: map[string]any{"command": "npm run test"}},
		{kind: "shell_family", tool: "bash", family: security.ShellPrefixGrant{Binary: "/usr/bin/npm", ArgPrefix: "run test"}},
		{kind: "path", tool: "bash", path: fspolicy.PathGrant{Path: filepath.Join(home, "workspace", "out.pdf"), Access: fspolicy.PathGrantAccessWrite}},
		{kind: "network_hosts", tool: "bash", hosts: []string{"example.com"}},
		{kind: "network_ports", tool: "bash"},
	}
}

// allowanceLengths are the three lengths a time choice offers (C-26 "Length": `1h`, `3h` or `1d`,
// the last being 24 hours). They are spec constants; nothing here reads them from the store.
var allowanceLengths = map[string]time.Duration{"1h": time.Hour, "3h": 3 * time.Hour, "1d": 24 * time.Hour}

// recordActualTimedStore records the answer a card gives through the real duration-bearing method of
// the subject's kind and returns the end the store reports. It decides nothing: callers compare the
// returned end with a literal derived from C-26, and the lookups are asserted separately.
func recordActualTimedStore(t *testing.T, store *security.ApprovalGrantStore, agent string, subject allowanceStoreSubject, duration string) time.Time {
	t.Helper()
	length, known := allowanceLengths[duration]
	require.True(t, known, "fixture: %q is not one of the three lengths of C-26", duration)
	var (
		end time.Time
		ok  bool
	)
	switch subject.kind {
	case "tool", "shell_exact":
		end, ok = store.RecordFor(agent, subject.tool, subject.recordArgs, length)
	case "shell_family":
		end, ok = store.RecordPrefixGrantFor(agent, subject.tool, subject.family, length)
	case "path":
		end, ok = store.RecordPathGrantFor(agent, subject.path, length)
	case "network_hosts", "network_ports":
		end, ok = store.RecordNetworkGrantFor(agent, subject.hosts, length)
	default:
		t.Fatalf("fixture: unknown subject kind %q", subject.kind)
	}
	require.True(t, ok, "the store refused a valid timed answer: agent=%s kind=%s duration=%s", agent, subject.kind, duration)
	return end
}

func recordLegacyStoreSubject(t *testing.T, store *security.ApprovalGrantStore, session, agent string, subject allowanceStoreSubject) {
	t.Helper()
	switch subject.kind {
	case "tool", "shell_exact":
		require.True(t, store.Record(session, agent, subject.tool, subject.recordArgs))
	case "shell_family":
		require.True(t, store.RecordPrefixGrant(session, agent, subject.tool, subject.family))
	case "path":
		require.True(t, store.RecordPathGrant(session, agent, subject.path))
	case "network_hosts":
		require.True(t, store.RecordNetworkGrantWithHosts(session, agent, subject.hosts))
	case "network_ports":
		require.True(t, store.RecordNetworkGrant(session, agent))
	default:
		t.Fatalf("fixture: unknown subject kind %q", subject.kind)
	}
}

// Observe actual production lookups; this is an assertion helper, not a
// boolean-returning implementation of coverage. Path/access/hosts are exact.
func assertActualStoreSubject(t *testing.T, store *security.ApprovalGrantStore, session, agent string, subject allowanceStoreSubject, active bool) {
	t.Helper()
	context := fmt.Sprintf("agent=%s session=%s kind=%s active=%t", agent, session, subject.kind, active)
	switch subject.kind {
	case "tool", "shell_exact":
		assert.Equal(t, active, store.IsAllowed(session, agent, subject.tool, subject.callArgs), context)
	case "shell_family":
		assert.Equal(t, active, store.IsPrefixAllowed(session, agent, subject.tool, subject.family.Binary, []string{"run", "test", "--", "--watch"}, false), context)
	case "path":
		if active {
			assert.Equal(t, []fspolicy.PathGrant{subject.path}, store.PathGrantsFor(session, agent), context)
		} else {
			assert.Empty(t, store.PathGrantsFor(session, agent), context)
		}
	case "network_hosts":
		if active {
			assert.Equal(t, subject.hosts, store.NetworkHostsFor(session, agent), context)
		} else {
			assert.Empty(t, store.NetworkHostsFor(session, agent), context)
		}
	case "network_ports":
		assert.Equal(t, active, store.HasNetworkGrant(session, agent), context)
	default:
		t.Fatalf("fixture: unknown subject kind %q", subject.kind)
	}
}

func TestApprovalGrantStore_AgentScopedTimed(t *testing.T) {
	if slices.Contains(os.Args, "--allowance-store-restart-child") {
		home := config.OmnipusHomeDir()
		require.Equal(t, os.Getenv(config.EnvHome), home, "child uses the same actual app home")
		require.FileExists(t, filepath.Join(home, "entities", "agents", "mia.json"))
		fresh := security.NewApprovalGrantStore()
		for _, subject := range allowanceStoreSubjects(home) {
			for _, session := range []string{"source-chat", "other-chat"} {
				for _, agent := range []string{"mia", "jim"} {
					assertActualStoreSubject(t, fresh, session, agent, subject, false)
					source := subject
					source.callArgs = source.recordArgs
					assertActualStoreSubject(t, fresh, session, agent, source, false)
				}
			}
		}
		t.Log("RESTART_INSTRUMENT: same-home fresh process, all six grant kinds absent")
		return
	}
	t.Run("instrument independent wall and monotonic clock", func(t *testing.T) {
		c := allowanceStoreClock{wall: allowanceStoreStart()}
		c.advance(30 * time.Minute)
		c.wall = c.wall.Add(48 * time.Hour)
		assert.Equal(t, 30*time.Minute, c.elapsed)
		assert.Equal(t, time.Date(2026, time.November, 24, 12, 30, 0, 0, time.UTC), c.wall)
		c.wall = c.wall.Add(-96 * time.Hour)
		assert.Equal(t, 30*time.Minute, c.elapsed)
		assert.Equal(t, time.Date(2026, time.November, 20, 12, 30, 0, 0, time.UTC), c.wall)
	})
	t.Run("instrument whole app-home observer", testAllowanceTreeInstrument)
	t.Run("control actual nil and fresh stores have no consent", func(t *testing.T) {
		home := seedAllowanceAppHome(t)
		var nilStore *security.ApprovalGrantStore
		for _, subject := range allowanceStoreSubjects(home) {
			assertActualStoreSubject(t, nilStore, "source-chat", "mia", subject, false)
			assertActualStoreSubject(t, security.NewApprovalGrantStore(), "source-chat", "mia", subject, false)
		}
	})
	t.Run("S-120 real agent-only whole-tool scope", func(t *testing.T) {
		home := seedAllowanceAppHome(t)
		subject := allowanceStoreSubjects(home)[0]
		store := security.NewApprovalGrantStore()
		recordLegacyStoreSubject(t, store, "source-chat", "mia", subject)
		source := subject
		source.callArgs = source.recordArgs
		require.True(t, store.IsAllowed("source-chat", "mia", source.tool, source.callArgs), "instrument: actual source record exists")
		assertActualStoreSubject(t, store, "other-chat", "mia", source, true)
		assertActualStoreSubject(t, store, "source-chat", "mia", subject, true)
		assertActualStoreSubject(t, store, "source-chat", "jim", source, false)
		assert.False(t, store.IsAllowed("source-chat", "mia", "create_task", source.callArgs), "another tool is not covered")
	})
	t.Run("instrument legacy memory-only same-home fresh process", func(t *testing.T) {
		home := seedAllowanceAppHome(t)
		before := readAllowanceAppTree(t, home)
		store := security.NewApprovalGrantStore()
		for _, subject := range allowanceStoreSubjects(home) {
			recordLegacyStoreSubject(t, store, "source-chat", "mia", subject)
			subject.callArgs = subject.recordArgs // Observe the actual recorded source, not changed arguments.
			assertActualStoreSubject(t, store, "source-chat", "mia", subject, true)
		}
		assertAllowanceTreeUnchanged(t, before, readAllowanceAppTree(t, home))
		runAllowanceFreshProcess(t, home)
		assertAllowanceTreeUnchanged(t, before, readAllowanceAppTree(t, home))
	})
	t.Run("C-26 exact lengths and boundaries", testActualTimedStoreLengths)
	t.Run("C-26 monotonic lifetime ignores wall-clock jumps", testActualTimedStoreWallJumps)
	t.Run("D17#12 second answer uses the later end", testActualTimedStoreLaterEnd)
	t.Run("S-122 timed memory-only and fresh-process restart", func(t *testing.T) {
		home := seedAllowanceAppHome(t)
		before := readAllowanceAppTree(t, home)
		store := newTimedStoreWithClock(t, &allowanceStoreClock{wall: allowanceStoreStart()})
		for _, subject := range allowanceStoreSubjects(home) {
			end := recordActualTimedStore(t, store, "mia", subject, "3h")
			assert.Equal(t, time.Date(2026, time.November, 22, 15, 0, 0, 0, time.UTC), end)
			assertActualStoreSubject(t, store, "other-chat", "mia", subject, true)
		}
		assertAllowanceTreeUnchanged(t, before, readAllowanceAppTree(t, home))
		runAllowanceFreshProcess(t, home)
		assertAllowanceTreeUnchanged(t, before, readAllowanceAppTree(t, home))
	})
	// C-26 End: "when the agent is deleted ... drop its allowances"; the plan row of test 57 and the impact table
	// say ClearAgent "drops a deleted agent's allowances and revokes its tokens". The store half of that is
	// asserted here: every timed allowance of the deleted agent is gone, its tokens are gone from the store
	// and handed back so the caller can revoke them at the proxy; another agent keeps everything.
	// The proxy half is observed by the gateway rows (REST and delete_agent tool).
	t.Run("C-26 actual ClearAgent drops one agent's timed allowances and tokens and keeps another's", func(t *testing.T) {
		home := seedAllowanceAppHome(t)
		store := newTimedStoreWithClock(t, &allowanceStoreClock{wall: allowanceStoreStart()})
		tokens := map[string][]string{}
		for _, agent := range []string{"mia", "jim"} {
			for _, subject := range allowanceStoreSubjects(home) {
				recordActualTimedStore(t, store, agent, subject, "3h")
				subject.callArgs = subject.recordArgs
				assertActualStoreSubject(t, store, "source-chat", agent, subject, true)
			}
			for _, session := range []string{"chat-a-", "chat-b-"} {
				tokens[agent] = append(tokens[agent], store.SessionEgressToken(session+agent, agent))
			}
			sort.Strings(tokens[agent])
			require.Equal(t, tokens[agent], store.NetworkTokensForAgent(agent), "instrument: the store holds both of %s's tokens", agent)
		}
		assert.Equal(t, tokens["mia"], store.ClearAgent("mia"), "the cleared agent's tokens are handed back for revocation, sorted")
		for _, subject := range allowanceStoreSubjects(home) {
			subject.callArgs = subject.recordArgs
			assertActualStoreSubject(t, store, "source-chat", "mia", subject, false)
			assertActualStoreSubject(t, store, "source-chat", "jim", subject, true)
		}
		assert.Empty(t, store.NetworkTokensForAgent("mia"), "no token of the deleted agent stays in the store")
		assert.Equal(t, tokens["jim"], store.NetworkTokensForAgent("jim"), "another agent keeps its tokens")
	})
}

func testActualTimedStoreLengths(t *testing.T) {
	rows := []struct {
		duration string
		length   time.Duration
		end      time.Time
	}{
		{"1h", time.Hour, time.Date(2026, time.November, 22, 13, 0, 0, 0, time.UTC)},
		{"3h", 3 * time.Hour, time.Date(2026, time.November, 22, 15, 0, 0, 0, time.UTC)},
		{"1d", 24 * time.Hour, time.Date(2026, time.November, 23, 12, 0, 0, 0, time.UTC)},
	}
	for _, subject := range allowanceStoreSubjects(t.TempDir()) {
		for _, row := range rows {
			t.Run(subject.kind+"/"+row.duration, func(t *testing.T) {
				clock := &allowanceStoreClock{wall: allowanceStoreStart()}
				store := newTimedStoreWithClock(t, clock)
				end := recordActualTimedStore(t, store, "mia", subject, row.duration)
				require.Equal(t, row.end, end, "exact server wall-clock end, independently derived from C-26")
				clock.advance(row.length - time.Nanosecond)
				assertActualStoreSubject(t, store, "other-chat", "mia", subject, true)
				assertActualStoreSubject(t, store, "other-chat", "jim", subject, false)
				clock.advance(time.Nanosecond)
				assertActualStoreSubject(t, store, "other-chat", "mia", subject, false)
				clock.advance(time.Nanosecond)
				assertActualStoreSubject(t, store, "other-chat", "mia", subject, false)
			})
		}
	}
}

func testActualTimedStoreWallJumps(t *testing.T) {
	for _, shift := range []time.Duration{48 * time.Hour, -48 * time.Hour} {
		t.Run(shift.String(), func(t *testing.T) {
			clock := &allowanceStoreClock{wall: allowanceStoreStart()}
			store := newTimedStoreWithClock(t, clock)
			subject := allowanceStoreSubjects(t.TempDir())[0]
			end := recordActualTimedStore(t, store, "mia", subject, "1h")
			require.Equal(t, time.Date(2026, time.November, 22, 13, 0, 0, 0, time.UTC), end)
			clock.wall = clock.wall.Add(shift)
			clock.advance(time.Hour - time.Nanosecond)
			assertActualStoreSubject(t, store, "other-chat", "mia", subject, true)
			clock.advance(time.Nanosecond)
			assertActualStoreSubject(t, store, "other-chat", "mia", subject, false)
		})
	}
}

func testActualTimedStoreLaterEnd(t *testing.T) {
	for _, subject := range allowanceStoreSubjects(t.TempDir()) {
		for _, first := range []string{"3h", "1h"} {
			t.Run(subject.kind+"/first-"+first, func(t *testing.T) {
				clock := &allowanceStoreClock{wall: allowanceStoreStart()}
				store := newTimedStoreWithClock(t, clock)
				firstEnd := recordActualTimedStore(t, store, "mia", subject, first)
				firstHour, second := 15, "1h"
				want := time.Date(2026, time.November, 22, 15, 0, 0, 0, time.UTC)
				if first == "1h" {
					firstHour, second = 13, "3h"
					want = time.Date(2026, time.November, 22, 15, 30, 0, 0, time.UTC)
				}
				require.Equal(t, time.Date(2026, time.November, 22, firstHour, 0, 0, 0, time.UTC), firstEnd)
				clock.advance(30 * time.Minute)
				secondEnd := recordActualTimedStore(t, store, "mia", subject, second)
				require.Equal(t, want, secondEnd, "later end wins, including the reported server expiry")
				clock.advance(want.Sub(allowanceStoreStart()) - clock.elapsed - time.Nanosecond)
				assertActualStoreSubject(t, store, "other-chat", "mia", subject, true)
				clock.advance(time.Nanosecond)
				assertActualStoreSubject(t, store, "other-chat", "mia", subject, false)
				clock.advance(time.Nanosecond)
				assertActualStoreSubject(t, store, "other-chat", "mia", subject, false)
			})
		}
	}
}

func seedAllowanceAppHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvConfig, filepath.Join(home, "config.json"))
	require.Equal(t, home, config.OmnipusHomeDir(), "the actual process resolves this app home")
	for relative, body := range map[string]string{
		"config.json":                   `{"sandbox":{"tool_policies":{"delete_task":"ask","bash":"ask"}}}`,
		"entities/agents/mia.json":      `{"id":"mia","name":"Mia","tools":{"builtin":{"policies":{"delete_task":"ask"}}}}`,
		"entities/agents/jim.json":      `{"id":"jim","name":"Jim","tools":{"builtin":{"policies":{"delete_task":"ask"}}}}`,
		"system/retention-sentinel.bin": "KEEP-SYSTEM-9Z\n",
		"unlisted/nested/preserve.bin":  "KEEP-UNLISTED-9Z\n",
	} {
		path := filepath.Join(home, relative)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	}
	return home
}

type allowanceAppEntry struct {
	info fs.FileInfo
	body []byte
}

func readAllowanceAppTree(t *testing.T, home string) map[string]allowanceAppEntry {
	t.Helper()
	out := map[string]allowanceAppEntry{}
	require.NoError(t, filepath.WalkDir(home, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("unexpected non-regular persisted path %s", path)
		}
		rel, err := filepath.Rel(home, path)
		if err != nil {
			return err
		}
		var body []byte
		if !entry.IsDir() {
			body, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		out[filepath.ToSlash(rel)] = allowanceAppEntry{info: info, body: body}
		return nil
	}))
	return out
}

func assertAllowanceTreeUnchanged(t *testing.T, before, after map[string]allowanceAppEntry) {
	t.Helper()
	require.Len(t, after, len(before), "no added or removed persisted path anywhere in the actual home")
	for path, old := range before {
		current, found := after[path]
		require.True(t, found, "persisted path remains: %s", path)
		assert.Equal(t, old.info.Mode(), current.info.Mode(), path)
		assert.Equal(t, old.body, current.body, path)
		assert.True(t, os.SameFile(old.info, current.info), "no atomic same-bytes replacement: %s", path)
		assert.Equal(t, old.info.ModTime(), current.info.ModTime(), "no rewrite: %s", path)
	}
}

func testAllowanceTreeInstrument(t *testing.T) {
	home := seedAllowanceAppHome(t)
	before := readAllowanceAppTree(t, home)
	added := filepath.Join(home, "unlisted", "new-allowance.json")
	require.NoError(t, os.WriteFile(added, []byte(`{"allowance":"must-be-seen"}`), 0o600))
	after := readAllowanceAppTree(t, home)
	require.Len(t, after, len(before)+1, "observer includes a previously unlisted new file")
	require.Equal(t, []byte(`{"allowance":"must-be-seen"}`), after["unlisted/new-allowance.json"].body)
	target := filepath.Join(home, "entities", "agents", "mia.json")
	original := before["entities/agents/mia.json"]
	require.NoError(t, os.WriteFile(target, []byte(`{"id":"mia","unexpected":"write"}`), 0o600))
	assert.NotEqual(t, original.body, readAllowanceAppTree(t, home)["entities/agents/mia.json"].body)
	replacement := filepath.Join(home, "replacement.tmp")
	require.NoError(t, os.WriteFile(replacement, original.body, 0o600))
	require.NoError(t, os.Rename(replacement, target))
	current := readAllowanceAppTree(t, home)["entities/agents/mia.json"]
	assert.Equal(t, original.body, current.body)
	assert.False(t, os.SameFile(original.info, current.info), "instrument sees same-bytes replacement")
}

func runAllowanceFreshProcess(t *testing.T, home string) {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestApprovalGrantStore_AgentScopedTimed$", "-test.v", "--", "--allowance-store-restart-child")
	cmd.Env = os.Environ() // Includes this row's real OMNIPUS_HOME and OMNIPUS_CONFIG.
	output, err := cmd.CombinedOutput()
	t.Logf("same-home fresh process:\n%s", output)
	require.NoError(t, err, "child exit/timeout is a fixture failure, not restart acceptance")
	require.NoError(t, ctx.Err())
	require.Equal(t, home, config.OmnipusHomeDir())
	require.Contains(t, string(output), "RESTART_INSTRUMENT: same-home fresh process, all six grant kinds absent")
}
