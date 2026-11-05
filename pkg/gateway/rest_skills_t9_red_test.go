package gateway

// RED tests — landing/skills (T9): the REST surface (§5.8). Scenarios
// S-6.10 (delete revision envelopes), S-6.5's REST legs via the new copy
// route (201/400/404/409), S-9.1/S-9.2/S-9.4 (read-time has_local_changes),
// S-11.19's REST legs (failure envelopes validating against the reused C-9
// ConfigurationMutationFailureState; the r5 rule that a cleanup warning
// rides the existing `message` field).
// Traces to: skills-spec-r15.md §5.8, §16, §17.1 rows 17/36/42, C-4/C-5/C-6/C-9.
//
// RED expectation: the copy route is a scaffold (501), DELETE ignores the
// revision query, the list rows lack has_local_changes, and no 500 leg
// produces the failure envelope — every RED assertion below fails on the
// baseline.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// t9SkillFixture writes a valid skill folder into the test API's skills dir
// and returns (skillsDir, revision).
func t9SkillFixture(t *testing.T, api *restAPI, slug string) (string, string) {
	t.Helper()
	skillsDir := filepath.Join(api.homePath, "skills")
	require.NoError(t, os.MkdirAll(filepath.Join(skillsDir, slug), 0o755))
	md := "---\nname: " + slug + "\ndescription: t9 fixture\n---\n\nbody of " + slug + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(skillsDir, slug, "SKILL.md"), []byte(md), 0o644))
	rev, err := skills.NewSkillWriter(skillsDir).SkillRevision(slug)
	require.NoError(t, err, "the live revision comes from the pinned revision helper")
	return skillsDir, rev
}

// t9Do runs a request against a handler and returns the recorder.
func t9Do(handler func(w http.ResponseWriter, r *http.Request), method, path, query, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.URL.Path = path
	req.URL.RawQuery = query
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

// t9Body parses a JSON response body into a map.
func t9Body(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m), "body must be JSON: %s", w.Body.String())
	return m
}

// TestRestDelete_RevisionEnvelope (S-6.10, M-09).
// BDD: DELETE with the current revision deletes (delete_skill by person);
// with a stale revision 409 SKILL_CHANGED; with no revision 400.
// Traces to: skills-spec-r15.md §5.8 delete row, S-6.10.
func TestRestDelete_RevisionEnvelope(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	skillsDir, rev := t9SkillFixture(t, api, "x")

	t.Run("current revision deletes", func(t *testing.T) {
		w := t9Do(api.HandleSkills, http.MethodDelete, "/api/v1/skills/x", "revision="+rev, "")
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		m := t9Body(t, w)
		for _, key := range []string{"persistence_status", "activation_status", "revision"} {
			assert.Contains(t, m, key, "the delete envelope is the reused ConfigurationMutationState (C-6/C-9)")
		}
		_, statErr := os.Stat(filepath.Join(skillsDir, "x"))
		assert.True(t, errors.Is(statErr, os.ErrNotExist), "the live folder is gone")
	})

	t.Run("stale revision is 409 SKILL_CHANGED", func(t *testing.T) {
		skillsDir2, _ := t9SkillFixture(t, api, "y")
		_ = skillsDir2
		w := t9Do(api.HandleSkills, http.MethodDelete, "/api/v1/skills/y", "revision=deadbeef", "")
		assert.Equal(t, http.StatusConflict, w.Code,
			"RED: today DELETE ignores the revision query and returns 200, body: %s", w.Body.String())
		assert.Contains(t, w.Body.String(), "SKILL_CHANGED", "the conflict names the code")
	})

	t.Run("missing revision is 400", func(t *testing.T) {
		t9SkillFixture(t, api, "z")
		w := t9Do(api.HandleSkills, http.MethodDelete, "/api/v1/skills/z", "", "")
		assert.Equal(t, http.StatusBadRequest, w.Code,
			"RED: today DELETE without revision deletes anyway, body: %s", w.Body.String())
	})
}

// TestRestCopy_Route (S-6.5's REST legs, §5.8 copy row).
// BDD: POST /skills/{name}/copy — 201 with the new skill; 400 invalid name;
// 404 missing source; 409 taken name (any case).
// Traces to: skills-spec-r15.md §5.8 copy row, D-1, S-6.5.
func TestRestCopy_Route(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	t9SkillFixture(t, api, "src")

	copyHandler := func(method, path, body string) *httptest.ResponseRecorder {
		return t9Do(api.HandleSkills, method, path, "", body)
	}

	t.Run("valid copy is 201 with the new skill", func(t *testing.T) {
		w := copyHandler(http.MethodPost, "/api/v1/skills/src/copy", `{"new_id":"dst"}`)
		assert.Equal(t, http.StatusCreated, w.Code,
			"RED: the copy route is a scaffold, got %d: %s", w.Code, w.Body.String())
		m := t9Body(t, w)
		require.Equal(t, "dst", m["id"], "the 201 body names the actual copy")
		t9ValidateSchema(t, "Skill", w)
		_, statErr := os.Stat(filepath.Join(api.homePath, "skills", "dst", "SKILL.md"))
		assert.NoError(t, statErr, "the destination exists")
	})

	t.Run("invalid name is 400", func(t *testing.T) {
		w := copyHandler(http.MethodPost, "/api/v1/skills/src/copy", `{"new_id":"Not A Slug"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("missing source is 404", func(t *testing.T) {
		w := copyHandler(http.MethodPost, "/api/v1/skills/absent/copy", `{"new_id":"dst2"}`)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("taken name (case twin) is 409", func(t *testing.T) {
		w := copyHandler(http.MethodPost, "/api/v1/skills/src/copy", `{"new_id":"SRC"}`)
		assert.Equal(t, http.StatusConflict, w.Code, "the one skill identity makes SRC and src collide")
	})
}

// TestRestList_HasLocalChanges (S-9.1, S-9.2 legs, S-9.4).
// BDD: rows carry has_local_changes; fresh install false; after an outside
// edit the flag is true at READ time and the read writes NO version.
// Traces to: skills-spec-r15.md §5.8 GET row, §9.2 rule 2, S-9.1–S-9.4.
func TestRestList_HasLocalChanges(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	skillsDir, _ := t9InstalledFixture(t, api, "x")

	w := t9Do(api.HandleSkills, http.MethodGet, "/api/v1/skills", "", "")
	require.Equal(t, http.StatusOK, w.Code)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows), "the list is a JSON array: %s", w.Body.String())
	require.NotEmpty(t, rows, "the fixture skill is listed")

	var target map[string]any
	for _, rm := range rows {
		if rm["id"] == "x" || rm["name"] == "x" {
			target = rm
		}
	}
	require.NotNil(t, target, "the fixture row is found")
	require.Contains(t, target, "has_local_changes",
		"RED: the list rows lack has_local_changes (§5.8 adds it), row: %v", target)
	assert.Equal(t, false, target["has_local_changes"], "S-9.1: fresh install is false")

	// Outside edit, then an immediate read: true at read time, no version
	// written by the read.
	historyRoot := filepath.Join(api.homePath, "system", "skill-history")
	manifestCount := func() int {
		entries, err := os.ReadDir(filepath.Join(historyRoot, "x", "versions"))
		if err != nil {
			require.NoError(t, err, "installed fixture must have readable history")
			return 0
		}
		return len(entries)
	}
	before := manifestCount()
	require.NoError(t, os.WriteFile(filepath.Join(skillsDir, "x", "SKILL.md"),
		[]byte("---\nname: x\ndescription: edited outside\n---\n\nnew body\n"), 0o644))

	w = t9Do(api.HandleSkills, http.MethodGet, "/api/v1/skills", "", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"has_local_changes":true`,
		"S-9.4: the read-time flag is true after an outside edit, body: %s", w.Body.String())
	assert.Equal(t, before, manifestCount(),
		"S-9.4: the read never writes history (no external_change version from a read)")
}

// TestRestS1119_FailureEnvelopes (S-11.19's REST legs; T-R3).
// BDD: every declared failure response is the reused ConfigurationMutation-
// FailureState shape (error_stage, persistence_status, activation_status,
// changed_fields); a class-3 success maps the cleanup warning into `message`
// and never invents a `warning` field.
// Traces to: skills-spec-r15.md S-11.19, C-4/C-5/C-6/C-9, §17.1 row 42.
func TestRestS1119_FailureEnvelopes(t *testing.T) {
	assertFailureEnvelope := func(t *testing.T, w *httptest.ResponseRecorder, wantStatus int) {
		t.Helper()
		require.Equal(t, wantStatus, w.Code, "body: %s", w.Body.String())
		m := t9Body(t, w)
		t9ValidateSchema(t, "ConfigurationMutationFailureState", w)
		stage, _ := m["error_stage"].(string)
		assert.NotEmpty(t, stage, "error_stage names the failed pipeline stage")
		persist, _ := m["persistence_status"].(string)
		assert.Contains(t, []string{"complete", "none", "partial"}, persist,
			"persistence_status is the C-9 enum")
		act, _ := m["activation_status"].(string)
		assert.Contains(t, []string{"active", "failed", "not_attempted"}, act,
			"activation_status is the C-9 enum")
		_, hasFields := m["changed_fields"]
		assert.True(t, hasFields, "changed_fields present (possibly empty)")
	}

	t.Run("leg d: delete manifest failure is 500 with the failure envelope", func(t *testing.T) {
		api, cleanup := newTestRestAPI(t)
		defer cleanup()
		_, rev := t9InstalledFixture(t, api, "x")
		prev := skills.FaultManifestWrite
		skills.FaultManifestWrite = t9FaultAlways
		defer func() { skills.FaultManifestWrite = prev }()

		w := t9Do(api.HandleSkills, http.MethodDelete, "/api/v1/skills/x", "revision="+rev, "")
		assertFailureEnvelope(t, w, http.StatusInternalServerError)
	})

	t.Run("leg b: copy storage failure is 500 with the failure envelope", func(t *testing.T) {
		api, cleanup := newTestRestAPI(t)
		defer cleanup()
		_, rev := t9InstalledFixture(t, api, "src")
		_ = rev
		prev := skills.FaultManifestWrite
		skills.FaultManifestWrite = t9FaultAlways
		defer func() { skills.FaultManifestWrite = prev }()

		w := t9Do(api.HandleSkills, http.MethodPost, "/api/v1/skills/src/copy", "", `{"new_id":"dst"}`)
		assertFailureEnvelope(t, w, http.StatusInternalServerError)
	})

	t.Run("leg a: class-3 success maps the warning into message, never a warning field", func(t *testing.T) {
		api, cleanup := newTestRestAPI(t)
		defer cleanup()
		_, rev := t9InstalledFixture(t, api, "x")
		prevSwap := skills.FaultSwapRemove
		skills.FaultSwapRemove = t9FaultAlways
		defer func() { skills.FaultSwapRemove = prevSwap }()

		w := t9Do(api.HandleSkills, http.MethodDelete, "/api/v1/skills/x", "revision="+rev, "")
		require.Equal(t, http.StatusOK, w.Code,
			"RED: the class-3 leg still completes (RED today: DELETE ignores faults entirely), body: %s", w.Body.String())
		m := t9Body(t, w)
		assert.NotContains(t, m, "warning", "no warning field exists on the envelope (r5, R4-MAJ-001)")
		msg, _ := m["message"].(string)
		assert.Contains(t, msg, "cleanup is incomplete",
			"the cleanup warning rides the existing message field")
	})
}

// t9FaultAlways is the always-failing fault hook (path-parameterized seam).
func t9FaultAlways(path string) error {
	return &t9FaultError{path: path}
}

// t9FaultError is the injected storage failure.
type t9FaultError struct{ path string }

func (e *t9FaultError) Error() string { return "t9 injected storage failure: " + e.path }

func t9ValidateSchema(t *testing.T, name string, w *httptest.ResponseRecorder) {
	t.Helper()
	msg, serverErr := validateBodyAgainstSchema(name, w.Body.Bytes())
	require.False(t, serverErr, "declared schema is loadable: %s", msg)
	require.Empty(t, msg, "real response validates against declared %s: %s", name, w.Body.String())
}

// A true marketplace-style installation, not an externally written folder.
// Stage bytes are fixture input; publication, history and read-time flags real.
func t9InstalledFixture(t *testing.T, api *restAPI, slug string) (string, string) {
	t.Helper()
	root := filepath.Join(api.homePath, "skills")
	require.NoError(t, os.MkdirAll(root, 0o755))
	staged := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(staged, "SKILL.md"), []byte("---\nname: "+slug+"\ndescription: t9 installed fixture\n---\ninstalled body\n"), 0o644))
	out, err := skills.ApplyChange(root, skills.ChangeRequest{Kind: skills.KindInstall, Slug: slug, StagedDir: staged, Expect: skills.Expectation{Absent: true}, Actor: skills.ChangeActor{Kind: "person", Tool: "rest-install"}})
	require.NoError(t, err, "RED: real installation pipeline required for installed-state fixture")
	return root, out.Revision
}

func TestRestList_ExternalNoHistoryIsLocallyChanged(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	t9SkillFixture(t, api, "external")
	w := t9Do(api.HandleSkills, http.MethodGet, "/api/v1/skills", "", "")
	require.Equal(t, 200, w.Code)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
	for _, row := range rows {
		if row["id"] == "external" {
			assert.Equal(t, true, row["has_local_changes"], "r15 §9.2 rule 3: no-history is conservatively local")
			return
		}
	}
	t.Fatal("external fixture was not listed")
}
