// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// A config built BEFORE the per-test home existed carries the user-home fallback
// workspace (config.DefaultConfig resolves it when called). The helper must move
// it under the per-test home, or the agent loop reads and writes ~/.omnipus.
func TestPinConfigWorkspaceToTestHome_MovesAFallbackWorkspaceUnderTheTestHome(t *testing.T) {
	if isolatedProcessHome == nil {
		t.Fatal("TestMain did not isolate the process home")
	}
	cfg := config.DefaultConfig() // built first, exactly like newSchedulesTestAPI
	fallback := isolatedProcessHome.OmnipusDir + string(filepath.Separator)
	if !strings.HasPrefix(cfg.Agents.Defaults.Home, fallback) {
		t.Fatalf("instrument check: DefaultConfig workspace %q is not under the fallback home %q", cfg.Agents.Defaults.Home, fallback)
	}

	ensureIsolatedTestOmnipusHome(t)
	pinConfigWorkspaceToTestHome(cfg)

	home := config.OmnipusHomeDir()
	if strings.HasPrefix(home, fallback) {
		t.Fatalf("the per-test home %q is still under the fallback home", home)
	}
	if got, want := cfg.Agents.Defaults.Home, filepath.Join(home, "workspace"); got != want {
		t.Fatalf("workspace = %q, want %q (under the per-test home)", got, want)
	}
}

// An empty workspace must not stay empty. initializeCore turns that into ".",
// and the task migrations then create tasks/ in the package directory.
func TestPinConfigWorkspaceToTestHome_FillsAnEmptyWorkspaceUnderTheTestHome(t *testing.T) {
	cfg := &config.Config{}
	ensureIsolatedTestOmnipusHome(t)
	pinConfigWorkspaceToTestHome(cfg)

	home := config.OmnipusHomeDir()
	if got, want := cfg.Agents.Defaults.Home, filepath.Join(home, "workspace"); got != want {
		t.Fatalf("workspace = %q, want %q (under the per-test home)", got, want)
	}
	if !filepath.IsAbs(cfg.Agents.Defaults.Home) {
		t.Fatalf("workspace %q is not absolute; a relative workspace writes tasks/ into the working directory", cfg.Agents.Defaults.Home)
	}
}

func TestPinConfigWorkspaceToTestHome_LeavesAnExplicitWorkspaceAlone(t *testing.T) {
	cfg := config.DefaultConfig()
	explicit := filepath.Join(t.TempDir(), "my-workspace")
	cfg.Agents.Defaults.Home = explicit
	ensureIsolatedTestOmnipusHome(t)
	pinConfigWorkspaceToTestHome(cfg)
	if cfg.Agents.Defaults.Home != explicit {
		t.Fatalf("workspace = %q, want the explicit %q untouched", cfg.Agents.Defaults.Home, explicit)
	}
}
