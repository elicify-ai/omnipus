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
// Harness: actual skills REST for S-104. CHECK F17 removes the whole
// publishWrittenSkill callback: S-98 now runs the real WS/loop/Skill/question
// /person-answer/write_file/edit_skill chain in workflow_w5_native_red_test.go.
// Only its model is scripted; no helper authors the expected final folder.
// Missing category behavior remains loud. GREEN, mutations and
// proof-of-failability defer to independent CHECK.
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

// Keep the spec-named root stable; all acceptance assertions now drive the
// actual workflow instead of accepting a pre-published folder from a callback.
func TestWriteSkillWorkflow_PublishesCategorisedSkill(t *testing.T) {
	testWriteSkillWorkflowNative(t)
}
