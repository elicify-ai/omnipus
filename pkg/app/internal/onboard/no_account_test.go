// Omnipus - Ultra-lightweight personal AI agent
// License: MIT

// no_account_test.go pins ADR-0008 ruling 2's "no local credential" invariant
// for PLATFORM MODE (hosted/desktop) — config.EditionAuthMode() ==
// AuthModePlatform. WP1.5b (ADR-0010) restored upstream's admin-account CLI
// path behind config.EditionAuthMode() == AuthModeLocal (see
// TestOnboard_LocalMode_CreatesAdminAccount in onboard_test.go for that
// mode's positive coverage), so the source default (core/local) is NOT what
// this file means — every test below pins EditionHosted explicitly.
package onboard

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// withEdition stamps config.Edition for the duration of a test and restores
// it on cleanup — same save/restore pattern as pkg/config/edition_test.go /
// pkg/gateway/rest_backup_test.go.
func withEdition(t *testing.T, e string) {
	t.Helper()
	prev := config.Edition
	config.Edition = e
	t.Cleanup(func() { config.Edition = prev })
}

// assertNoAccountWritten is the single oracle for ADR-0008 ruling 2 as it
// applies to this command: the CLI creates no account and no password.
//
// It asserts two independent things, because either one alone can be satisfied
// by an implementation that still bricks the install:
//
//  1. the raw config.json bytes contain no "password_hash" key at all — a
//     hash is a hash whether or not it sits under gateway.users; and
//  2. gateway.users holds no rows. This is the one that matters operationally:
//     ensurePlatformUser (pkg/gateway/rest_platform_auth.go) returns
//     errPlatformSubjectMismatch for ANY pre-existing row whose username is
//     not the platform subject's, so a single CLI-seeded row refuses the first
//     platform sign-in permanently.
func assertNoAccountWritten(t *testing.T, home string) {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}
	if bytes.Contains(raw, []byte("password_hash")) {
		t.Errorf("config.json contains a password_hash key — the CLI must never mint one:\n%s", raw)
	}

	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse config.json: %v", err)
	}
	gw, _ := cfg["gateway"].(map[string]any)
	users, _ := gw["users"].([]any)
	if len(users) != 0 {
		t.Errorf("gateway.users has %d row(s); the CLI must append none — "+
			"a seeded row makes the first platform sign-in fail with subject_mismatch: %v",
			len(users), users)
	}
}

// TestOnboard_InteractiveRun_MintsNoPasswordHash drives the full interactive
// wizard and asserts the on-disk result carries no credential.
func TestOnboard_InteractiveRun_MintsNoPasswordHash(t *testing.T) {
	withEdition(t, config.EditionHosted)
	home := t.TempDir()

	var stdout bytes.Buffer
	keys := []string{"sk-or-v1-no-account"}
	idx := 0
	wio := wizardIO{
		// "1" = OpenRouter, "" = default model. Nothing after that: if the
		// wizard still asks for a username it reads EOF and fails loudly.
		stdin:  strings.NewReader("1\n\n"),
		stdout: &stdout,
		stderr: io.Discard,
		readPassword: func() (string, error) {
			if idx >= len(keys) {
				// A second hidden read can only be a password prompt.
				t.Fatal("readPassword called twice — the wizard must ask for the API key and nothing else")
			}
			out := keys[idx]
			idx++
			return out, nil
		},
		skipVerify: true,
	}

	if err := Run(home, wio); err != nil {
		t.Fatalf("Run: %v\n--- stdout ---\n%s", err, stdout.String())
	}

	assertNoAccountWritten(t, home)

	// The completion message must not promise a login this build cannot honour.
	if out := stdout.String(); strings.Contains(out, "Log in as") {
		t.Errorf("completion text still advertises a local login:\n%s", out)
	}
}

// TestOnboard_Headless_MintsNoPasswordHash is the same assertion against the
// --non-interactive path, which is the one an operator scripts.
func TestOnboard_Headless_MintsNoPasswordHash(t *testing.T) {
	withEdition(t, config.EditionHosted)
	home := t.TempDir()

	var stdout bytes.Buffer
	wio := wizardIO{
		stdin:  strings.NewReader(""),
		stdout: &stdout,
		stderr: io.Discard,
		readPassword: func() (string, error) {
			t.Fatal("readPassword called on the headless path")
			return "", nil
		},
	}
	in := Input{
		ProviderID:     "openrouter",
		APIKey:         "sk-or-v1-headless-no-account",
		Model:          "z-ai/glm-5v-turbo",
		SkipVerify:     true,
		NonInteractive: true,
	}
	if err := RunHeadless(home, wio, in); err != nil {
		t.Fatalf("RunHeadless: %v", err)
	}

	assertNoAccountWritten(t, home)
}

// TestOnboardCommand_ExposesNoAccountFlags pins the CLI surface itself IN
// PLATFORM MODE. A flag that still exists there is a flag a runbook can
// still pass, so the absence is asserted on the command rather than inferred
// from the writes above. (Local mode's command DOES expose these flags —
// see TestOnboardCommand_LocalMode_ExposesAdminFlags in onboard_test.go —
// which is why this test pins EditionHosted rather than relying on the
// source default.)
func TestOnboardCommand_ExposesNoAccountFlags(t *testing.T) {
	withEdition(t, config.EditionHosted)
	cmd := NewOnboardCommand()
	for _, name := range []string{"admin-username", "admin-password", "admin-password-stdin"} {
		if f := cmd.Flags().Lookup(name); f != nil {
			t.Errorf("--%s still registered; the CLI creates no account (ADR-0008 ruling 2)", name)
		}
	}
	if strings.Contains(cmd.Long, "--admin-") {
		t.Errorf("help text still documents an --admin-* flag:\n%s", cmd.Long)
	}
	if strings.Contains(cmd.Short, "admin account") || strings.Contains(cmd.Short, "admin user") {
		t.Errorf("Short still advertises an admin account: %q", cmd.Short)
	}
}

// TestOnboardPackage_ContainsNoPasswordHashing (the structural, package-wide
// grep guard this comment used to describe) is RETIRED by WP1.5b (ADR-0010):
// its premise — "no code path in this package may ever hash a password" —
// was true before WP5/WP1.5b and is now false BY DESIGN, because local mode's
// own CLI-created password login is permanent (founder decision, ADR-0010)
// and this package is exactly where it is restored, gated behind
// config.EditionAuthMode() == AuthModeLocal (onboard.go's applyInput /
// mutateConfigFile). A package-wide string scan cannot distinguish "gated
// behind local mode" from "reachable in platform mode", so it would either
// false-positive on every local-mode line or be too narrow to protect
// anything real. The invariant that actually matters — PLATFORM MODE mints
// no credential — is what TestOnboard_InteractiveRun_MintsNoPasswordHash and
// TestOnboard_Headless_MintsNoPasswordHash above assert behaviourally,
// pinned to EditionHosted; TestOnboard_LocalMode_CreatesAdminAccount
// (onboard_test.go) is the positive mirror for local mode. See rest_onboarding.go's
// writeLocalAdminUser in pkg/gateway (WP5's identical composition on the REST
// side) for the same reasoning applied to the gateway.
