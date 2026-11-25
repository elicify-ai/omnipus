//go:build goolm && stdjson

package gateway

// Fix round 4, part E item 2 (team-lead) — a project skill has its own key, and
// a key nobody stored yet starts at Ask.
//
// Oracles (skills-tools-redesign-spec.md):
//   - C-19, the shelf table: the project shelf of workspace W has the key
//     `project:<W>/<slug>`, "created when Omnipus first reads W's project shelf
//     (turn start or reconcile)".
//   - C-19, the bullets under it: "A project skill and an installed skill with the
//     same slug have different keys" (S-102), and "Their starting permission is
//     Ask, like every newly added skill (ST-Q3 B)".
//   - FR-080: "`GET /skills` and agent turns MUST create missing project-skill
//     keys at first sight and remove them with their workspace or folder (C-19)".
//   - C-12 / C-24: a load whose permission is Ask raises the card (trust Allow
//     here, so the permission is the only open check).
//
// Why this test exists (handoff-5, item 3, and team-lead's part E item 2): the
// "successive turns" instrument stored only the installed key `notes-helper: allow`
// and expected the project skill to load. By C-19 that key is a different key, so
// the load must ask; the instrument now stores `project:d10/notes-helper: allow`
// (TestSkillConsent_Table), and this test pins the part that was never pinned: a
// project key with no stored value starts at Ask.
//
// The case "a slug added to the shelf after the mount gets its Ask key at the next
// turn" is NOT written here: the backend wrote it (pkg/gateway/
// project_skill_key_turn_start_red_test.go, commit cc616bf74) and team-lead ruled
// there is to be no second one; this file's first draft of it was removed unproved.
//
// RED-ness against a build without turn-start key creation: without a stored entry
// the load "fails closed to deny" and no card is raised (handoff-5, item 3), so the
// test fails at the wait for the card, with the C-19/FR-080 text below.
// GREEN, mutation probes and proof-of-failability are CHECK's.

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// applyNativeD10Sandbox merges one sandbox fragment into the live configuration the way
// seedNativeD10 stores the value of its project rows: unmarshal it into cfg.Sandbox, save
// config.json, reload the loop from that configuration and re-bind each agent's skill allow-list
// and grants. Every member of the fragment is read back from the marshalled sandbox, so a build
// that cannot hold it fails loudly instead of leaving the key unset.
func applyNativeD10Sandbox(t *testing.T, h *nativeSkillHarness, fragment map[string]any) map[string]json.RawMessage {
	t.Helper()
	cfg := h.api.agentLoop.GetConfig()
	raw, err := json.Marshal(fragment)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &cfg.Sandbox))
	sandbox, err := json.Marshal(cfg.Sandbox)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(sandbox, &fields))
	for member := range fragment {
		if len(fields[member]) == 0 {
			t.Fatalf("BLOCKED: native sandbox.%s input is not implemented — required by C-2/C-19", member)
		}
	}
	require.NoError(t, config.SaveConfig(filepath.Join(h.api.homePath, "config.json"), cfg))
	require.NoError(t, h.api.agentLoop.ReloadProviderAndConfig(context.Background(), h.script, cfg))
	for _, record := range cfg.Agents.List {
		inst, ok := h.api.agentLoop.GetRegistry().GetAgent(record.ID)
		require.True(t, ok)
		inst.ContextBuilder.WithSkillAllowlist(record.Skills).WithSkillBindings(record.SkillGrants)
	}
	return fields
}

// markNativeD10UpgradeDone records that the one-time skill-permission upgrade of C-19 is done,
// as it is on every upgraded install and on every fresh install after its first start
// (skills-tools-redesign-spec.md line 401: "sandbox.migrations.skill_permissions = done ... then
// the C-2 self-heal"; line 153: the self-heal "runs after the one-time migrations of C-19").
// Before that marker exists the system is mid-upgrade and the old per-agent limits still
// apply, so a test of the first-sight key creation must start from the upgraded state: without
// the marker it would test the mid-upgrade state, which the spec leaves to the upgrade itself.
func markNativeD10UpgradeDone(t *testing.T, h *nativeSkillHarness) {
	t.Helper()
	fields := applyNativeD10Sandbox(t, h, map[string]any{"migrations": map[string]string{"skill_permissions": "done"}})
	var migrations map[string]string
	require.NoError(t, json.Unmarshal(fields["migrations"], &migrations))
	require.Equal(t, "done", migrations["skill_permissions"], "the live configuration holds the upgrade marker")
}

// storeNativeD10Permission stores one skill-permission value of an upgraded install (marker
// included) in sandbox.skill_policies. The stored value is read back from the marshalled sandbox.
func storeNativeD10Permission(t *testing.T, h *nativeSkillHarness, key, value string) {
	t.Helper()
	fields := applyNativeD10Sandbox(t, h, map[string]any{
		"skill_policies": map[string]string{key: value},
		"migrations":     map[string]string{"skill_permissions": "done"},
	})
	var policies map[string]string
	require.NoError(t, json.Unmarshal(fields["skill_policies"], &policies))
	require.Equal(t, value, policies[key], "the live configuration holds the stored value")
}

// nativeD10StoredPolicy reads one key of the stored sandbox.skill_policies map from config.json.
func nativeD10StoredPolicy(t *testing.T, h *nativeSkillHarness, key string) (string, bool) {
	t.Helper()
	var document map[string]any
	require.NoError(t, json.Unmarshal(readNativeConfig(t, h.api.homePath), &document))
	sandbox, ok := document["sandbox"].(map[string]any)
	require.True(t, ok, "stored config sandbox must be an object")
	policies, _ := sandbox["skill_policies"].(map[string]any)
	value, present := policies[key].(string)
	return value, present
}

// awaitProjectKeyCard waits for the one pending card of a project skill's first load. It is the
// same wait as nativeSkillHarness.pending with this test's own failure text: a turn that ends
// without a card is a C-19 / FR-080 failure here, not a C-12 one.
func awaitProjectKeyCard(t *testing.T, h *nativeSkillHarness, run nativeSkillRun, key string) *approvalEntry {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if rows := h.reg.pendingApprovals(); len(rows) > 0 {
			require.Len(t, rows, 1)
			return rows[0]
		}
		select {
		case err := <-run.done:
			require.NoError(t, err, "fixture: a real turn error is not a consent decision")
			var results []string
			for _, request := range h.script.captured(run.prompt) {
				for _, message := range request {
					if message.Role == "tool" {
						results = append(results, message.Content)
					}
				}
			}
			t.Fatalf("C-19/FR-080: %s had no stored value, so its first sight at turn start must create it at Ask and the "+
				"load must raise the permission card; the turn ended without a card (tool results=%q)", key, results)
		case <-timer.C:
			t.Fatal("fixture UNVERIFIED: the project-skill turn reached neither a card nor completion")
		case <-tick.C:
		}
	}
}

// assertProjectKeyAskedAndRefused checks the card of a project key that started at Ask, answers it
// with Deny and checks that the key stays at Ask and the skill's instructions never reached the model.
func assertProjectKeyAskedAndRefused(t *testing.T, h *nativeSkillHarness, run nativeSkillRun, key, body string) {
	t.Helper()
	entry := awaitProjectKeyCard(t, h, run, key)
	snap := h.handler.pendingApprovalSnapshot(entry)
	require.NotNil(t, snap.Skill, "C-9: the card of a skill load carries the native skill facts")
	assert.Equal(t, key, snap.Skill.Key, "C-19: the card is for the project key, not for a same-slug installed key")
	stored, present := nativeD10StoredPolicy(t, h, key)
	assert.True(t, present, "C-19/FR-080: the key was created at first sight, before the person answered")
	assert.Equal(t, "ask", stored, "C-19: a project skill's starting permission is Ask")

	w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"deny"}`)
	require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
	finishNativeD10(t, h, run, false, false, body)
	stored, _ = nativeD10StoredPolicy(t, h, key)
	assert.Equal(t, "ask", stored, "a refused load changes no stored value")
}

// C-19 / S-102 / FR-080: the installed key of the same slug does not cover the project key; the
// project key starts at Ask and the first load asks.
func TestProjectSkillKey_StartsAtAskAtFirstSight(t *testing.T) {
	const key = "project:d10/notes-helper"
	h, _ := newNativeD10Harness(t, "allow", "allow") // installed key notes-helper: allow, trust Allow
	seedNativeD10Project(t, h, nativeSkillR2)        // mount d10 with project skill notes-helper
	markNativeD10UpgradeDone(t, h)                   // an upgraded install (C-19 upgrade done)
	// The fixture stores nothing for this key. C-19 lets the product create it at "turn start or
	// reconcile", so whether it already exists before the turn is recorded, not required: what is
	// required is that it exists, at Ask, by the time the load raises its card.
	_, early := nativeD10StoredPolicy(t, h, key)
	t.Logf("observed: key %s stored before the turn: %v", key, early)

	run := beginNativeD10(t, h, "load", "chat", "test-agent", "project")
	assertProjectKeyAskedAndRefused(t, h, run, key, nativeSkillR2)
}
