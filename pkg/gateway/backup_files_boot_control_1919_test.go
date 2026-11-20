// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// backup_files_boot_control_1919_test.go — CONTROL test for issue #1919
// (founder decision P-Q3b B, 2026-11-19): Backup and Restore is removed
// from Settings, Data, and the removal must NOT delete existing backup
// files on users' disks ("Do not delete existing backup files on users'
// disks").
//
// This is an explicit control: it PASSES on the pre-change code (boot has
// never deleted these files) and must KEEP passing after the feature is
// removed — including when the boot step that created the backups dir
// (datamodel.Init's WP4 block) goes away and when an install that ran the
// old feature still carries archives on disk. It boots the real boot
// sequence (bootLoggingAndDataModel → setupAndStartServices →
// stopAndCleanupServices, the same harness shape
// goal_pause_b1_boot_contract_test.go uses) in both editions and asserts
// the seeded archive set is byte-identical while running and after
// shutdown.
package gateway

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestGatewayBootLeavesBackupsDirUntouched_1919(t *testing.T) {
	for _, edition := range []string{config.EditionCore, config.EditionHosted} {
		t.Run(edition, func(t *testing.T) {
			withEdition(t, edition)
			home := t.TempDir()
			t.Setenv("OMNIPUS_HOME", home)
			t.Setenv("OMNIPUS_BEARER_TOKEN", "")

			backupsDir := filepath.Join(home, "backups")
			require.NoError(t, os.MkdirAll(backupsDir, 0o700))
			seeded := map[string][]byte{
				// An archive in the shape the removed feature produced.
				"backup-20250101T000000Z.tar.gz": minimalTarGz_1919(t),
				// A legacy, differently named snapshot a user could have made
				// by hand — guards against name-pattern-based pruning.
				"vault-snapshot-2019.tar": []byte("pre-existing user snapshot, never created by the removed feature (#1919)"),
			}
			for name, content := range seeded {
				require.NoError(t, os.WriteFile(filepath.Join(backupsDir, name), content, 0o600))
			}

			// Mirror b1CompBoot's logging save/restore around the real boot
			// preamble (bootLoggingAndDataModel runs EnableFileLogging,
			// installSlogBridge and datamodel.Init — the boot step that owns
			// the backups directory).
			previousSlog := slog.Default()
			t.Cleanup(func() {
				slog.SetDefault(previousSlog)
				logger.DisableFileLogging()
			})
			require.NoError(t, bootLoggingAndDataModel(home))

			cfg := seededBootConfig(t)
			cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
			cfg.Gateway.Host = "127.0.0.1"
			cfg.Gateway.Port = 0
			cfg.Gateway.Users = []config.UserConfig{{Username: "backup-canary"}}
			mb := bus.NewMessageBus()
			al := mustAgentLoop(t, cfg, mb, &restMockProvider{})
			store := credentials.NewStore(filepath.Join(home, "credentials.json"))
			require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x41}, 32)))

			bootCtx, bootCancel := context.WithCancel(context.Background())
			rs, err := setupAndStartServices(bootCtx, cfg, credentials.SecretBundle{}, al, mb, home, store, &SandboxApplyResult{}, tools.NewBuiltinRegistry(), tools.NewMCPRegistry(), false)
			require.NoError(t, err, "genuine top-level boot must succeed")
			require.NotNil(t, rs, "boot must return a running-services state — an empty boot would make this control vacuous")
			// Boot-depth guard (same as the b1 boot-contract harness): services
			// must have actually started, or the "boot never touches backups"
			// claim below would be proven against nothing.
			require.NotNil(t, rs.PlanEngine, "boot must have started the services — an empty services state makes this control vacuous")

			// The risk window is boot itself: assert while everything runs,
			// then again after the gateway shuts down cleanly.
			assertBackupsUntouched_1919(t, backupsDir, seeded)
			bootCancel()
			stopAndCleanupServices(rs, 5*time.Second, false)
			assertBackupsUntouched_1919(t, backupsDir, seeded)
		})
	}
}

// assertBackupsUntouched_1919 is the whole control: the exact seeded name
// set (nothing deleted, nothing appeared), byte-identical contents, and the
// directory itself still present.
func assertBackupsUntouched_1919(t *testing.T, backupsDir string, seeded map[string][]byte) {
	t.Helper()
	entries, err := os.ReadDir(backupsDir)
	require.NoError(t, err, "the backups directory must still exist and be readable after boot")

	got := make(map[string]bool, len(entries))
	for _, e := range entries {
		got[e.Name()] = true
	}
	for name := range seeded {
		assert.Truef(t, got[name], "existing backup file %q must survive boot untouched (issue #1919)", name)
	}
	for name := range got {
		_, known := seeded[name]
		assert.Truef(t, known, "boot must not write anything new into backups/ — found unexpected entry %q", name)
	}
	for name, want := range seeded {
		gotBytes, readErr := os.ReadFile(filepath.Join(backupsDir, name))
		if readErr != nil {
			continue // missing file is already asserted above with its own message
		}
		assert.Truef(t, bytes.Equal(want, gotBytes), "existing backup file %q must be byte-identical after boot (issue #1919)", name)
	}
}

// minimalTarGz_1919 builds a small real tar.gz so the seeded archive is not
// an empty stub — a prune step that validates archives before deleting them
// must still see a plausible archive here.
func minimalTarGz_1919(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	content := []byte("canary archive content for issue #1919 control")
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "sessions/one.json", Mode: 0o600, Size: int64(len(content)), Typeflag: tar.TypeReg}))
	_, err := tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}
