//go:build !cgo

// Tests for GET /api/v1/skills/search (ClawHub marketplace search) and the
// install-by-slug validation on POST /api/v1/skills/install. The registry is
// stubbed via a fake SkillRegistry — these tests never reach the real
// clawhub.ai network endpoint.

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/skills"
)

// fakeSkillRegistry is an in-memory SkillRegistry stub for handler tests.
type fakeSkillRegistry struct {
	results    []skills.SearchResult
	searchErr  error
	installErr error
	installRes *skills.InstallResult
	// lastQuery / lastLimit capture the most recent Search call args.
	lastQuery string
	lastLimit int
}

func (f *fakeSkillRegistry) Name() string { return "fake" }

func (f *fakeSkillRegistry) Search(_ context.Context, query string, limit int) ([]skills.SearchResult, error) {
	f.lastQuery = query
	f.lastLimit = limit
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.results, nil
}

func (f *fakeSkillRegistry) GetSkillMeta(_ context.Context, _ string) (*skills.SkillMeta, error) {
	return nil, nil
}

func (f *fakeSkillRegistry) DownloadAndInstall(_ context.Context, _, _, _ string) (*skills.InstallResult, error) {
	if f.installErr != nil {
		return nil, f.installErr
	}
	if f.installRes != nil {
		return f.installRes, nil
	}
	return &skills.InstallResult{}, nil
}

// TestSearchSkillsMapsResults verifies a stubbed registry's results are mapped
// onto the SkillSearchResult wire type field-for-field.
func TestSearchSkillsMapsResults(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	fake := &fakeSkillRegistry{
		results: []skills.SearchResult{
			{
				Slug:         "web-search",
				DisplayName:  "Web Search",
				Summary:      "Search the web.",
				Version:      "1.4.0",
				Score:        0.92,
				RegistryName: "clawhub",
				OwnerHandle:  "octofleet",
			},
		},
	}
	api.skillRegistry = fake

	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills/search?q=web&limit=5", nil)
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var out []gen.SkillSearchResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out, 1)

	got := out[0]
	assert.Equal(t, "web-search", got.Slug)
	require.NotNil(t, got.DisplayName)
	assert.Equal(t, "Web Search", *got.DisplayName)
	require.NotNil(t, got.Summary)
	assert.Equal(t, "Search the web.", *got.Summary)
	require.NotNil(t, got.Version)
	assert.Equal(t, "1.4.0", *got.Version)
	require.NotNil(t, got.Score)
	assert.InDelta(t, 0.92, *got.Score, 0.0001)
	require.NotNil(t, got.RegistryName)
	assert.Equal(t, "clawhub", *got.RegistryName)
	require.NotNil(t, got.OwnerHandle)
	assert.Equal(t, "octofleet", *got.OwnerHandle)

	// The handler forwarded the query and clamped limit correctly.
	assert.Equal(t, "web", fake.lastQuery)
	assert.Equal(t, 5, fake.lastLimit)
}

// TestSearchSkillsLimitClampedAndDefaulted verifies limit defaults to 20 and is
// capped at 50.
func TestSearchSkillsLimitClampedAndDefaulted(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	fake := &fakeSkillRegistry{}
	api.skillRegistry = fake

	// No limit → default 20.
	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills/search?q=x", nil)
	api.HandleSkills(httptest.NewRecorder(), r)
	assert.Equal(t, 20, fake.lastLimit)

	// Over-cap → clamped to 50.
	r = httptest.NewRequest(http.MethodGet, "/api/v1/skills/search?q=x&limit=999", nil)
	api.HandleSkills(httptest.NewRecorder(), r)
	assert.Equal(t, 50, fake.lastLimit)
}

// TestSearchSkillsEmptyQuery400 verifies an empty/missing q is a 400.
func TestSearchSkillsEmptyQuery400(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	api.skillRegistry = &fakeSkillRegistry{}

	for _, url := range []string{"/api/v1/skills/search", "/api/v1/skills/search?q=", "/api/v1/skills/search?q=%20%20"} {
		r := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()
		api.HandleSkills(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code, "url %q must be 400; body: %s", url, w.Body.String())
	}
}

// TestSearchSkillsBadLimit400 verifies a non-numeric or non-positive limit is 400.
func TestSearchSkillsBadLimit400(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	api.skillRegistry = &fakeSkillRegistry{}

	for _, url := range []string{"/api/v1/skills/search?q=x&limit=abc", "/api/v1/skills/search?q=x&limit=0", "/api/v1/skills/search?q=x&limit=-3"} {
		r := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()
		api.HandleSkills(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code, "url %q must be 400; body: %s", url, w.Body.String())
	}
}

// TestSearchSkillsRegistryError502 verifies a registry failure is surfaced as a
// 502 (not a 500), with a clear message.
func TestSearchSkillsRegistryError502(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	api.skillRegistry = &fakeSkillRegistry{searchErr: assertError("registry down")}

	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills/search?q=web", nil)
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)

	assert.Equal(t, http.StatusBadGateway, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, strings.ToLower(w.Body.String()), "registry unavailable")
}

// TestSearchSkillsNoRegistry502 verifies a nil registry yields 502, not a panic
// or 500.
func TestSearchSkillsNoRegistry502(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	api.skillRegistry = nil

	r := httptest.NewRequest(http.MethodGet, "/api/v1/skills/search?q=web", nil)
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)

	assert.Equal(t, http.StatusBadGateway, w.Code, "body: %s", w.Body.String())
}

// TestInstallSkillRejectsBadSlug verifies install rejects a path-traversal slug
// with 400 before touching the registry.
func TestInstallSkillRejectsBadSlug(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	api.skillRegistry = &fakeSkillRegistry{}

	body := `{"slug":"../etc/passwd"}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/skills/install", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

// TestInstallSkillMissingSlug400 verifies an empty slug is a 400.
func TestInstallSkillMissingSlug400(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	api.skillRegistry = &fakeSkillRegistry{}

	body := `{"slug":""}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/skills/install", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

// TestInstallSkillSuccess verifies a valid slug installs via the stubbed
// registry and returns the installed skill.
func TestInstallSkillSuccess(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	api.skillRegistry = &fakeSkillRegistry{
		installRes: &skills.InstallResult{Version: "2.0.0", Summary: "Does a thing.", Verified: true},
	}

	body := `{"slug":"cool-skill"}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/skills/install", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var skill gen.Skill
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &skill))
	assert.Equal(t, "cool-skill", skill.Id)
	assert.Equal(t, "2.0.0", skill.Version)
	assert.True(t, skill.Verified)
}

// assertError is a tiny error helper to avoid importing errors in every test.
type assertError string

func (e assertError) Error() string { return string(e) }
