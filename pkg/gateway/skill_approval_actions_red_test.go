//go:build goolm && stdjson

package gateway

// Order 51 — TestSkillApprovalActions_Handler (S-112, S-114; C-24). Oracle:
// skills-tools-redesign-spec.md —
//   C-24 offered actions: an action the card did not offer → 409 "This
//        choice is not available for this request.", nothing stored
//        (S-112's third leg: approve_version_allow_for on a card that does
//        not offer it).
//   S-112: a combined card answered after the skill's file changed → 409
//        "The skill changed after this request.", no trust approval and no
//        allowance stored, the waiting call refused with that text; the
//        same answer again → 404 and nothing happens twice.
//   S-114: a seam-failed allowance record → trust stored, call released,
//        applied.timed_allowance false and not_applied_reason "Approved for
//        this request only. The time limit was not saved: <reason>."; a
//        seam-failed trust write → 500 "Could not save the approval.
//        Nothing was changed.", nothing stored, the card still answerable.
//
// Harness: the real approvals handler (rest_tool_registry.go::
// HandleToolApprovals) and the real raise path the loop uses
// (approvals.go::approvalRegistryV2.requestApproval) — the same seeding the
// ADR-092 prefix-grant test uses. The skill semantics of a pending approval
// (its skill key, revision, offered actions) do not exist yet —
// seedSkillCardApproval below is that missing seed; GREEN wires it to the
// card-raising side that stores the C-12 decision, and the stale/replay and
// partial-failure legs run through the real handler.
//
// RED today: the handler validates actions against today's set only
// ("unknown action … must be allow, allow_once, deny, or cancel"), so leg 1
// answers 400 where the landed rule answers 409 with the spec's text, and
// the skill legs are BLOCKED at their seams.
import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const notOfferedText = "This choice is not available for this request."

func TestSkillApprovalActions_Handler(t *testing.T) {
	t.Run("C-24: an action the card did not offer is 409 with the spec's text", func(t *testing.T) {
		api, reg := newTestRestAPIWithApprovalReg(t)
		entry, accepted := reg.requestApproval(
			"tc-skill-1", "Skill", map[string]any{"name": "notes-helper"},
			"mia", "session-s112", "turn-s112",
		)
		require.True(t, accepted, "the raised approval must be admitted as pending")
		require.NotNil(t, entry)

		w := postToolApprovalRaw(t, api, entry.ApprovalID,
			`{"action":"approve_version_allow_for","duration":"3h"}`)
		require.Equal(t, http.StatusConflict, w.Code,
			"C-24: an unoffered action must be 409 with the spec's text, got %d, body: %s",
			w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), notOfferedText,
			"C-24: the 409 carries the spec's text, body: %s", w.Body.String())
	})

	t.Run("S-112: a card answered after the skill changed stores nothing; the replay is 404", func(t *testing.T) {
		if seedSkillCardApproval == nil {
			t.Fatal("BLOCKED: the pending skill approval (its C-12 decision, skill key and " +
				"revision, offered actions) is not implemented — required by C-24's stored-entry " +
				"binding and S-112's combined-card legs")
		}
		api, reg := newTestRestAPIWithApprovalReg(t)
		approvalID := seedSkillCardApproval(t, api, reg, "notes-helper", "R1", true)

		// The skill's file changes before the answer.
		changeSkillRevision(t, api, "notes-helper")

		w := postToolApprovalRaw(t, api, approvalID,
			`{"action":"approve_version_allow_for","duration":"3h"}`)
		require.Equal(t, http.StatusConflict, w.Code,
			"S-112: the stale answer must be 409, got %d, body: %s", w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "The skill changed after this request.",
			"S-112: the refusal carries the spec's text")

		// Nothing was stored (no trust approval file).
		require.NoFileExists(t, trustApprovalsPath(api),
			"S-112: a stale answer must store no trust approval")

		// The same answer sent twice: the second is 404 — nothing happens twice.
		w = postToolApprovalRaw(t, api, approvalID,
			`{"action":"approve_version_allow_for","duration":"3h"}`)
		require.Equal(t, http.StatusNotFound, w.Code,
			"S-112: the replay must be 404, got %d, body: %s", w.Code, w.Body.String())
	})

	t.Run("S-114: a half-saved approval is reported as half-saved", func(t *testing.T) {
		if seedSkillCardApproval == nil || breakAllowanceRecording == nil || breakTrustApprovalWrite == nil {
			t.Fatal("BLOCKED: the skill approval's effect order with its failure seams (C-24: " +
				"trust approval, then timed allowance, then release) is not implemented — " +
				"required by S-114")
		}
		api, reg := newTestRestAPIWithApprovalReg(t)

		// The allowance record fails after the trust approval stored.
		approvalID := seedSkillCardApproval(t, api, reg, "notes-helper", "R1", true)
		breakAllowanceRecording(t, api)
		w := postToolApprovalRaw(t, api, approvalID,
			`{"action":"approve_version_allow_for","duration":"3h"}`)
		require.Equal(t, http.StatusOK, w.Code,
			"S-114: the call is still released when only the allowance failed, got %d, body: %s",
			w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), `"timed_allowance":false`,
			"S-114: applied.timed_allowance is false, body: %s", w.Body.String())
		require.Contains(t, w.Body.String(),
			"Approved for this request only. The time limit was not saved:",
			"S-114: not_applied_reason reads the spec's line, body: %s", w.Body.String())

		// The trust-approval write fails instead: nothing stored, the card
		// can still be answered.
		api, reg = newTestRestAPIWithApprovalReg(t)
		approvalID = seedSkillCardApproval(t, api, reg, "notes-helper", "R1", true)
		breakTrustApprovalWrite(t, api)
		w = postToolApprovalRaw(t, api, approvalID,
			`{"action":"approve_version_allow_for","duration":"3h"}`)
		require.Equal(t, http.StatusInternalServerError, w.Code,
			"S-114: a failed trust write is 500, got %d, body: %s", w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "Could not save the approval. Nothing was changed.",
			"S-114: the 500 carries the spec's text")
		require.NoFileExists(t, trustApprovalsPath(api),
			"S-114: a failed trust write stores nothing")
	})
}

// postToolApprovalRaw POSTs an action body to HandleToolApprovals the way
// rest_tool_registry_test.go::postToolApproval does, with the caller's exact
// body (action and duration).
func postToolApprovalRaw(t *testing.T, api *restAPI, approvalID, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tool-approvals/"+approvalID, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)
	w := httptest.NewRecorder()
	r.URL.Path = "/api/v1/tool-approvals/" + approvalID
	api.HandleToolApprovals(w, r)
	return w
}

// changeSkillRevision changes the skill's file on disk, so its revision
// differs from the one a seeded card bound (the real revision input the
// handler re-checks under the C-24 lock).
func changeSkillRevision(t *testing.T, api *restAPI, slug string) {
	t.Helper()
	path := filepath.Join(api.homePath, "skills", slug, "SKILL.md")
	require.NoError(t, os.WriteFile(path,
		[]byte("---\nname: "+slug+"\ndescription: Changed after the card was raised.\n---\n\nChanged body.\n"),
		0o600), "the seeded skill must exist on disk to change its revision")
}

// seedSkillCardApproval raises a pending skill approval through the
// production path with its C-12 decision stored: skillKey, the revision the
// card bound, and combined (both checks open). It returns the approval id.
// GREEN wires it to the card-raising side; nil keeps the RED run loud
// (BLOCKED), never a skip.
var seedSkillCardApproval func(t testing.TB, api *restAPI, reg *approvalRegistryV2, skillKey, revision string, combined bool) string

// breakAllowanceRecording makes the NEXT timed-allowance record fail (the
// C-24 partial-failure seam for S-114's first half). GREEN wires it to the
// allowance store; nil keeps the RED run loud (BLOCKED).
var breakAllowanceRecording func(t testing.TB, api *restAPI)

// breakTrustApprovalWrite makes the NEXT trust-approval durable write fail
// (S-114's second half). GREEN wires it to the trust store; nil keeps the
// RED run loud (BLOCKED).
var breakTrustApprovalWrite func(t testing.TB, api *restAPI)

// trustApprovalsPath is where C-24 stores a person's trust approvals
// ($OMNIPUS_HOME/system/skill-trust-approvals.json).
func trustApprovalsPath(api *restAPI) string {
	return api.homePath + "/system/skill-trust-approvals.json"
}
