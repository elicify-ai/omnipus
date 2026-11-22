//go:build goolm && stdjson

package gateway

// Order 43 — TestEditSkill_SetCategoryOnce (S-104) and
// TestWriteSkillWorkflow_PublishesCategorisedSkill (S-98; C-21, FR-077).
// Oracles: skills-tools-redesign-spec.md —
//   S-104: skill trip-notes stored as Uncategorised; PUT
//        /skills/trip-notes/category with lifestyle saves it; a later PUT
//        with research is refused with 409 "The category is set at install
//        and cannot be changed." — the category is set once.
//   S-98: the write-skill workflow publishes trip-notes approved with
//        category Lifestyle; GET /skills lists trip-notes with category
//        lifestyle and no agent granted; GET /security/permissions shows
//        trip-notes: ask; trip-notes/SKILL.md contains no category field
//        added by Omnipus (the category is metadata, never a SKILL.md
//        front-matter field).
//
// Harness: the real skills routes (rest_skills.go::HandleSkills) and the
// real permissions read model (security_permissions_red_test.go::
// getPermissions). The write-skill workflow's publish entry carrying a
// category does not exist yet (FR-077 names it) — publishWrittenSkill below
// is that missing entry; GREEN wires it to the workflow's publish step
// (the approval-card outcome with the chosen category), and the assertions
// stay on the real read routes.
//
// RED today: the category route does not exist (rest_skills.go dispatches no
// category path), so S-104's first PUT answers 404, and the workflow publish
// entry is nil (BLOCKED).
import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const categoryOnceText = "The category is set at install and cannot be changed."

// seedUncategorisedSkill writes trip-notes as a plain installed skill with
// no category anywhere (the S-104 Given).
func seedUncategorisedSkill(t *testing.T, api *restAPI) {
	t.Helper()
	dir := filepath.Join(api.homePath, "skills", "trip-notes")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	body := "---\nname: trip-notes\ndescription: Notes for trips.\n---\n\nTrip notes body.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600))
}

// listedCategory GETs /skills and returns the category stored on the named
// entry ("" when the entry has no category member — the Uncategorised
// state).
func listedCategory(t *testing.T, api *restAPI, slug string) string {
	t.Helper()
	body := listSkillsBody(t, api)
	var skills []map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &skills), "GET /skills returns an array: %s", body)
	for _, s := range skills {
		if s["id"] == slug || s["name"] == slug {
			category, _ := s["category"].(string)
			return category
		}
	}
	require.FailNow(t, "S-104: GET /skills must list "+slug, "body: %s", body)
	return ""
}

func putCategory(t *testing.T, api *restAPI, slug, category string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/skills/"+slug+"/category",
		strings.NewReader(`{"category":"`+category+`"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)
	return w
}

func TestEditSkill_SetCategoryOnce(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	seedUncategorisedSkill(t, api)

	// The first PUT saves Lifestyle.
	w := putCategory(t, api, "trip-notes", "lifestyle")
	require.Equal(t, http.StatusOK, w.Code,
		"S-104: the first category PUT saves Lifestyle, got %d, body: %s", w.Code, w.Body.String())
	require.Equal(t, "lifestyle", listedCategory(t, api, "trip-notes"),
		"S-104: GET /skills lists trip-notes with category lifestyle after the save")

	// A later PUT is refused: the category is set once.
	w = putCategory(t, api, "trip-notes", "research")
	require.Equal(t, http.StatusConflict, w.Code,
		"S-104: the second category PUT must be refused with 409, got %d, body: %s", w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), categoryOnceText,
		"S-104: the refusal carries the spec's text")

	// The refusal changed nothing.
	require.Equal(t, "lifestyle", listedCategory(t, api, "trip-notes"),
		"S-104: the refused PUT leaves Lifestyle stored")
}

// publishWrittenSkill is the test-local seam for the write-skill workflow's
// publish entry (FR-077): given the approved card outcome for slug with the
// chosen category, it publishes the skill exactly as the workflow does.
// GREEN wires it to the real publish step; nil keeps the RED run loud
// (BLOCKED), never a skip.
var publishWrittenSkill func(t testing.TB, api *restAPI, slug, category string)

func TestWriteSkillWorkflow_PublishesCategorisedSkill(t *testing.T) {
	if publishWrittenSkill == nil {
		t.Fatal("BLOCKED: the write-skill workflow's publish entry carrying a category is not " +
			"implemented — required by skills-tools-redesign-spec S-98 and FR-077 (the " +
			"write-skill workflow sets the category)")
	}
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	publishWrittenSkill(t, api, "trip-notes", "lifestyle")

	// GET /skills lists trip-notes with category lifestyle.
	require.Equal(t, "lifestyle", listedCategory(t, api, "trip-notes"),
		"S-98: GET /skills lists trip-notes with category lifestyle")

	// No agent granted: no agent's skills grant carries trip-notes.
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	r = withAdminRole(r)
	w := httptest.NewRecorder()
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "GET /agents must answer, body: %s", w.Body.String())
	var agents []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &agents), "GET /agents returns an array: %s", w.Body.String())
	for _, agent := range agents {
		granted, _ := agent["skills"].([]any)
		for _, g := range granted {
			name, _ := g.(string)
			require.NotEqual(t, "trip-notes", name,
				"S-98: the published skill must not be granted to agent %v", agent["id"])
		}
	}

	// GET /security/permissions shows trip-notes: ask (unverified → Ask, C-3).
	t.Setenv("OMNIPUS_BEARER_TOKEN", permissionsTestBearer)
	routes := routeTable{}
	api.registerAdditionalEndpoints(routes)
	w = getPermissions(t, api, routes)
	require.Equal(t, http.StatusOK, w.Code, "permissions read model (body: %s", w.Body.String())
	require.Contains(t, w.Body.String(), `"trip-notes":"ask"`,
		"S-98: GET /security/permissions shows trip-notes: ask, body: %s", w.Body.String())

	// SKILL.md contains no category field added by Omnipus.
	raw, err := os.ReadFile(filepath.Join(api.homePath, "skills", "trip-notes", "SKILL.md"))
	require.NoError(t, err, "the published skill's SKILL.md must exist on disk")
	require.NotContains(t, string(raw), "category",
		"S-98: Omnipus must not add a category field to SKILL.md (the category is metadata)")
}
