//go:build !cgo

package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/agent/testutil"
)

// TestUploadMediaRef_StoreSwapResolves uploads a file before and after a
// gateway config reload and asserts that both media refs remain resolvable.
//
// BDD: Given a running gateway
//
//	When a file is uploaded (yields ref A)
//	And a config reload is triggered (swaps the media store)
//	And another file is uploaded (yields ref B)
//	Then GET /api/v1/media/<refID-A> returns 200
//	And  GET /api/v1/media/<refID-B> returns 200
//
// Traces to: #254 stale media store (BLOCKER)
func TestUploadMediaRef_StoreSwapResolves(t *testing.T) {

	mock := mockLLMServer(t, "ok")
	gw := testutil.StartTestGateway(t,
		testutil.WithAPIBase(mock.URL),
		testutil.WithBearerAuth(),
	)

	sessionID := "media-swap-test-session"

	// Upload a file BEFORE any config reload.
	refA := mustUploadFile(t, gw, sessionID, "before-swap.txt", "before reload content")

	// Trigger a config reload to swap the media store. The reload endpoint
	// writes the current config back, which triggers restartServices.
	triggerReload(t, gw)

	// Wait briefly for the reload to complete.
	time.Sleep(300 * time.Millisecond)

	// Upload another file AFTER the reload. Without the fix, this ref would be
	// registered in the NEW store which the old a.mediaStore pointer doesn't see,
	// but the agent loop's GetMediaStore() always returns the current store.
	refB := mustUploadFile(t, gw, sessionID, "after-swap.txt", "after reload content")

	// Both refs must resolve via /api/v1/media/{refID}.
	assertMediaRefResolvable(t, gw, refA, "before-swap.txt")
	assertMediaRefResolvable(t, gw, refB, "after-swap.txt")
}

// TestUploadMediaRef_UnknownRefDropped verifies that uploading a file registers
// a media:// ref, and that a bogus ref does NOT resolve (returns 404).
//
// BDD: Given a valid upload
//
//	When the agent resolves the ref via /api/v1/media/{real-refID}
//	Then it gets 200
//	When a random bogus ref is resolved
//	Then it gets 404
func TestUploadMediaRef_UnknownRefDropped(t *testing.T) {

	mock := mockLLMServer(t, "ok")
	gw := testutil.StartTestGateway(t,
		testutil.WithAPIBase(mock.URL),
		testutil.WithBearerAuth(),
	)

	sessionID := "media-ref-unknown-test"
	ref := mustUploadFile(t, gw, sessionID, "real.txt", "real content")

	// Real ref must resolve.
	assertMediaRefResolvable(t, gw, ref, "real.txt")

	// Bogus ref must not resolve.
	bogusID := "00000000-0000-0000-0000-000000000000"
	resp, err := gw.HTTPClient.Get(gw.URL + "/api/v1/media/" + bogusID)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "bogus ref must return 404")
}

// mustUploadFile uploads a single file to the gateway and returns its media:// ref.
func mustUploadFile(t *testing.T, gw *testutil.TestGateway, sessionID, filename, content string) string {
	t.Helper()

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	require.NoError(t, w.WriteField("session_id", sessionID))
	fw, err := w.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = io.WriteString(fw, content)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	req, err := http.NewRequest(http.MethodPost, gw.URL+"/api/v1/upload", body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if tok := gw.Token(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	req.Header.Set("Origin", gw.URL)

	resp, err := gw.HTTPClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "upload must succeed; body: %s", raw)

	var uploadResp struct {
		Files []struct {
			Ref *string `json:"ref"`
		} `json:"files"`
	}
	require.NoError(t, json.Unmarshal(raw, &uploadResp))
	require.Len(t, uploadResp.Files, 1, "upload response must contain 1 file")
	require.NotNil(t, uploadResp.Files[0].Ref, "upload must return a media:// ref")
	ref := *uploadResp.Files[0].Ref
	require.True(t, strings.HasPrefix(ref, "media://"), "ref must be a media:// URI, got %q", ref)
	return ref
}

// assertMediaRefResolvable verifies that /api/v1/media/{refID} returns 200 and
// that the filename header or body indicates the expected file.
func assertMediaRefResolvable(t *testing.T, gw *testutil.TestGateway, ref, expectedFilename string) {
	t.Helper()

	refID := strings.TrimPrefix(ref, "media://")
	req, err := http.NewRequest(http.MethodGet, gw.URL+"/api/v1/media/"+refID, nil)
	require.NoError(t, err)
	if tok := gw.Token(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	req.Header.Set("Origin", gw.URL)

	resp, err := gw.HTTPClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"media ref %q must resolve to 200; Content-Disposition=%q",
		ref, resp.Header.Get("Content-Disposition"))

	disposition := resp.Header.Get("Content-Disposition")
	if disposition != "" && expectedFilename != "" {
		assert.Contains(t, disposition, expectedFilename,
			"Content-Disposition must mention the expected filename")
	}
}

// triggerReload triggers a config reload by PUT-ing the current config back to the
// config endpoint. This causes restartServices to run and swap the media store.
// It fatals the test on any read or PUT failure so a no-op reload doesn't yield a
// false green (G5 fix).
func triggerReload(t *testing.T, gw *testutil.TestGateway) {
	t.Helper()

	// Read current config.
	configPath := filepath.Join(gw.HomeDir(), "config.json")
	data, err := os.ReadFile(configPath)
	require.NoError(t, err, "triggerReload: failed to read config.json — reload cannot proceed")

	// PUT /api/v1/config refuses credential and security-sensitive top-level
	// keys (providers, sandbox, credentials, security, and *api_key*/*secret*/
	// *password* keys) — they must be mutated via their dedicated endpoints.
	// A full config.json always contains at least "providers", so PUT-ing it
	// verbatim is a guaranteed 403. Strip those keys here: the remaining benign
	// body still triggers safeUpdateConfigJSON → restartServices, which is all
	// this test needs to swap the media store.
	var cfgMap map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &cfgMap),
		"triggerReload: config.json is not a JSON object")
	for k := range cfgMap {
		kl := strings.ToLower(k)
		if kl == "providers" || kl == "sandbox" || kl == "credentials" || kl == "security" ||
			strings.Contains(kl, "api_key") || strings.Contains(kl, "secret") ||
			strings.Contains(kl, "password") {
			delete(cfgMap, k)
		}
	}
	require.NotEmpty(t, cfgMap,
		"triggerReload: nothing left to PUT after stripping blocked keys")
	data, err = json.Marshal(cfgMap)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPut, gw.URL+"/api/v1/config", bytes.NewReader(data))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if tok := gw.Token(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	req.Header.Set("Origin", gw.URL)

	resp, err := gw.HTTPClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	require.True(t,
		resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted,
		"triggerReload: PUT /api/v1/config returned %d (want 200/202): %s",
		resp.StatusCode, body,
	)
	t.Logf("triggerReload: config reload triggered (status=%d)", resp.StatusCode)
}
