//go:build goolm && stdjson

package gateway

// Order 53 / CHECK F20 and Appendix D W5. C-13, S-115 and D15: native
// review/HTTP/publication/history/readback. Only publish entry pause/failure
// and the external clock may be injected. Success ALWAYS forwards to native
// publication, never a fabricated successful PublishOutcome. Exact upload ref
// is retained across stage/submit/retry. UI polling is in the TS half.
// Missing review/clock implementation is loud RED. GREEN and mutation / proof
// of failability remain deferred to an independent CHECK instance.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const reviewWeatherR1 = "---\nname: weather\ndescription: F20 native install fixture.\n---\n\nF20-WEATHER-R1-INSTRUCTIONS-8N\n"
const reviewWeatherR2 = "---\nname: weather\ndescription: Changed F20 native install fixture.\n---\n\nF20-WEATHER-R2-INSTRUCTIONS-4P\n"

// Test-only command input: one actual stored upload and its expected literal
// package revision. The request itself uses the generated contract carrier.
type nativeReviewUpload struct {
	request  gen.SkillInstallRequest
	revision string
}

func newNativeReviewUpload(t *testing.T, api *restAPI, markdown, replacing string) nativeReviewUpload {
	t.Helper()
	uploadDir := filepath.Join(api.homePath, "uploads", "owner-session")
	require.NoError(t, os.MkdirAll(uploadDir, 0o700))
	path := filepath.Join(uploadDir, "weather.zip")
	data := uat2BuildZip(t, []uat2ZipEntry{{name: "SKILL.md", mode: 0o644, body: markdown}})
	require.NoError(t, os.WriteFile(path, data, 0o600))
	if api.mediaStore == nil {
		api.mediaStore = newTestFileMediaStore(t)
	}
	ref, err := api.mediaStore.Store(path, media.MediaMeta{Filename: "weather.zip", Source: "upload:webchat", CleanupPolicy: media.CleanupPolicyForgetOnly}, "upload:owner-session")
	require.NoError(t, err)
	category := gen.SkillCategory("research")
	req := gen.SkillInstallRequest{UploadId: &ref, Category: &category}
	if replacing != "" {
		req.Revision = &replacing
	}
	// Independent input tree: expected revision never comes from a publish
	// response, returned status, or the implementation's chosen installed bytes.
	literal := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(literal, "SKILL.md"), []byte(markdown), 0o644))
	revision, err := skills.ContentRevision(literal)
	require.NoError(t, err)
	return nativeReviewUpload{request: req, revision: revision}
}

func postNativeReviewRequest(api *restAPI, path string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)
	return w
}
func nativeReviewRequestBytes(t *testing.T, req gen.SkillInstallRequest) []byte {
	t.Helper()
	body, err := json.Marshal(req)
	require.NoError(t, err)
	return body
}
func stageNativeReview(t *testing.T, api *restAPI, upload nativeReviewUpload) gen.SkillInstallReview {
	t.Helper()
	w := postNativeReviewRequest(api, "/api/v1/skills/install/review", nativeReviewRequestBytes(t, upload.request))
	require.Equal(t, http.StatusOK, w.Code, "BLOCKED: native C-13 review route must stage the exact authorized source, not publish; response=%s", w.Body.String())
	var review gen.SkillInstallReview
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &review))
	require.NotEmpty(t, review.ReviewId, "real review operation id")
	require.Equal(t, "weather", review.Name)
	require.Equal(t, upload.revision, review.Revision, "C-13 review pins the literal uploaded folder")
	require.False(t, review.Verified, "local zip has no registry hash")
	return review
}
func submitNativeReview(t *testing.T, api *restAPI, upload nativeReviewUpload, review gen.SkillInstallReview) *httptest.ResponseRecorder {
	t.Helper()
	req := upload.request
	req.ReviewId = &review.ReviewId
	return postNativeReviewRequest(api, "/api/v1/skills/install", nativeReviewRequestBytes(t, req))
}
func boundedNativeReviewHTTP(t *testing.T, api *restAPI, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	ctx, cancel := context.WithCancel(request.Context())
	done, joined := make(chan *httptest.ResponseRecorder, 1), make(chan struct{})
	go func() {
		defer close(joined)
		w := httptest.NewRecorder()
		api.HandleSkills(w, request.WithContext(ctx))
		done <- w
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-joined:
		case <-time.After(5 * time.Second):
			t.Error("fixture UNVERIFIED: native review request did not join")
		}
	})
	select {
	case w := <-done:
		return w
	case <-time.After(5 * time.Second):
		t.Fatal("C-13: native status or repeated submit blocked instead of answering during publication")
		return nil
	}
}
func getNativeReviewStatus(t *testing.T, api *restAPI, id string) gen.SkillInstallReviewStatus {
	t.Helper()
	w := boundedNativeReviewHTTP(t, api, httptest.NewRequest(http.MethodGet, "/api/v1/skills/install/review/"+id, nil))
	require.Equal(t, http.StatusOK, w.Code, "native review status: %s", w.Body.String())
	var state gen.SkillInstallReviewStatus
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &state))
	require.Equal(t, id, state.ReviewId)
	return state
}
func assertNativeReviewInstalled(t *testing.T, api *restAPI, body, revision string, versions int) {
	t.Helper()
	root := filepath.Join(api.homePath, "skills")
	got, err := os.ReadFile(filepath.Join(root, "weather", "SKILL.md"))
	require.NoError(t, err, "real publication must write the installed file")
	assert.Equal(t, body, string(got), "complete literal package bytes, not a matching name")
	obs, err := skills.ObserveSkill(root, "weather", skills.VersionQuery{Want: true, Limit: 100})
	require.NoError(t, err)
	assert.True(t, obs.Present)
	assert.Equal(t, revision, obs.Revision)
	assert.Equal(t, body, string(obs.Instruction))
	assert.Equal(t, versions, obs.VersionsTotal, "one actual history publication per successful operation")
}

// Forwarding the previous seam is safe only when nonnil. Nil is production's
// default; use the exact native publishInstall path in that case, not a fake
// successful result. These cases do not authorize replacing local edits.
func forwardNativeReviewPublish(previous func(string, string, string, string) (skills.PublishOutcome, error), root, name, stage, expected string) (skills.PublishOutcome, error) {
	if previous != nil {
		return previous(root, name, stage, expected)
	}
	_, outcome, err := publishInstall(root, name, stage, expected, false)
	return outcome, err
}

func TestInstallReview_OutcomeStatus(t *testing.T) {
	t.Run("instrument: forwarding publish executes native install and real replacement, bytes revision and history", testNativeReviewPublishInstrument)
	t.Run("D15#6 unknown review is 404 through the actual status route", func(t *testing.T) {
		api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
		w := httptest.NewRecorder()
		api.HandleSkills(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills/install/review/no-such-review", nil))
		require.Equal(t, http.StatusNotFound, w.Code, "C-13 native status route (405 is missing implementation): %s", w.Body.String())
	})
	t.Run("D15#2 lost successful response reads exact installed revision and real bytes", func(t *testing.T) {
		api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
		upload := newNativeReviewUpload(t, api, reviewWeatherR2, "")
		review := stageNativeReview(t, api, upload)
		w := submitNativeReview(t, api, upload, review)
		require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
		assertNativeReviewInstalled(t, api, reviewWeatherR2, upload.revision, 1)
		assert.Equal(t, gen.SkillInstallReviewStatus{ReviewId: review.ReviewId, State: "installed", Revision: upload.revision, InstalledRevision: &upload.revision}, getNativeReviewStatus(t, api, review.ReviewId))
	})
	t.Run("D15#3 slow publish signals entry, reports publishing, then installs exactly the pin once", testNativeReviewSlowPublish)
	t.Run("D15#5 failed real replacement preserves old bytes and revision, exact reason and replay", testNativeReviewFailedReplacement)
	t.Run("D15#7 repeated successful submit has one real publication and exact result", testNativeReviewRepeatedSubmit)
	t.Run("D15#8 cancelled review remains cancelled and replay never publishes", testNativeReviewCancelled)
	t.Run("D15#1 native expiry boundary and terminal record retention", testNativeReviewExpiry)
}

func testNativeReviewPublishInstrument(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	previous := publishRESTSkill
	var calls atomic.Int64
	publishRESTSkill = func(root, name, stage, expected string) (skills.PublishOutcome, error) {
		calls.Add(1)
		return forwardNativeReviewPublish(previous, root, name, stage, expected)
	}
	t.Cleanup(func() { publishRESTSkill = previous })
	// Trust Allow is an input. Both current and target implementations must
	// support an unverified direct install without an approval at this setting.
	api.agentLoop.GetConfig().Sandbox.SkillTrust = "allow"
	first := newNativeReviewUpload(t, api, reviewWeatherR1, "")
	w := postNativeReviewRequest(api, "/api/v1/skills/install", nativeReviewRequestBytes(t, first.request))
	require.Equal(t, http.StatusOK, w.Code, "instrument actual initial route: %s", w.Body.String())
	assertNativeReviewInstalled(t, api, reviewWeatherR1, first.revision, 1)
	second := newNativeReviewUpload(t, api, reviewWeatherR2, first.revision)
	w = postNativeReviewRequest(api, "/api/v1/skills/install", nativeReviewRequestBytes(t, second.request))
	require.Equal(t, http.StatusOK, w.Code, "instrument actual replacement route: %s", w.Body.String())
	assertNativeReviewInstalled(t, api, reviewWeatherR2, second.revision, 2)
	assert.Equal(t, int64(2), calls.Load())
}

func testNativeReviewSlowPublish(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	upload := newNativeReviewUpload(t, api, reviewWeatherR2, "")
	review := stageNativeReview(t, api, upload)
	previous := publishRESTSkill
	entered, release := make(chan struct{}), make(chan struct{})
	var entryOnce, releaseOnce sync.Once
	// Defers run before testing cleanup: release the first writer even if a
	// bounded status/repeated-submit request below fails its assertion.
	defer releaseOnce.Do(func() { close(release) })
	var calls atomic.Int64
	publishRESTSkill = func(root, name, stage, expected string) (skills.PublishOutcome, error) {
		if calls.Add(1) == 1 {
			entryOnce.Do(func() { close(entered) })
			<-release
		}
		// A duplicate entry is not allowed to deadlock the test on the same
		// barrier: it still calls real publication and fails count/state checks.
		return forwardNativeReviewPublish(previous, root, name, stage, expected)
	}
	req := upload.request
	req.ReviewId = &review.ReviewId
	body := nativeReviewRequestBytes(t, req) // no fatal assertions in the writer goroutine
	done := make(chan *httptest.ResponseRecorder, 1)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		done <- postNativeReviewRequest(api, "/api/v1/skills/install", body)
	}()
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		select {
		case <-joined:
			publishRESTSkill = previous
		case <-time.After(5 * time.Second):
			t.Error("fixture UNVERIFIED: real publication writer did not join after release; hook is not restored over a live writer")
		}
	})
	select {
	case <-entered:
	case w := <-done:
		t.Fatalf("native review never entered publication: status=%d body=%s", w.Code, w.Body.String())
	case <-time.After(5 * time.Second):
		t.Fatal("fixture UNVERIFIED: neither native publication entry nor completion observed")
	}
	assert.NoDirExists(t, filepath.Join(api.homePath, "skills", "weather"), "nothing live before publication release")
	assert.Equal(t, gen.SkillInstallReviewStatus{ReviewId: review.ReviewId, State: "publishing", Revision: upload.revision}, getNativeReviewStatus(t, api, review.ReviewId))
	// A concurrent repeated answer must be 202, not another publication.
	pendingRequest := httptest.NewRequest(http.MethodPost, "/api/v1/skills/install", bytes.NewReader(body))
	pendingRequest.Header.Set("Content-Type", "application/json")
	pending := boundedNativeReviewHTTP(t, api, pendingRequest)
	require.Equal(t, http.StatusAccepted, pending.Code, "%s", pending.Body.String())
	var pendingState gen.SkillInstallReviewStatus
	require.NoError(t, json.Unmarshal(pending.Body.Bytes(), &pendingState))
	assert.Equal(t, gen.SkillInstallReviewStatus{ReviewId: review.ReviewId, State: "publishing", Revision: upload.revision}, pendingState)
	assert.Equal(t, int64(1), calls.Load())
	releaseOnce.Do(func() { close(release) })
	select {
	case result := <-done:
		require.Equal(t, http.StatusOK, result.Code, "%s", result.Body.String())
	case <-time.After(5 * time.Second):
		t.Fatal("real install did not finish after native publish released")
	}
	<-joined
	assertNativeReviewInstalled(t, api, reviewWeatherR2, upload.revision, 1)
	assert.Equal(t, gen.SkillInstallReviewStatus{ReviewId: review.ReviewId, State: "installed", Revision: upload.revision, InstalledRevision: &upload.revision}, getNativeReviewStatus(t, api, review.ReviewId))
	assert.Equal(t, int64(1), calls.Load())
}

func testNativeReviewFailedReplacement(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	api.agentLoop.GetConfig().Sandbox.SkillTrust = "allow"
	old := newNativeReviewUpload(t, api, reviewWeatherR1, "")
	installed := postNativeReviewRequest(api, "/api/v1/skills/install", nativeReviewRequestBytes(t, old.request))
	require.Equal(t, http.StatusOK, installed.Code, "%s", installed.Body.String())
	assertNativeReviewInstalled(t, api, reviewWeatherR1, old.revision, 1)
	newer := newNativeReviewUpload(t, api, reviewWeatherR2, old.revision)
	require.NotEqual(t, old.revision, newer.revision, "fixture must really be a replacement")
	review := stageNativeReview(t, api, newer)
	const reason = "F20 publication boundary refused the staged package"
	previous := publishRESTSkill
	var calls atomic.Int64
	publishRESTSkill = func(string, string, string, string) (skills.PublishOutcome, error) {
		calls.Add(1)
		return skills.PublishOutcome{PersistenceStatus: skills.PersistenceNone, ActivationStatus: skills.ActivationNotAttempted, ErrorStage: skills.PublicationErrorStageSave, Message: reason}, errors.New(reason)
	}
	t.Cleanup(func() { publishRESTSkill = previous })
	first := submitNativeReview(t, api, newer, review)
	require.NotEqual(t, http.StatusOK, first.Code, "injected publish failure must not claim success")
	assert.Equal(t, gen.SkillInstallReviewStatus{ReviewId: review.ReviewId, State: "failed", Revision: newer.revision, Reason: stringPtrForReview(reason)}, getNativeReviewStatus(t, api, review.ReviewId))
	assertNativeReviewInstalled(t, api, reviewWeatherR1, old.revision, 1)
	w := httptest.NewRecorder()
	api.HandleSkills(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil))
	require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
	var list []gen.Skill
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	var weather []gen.Skill
	for _, skill := range list {
		if skill.Id == "weather" {
			weather = append(weather, skill)
		}
	}
	require.Len(t, weather, 1)
	assert.Equal(t, old.revision, weather[0].Revision, "old listing is not an install outcome oracle")
	retry := submitNativeReview(t, api, newer, review)
	assert.Contains(t, []int{http.StatusConflict, http.StatusGone}, retry.Code, "C-13 terminal replay: %s", retry.Body.String())
	secondRetry := submitNativeReview(t, api, newer, review)
	assert.Equal(t, retry.Code, secondRetry.Code)
	assert.JSONEq(t, retry.Body.String(), secondRetry.Body.String())
	assert.Equal(t, int64(1), calls.Load(), "a failed review is never republished")
	assertNativeReviewInstalled(t, api, reviewWeatherR1, old.revision, 1)
}

func testNativeReviewRepeatedSubmit(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	upload := newNativeReviewUpload(t, api, reviewWeatherR2, "")
	review := stageNativeReview(t, api, upload)
	previous := publishRESTSkill
	var calls atomic.Int64
	publishRESTSkill = func(root, name, stage, expected string) (skills.PublishOutcome, error) {
		calls.Add(1)
		return forwardNativeReviewPublish(previous, root, name, stage, expected)
	}
	t.Cleanup(func() { publishRESTSkill = previous })
	first := submitNativeReview(t, api, upload, review)
	require.Equal(t, http.StatusOK, first.Code, "%s", first.Body.String())
	second := submitNativeReview(t, api, upload, review)
	require.Equal(t, http.StatusOK, second.Code, "completed operation is 200, not the in-flight 202: %s", second.Body.String())
	var result, replay gen.Skill
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &result))
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &replay))
	assert.Equal(t, result, replay)
	assert.Equal(t, "weather", result.Id)
	assert.Equal(t, upload.revision, result.Revision)
	assert.Equal(t, int64(1), calls.Load(), "count native publication, not response equality")
	assertNativeReviewInstalled(t, api, reviewWeatherR2, upload.revision, 1)
}

func testNativeReviewCancelled(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	upload := newNativeReviewUpload(t, api, reviewWeatherR2, "")
	review := stageNativeReview(t, api, upload)
	w := httptest.NewRecorder()
	api.HandleSkills(w, httptest.NewRequest(http.MethodDelete, "/api/v1/skills/install/review/"+review.ReviewId, nil))
	require.Equal(t, http.StatusNoContent, w.Code, "%s", w.Body.String())
	assert.Empty(t, w.Body.String(), "C-13 DELETE is 204, not a UI sentence")
	first := submitNativeReview(t, api, upload, review)
	assert.Contains(t, []int{http.StatusConflict, http.StatusGone}, first.Code, "%s", first.Body.String())
	second := submitNativeReview(t, api, upload, review)
	assert.Equal(t, first.Code, second.Code)
	assert.JSONEq(t, first.Body.String(), second.Body.String())
	assert.Equal(t, gen.SkillInstallReviewStatus{ReviewId: review.ReviewId, State: "cancelled", Revision: upload.revision}, getNativeReviewStatus(t, api, review.ReviewId))
	assert.NoDirExists(t, filepath.Join(api.homePath, "skills", "weather"))
}

// Only the external now input. Reconciliation must bind this to the native
// review clock; it must not manufacture review status or expire a fake store.
var bindNativeInstallReviewClock func(*restAPI, func() time.Time) func()

func testNativeReviewExpiry(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	nativebinding.Require(t, "pkg/gateway", bindNativeInstallReviewClock,
		"C-13/D15#1: named native review-store clock used by HandleSkills review/stage/status/submit and retention")
	start := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	var clockNanos atomic.Int64 // Native expiry workers may also read this input.
	clockNanos.Store(start.UnixNano())
	restore := bindNativeInstallReviewClock(api, func() time.Time { return time.Unix(0, clockNanos.Load()).UTC() })
	require.NotNil(t, restore, "clock binding restores its real input")
	t.Cleanup(restore)
	upload := newNativeReviewUpload(t, api, reviewWeatherR2, "")
	review := stageNativeReview(t, api, upload)
	assert.Equal(t, start.Add(15*time.Minute), review.ExpiresAt)
	clockNanos.Add(int64(15*time.Minute - time.Nanosecond))
	assert.Equal(t, gen.SkillInstallReviewStatus{ReviewId: review.ReviewId, State: "staged", Revision: upload.revision}, getNativeReviewStatus(t, api, review.ReviewId))
	clockNanos.Add(int64(time.Nanosecond))
	assert.Equal(t, gen.SkillInstallReviewStatus{ReviewId: review.ReviewId, State: "expired", Revision: upload.revision}, getNativeReviewStatus(t, api, review.ReviewId))
	w := submitNativeReview(t, api, upload, review)
	require.Equal(t, http.StatusGone, w.Code, "%s", w.Body.String())
	var failure gen.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &failure))
	assert.Equal(t, "This review expired. Open the skill again to review it.", failure.Error)
	clockNanos.Add(int64(15*time.Minute - time.Nanosecond))
	assert.Equal(t, "expired", string(getNativeReviewStatus(t, api, review.ReviewId).State))
	clockNanos.Add(int64(time.Nanosecond))
	gone := httptest.NewRecorder()
	api.HandleSkills(gone, httptest.NewRequest(http.MethodGet, "/api/v1/skills/install/review/"+review.ReviewId, nil))
	assert.Equal(t, http.StatusNotFound, gone.Code, "terminal record retained 15 min, then removed")
	assert.NoDirExists(t, filepath.Join(api.homePath, "skills", "weather"))
}

func stringPtrForReview(value string) *string { return &value }
