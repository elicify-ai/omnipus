//go:build goolm && stdjson

package gateway

// Order 40 — TestProjectSkillKeys (S-102; C-19; FR-075, FR-080). Fix round 4, Part H, item 3.
//
// Oracle: docs/internal/specs/skills-tools-redesign-spec.md at b27c64109, scenario S-102:
//
//	Given installed `notes` at Allow, project skill `notes` in workspace W1 at Deny
//	      (`project:w1/notes`), Mia mounted on W1 and granted installed `notes`, and Ray mounted
//	      on W1 and not granted installed `notes`
//	When  Mia loads `notes`, then Ray loads `notes`
//	Then  Mia gets the installed skill's instructions (a granted installed skill wins over a
//	      same-slug project skill)
//	And   Ray's load is refused naming the Deny of the project skill `notes` in W1
//	And   afterwards installed `notes` is still Allow and `project:w1/notes` is still Deny
//	And   the expected winners are literal in the test; the test never calls `ResolveSkillName` to build them
//
// Names in this fixture: installed `notes` is the native fixture's installed `notes-helper`,
// workspace W1 is the fixture's `d10` (key `project:d10/notes-helper`), Mia is `test-agent`
// and Ray is `peer-agent`, both on the core team of `d10`.
//
// Where this test lives. It replaces pkg/config/project_skill_keys_red_test.go, which called a
// test-local seam, projectSkillWinnerOf(installedPolicy, projectPolicy, grantedInstalled), that
// no production code could fill: an internal pkg/config test cannot import pkg/tools (import
// cycle), its "winner" strings were never production output, and its leaf "S-102: both stored
// values survive the loads unchanged" compared two adapter returns instead of reading config.json
// (CHECK round 4, false-green table row 5). This version drives the real Skill load of the native D10 harness (real registry,
// real resolver, real policy gate, real config.json and real agent records; only the model is
// scripted) and reads config.json back. The spec's table keeps the name TestProjectSkillKeys.
//
// Fix round 4, Part H. Written against the spec; RED/GREEN class is stated in the pack from the
// saved runs, never from this comment. Mutation and proof of failability: deferred to CHECK.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The winners, literal (S-102's last line). Each is the instruction line of one fixture body:
// the installed skill's body is nativeSkillR1, the project skill's body is nativeSkillR2.
const (
	s102InstalledInstructions = "F18-NOTES-R1-INSTRUCTIONS-9Z"
	s102ProjectInstructions   = "F18-NOTES-R2-INSTRUCTIONS-7Q"
	s102InstalledKey          = "notes-helper"
	s102ProjectKey            = "project:d10/notes-helper"
	s102Mia                   = "test-agent"
	s102Ray                   = "peer-agent"
)

func TestProjectSkillKeys(t *testing.T) {
	require.Contains(t, nativeSkillR1, s102InstalledInstructions, "instrument: the installed fixture carries its literal marker")
	require.Contains(t, nativeSkillR2, s102ProjectInstructions, "instrument: the project fixture carries its literal marker")

	// Trust Allow and the installed key at Allow; the harness grants the installed skill to both agents.
	h, _ := newNativeD10Harness(t, "allow", "allow")
	seedS102ProjectSkill(t, h)
	s102UngrantRay(t, h)
	// One write holds both stored values and the upgrade marker (an upgraded install), then every
	// running agent is rebound from the live roster, which no longer lists Ray's grant.
	fields := applyNativeD10Sandbox(t, h, map[string]any{
		"skill_policies": map[string]string{s102InstalledKey: "allow", s102ProjectKey: "deny"},
		"migrations":     map[string]string{"skill_permissions": "done"},
	})
	var live map[string]string
	require.NoError(t, json.Unmarshal(fields["skill_policies"], &live))
	require.Equal(t, "allow", live[s102InstalledKey], "Given: the live configuration holds installed notes at Allow")
	require.Equal(t, "deny", live[s102ProjectKey], "Given: the live configuration holds the project skill at Deny")
	// The same two values are on disk before any load, so "still" below means unchanged, never created.
	assertS102Stored(t, h, "Given (before any load)")

	t.Run("S-102: Mia, granted the installed skill, gets the installed instructions", func(t *testing.T) {
		run := beginNativeD10(t, h, "load", "chat", s102Mia, "project")
		seen, results := s102Turn(t, h, run)
		require.Len(t, results, 1, "the model received exactly one Skill result")
		assert.Contains(t, results[0], s102InstalledInstructions,
			"S-102: a granted installed skill wins over a same-slug project skill, so Mia's load returns the installed instructions")
		assert.NotContains(t, seen, s102ProjectInstructions,
			"S-102: the project skill's instructions never reach Mia, whose granted installed skill won")
	})

	t.Run("S-102: Ray, not granted the installed skill, is refused naming the Deny of the project skill", func(t *testing.T) {
		run := beginNativeD10(t, h, "load", "chat", s102Ray, "project")
		seen, results := s102Turn(t, h, run)
		require.Len(t, results, 1, "the model received exactly one Skill result")
		// Spec line 260 (C-12): "A Deny anywhere ... refuses before either check: no card, and the refusal
		// names the setting". S-102 adds that the Deny named is the project skill's. The sentence
		// itself is not pinned by the spec, so this asserts what is: the refusal names the Deny and
		// the skill, and nothing of either skill's instructions reaches the model.
		assert.Contains(t, results[0], "Deny", "S-102: Ray's refusal names the Deny setting; got %q", results[0])
		assert.Contains(t, results[0], s102InstalledKey, "S-102: Ray's refusal names the skill; got %q", results[0])
		assert.NotContains(t, seen, s102InstalledInstructions,
			"S-102: the installed skill is not Ray's (not granted), so its instructions never reach him")
		assert.NotContains(t, seen, s102ProjectInstructions,
			"S-102: the project skill is at Deny, so its instructions never reach Ray")
		t.Logf("observed: Ray's refusal as the model received it: %q", results[0])
	})

	t.Run("S-102: afterwards installed notes is still Allow and the project key is still Deny", func(t *testing.T) {
		assertS102Stored(t, h, "after both loads")
	})
}

// assertS102Stored reads config.json back and checks the two stored values the scenario names.
func assertS102Stored(t *testing.T, h *nativeSkillHarness, when string) {
	t.Helper()
	for _, row := range []struct{ key, want string }{
		{s102InstalledKey, "allow"},
		{s102ProjectKey, "deny"},
	} {
		got, present := nativeD10StoredPolicy(t, h, row.key)
		assert.True(t, present, "%s: config.json holds %s", when, row.key)
		assert.Equal(t, row.want, got, "%s: stored value of %s", when, row.key)
	}
}

// seedS102ProjectSkill gives workspace d10 (core team Mia and Ray) a mount whose
// .claude/skills/notes-helper is the project skill of the same slug as the installed skill. The
// installed skill stays installed, which is the point of S-102 (seedNativeD10Project removes it,
// for tests where no same-name installed package may satisfy the load).
func seedS102ProjectSkill(t *testing.T, h *nativeSkillHarness) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	dir := filepath.Join(root, ".claude", "skills", s102InstalledKey)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(nativeSkillR2), 0o600))
	require.NoError(t, workspace.SaveRecord(h.api.homePath, workspace.Workspace{
		ID: "d10", Name: "D10 project", Status: "active", CoreTeam: []string{s102Mia, s102Ray},
	}))
	_, err = workspace.EnsureWorkDir(h.api.homePath, "d10")
	require.NoError(t, err)
	_, warning, err := workspace.CreateMount(h.api.homePath, "d10", "project", root)
	require.NoError(t, err)
	require.Empty(t, warning)
	require.Equal(t, []string{root}, workspace.AllowedMountRoots(h.api.homePath, "d10"))
	_, statErr := os.Stat(filepath.Join(h.api.homePath, "skills", s102InstalledKey, "SKILL.md"))
	require.NoError(t, statErr, "instrument: the installed skill of the same slug is still installed")
}

// s102UngrantRay removes Ray's grant of the installed skill through the agent store (the product's
// own grant removal, which also drops the binding), checks that Mia still holds hers, and makes the
// live roster say the same as the stored records. The running agents are rebound from the roster by
// the sandbox write that follows.
func s102UngrantRay(t *testing.T, h *nativeSkillHarness) {
	t.Helper()
	store := agentstore.New(h.api.homePath)
	mia, err := store.Get(s102Mia)
	require.NoError(t, err)
	require.Contains(t, mia.Skills, s102InstalledKey, "Given: Mia is granted the installed skill")
	require.NotEmpty(t, mia.SkillGrants[s102InstalledKey], "Given: Mia's grant is bound to the installed folder")

	changed, err := store.RemoveSkillGrant(s102Ray, s102InstalledKey)
	require.NoError(t, err)
	require.True(t, changed, "instrument: Ray held the grant before it was removed")
	ray, err := store.Get(s102Ray)
	require.NoError(t, err)
	require.Empty(t, ray.Skills, "Given: Ray is not granted the installed skill")
	require.Empty(t, ray.SkillGrants, "Given: Ray holds no binding of the installed skill")

	roster := h.api.agentLoop.GetConfig().Agents.List
	found := false
	for i := range roster {
		if roster[i].ID == s102Ray {
			roster[i].Skills, roster[i].SkillGrants = ray.Skills, ray.SkillGrants
			found = true
		}
	}
	require.True(t, found, "instrument: Ray is on the live roster")
}

// s102Turn waits for the turn to end and fails at once if it raises a card (a Deny refuses before
// any card, and an Allow raises none). It returns the text of every model request of the turn and
// the tool results the model received in the request after its Skill call.
func s102Turn(t *testing.T, h *nativeSkillHarness, run nativeSkillRun) (seen string, toolResults []string) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for finished := false; !finished; {
		if rows := h.reg.pendingApprovals(); len(rows) > 0 {
			t.Fatalf("S-102: the load raised a card (%d pending); a Deny refuses before any card and an Allow raises none", len(rows))
		}
		select {
		case err := <-run.done:
			require.NoError(t, err)
			finished = true
		case <-timer.C:
			t.Fatal("fixture UNVERIFIED: the native turn reached neither a card nor completion; a deadline is not a refusal")
		case <-tick.C:
		}
	}
	require.Empty(t, h.reg.pendingApprovals(), "no card is left after the turn")
	requests := h.script.captured(run.prompt)
	require.GreaterOrEqual(t, len(requests), 2, "the scripted model saw the call and then the tool result")
	var text strings.Builder
	for i, request := range requests {
		for _, message := range request {
			text.WriteString(message.Content)
			text.WriteByte('\n')
			for _, part := range message.SystemParts {
				text.WriteString(part.Text)
				text.WriteByte('\n')
			}
			if i > 0 && message.Role == "tool" {
				toolResults = append(toolResults, message.Content)
			}
		}
	}
	return text.String(), toolResults
}
