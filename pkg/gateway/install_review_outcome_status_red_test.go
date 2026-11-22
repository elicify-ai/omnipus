//go:build goolm && stdjson

package gateway

// Order 53 — TestInstallReview_OutcomeStatus (S-115; Dataset D15; C-13 "One
// operation per review" and "Lost answer"). Oracle: skills-tools-redesign-spec.md —
//   C-13: GET /skills/install/review/{review_id} returns SkillInstallReviewStatus
//        (state staged|publishing|installed|failed|expired|cancelled, revision,
//        installed_revision, replaced_revision, reason); 404 when the record no
//        longer exists. The review id is also the install operation's id: while
//        publishing → 202 with the status; after installed → 200 with the same
//        result and NO second publish; after failed/expired/cancelled → the same
//        terminal answer again (409 or 410). A record is kept 15 minutes after a
//        terminal state, then removed.
//   D15: #1 staged→expired shows "Not installed."; #2 installed with
//        installed_revision R2; #3 publishing then installed; #4 still publishing
//        (the 60-second cut is the review screen's poll limit, TS half); #5 failed
//        with its reason and the skills list STILL shows the old revision (the list
//        never decides); #6 a failed read or 404; #7 Install twice with one review
//        id → one publish; #8 Install after Cancel → 409/410 again on retry.
//   The shown texts per row are the review screen's oracle — the TS half of this
//        order (src/components/skills/InstallReview.lostAnswer.test.tsx).
//
// Harness: the real HandleSkills routes and the package's upload recipe
// (skills_uat2_zip_install_test.go). The publish step is blocked or failed
// through the production seam publishRESTSkill (rest_skills.go, nil in
// production), restored by cleanup. The 15-minute review expiry has no entry
// point to advance — expireInstallReviews below is that seam; GREEN wires it to
// the review store's clock. Nil keeps the RED run loud (BLOCKED), never a skip.
//
// RED today: no review route exists — HandleSkills routes only GET /skills,
// POST /skills/install, /copy and DELETE (rest_skills.go::HandleSkills), so
// GET /skills/install/review/{id} and POST /skills/install/review fall to the
// default 405, and DELETE hits deleteSkill with a non-skill id. Every leg below
// fails on that missing route (order 35 owns the stage route's own red).
import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/skills"

	"github.com/stretchr/testify/require"
)

func TestInstallReview_OutcomeStatus(t *testing.T) {
	const weatherMD = "---\nname: weather\ndescription: Use when the install outcome test needs a skill.\n---\n\nBody.\n"

	// stageInstallReview uploads weather.zip and POSTs the stage-only review
	// (C-13). It fails the test with the missing-route reason when the review
	// endpoint does not answer 200.
	stageInstallReview := func(t *testing.T, api *restAPI) (reviewID, revision string) {
		t.Helper()
		zip := uat2BuildZip(t, []uat2ZipEntry{{name: "SKILL.md", body: weatherMD}})
		uploadDir := filepath.Join(api.homePath, "uploads", "owner-session")
		require.NoError(t, os.MkdirAll(uploadDir, 0o755))
		uploadPath := filepath.Join(uploadDir, "weather.zip")
		require.NoError(t, os.WriteFile(uploadPath, zip, 0o644))
		store := newTestFileMediaStore(t)
		api.mediaStore = store
		ref, err := store.Store(uploadPath, media.MediaMeta{Filename: "weather.zip", Source: "upload:webchat", CleanupPolicy: media.CleanupPolicyForgetOnly}, "upload:owner-session")
		require.NoError(t, err)
		r := httptest.NewRequest(http.MethodPost, "/api/v1/skills/install/review", bytes.NewReader([]byte(`{"upload_id":"`+ref+`"}`)))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.HandleSkills(w, r)
		require.Equal(t, http.StatusOK, w.Code,
			"C-13: the stage-only review endpoint POST /skills/install/review must exist, got %d, body: %s",
			w.Code, w.Body.String())
		var review struct {
			ReviewID string `json:"review_id"`
			Revision string `json:"revision"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &review), "body: %s", w.Body.String())
		require.NotEmpty(t, review.ReviewID, "the review response carries a review_id (C-13)")
		return review.ReviewID, review.Revision
	}

	t.Run("D15#6: the status route exists — an unknown review id is 404", func(t *testing.T) {
		api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
		r := httptest.NewRequest(http.MethodGet, "/api/v1/skills/install/review/no-such-review", nil)
		w := httptest.NewRecorder()
		api.HandleSkills(w, r)
		require.NotEqual(t, http.StatusMethodNotAllowed, w.Code,
			"C-13: GET /skills/install/review/{review_id} must exist, got 405 (no route)")
		require.Equal(t, http.StatusNotFound, w.Code,
			"C-13: an unknown review id is 404, got %d, body: %s", w.Code, w.Body.String())
	})

	t.Run("D15#2: a lost answer after publishing finished reports installed with its revision", func(t *testing.T) {
		api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
		reviewID, _ := stageInstallReview(t, api)

		// The Install response is "lost" — the test simply continues to the
		// status read, the recovery path S-115 pins.
		w := uat2InstallNamed(t, api, "weather.zip", uat2BuildZip(t, []uat2ZipEntry{{name: "SKILL.md", body: weatherMD}}),
			map[string]any{"review_id": reviewID})
		require.Equal(t, http.StatusOK, w.Code, "the install must publish: %s", w.Body.String())

		r := httptest.NewRequest(http.MethodGet, "/api/v1/skills/install/review/"+reviewID, nil)
		w2 := httptest.NewRecorder()
		api.HandleSkills(w2, r)
		require.Equal(t, http.StatusOK, w2.Code, "D15#2: the status read answers 200, got %d, body: %s", w2.Code, w2.Body.String())
		var status struct {
			State             string `json:"state"`
			InstalledRevision string `json:"installed_revision"`
			Revision          string `json:"revision"`
		}
		require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &status), "body: %s", w2.Body.String())
		require.Equal(t, "installed", status.State, "D15#2: state installed")
		require.NotEmpty(t, status.InstalledRevision, "D15#2: installed_revision is set when installed (C-13)")
		require.Equal(t, status.Revision, status.InstalledRevision,
			"D15#2: the installed revision equals the reviewed revision (the review shows Installed weather.)")
	})

	t.Run("D15#3: a slow publish is readable as publishing, then installed", func(t *testing.T) {
		api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
		release := make(chan struct{})
		original := publishRESTSkill
		publishRESTSkill = func(root, name, stagedDir, expectedRevision string) (skills.PublishOutcome, error) {
			<-release
			return skills.PublishOutcome{Revision: expectedRevision}, nil
		}
		t.Cleanup(func() { publishRESTSkill = original })

		reviewID, _ := stageInstallReview(t, api)
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			done <- uat2InstallNamed(t, api, "weather.zip", uat2BuildZip(t, []uat2ZipEntry{{name: "SKILL.md", body: weatherMD}}),
				map[string]any{"review_id": reviewID})
		}()

		// While publishing, the status read answers publishing (the review
		// screen shows "Checking…" and polls — TS half).
		r := httptest.NewRequest(http.MethodGet, "/api/v1/skills/install/review/"+reviewID, nil)
		w := httptest.NewRecorder()
		api.HandleSkills(w, r)
		require.Equal(t, http.StatusOK, w.Code, "the status read answers during a slow publish, got %d, body: %s", w.Code, w.Body.String())
		var status struct {
			State string `json:"state"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &status), "body: %s", w.Body.String())
		require.Equal(t, "publishing", status.State, "D15#3: the state is publishing while the publish runs")

		close(release)
		wPost := <-done
		require.Equal(t, http.StatusOK, wPost.Code, "the released install must publish: %s", wPost.Body.String())
	})

	t.Run("D15#5: a failed publish reports its reason and the list still shows the old revision", func(t *testing.T) {
		api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
		// Old weather R1 is installed first (the replacement case).
		wOld := uat2InstallNamed(t, api, "weather.zip", uat2BuildZip(t, []uat2ZipEntry{{name: "SKILL.md", body: weatherMD}}), nil)
		require.Equal(t, http.StatusOK, wOld.Code, "setup: the old install must succeed: %s", wOld.Body.String())

		original := publishRESTSkill
		publishRESTSkill = func(root, name, stagedDir, expectedRevision string) (skills.PublishOutcome, error) {
			return skills.PublishOutcome{Message: "the staged copy vanished"}, errPublishFailedForTest
		}
		t.Cleanup(func() { publishRESTSkill = original })

		reviewID, _ := stageInstallReview(t, api)
		w := uat2InstallNamed(t, api, "weather.zip", uat2BuildZip(t, []uat2ZipEntry{{name: "SKILL.md", body: weatherMD}}),
			map[string]any{"review_id": reviewID})
		require.NotEqual(t, http.StatusOK, w.Code, "D15#5: a failed publish is not a 200 install")

		r := httptest.NewRequest(http.MethodGet, "/api/v1/skills/install/review/"+reviewID, nil)
		w2 := httptest.NewRecorder()
		api.HandleSkills(w2, r)
		require.Equal(t, http.StatusOK, w2.Code, "body: %s", w2.Body.String())
		var status struct {
			State  string `json:"state"`
			Reason string `json:"reason"`
		}
		require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &status), "body: %s", w2.Body.String())
		require.Equal(t, "failed", status.State, "D15#5: the state is failed")
		require.NotEmpty(t, status.Reason, "D15#5: failed carries its reason (the review shows Not installed: <reason>.)")

		// The skills list still shows the old revision — the list never decides
		// (D15#5's second half; the shown text is the TS half).
		r3 := httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil)
		w3 := httptest.NewRecorder()
		api.HandleSkills(w3, r3)
		require.Equal(t, http.StatusOK, w3.Code, "body: %s", w3.Body.String())
		require.Contains(t, w3.Body.String(), "weather",
			"D15#5: the failed replacement leaves the old weather listed")
	})

	t.Run("D15#7: Install pressed twice with one review id publishes once", func(t *testing.T) {
		api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
		reviewID, _ := stageInstallReview(t, api)

		first := uat2InstallNamed(t, api, "weather.zip", uat2BuildZip(t, []uat2ZipEntry{{name: "SKILL.md", body: weatherMD}}),
			map[string]any{"review_id": reviewID})
		require.Equal(t, http.StatusOK, first.Code, "the first Install publishes: %s", first.Body.String())

		second := uat2InstallNamed(t, api, "weather.zip", uat2BuildZip(t, []uat2ZipEntry{{name: "SKILL.md", body: weatherMD}}),
			map[string]any{"review_id": reviewID})
		require.Contains(t, []int{http.StatusOK, http.StatusAccepted}, second.Code,
			"D15#7: the repeated answer is 202 or 200 with the same result, got %d, body: %s",
			second.Code, second.Body.String())
		require.Equal(t, first.Body.String(), second.Body.String(),
			"D15#7: the repeated answer carries the same result (idempotent, C-13)")
	})

	t.Run("D15#8: Install after Cancel is refused, and refused again on retry", func(t *testing.T) {
		api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
		reviewID, _ := stageInstallReview(t, api)

		del := httptest.NewRequest(http.MethodDelete, "/api/v1/skills/install/review/"+reviewID, nil)
		wDel := httptest.NewRecorder()
		api.HandleSkills(wDel, del)
		require.Equal(t, http.StatusNoContent, wDel.Code,
			"C-13: Cancel discards the staged review with 204, got %d, body: %s", wDel.Code, wDel.Body.String())

		w := uat2InstallNamed(t, api, "weather.zip", uat2BuildZip(t, []uat2ZipEntry{{name: "SKILL.md", body: weatherMD}}),
			map[string]any{"review_id": reviewID})
		require.Contains(t, []int{http.StatusConflict, http.StatusGone}, w.Code,
			"D15#8: Install after Cancel is 409 or 410, got %d, body: %s", w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "Nothing was installed.",
			"D15#8: the truthful cancel line (C-13), body: %s", w.Body.String())

		retry := uat2InstallNamed(t, api, "weather.zip", uat2BuildZip(t, []uat2ZipEntry{{name: "SKILL.md", body: weatherMD}}),
			map[string]any{"review_id": reviewID})
		require.Contains(t, []int{http.StatusConflict, http.StatusGone}, retry.Code,
			"D15#8: the retry answers the same terminal code again, got %d, body: %s", retry.Code, retry.Body.String())
	})

	t.Run("D15#1: an expired review is Not installed", func(t *testing.T) {
		if expireInstallReviews == nil {
			t.Fatal("BLOCKED: the 15-minute review expiry (C-13) has no entry point the test " +
				"can advance — required by Dataset D15 row 1 (staged, then expired → Not installed.)")
		}
		api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
		reviewID, _ := stageInstallReview(t, api)
		expireInstallReviews(t, api)

		w := uat2InstallNamed(t, api, "weather.zip", uat2BuildZip(t, []uat2ZipEntry{{name: "SKILL.md", body: weatherMD}}),
			map[string]any{"review_id": reviewID})
		require.Equal(t, http.StatusGone, w.Code,
			"C-13: an expired review is 410, got %d, body: %s", w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "This review expired. Open the skill again to review it.",
			"C-13: the expiry text, body: %s", w.Body.String())

		r := httptest.NewRequest(http.MethodGet, "/api/v1/skills/install/review/"+reviewID, nil)
		w2 := httptest.NewRecorder()
		api.HandleSkills(w2, r)
		var status struct {
			State string `json:"state"`
		}
		require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &status), "body: %s", w2.Body.String())
		require.Equal(t, "expired", status.State, "D15#1: the status state is expired")
	})
}

// errPublishFailedForTest is the error the D15#5 leg's publish seam returns
// (a failed publish must surface as the failed state with its reason).
var errPublishFailedForTest = &publishFailedError{}

type publishFailedError struct{}

func (*publishFailedError) Error() string { return "publish failed (test seam)" }

// expireInstallReviews advances every staged review past its 15-minute expiry
// (C-13). GREEN wires it to the review store's clock; nil keeps the RED run
// loud (BLOCKED), never a skip.
var expireInstallReviews func(t testing.TB, api *restAPI)
