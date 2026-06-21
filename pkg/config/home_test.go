// Omnipus - Ultra-lightweight personal AI agent
// License: MIT

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOmnipusHomeDir_HonorsEnvOverride(t *testing.T) {
	t.Setenv(EnvHome, "/opt/custom-omnipus")

	got := OmnipusHomeDir()
	if got != "/opt/custom-omnipus" {
		t.Errorf("OmnipusHomeDir() with env override = %q, want %q", got, "/opt/custom-omnipus")
	}
}

func TestOmnipusHomeDir_FallsBackToHomeDirDotOmnipus(t *testing.T) {
	t.Setenv(EnvHome, "")

	got := OmnipusHomeDir()
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("UserHomeDir failed on this host; skipping: %v", err)
	}
	want := filepath.Join(userHome, ".omnipus")
	if got != want {
		t.Errorf("OmnipusHomeDir() with empty env = %q, want %q", got, want)
	}
}

func TestOmnipusHomeDir_EmptyEnvIsTreatedAsUnset(t *testing.T) {
	// An empty OMNIPUS_HOME must NOT be returned verbatim — otherwise callers
	// that join ".omnipus" onto it would end up writing to the process cwd.
	t.Setenv(EnvHome, "")

	got := OmnipusHomeDir()
	if got == "" {
		t.Fatal("OmnipusHomeDir() returned empty string for empty env; expected fallback")
	}
	if !strings.Contains(got, ".omnipus") && !strings.Contains(got, "omnipus-") {
		t.Errorf("OmnipusHomeDir() fallback = %q, expected it to contain \".omnipus\" or a temp prefix", got)
	}
}

func TestOmnipusHomeDir_RelativeOverrideIsResolvedAgainstCWD(t *testing.T) {
	// A relative OMNIPUS_HOME used to be returned "trusted verbatim", which
	// meant runtime files (cost.json, state.json) would land inside the
	// source tree when the gateway was started from the repo root with
	// `OMNIPUS_HOME=pkg/gateway` (or any other relative override). The
	// resolution must always return an absolute path, resolved against the
	// process CWD at call time, so the home directory is stable across CWD
	// changes and the cost-tracker points at the right location.
	t.Setenv(EnvHome, "relative/override")

	got := OmnipusHomeDir()
	if !filepath.IsAbs(got) {
		t.Errorf("OmnipusHomeDir() with relative env = %q, want absolute path", got)
	}
	// The absolute path should be the CWD joined with the relative override,
	// cleaned of any double-separator or trailing-slash artifacts.
	cwd, err := os.Getwd()
	if err != nil {
		t.Skipf("Getwd failed on this host; skipping: %v", err)
	}
	want := filepath.Clean(filepath.Join(cwd, "relative", "override"))
	if got != want {
		t.Errorf("OmnipusHomeDir() with relative env = %q, want %q", got, want)
	}
}

func TestOmnipusHomeDir_RelativeOverrideIsCleaned(t *testing.T) {
	// Even an "absolute-looking" relative path with redundant separators
	// must be cleaned before returning, so downstream filepath.Join calls
	// don't produce paths with "//" or trailing slashes.
	t.Setenv(EnvHome, "rel/./with/../trailing/")

	got := OmnipusHomeDir()
	if !filepath.IsAbs(got) {
		t.Errorf("OmnipusHomeDir() with messy relative env = %q, want absolute path", got)
	}
	if strings.Contains(got, "//") || strings.Contains(got, "/./") {
		t.Errorf("OmnipusHomeDir() returned un-cleaned path %q", got)
	}
}
