//go:build goolm && stdjson

package gateway

// Team-lead round 11, item 1 (Skills & Tools #1963; test author Part H,
// finding 1). Oracle: skills-tools-redesign-spec.md S-102 "And Ray's load is
// refused naming the Deny of the project skill `notes` in W1"; C-12 "the
// refusal names the setting". A load refused by a project skill's own Deny
// names the project skill, its name and its workspace, so the person finds
// the row that refused it. The refusal of an installed skill's Deny keeps its
// wording.
//
// Fixture names: the native D10 harness's skill `notes-helper`; workspace W1
// is `d10`, named "D10 project"; the loading agent is `test-agent`.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	projectDenyRefusal   = "The project skill `notes-helper` in workspace \"D10 project\" is set to Deny in Skills & Tools. Change its permission there to use it."
	installedDenyRefusal = "`notes-helper` is set to Deny in Skills & Tools."
)

func TestSkillLoadRefusal_NamesTheProjectSkillsDeny(t *testing.T) {
	t.Run("project skill at Deny: the refusal names the project skill and its workspace", func(t *testing.T) {
		h, _ := newNativeD10Harness(t, "allow", "allow")
		seedNativeD10Project(t, h, nativeSkillR2)
		applyNativeD10Sandbox(t, h, map[string]any{
			"skill_policies": map[string]string{"project:d10/notes-helper": "deny"},
			"migrations":     map[string]string{"skill_permissions": "done"},
		})
		result := refusedSkillLoadResult(t, h, beginNativeD10(t, h, "load", "chat", "test-agent", "project"))
		assert.Contains(t, result, projectDenyRefusal, "S-102: the refusal names the Deny of the project skill in its workspace")
		assert.NotContains(t, result, "F18-NOTES-R2-INSTRUCTIONS-7Q", "the denied project skill's instructions never reach the model")
	})

	t.Run("installed skill at Deny: the refusal keeps its wording", func(t *testing.T) {
		h, _ := newNativeD10Harness(t, "allow", "deny")
		markNativeD10UpgradeDone(t, h)
		result := refusedSkillLoadResult(t, h, beginNativeD10(t, h, "load", "chat", "test-agent", "installed"))
		assert.Contains(t, result, installedDenyRefusal, "C-12: an installed skill's Deny names the setting as before")
		assert.NotContains(t, result, "project skill", "an installed skill's refusal never calls it a project skill")
	})
}

// refusedSkillLoadResult waits for the turn, fails if it raised a card (a Deny
// refuses before any card), and returns the one Skill result the model got.
func refusedSkillLoadResult(t *testing.T, h *nativeSkillHarness, run nativeSkillRun) string {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for finished := false; !finished; {
		if rows := h.reg.pendingApprovals(); len(rows) > 0 {
			t.Fatalf("the load raised a card (%d pending); a Deny refuses before any card", len(rows))
		}
		select {
		case err := <-run.done:
			require.NoError(t, err)
			finished = true
		case <-timer.C:
			t.Fatal("fixture UNVERIFIED: the turn did not complete; a deadline is not a refusal")
		case <-tick.C:
		}
	}
	requests := h.script.captured(run.prompt)
	require.GreaterOrEqual(t, len(requests), 2, "the scripted model saw the call and then the tool result")
	var results []string
	for _, message := range requests[len(requests)-1] {
		if message.Role == "tool" {
			results = append(results, message.Content)
		}
	}
	require.Len(t, results, 1, "the model received exactly one Skill result")
	t.Logf("observed refusal: %q", results[0])
	var payload struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(results[0]), &payload), "a refused load reaches the model as the denial payload")
	require.NotEmpty(t, payload.Message, "the denial payload carries a message")
	return payload.Message
}
