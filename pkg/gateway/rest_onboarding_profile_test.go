// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestOnboardingProfileSection_AllToneDetailCombinations verifies that
// onboardingProfileSection writes prose naming the chosen tone/detail and
// their descriptions, for every combination FR-OB-010 allows (3 tones x 2
// details).
func TestOnboardingProfileSection_AllToneDetailCombinations(t *testing.T) {
	tones := []gen.OnboardingPreferencesTone{
		gen.OnboardingPreferencesToneDirect,
		gen.OnboardingPreferencesToneWarm,
		gen.OnboardingPreferencesToneFormal,
	}
	details := []gen.OnboardingPreferencesDetail{gen.OnboardingPreferencesDetailBrief, gen.OnboardingPreferencesDetailThorough}

	for _, tone := range tones {
		for _, detail := range details {
			t.Run(string(tone)+"_"+string(detail), func(t *testing.T) {
				section, ok := onboardingProfileSection(gen.OnboardingPreferences{
					Name:   "Daniel",
					Tone:   tone,
					Detail: detail,
				})

				require.True(t, ok, "every enum member must produce prose")
				assert.Contains(t, section, "Call me Daniel.")
				assert.Contains(t, section, "## How I like to be talked to")
				assert.Contains(t, section, "- Tone: "+string(tone)+" — "+onboardingToneDescriptions[tone])
				assert.Contains(t, section, "- Detail: "+string(detail)+" — "+onboardingDetailDescriptions[detail])
			})
		}
	}
}

// TestSanitizeOnboardingName_NoNewHeading verifies FR-OB-014's rule: a name
// carrying Markdown heading markers and a newline produces no new heading in
// the sanitized single-line result.
func TestSanitizeOnboardingName_NoNewHeading(t *testing.T) {
	got := sanitizeOnboardingName("# Hi\n## Agents")

	require.NotContains(t, got, "\n", "the name must be a single line")
	require.False(t, strings.HasPrefix(got, "#"), "must not open a heading")

	section, ok := onboardingProfileSection(gen.OnboardingPreferences{
		Name:   "# Hi\n## Agents",
		Tone:   gen.OnboardingPreferencesToneDirect,
		Detail: gen.OnboardingPreferencesDetailBrief,
	})
	require.True(t, ok)
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, "Call me ") {
			assert.False(t, strings.Contains(line, "\n#"),
				"the 'Call me' line must not contain a line-starting '#' after the name")
		}
	}
	// The only headings in the section are the two structural ones this
	// function itself writes ("# About Me" and "## How I like to be talked
	// to") — never one contributed by the name.
	headingLines := 0
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, "#") {
			headingLines++
		}
	}
	assert.Equal(t, 2, headingLines,
		"exactly the two structural headings this function writes, nothing from the name")
}

// TestSanitizeOnboardingName_StripsBackticksAndHash verifies the rest of
// FR-OB-014's rule beyond the heading case: backticks are stripped (no
// fencing) and \r is stripped.
func TestSanitizeOnboardingName_StripsBackticksAndHash(t *testing.T) {
	got := sanitizeOnboardingName("`rm -rf /`\r")
	assert.NotContains(t, got, "`")
	assert.NotContains(t, got, "\r")
}

// TestSanitizeOnboardingName_TruncatesTo200 verifies FR-OB-014's truncation:
// a 500-character name is truncated to exactly 200 characters.
func TestSanitizeOnboardingName_TruncatesTo200(t *testing.T) {
	long := strings.Repeat("a", 500)
	got := sanitizeOnboardingName(long)
	assert.Len(t, got, 200)
	assert.Equal(t, strings.Repeat("a", 200), got)
}

// TestSanitizeOnboardingName_CollapsesWhitespace verifies internal whitespace
// (including what \r/\n become) is collapsed to single spaces.
func TestSanitizeOnboardingName_CollapsesWhitespace(t *testing.T) {
	got := sanitizeOnboardingName("Daniel   Van   Night")
	assert.Equal(t, "Daniel Van Night", got)
}

// TestWriteOnboardingUserProfile_FreshInstall_WritesFullSection verifies that
// on a fresh install (no USER.md yet) the full "# About Me" document is
// written at config.UserProfilePath().
func TestWriteOnboardingUserProfile_FreshInstall_WritesFullSection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	warning := writeOnboardingUserProfile(gen.OnboardingPreferences{
		Name:   "Daniel",
		Tone:   gen.OnboardingPreferencesToneWarm,
		Detail: gen.OnboardingPreferencesDetailThorough,
	})
	require.Empty(t, warning)

	data, err := os.ReadFile(config.UserProfilePath())
	require.NoError(t, err)
	content := string(data)
	assert.True(t, strings.HasPrefix(content, "# About Me"))
	assert.Contains(t, content, "Call me Daniel.")
	assert.Contains(t, content, "- Tone: warm — Friendly, a little conversational.")
	assert.Contains(t, content, "- Detail: thorough — Show the reasoning and the caveats.")
}

// TestWriteOnboardingUserProfile_ExistingFile_AppendsWithoutOverwriting
// verifies FR-OB-015: an existing USER.md's content is preserved, and the
// preferences section is appended rather than replacing the file.
func TestWriteOnboardingUserProfile_ExistingFile_AppendsWithoutOverwriting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	const existing = "# Existing profile\n\nSome prior content that must survive.\n"
	require.NoError(t, os.WriteFile(config.UserProfilePath(), []byte(existing), 0o600))

	warning := writeOnboardingUserProfile(gen.OnboardingPreferences{
		Name:   "Daniel",
		Tone:   gen.OnboardingPreferencesToneDirect,
		Detail: gen.OnboardingPreferencesDetailBrief,
	})
	require.Empty(t, warning)

	data, err := os.ReadFile(config.UserProfilePath())
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "Some prior content that must survive.",
		"existing content must be preserved (FR-OB-015)")
	assert.Contains(t, content, "Call me Daniel.", "the new section must be appended")
}

// TestWriteOnboardingUserProfile_WriteFailure_ReturnsWarning verifies
// FR-OB-016: when the write fails (here, a read-only USER.md parent
// directory), the function reports a warning rather than panicking or
// returning an error the caller must fail on.
func TestWriteOnboardingUserProfile_WriteFailure_ReturnsWarning(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.Chmod(home, 0o500)) // read + execute, no write
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })
	t.Setenv("OMNIPUS_HOME", home)

	warning := writeOnboardingUserProfile(gen.OnboardingPreferences{
		Name:   "Daniel",
		Tone:   gen.OnboardingPreferencesToneDirect,
		Detail: gen.OnboardingPreferencesDetailBrief,
	})
	assert.NotEmpty(t, warning, "a write failure must surface a warning, not a silent no-op")
}

// TestHandleCompleteOnboarding_ProfileWriteFailure_StillSucceedsWithWarning
// is the end-to-end version of FR-OB-016: even when the USER.md write fails,
// POST /onboarding/complete still returns 200 (onboarding is not a courtesy;
// the preferences are) and the response carries a one-line warning.
func TestHandleCompleteOnboarding_ProfileWriteFailure_StillSucceedsWithWarning(t *testing.T) {
	withEdition(t, config.EditionHosted)
	tmpDir := t.TempDir()
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := newOnboardingTestAPI(t, tmpDir, al)

	// USER.md's path (config.UserProfilePath()) is resolved independently of
	// tmpDir/a.homePath, via $OMNIPUS_HOME — point it at a read-only
	// directory so only the profile write fails; config.json, credentials
	// and the onboarding manager keep using tmpDir untouched.
	profileHome := t.TempDir()
	require.NoError(t, os.Chmod(profileHome, 0o500))
	t.Cleanup(func() { _ = os.Chmod(profileHome, 0o700) })
	t.Setenv("OMNIPUS_HOME", profileHome)

	body := `{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test"},` +
		`"preferences":{"name":"Daniel","tone":"direct","detail":"brief"}}`
	body = hermeticOnboardBody(t, body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleCompleteOnboarding(w, signedIn(req))

	require.Equal(t, http.StatusOK, w.Code,
		"onboarding must still succeed — the preferences are a courtesy, the flow is not (FR-OB-016)")
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	warn, _ := resp["warning"].(string)
	assert.NotEmpty(t, warn, "a profile-write failure must surface as a one-line warning")
}

// TestHandleCompleteOnboarding_WritesProfileFromPreferences is the end-to-end
// happy path: preferences submitted with the completion request land in
// USER.md.
func TestHandleCompleteOnboarding_WritesProfileFromPreferences(t *testing.T) {
	withEdition(t, config.EditionHosted)
	tmpDir := t.TempDir()
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := newOnboardingTestAPI(t, tmpDir, al)

	profileHome := t.TempDir()
	t.Setenv("OMNIPUS_HOME", profileHome)

	body := `{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test"},` +
		`"preferences":{"name":"Daniel","tone":"warm","detail":"thorough"}}`
	body = hermeticOnboardBody(t, body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleCompleteOnboarding(w, signedIn(req))

	require.Equal(t, http.StatusOK, w.Code, "onboarding must succeed: %s", w.Body.String())

	data, err := os.ReadFile(config.UserProfilePath())
	require.NoError(t, err, "USER.md must have been written")
	content := string(data)
	assert.Contains(t, content, "Call me Daniel.")
	assert.Contains(t, content, "- Tone: warm — Friendly, a little conversational.")
	assert.Contains(t, content, "- Detail: thorough — Show the reasoning and the caveats.")
}
