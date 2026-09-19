// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// onboardingProfileWriteWarning is the non-blocking warning surfaced on
// OnboardingCompleteResponse.Warning when writing USER.md fails. FR-OB-016:
// the preferences are a courtesy; the flow itself must not fail on this.
const onboardingProfileWriteWarning = "Your name and preferences could not be saved to your profile — " +
	"you can add them from Profile."

// onboardingToneDescriptions/onboardingDetailDescriptions mirror the one-line
// descriptions shown next to each choice on onboarding step 1
// (TONE_OPTIONS/DETAIL_OPTIONS in src/routes/onboarding.tsx), so the prose
// written into USER.md matches what the person actually saw and chose.
var onboardingToneDescriptions = map[gen.OnboardingPreferencesTone]string{
	gen.OnboardingPreferencesToneDirect: "Answers first. No preamble.",
	gen.OnboardingPreferencesToneWarm:   "Friendly, a little conversational.",
	gen.OnboardingPreferencesToneFormal: "Professional register throughout.",
}

var onboardingDetailDescriptions = map[gen.OnboardingPreferencesDetail]string{
	// oapi-codegen names these two constants without the enum-type prefix
	// (gen.OnboardingPreferencesDetailBrief / ...Thorough), the prefixed names
	// generated enum member — unlike the Tone constants just above, which do
	// collide and so keep the OnboardingPreferencesTone* prefix.
	gen.OnboardingPreferencesDetailBrief:    "The short version unless I ask.",
	gen.OnboardingPreferencesDetailThorough: "Show the reasoning and the caveats.",
}

// onboardingLeadingHashRE matches one or more leading '#' characters (and any
// whitespace immediately following them) at the START of the sanitized name —
// the Markdown ATX-heading marker. Only ever applied after newlines have
// already been collapsed to spaces, so this is the only place a '#' could
// still open a heading.
var onboardingLeadingHashRE = regexp.MustCompile(`^#+\s*`)

// onboardingWhitespaceRE collapses any run of whitespace (including the
// spaces \r/\n were replaced with) into one.
var onboardingWhitespaceRE = regexp.MustCompile(`\s+`)

// sanitizeOnboardingLine reduces a caller-supplied value to a single Markdown
// line that cannot open a heading or a code fence: \r stripped, \n replaced
// with a space, backticks stripped, a leading run of '#' removed, whitespace
// collapsed and trimmed.
//
// USER.md is concatenated into every agent's prompt (pkg/agent/context.go), so
// ANY caller-supplied text written into it is a prompt-injection surface, not
// only the name. Every interpolation site in this file goes through here.
func sanitizeOnboardingLine(raw string) string {
	s := strings.ReplaceAll(raw, "\r", "")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "`", "")
	s = onboardingLeadingHashRE.ReplaceAllString(s, "")
	s = onboardingWhitespaceRE.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// sanitizeOnboardingName applies FR-OB-014's rule, verbatim: the name is
// written as a single-line value with leading '#' characters and backticks
// stripped, \r/\n stripped, whitespace collapsed, and truncated to 200
// characters. No escaping, no fencing — the point is that the result cannot
// open a heading or a code fence no matter what was typed.
func sanitizeOnboardingName(raw string) string {
	s := sanitizeOnboardingLine(raw)
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200])
	}
	return s
}

// onboardingUnknownToneMsg / onboardingUnknownDetailMsg are the 400 bodies for
// a tone or detail that is not a member of its enum. They deliberately name
// the accepted values and NOT the value the caller sent — echoing it back
// would put caller-controlled text in an error surface for no benefit.
const (
	onboardingUnknownToneMsg = "preferences.tone must be one of: direct, warm, formal"

	onboardingUnknownDetailMsg = "preferences.detail must be one of: brief, thorough"
)

// validateOnboardingPreferences refuses a tone or detail that is not a key of
// the description maps above, returning the 400 message and the offending
// field.
//
// This is the ONLY thing standing between a request body and USER.md, because
// the inbound JSON-schema check that enforces the enum runs only when
// Gateway.ValidateInbound is on — and it defaults to FALSE (pkg/config).
// encoding/json will happily decode any string into the generated enum's
// underlying string type, so without this an attacker-chosen tone reaches the
// file that is injected into every agent prompt.
//
// An out-of-enum value is a MALFORMED REQUEST, not something to sanitise and
// pass through: there is no prose to write for a tone nobody defined, so the
// only honest answer is 400.
func validateOnboardingPreferences(prefs gen.OnboardingPreferences) (msg, field string, ok bool) {
	if _, known := onboardingToneDescriptions[prefs.Tone]; !known {
		return onboardingUnknownToneMsg, "preferences.tone", false
	}
	if _, known := onboardingDetailDescriptions[prefs.Detail]; !known {
		return onboardingUnknownDetailMsg, "preferences.detail", false
	}
	return "", "", true
}

// onboardingProfileSection builds the USER.md prose FR-OB-013/-014 describe,
// from step 1's preferences. Shape follows the demo's `contextDraft`
// (docs/mockups/auth-demo/src/App.tsx:161-167), but the name is sanitized
// (FR-OB-014) rather than interpolated raw — the demo's whole-document
// replace with raw interpolation is explicitly a display mock, not the
// implementation (spec FR-OB-015).
//
// Tone and detail are NOT interpolated from the request either. Each is looked
// up in its description map and the section is built only if BOTH are known
// members; ok=false otherwise. Combined with the 400 that
// validateOnboardingPreferences produces at the boundary, that makes writing
// an attacker-chosen string into USER.md structurally impossible rather than
// merely unlikely — the second half of the belt-and-braces pair, since USER.md
// is injected verbatim into every agent prompt.
func onboardingProfileSection(prefs gen.OnboardingPreferences) (section string, ok bool) {
	toneDesc, toneKnown := onboardingToneDescriptions[prefs.Tone]
	detailDesc, detailKnown := onboardingDetailDescriptions[prefs.Detail]
	if !toneKnown || !detailKnown {
		return "", false
	}

	name := sanitizeOnboardingName(prefs.Name)
	if name == "" {
		// Continue is disabled client-side until the name is non-empty
		// (FR-OB-012), so this is defence in depth, not an expected path —
		// but the write must still produce well-formed prose.
		name = "there"
	}
	// The enum labels are our own constants by the time we get here, so this
	// sanitiser pass can never change them. It is here so that no future edit
	// can widen the map's keys into a write path that skips it.
	tone := sanitizeOnboardingLine(string(prefs.Tone))
	detail := sanitizeOnboardingLine(string(prefs.Detail))

	var b strings.Builder
	b.WriteString("# About Me\n\n")
	b.WriteString("Call me " + name + ".\n\n")
	b.WriteString("## How I like to be talked to\n\n")
	b.WriteString("- Tone: " + tone + " — " + toneDesc + "\n")
	b.WriteString("- Detail: " + detail + " — " + detailDesc + "\n")
	return b.String(), true
}

// writeOnboardingUserProfile writes step 1's preferences into the global
// USER.md (FR-OB-013a: inside the completion transaction, after the config
// write and before the response). It NEVER overwrites existing content
// (FR-OB-015) — a fresh install has no USER.md yet and this is moot, but the
// implementation must not be structurally able to clobber, so an existing
// file gets the section appended instead.
//
// Returns "" on success, or onboardingProfileWriteWarning on any failure —
// read, mkdir, or write. The caller surfaces that as a non-blocking warning
// on the response and completes onboarding regardless (FR-OB-016): the
// preferences are a courtesy, the flow is not.
func writeOnboardingUserProfile(prefs gen.OnboardingPreferences) string {
	section, ok := onboardingProfileSection(prefs)
	if !ok {
		// Unreachable: validateOnboardingPreferences already answered such a
		// request with 400 at the boundary. If it is ever reached, NOTHING is
		// written — an unknown tone or detail must not become prose in a file
		// that is injected into every agent prompt.
		slog.Error("onboarding: USER.md profile skipped — preferences carry an unknown tone or detail",
			"tone", string(prefs.Tone), "detail", string(prefs.Detail))
		return onboardingProfileWriteWarning
	}
	path := config.UserProfilePath()

	existing, err := os.ReadFile(path)
	switch {
	case err == nil:
		content := string(existing)
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "\n" + section
		if writeErr := writeOnboardingProfileFile(path, content); writeErr != nil {
			slog.Error("onboarding: USER.md profile append failed", "path", path, "error", writeErr)
			return onboardingProfileWriteWarning
		}
		return ""
	case os.IsNotExist(err):
		if writeErr := writeOnboardingProfileFile(path, section); writeErr != nil {
			slog.Error("onboarding: USER.md profile write failed", "path", path, "error", writeErr)
			return onboardingProfileWriteWarning
		}
		return ""
	default:
		slog.Error("onboarding: USER.md profile read failed", "path", path, "error", err)
		return onboardingProfileWriteWarning
	}
}

// writeOnboardingProfileFile creates USER.md's parent directory (a fresh
// install has no $OMNIPUS_HOME/USER.md yet, but the home dir itself should
// already exist by the time onboarding completes) and writes the file
// atomically at 0600 — it is the user's own profile, not shared config.
func writeOnboardingProfileFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, []byte(content), 0o600)
}
