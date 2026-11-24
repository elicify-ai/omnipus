//go:build goolm && stdjson

package config_test

// Order 49 / CHECK F17, Appendix C#19–22: C-19, S-110, all D13 rows.
// The legacy inputs, real workspace/mount, raw on-disk observers and whole-tree
// fingerprint are test-owned. They never come from the production planner.
// The only unbound adapter is the absent C-19 startup operation, with input-only
// stop and write-fault injection. It must call the real startup upgrader, never
// seed files or manufacture a result. Nil is loud BLOCKED in every D13 row.
// Boot-before-turn wiring and the Skills-tab notice remain UNVERIFIED until
// actual gateway startup and that UI are exercised; a bound migration helper
// alone cannot certify either. This file is partial F17, not their replacement.
// Plan: stop a real subprocess at each boundary, restart the same home twice,
// read config/agent/ledger/snapshot bytes directly, preserve an intervening
// person's edit, retry only the failed agent, and leave malformed input intact.
// Mutation targets (CHECK): snapshot from fixed Skill:allow; repeated snapshot
// rewrite; overwrite the person's edit; forget an in_flight agent; rewrite a
// done home. GREEN/mutation proof is deferred, not satisfied by nil guards.

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These are test-adapter arguments, not production or wire types. No expected
// maps, completion flags, fingerprints or notices are returned by the adapter.
var startSkillUpgradeAtBoot func(home string, boundary func(point, agent string), beforeAgentWrite func(agent string) error) error

const upgradeLedger = "entities/.skill-permissions-upgrade-ledger.json"
const upgradeSnapshot = "entities/.skill-permissions-upgrade-snapshot.json"
const upgradeSnapshotDone = "entities/.skill-permissions-upgrade-snapshot.done.json"

func seedLiteralS110Home(t *testing.T, malformedRay bool) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	write := func(relative, content string) {
		path := filepath.Join(home, relative)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
	// Literal old state. Do not marshal DefaultConfig/new policy structs:
	// that could erase the exact old master values being translated.
	write("config.json", `{"version":1,"agents":{"defaults":{"max_tool_iterations":3}},"sandbox":{"tool_policies":{"Skill":"deny","bash":"ask","edit_skill":"deny"},"skill_trust":"warn_unverified","god_mode":false},"tools":{"manifest":{"compressed":true,"preview_all_lazy":false},"mcp":{"discovery":{"enabled":true},"servers":{}}}}`)
	write("entities/agents/mia.json", `{"id":"mia","name":"Mia","skills":["plan"],"skill_grants":{"plan":"11111111-2222-3333-4444-555555555555"},"tools":{"builtin":{"policies":{"Skill":"ask","edit_skill":"deny"}}}}`)
	write("entities/agents/jim.json", `{"id":"jim","name":"Jim","skills":[],"tools":{"builtin":{"policies":{"edit_skill":"deny"}}}}`)
	for _, slug := range []string{"plan", "deep-research"} {
		write("skills/"+slug+"/SKILL.md", "---\nname: "+slug+"\ndescription: S110 literal old fixture.\n---\n\nLEGACY-"+slug+"-9Z\n")
		write("skills/"+slug+"/.omnipus-identity", "11111111-2222-3333-4444-555555555555\n")
	}
	mount, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	notes := filepath.Join(mount, ".claude", "skills", "notes")
	require.NoError(t, os.MkdirAll(notes, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(notes, "SKILL.md"), []byte("---\nname: notes\ndescription: S110 mounted project.\n---\n\nPROJECT-NOTES-9Z\n"), 0o600))
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{
		ID: "w1", Name: "W1", Status: "active", CoreTeam: []string{"mia"},
		CreatedAt: "2026-11-22T00:00:00Z", UpdatedAt: "2026-11-22T00:00:00Z",
	}))
	_, warning, err := workspace.CreateMount(home, "w1", "project", mount)
	require.NoError(t, err)
	require.Empty(t, warning)
	require.Equal(t, []string{mount}, workspace.AllowedMountRoots(home, "w1"), "fixture: actual protected workspace mount")
	write("unrelated/retain.bin", "DO-NOT-REWRITE-9Z\n")
	if malformedRay {
		write("entities/agents/ray.json", `{"id":"ray","tools":`)
	}
	// Instrument the raw fixture reader before any startup can change it.
	cfg := upgradeReadObject(t, home, "config.json")
	require.Equal(t, "deny", upgradeChildObject(t, upgradeChildObject(t, cfg, "sandbox"), "tool_policies")["Skill"])
	mia := upgradeReadObject(t, home, "entities/agents/mia.json")
	require.Equal(t, "ask", upgradeChildObject(t, upgradeChildObject(t, upgradeChildObject(t, mia, "tools"), "builtin"), "policies")["Skill"])
	return home
}

func upgradeReadObject(t testing.TB, home, relative string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, relative))
	require.NoError(t, err, "read actual persisted %s", relative)
	var object map[string]any
	require.NoError(t, json.Unmarshal(raw, &object), "parse actual persisted %s", relative)
	return object
}

func upgradeChildObject(t testing.TB, parent map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := parent[key].(map[string]any)
	require.True(t, ok, "actual object must carry %s", key)
	return value
}

// Include every file, directory, new name, mode and byte: no hand-maintained
// three-file list. Keep FileInfo to detect same-bytes atomic replacements too.
type upgradeDiskEntry struct {
	info fs.FileInfo
	body []byte
}

// readUpgradeTree fingerprints every path under home. A symlink is recorded as
// itself and never followed (CHECK round 4 N22): the fixture's real workspace
// mount (workspace.CreateMount) is a symlink inside the home to a host
// directory outside it, so the entry carries the link's own lstat metadata and
// its target text. Reading through it would fail on a directory target and pull
// the mounted project into the home fingerprint. A retargeted, retyped or
// replaced link still changes the fingerprint (target text, mode, inode).
func readUpgradeTree(t testing.TB, home string) map[string]upgradeDiskEntry {
	t.Helper()
	out := map[string]upgradeDiskEntry{}
	require.NoError(t, filepath.WalkDir(home, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(home, path)
		if err != nil {
			return err
		}
		var body []byte
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			var target string
			if target, err = os.Readlink(path); err != nil {
				return err
			}
			body = []byte(target)
		case !entry.IsDir():
			body, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		out[rel] = upgradeDiskEntry{info, body}
		return nil
	}))
	return out
}

func upgradeTreeDigest(tree map[string]upgradeDiskEntry) [32]byte {
	keys := make([]string, 0, len(tree))
	for key := range tree {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	hash := sha256.New()
	for _, key := range keys {
		entry := tree[key]
		fmt.Fprintf(hash, "%q:%o:%d:", key, entry.info.Mode(), len(entry.body))
		hash.Write(entry.body)
	}
	return [32]byte(hash.Sum(nil))
}

func assertUpgradeTreeUnchanged(t testing.TB, before, after map[string]upgradeDiskEntry) {
	t.Helper()
	require.Equal(t, upgradeTreeDigest(before), upgradeTreeDigest(after), "C-19: no bytes, names or modes change after done")
	for name, old := range before {
		current, ok := after[name]
		require.True(t, ok)
		assert.True(t, os.SameFile(old.info, current.info), "C-19: no same-bytes atomic rewrite of %s", name)
		assert.Equal(t, old.info.ModTime(), current.info.ModTime(), "C-19: no same-inode rewrite of %s", name)
	}
}

func personEditsPlanToAsk(t *testing.T, home string) {
	t.Helper()
	cfg := upgradeReadObject(t, home, "config.json")
	policies := upgradeChildObject(t, upgradeChildObject(t, cfg, "sandbox"), "skill_policies")
	policies["plan"] = "ask" // D13#9: real raw-file edit between process starts.
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), raw, 0o600))
}

func assertLiteralS110End(t *testing.T, home string, personEdit bool) {
	t.Helper()
	require.Equal(t, "done", upgradeReadObject(t, home, upgradeLedger)["state"])
	sandbox := upgradeChildObject(t, upgradeReadObject(t, home, "config.json"), "sandbox")
	global := upgradeChildObject(t, sandbox, "skill_policies")
	wantPlan := "deny"
	if personEdit {
		wantPlan = "ask"
	}
	assert.Equal(t, wantPlan, global["plan"], "S-110/D13: never infer legacy global from the fixed Allow")
	assert.Equal(t, "deny", global["deep-research"])
	assert.Equal(t, "deny", global["project:w1/notes"], "C-19: existing project skill also inherits the snapshot's global Deny")
	// C-6 requires the final stored fixed Allow; C-19 step 5 self-heals after
	// removing the old global restriction. It is never a source of old values.
	assert.Equal(t, "allow", upgradeChildObject(t, sandbox, "tool_policies")["Skill"])
	assert.Equal(t, "done", upgradeChildObject(t, sandbox, "migrations")["skill_permissions"])
	mia := upgradeChildObject(t, upgradeReadObject(t, home, "entities/agents/mia.json"), "tools")
	assert.Equal(t, map[string]any{"plan": "ask", "project:w1/notes": "ask"}, upgradeChildObject(t, upgradeChildObject(t, mia, "skills"), "policies"))
	assert.NotContains(t, upgradeChildObject(t, upgradeChildObject(t, mia, "builtin"), "policies"), "Skill", "old agent master removed")
	jim := upgradeChildObject(t, upgradeReadObject(t, home, "entities/agents/jim.json"), "tools")
	if rawSkills, exists := jim["skills"]; exists {
		skills, ok := rawSkills.(map[string]any)
		require.True(t, ok, "Jim's optional skills settings must be an object")
		assert.Empty(t, skills["policies"], "Jim had no master, so gets no backfill")
	}
	raw, err := os.ReadFile(filepath.Join(home, upgradeSnapshotDone))
	require.NoError(t, err, "C-19: snapshot retained as done")
	assert.True(t, json.Valid(raw))
	_, err = os.Stat(filepath.Join(home, upgradeSnapshot))
	assert.ErrorIs(t, err, os.ErrNotExist, "C-19: old snapshot was renamed, not left active")
	raw, err = os.ReadFile(filepath.Join(home, "unrelated/retain.bin"))
	require.NoError(t, err)
	assert.Equal(t, "DO-NOT-REWRITE-9Z\n", string(raw))
}

func TestSkillPermissionsUpgrade_StopAtEveryStep(t *testing.T) {
	if slices.Contains(os.Args, "--skill-upgrade-child") {
		nativebinding.Require(t, "pkg/config", startSkillUpgradeAtBoot,
			"C-19/S-110/D13: actual startup upgrader with input-only stop/write faults; disk/process observers stay real")
		home, point := os.Getenv("S110_HOME"), os.Getenv("S110_STOP")
		require.DirExists(t, home)
		t.Setenv(config.EnvHome, home)
		require.NoError(t, logger.EnableFileLogging(os.Getenv("S110_LOG")))
		t.Cleanup(logger.DisableFileLogging)
		err := startSkillUpgradeAtBoot(home, func(boundary, agent string) {
			if point != "" && boundary == point {
				if (point == "in-flight-before-write" || point == "record-written") && agent != "mia" {
					return // D13#5–6 explicitly stop on Mia, never the first other agent.
				}
				os.Exit(73) // D13: stop the actual test process, not a returned state.
			}
		}, func(agent string) error {
			if point == "jim-record-readonly" && agent == "jim" {
				return fs.ErrPermission // Fault at the atomic write, not ineffective root chmod.
			}
			return nil
		})
		if err != nil {
			require.Equal(t, "jim-record-readonly", point, "only the deliberately injected fault may fail startup")
			require.ErrorIs(t, err, fs.ErrPermission, "unexpected errors are never treated as the requested failure")
		}
		return
	}
	t.Run("disk observer instrument", assertUpgradeDiskInstrument)
	t.Run("disk observer instrument real workspace mount", assertUpgradeMountInstrument)
	t.Run("S-110 gateway boot before agent turns", func(t *testing.T) {
		t.Fatal("BLOCKED: C-19 gateway startup migration/turn-admission wiring is not implemented — required by S-110; a migration helper alone must never turn this boot-order obligation green")
	})
	for _, agent := range []string{"Jim", "Ray"} {
		t.Run("D13 Skills-tab notice names "+agent, func(t *testing.T) {
			t.Fatalf("BLOCKED: the actual Skills-tab upgrade notice is not implemented — required by C-19/D13#10–11; it must name %s, and a startup log is not a substitute for that UI", agent)
		})
	}
	rows := []string{"lock-before-snapshot", "snapshot", "ledger", "config-written", "in-flight-before-write", "record-written", "before-done", "marker", "person-edit", "jim-record-readonly", "ray-record-malformed"}
	for index, point := range rows {
		t.Run(fmt.Sprintf("D13#%d/%s", index+1, point), func(t *testing.T) {
			home := seedLiteralS110Home(t, point == "ray-record-malformed")
			nativebinding.Require(t, "pkg/config", startSkillUpgradeAtBoot,
				"C-19/S-110/D13: actual startup upgrader with input-only stop/write faults; disk/process observers stay real")
			start := func(stop string) []byte {
				t.Helper()
				logPath := filepath.Join(t.TempDir(), "startup.jsonl")
				exe, err := os.Executable()
				require.NoError(t, err)
				cmd := exec.Command(exe, "-test.run=^TestSkillPermissionsUpgrade_StopAtEveryStep$", "-test.v", "--", "--skill-upgrade-child")
				cmd.Env = append(os.Environ(), "S110_HOME="+home, "S110_STOP="+stop, "S110_LOG="+logPath)
				out, err := cmd.CombinedOutput()
				t.Logf("actual startup stop=%q:\n%s", stop, out)
				if stop != "" && stop != "jim-record-readonly" && stop != "ray-record-malformed" {
					var exit *exec.ExitError
					require.True(t, errors.As(err, &exit), "process must reach the requested stop, not return success")
					require.Equal(t, 73, exit.ExitCode())
				} else {
					require.NoError(t, err)
				}
				rawLog, readErr := os.ReadFile(logPath)
				require.NoError(t, readErr, "read actual child startup log")
				return rawLog
			}
			stop := point
			if point == "person-edit" {
				stop = "config-written"
			}
			firstLog := start(stop)
			if point == "jim-record-readonly" {
				assertUpgradeDiagnostic(t, firstLog, "error", "jim")
			}
			if point == "ray-record-malformed" {
				assertUpgradeDiagnostic(t, firstLog, "warn", "ray")
			}
			snapshotPath := filepath.Join(home, upgradeSnapshot)
			snapshot, snapshotErr := os.ReadFile(snapshotPath)
			if errors.Is(snapshotErr, os.ErrNotExist) && (point == "marker" || point == "ray-record-malformed") {
				snapshot, snapshotErr = os.ReadFile(filepath.Join(home, upgradeSnapshotDone))
			}
			if point == "lock-before-snapshot" {
				require.ErrorIs(t, snapshotErr, os.ErrNotExist, "D13#1: no snapshot was written before the stop")
			} else {
				require.NoError(t, snapshotErr, "C-19: snapshot must be durable before any later boundary")
			}
			if point == "in-flight-before-write" || point == "record-written" {
				assert.Equal(t, "mia", upgradeReadObject(t, home, upgradeLedger)["in_flight"], "D13#5–6: observe actual durable in-flight Mia at the stop")
			}
			if point == "person-edit" {
				personEditsPlanToAsk(t, home)
			}
			var miaBeforeJimRetry map[string]upgradeDiskEntry
			if point == "jim-record-readonly" {
				ledger := upgradeReadObject(t, home, upgradeLedger)
				assert.Equal(t, []any{"jim"}, ledger["pending"], "D13#10: failed agent stays pending, others complete")
				miaBeforeJimRetry = readUpgradeTree(t, filepath.Join(home, "entities", "agents", "mia.json"))
			}
			start("")
			if miaBeforeJimRetry != nil {
				assertUpgradeTreeUnchanged(t, miaBeforeJimRetry, readUpgradeTree(t, filepath.Join(home, "entities", "agents", "mia.json")))
			}
			assertLiteralS110End(t, home, point == "person-edit")
			if snapshotErr == nil {
				doneSnapshot, readErr := os.ReadFile(filepath.Join(home, upgradeSnapshotDone))
				require.NoError(t, readErr)
				assert.Equal(t, snapshot, doneSnapshot, "C-19: original snapshot never rewritten, including on replay")
			}
			before := readUpgradeTree(t, home)
			start("")
			assertUpgradeTreeUnchanged(t, before, readUpgradeTree(t, home))
			if point == "ray-record-malformed" {
				raw, err := os.ReadFile(filepath.Join(home, "entities/agents/ray.json"))
				require.NoError(t, err)
				assert.Equal(t, `{"id":"ray","tools":`, string(raw), "D13#11: malformed record is skipped, not repaired by the test")
			}
		})
	}
}

func assertUpgradeDiskInstrument(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "existing.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"marker":"before"}`), 0o600))
	before := upgradeTreeDigest(readUpgradeTree(t, home))
	require.NoError(t, os.WriteFile(filepath.Join(home, "new-unlisted-record.json"), []byte("ADDED-9Z"), 0o600))
	added := upgradeTreeDigest(readUpgradeTree(t, home))
	require.NotEqual(t, before, added, "instrument: a new unlisted path must be visible")
	require.NoError(t, os.WriteFile(path, []byte(`{"marker":"after"}`), 0o600))
	require.NotEqual(t, added, upgradeTreeDigest(readUpgradeTree(t, home)), "instrument: changed existing bytes must be visible")
	object := upgradeReadObject(t, home, "existing.json")
	require.Equal(t, "after", object["marker"], "instrument: raw reader sees actual changed state")
}

// upgradeTreeLinks lists the symlink entries of a fingerprint, sorted.
func upgradeTreeLinks(tree map[string]upgradeDiskEntry) []string {
	var links []string
	for name, entry := range tree {
		if entry.info.Mode()&os.ModeSymlink != 0 {
			links = append(links, name)
		}
	}
	slices.Sort(links)
	return links
}

// assertUpgradeMountInstrument is the positive control for the whole-tree
// fingerprint on the fixture's real workspace mount (CHECK round 4 N22).
// workspace.CreateMount puts a symlink to the host project directory inside
// the home, so the observer has to account for that link as itself: never
// reject it (a correct upgrader leaves the protected mount intact), never
// follow it out of the home, and still see an unrelated file change or a
// replaced link. It runs the real fixture and no upgrader, so it is observed
// on today's product.
func assertUpgradeMountInstrument(t *testing.T) {
	t.Run("the real mount link is recorded as itself and not followed", func(t *testing.T) {
		home := seedLiteralS110Home(t, false)
		mount := workspace.AllowedMountRoots(home, "w1")[0]
		tree := readUpgradeTree(t, home)
		links := upgradeTreeLinks(tree)
		require.Len(t, links, 1, "instrument: the fixture's one real workspace mount is the tree's one symlink: %v", links)
		require.Equal(t, mount, string(tree[links[0]].body), "instrument: the link is recorded with its own target text")
		for name, entry := range tree {
			require.False(t, strings.HasPrefix(name, links[0]+string(filepath.Separator)),
				"instrument: nothing behind the link enters the home fingerprint: %s", name)
			require.NotContains(t, string(entry.body), "PROJECT-NOTES-9Z",
				"instrument: the mounted project's files are outside the home fingerprint: %s", name)
		}
		assertUpgradeTreeUnchanged(t, tree, readUpgradeTree(t, home))
	})
	for _, change := range []struct {
		name string
		// digestSees is true when names, modes and bytes already show the
		// change; false when only the entry's own identity (inode) does, which
		// is what assertUpgradeTreeUnchanged's same-file check compares.
		digestSees bool
		apply      func(t *testing.T, home, link, mount string)
	}{
		{"an unrelated file's bytes change", true, func(t *testing.T, home, _, _ string) {
			require.NoError(t, os.WriteFile(filepath.Join(home, "unrelated", "retain.bin"), []byte("CHANGED-9Z\n"), 0o600))
		}},
		{"the mount link is retargeted", true, func(t *testing.T, _, link, _ string) {
			require.NoError(t, os.Remove(link))
			require.NoError(t, os.Symlink(t.TempDir(), link))
		}},
		{"the mount link is replaced by a regular file", true, func(t *testing.T, _, link, _ string) {
			require.NoError(t, os.Remove(link))
			require.NoError(t, os.WriteFile(link, []byte("not a link"), 0o600))
		}},
		{"the mount link is replaced by a new link to the same target", false, func(t *testing.T, _, link, mount string) {
			fresh := link + ".new"
			require.NoError(t, os.Symlink(mount, fresh))
			require.NoError(t, os.Rename(fresh, link)) // atomic replace: the new inode exists while the old one does
		}},
	} {
		t.Run("still sees: "+change.name, func(t *testing.T) {
			home := seedLiteralS110Home(t, false)
			mount := workspace.AllowedMountRoots(home, "w1")[0]
			before := readUpgradeTree(t, home)
			links := upgradeTreeLinks(before)
			require.Len(t, links, 1, "instrument: the fixture's one real workspace mount is the tree's one symlink: %v", links)
			change.apply(t, home, filepath.Join(home, links[0]), mount)
			after := readUpgradeTree(t, home)
			if change.digestSees {
				require.NotEqual(t, upgradeTreeDigest(before), upgradeTreeDigest(after),
					"instrument: the change is visible in the fingerprint")
				return
			}
			require.Equal(t, upgradeTreeDigest(before), upgradeTreeDigest(after),
				"instrument: a same-target link has the same name, mode and target text")
			require.False(t, os.SameFile(before[links[0]].info, after[links[0]].info),
				"instrument: the replacement link's own identity differs from the original's")
		})
	}
}

func assertUpgradeDiagnostic(t *testing.T, raw []byte, level, agent string) {
	t.Helper()
	matched := 0
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
		var entry map[string]any
		require.NoError(t, json.Unmarshal(line, &entry))
		if entry["level"] == level && strings.Contains(strings.ToLower(string(line)), agent) {
			matched++
		}
	}
	assert.Positive(t, matched, "C-19: actual %s diagnostic names %s", level, agent)
}
