//go:build goolm && stdjson

package gateway

// Order 35 — TestInstallReview_StageBindsRevision (S-94, S-95; C-13).
// Oracle: skills-tools-redesign-spec.md —
//   S-95: trust Ask; POST /skills/install for an unverified skill WITHOUT
//        review_id → 409 "Review needed: this skill is unverified, and
//        unverified skills are set to Ask. Review it in Skills & Tools
//        first." and nothing is installed.
//   S-94: a review of uploaded weather.zip at revision R1; staged files
//        change before Install → the answer is "The skill changed after
//        review. Review it again." and nothing is installed; Cancel before
//        Install discards the staged copy with "Nothing was installed.".
//
// Harness: the real install route and the package's upload recipe
// (skills_uat2_zip_install_test.go::uat2InstallNamed — a real upload in a
// test media store). The review route path is the one already cited by
// order 6's test (POST /skills/install/review); the S-94 texts are quoted
// from the spec, never from an implementation.
//
// RED today: the install path reads no trust and no review binding at all
// (rest_skills.go::installSkill → publishSkillInstall has no trust gate and
// SkillInstallRequest carries no review_id), and the review route does not
// exist, so S-95 installs today (200) and S-94's legs answer 404.
import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/media"

	"github.com/stretchr/testify/require"
)

func TestInstallReview_StageBindsRevision(t *testing.T) {
	const s95Text = "Review needed: this skill is unverified, and unverified skills are set to Ask. Review it in Skills & Tools first."
	const staleText = "The skill changed after review. Review it again."
	const cancelText = "Nothing was installed."

	t.Run("S-95: a direct install of an unverified skill under trust Ask is refused without a review", func(t *testing.T) {
		api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
		// The zip must carry SKILL.md at its ROOT (a wrapped folder is
		// refused as a client error — that 400 is not this row).
		zip := uat2BuildZip(t, []uat2ZipEntry{{name: "SKILL.md", body: "---\nname: weather\ndescription: Use when the install review test needs a skill.\n---\n\nBody.\n"}})
		// No review_id in the request — the direct install of an unverified
		// zip (no manifest → unverified).
		w := uat2InstallNamed(t, api, "weather.zip", zip, nil)
		require.Equal(t, http.StatusConflict, w.Code, "S-95: the direct install must be refused with 409, got %d, body: %s", w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), s95Text, "S-95: the refusal carries the spec's text")

		// Nothing on disk afterwards.
		installed := filepath.Join(api.homePath, "skills", "weather")
		_, err := os.Stat(installed)
		require.True(t, os.IsNotExist(err), "S-95: nothing may be installed at %s", installed)
	})

	t.Run("S-94: the stage-only review endpoint exists and binds the revision", func(t *testing.T) {
		api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
		zip := uat2BuildZip(t, []uat2ZipEntry{{name: "weather/SKILL.md", body: uat2SkillMD}})
		uploadDir := filepath.Join(api.homePath, "uploads", "owner-session")
		require.NoError(t, os.MkdirAll(uploadDir, 0o755))
		uploadPath := filepath.Join(uploadDir, "weather.zip")
		require.NoError(t, os.WriteFile(uploadPath, zip, 0o644))
		store := newTestFileMediaStore(t)
		api.mediaStore = store
		ref, err := store.Store(uploadPath, media.MediaMeta{Filename: "weather.zip", Source: "upload:webchat", CleanupPolicy: media.CleanupPolicyForgetOnly}, "upload:owner-session")
		require.NoError(t, err)

		// Stage-only review: returns the review id, the reviewed revision and
		// the verification result (C-13), installs nothing.
		r := httptest.NewRequest(http.MethodPost, "/api/v1/skills/install/review", strings.NewReader(`{"upload_id":"`+ref+`"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.HandleSkills(w, r)
		require.NotEqual(t, http.StatusNotFound, w.Code, "C-13: the stage-only review endpoint POST /skills/install/review must exist (S-94), got 404, body: %s", w.Body.String())
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

		// The staged files change before Install: the review binds revision
		// R1, so the install with that review_id must refuse.
		var review struct {
			ReviewID string `json:"review_id"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &review), "the review response carries a review_id: %s", w.Body.String())
		require.NotEmpty(t, review.ReviewID, "the review response carries a review_id")
		// Tamper with the staged copy the review bound.
		staged := filepath.Join(api.homePath, "skills", ".staging")
		require.NoError(t, filepath.Walk(staged, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && info.Name() == "SKILL.md" {
				return os.WriteFile(path, []byte("---\nname: weather\ndescription: Changed after review.\n---\n\nTampered.\n"), 0o600)
			}
			return nil
		}), "the staged copy must exist to be tampered with")

		w = uat2InstallNamed(t, api, "weather.zip", zip, map[string]any{"review_id": review.ReviewID})
		require.Equal(t, http.StatusConflict, w.Code, "S-94: the stale review must refuse the install, got %d, body: %s", w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), staleText, "S-94: the stale-review refusal carries the spec's text")
	})

	t.Run("S-94: cancelling a review discards the staged copy — nothing was installed", func(t *testing.T) {
		api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
		r := httptest.NewRequest(http.MethodPost, "/api/v1/skills/install/review/some-review-id/cancel", nil)
		w := httptest.NewRecorder()
		api.HandleSkills(w, r)
		require.NotEqual(t, http.StatusNotFound, w.Code, "C-13: the review cancel endpoint must exist, got 404")
		require.Contains(t, w.Body.String(), cancelText, "S-94: a truthful cancel says nothing was installed")
	})
}
