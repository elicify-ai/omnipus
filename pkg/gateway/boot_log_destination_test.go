// Copyright (c) 2026 Omnipus contributors
// License: MIT

package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// Oracle: OMNIPUS_LOG_FILE selects the diagnostic sink requested by the
// terminal-outcome E2E acceptance. The existing ConfigureFromEnv contract also
// expands a home-relative path. Exercise real gateway boot, not just that parser:
// boot and subsequent structured records must reach the selected file.
func TestBootLogging_EnvironmentDestination(t *testing.T) {
	for _, mode := range []string{"default", "absolute", "home_relative", "invalid_destination"} {
		t.Run(mode, func(t *testing.T) {
			previousHandler := slog.Default()
			previousLevel := logger.GetLevel()
			t.Cleanup(func() {
				logger.DisableFileLogging()
				logger.SetLevel(previousLevel)
				slog.SetDefault(previousHandler)
			})
			logger.SetLevel(logger.INFO)
			home := t.TempDir()
			defaultLog := filepath.Join(home, "logs", "gateway.log")
			wantedLog := defaultLog
			t.Setenv("OMNIPUS_LOG_FILE", "")
			if mode != "default" {
				userHome := t.TempDir()
				wantedLog = filepath.Join(userHome, "diagnostics", "gateway.jsonl")
				t.Setenv("HOME", userHome)
				t.Setenv("OMNIPUS_LOG_FILE", wantedLog)
				if mode == "home_relative" {
					t.Setenv("OMNIPUS_LOG_FILE", "~/diagnostics/gateway.jsonl")
				}
				if mode == "invalid_destination" {
					require.NoError(t, os.MkdirAll(wantedLog, 0o755))
				} else {
					require.NoError(t, os.MkdirAll(filepath.Dir(wantedLog), 0o755))
					require.NoError(t, os.WriteFile(wantedLog, []byte("{\"message\":\"existing operator record\"}\n"), 0o600))
				}
			}

			if mode == "invalid_destination" {
				var caught any
				func() {
					defer func() { caught = recover() }()
					_ = bootLoggingAndDataModel(home)
				}()
				err, ok := caught.(error)
				require.True(t, ok, "an unusable explicit log destination must refuse boot, not fall back; panic=%v", caught)
				var pathErr *os.PathError
				require.True(t, errors.As(err, &pathErr), "boot refusal must retain the filesystem cause: %v", err)
				require.Equal(t, "open", pathErr.Op)
				require.Equal(t, wantedLog, pathErr.Path)
				require.Equal(t, fmt.Sprintf("error enabling file logging: failed to open log file: %s", pathErr.Error()), err.Error())
				_, err = os.Stat(defaultLog)
				require.True(t, os.IsNotExist(err), "no silent fallback diagnostic file")
				_, err = os.Stat(filepath.Join(home, "config.json"))
				require.True(t, os.IsNotExist(err), "boot must stop before data initialization")
				return
			}

			require.NoError(t, bootLoggingAndDataModel(home))
			_, err := os.Stat(filepath.Join(home, "config.json"))
			require.NoError(t, err, "positive control: actual datamodel initialization ran")
			slog.Info("post-boot destination probe", "event_kind", "probe", "turn_id", "boot-log-probe")
			raw, err := os.ReadFile(wantedLog)
			require.NoError(t, err, "gateway must open the requested sink")
			var startupCount, probeCount, existingCount int
			for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				var row map[string]any
				require.NoError(t, json.Unmarshal([]byte(line), &row), "real diagnostic must be JSON")
				switch row["message"] {
				case "datamodel: first-run setup complete — default config written":
					startupCount++
				case "post-boot destination probe":
					probeCount++
					require.Equal(t, "probe", row["event_kind"])
					require.Equal(t, "boot-log-probe", row["turn_id"])
				case "existing operator record":
					existingCount++
				}
			}
			require.Equal(t, 1, startupCount, "first subsystem log must reach the selected sink")
			require.Equal(t, 1, probeCount, "subsequent structured diagnostics must stay in that sink")
			if mode != "default" {
				require.Equal(t, 1, existingCount, "configured sink is appended, never truncated")
				_, err = os.Stat(defaultLog)
				require.True(t, os.IsNotExist(err), "explicit sink replaces the default destination")
			} else {
				require.Zero(t, existingCount)
			}
		})
	}
}
