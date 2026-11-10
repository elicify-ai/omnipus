//go:build linux || darwin

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// #1103: the config re-read used to decide automatic DuckDuckGo fallback
// must not fail silently. The handler delegates this branch to
// validateSearchIntegrationRoleWrite; that method and both loaders stay real.
//
// Oracle: https://github.com/elicify-ai/omnipus/issues/1103 accepts a warning/error
// containing the load error (like integrationRoleSnapshot), or rejection with
// the same 500 configuration error as the earlier identical read.
// The positive materialization control comes from the web-search provider
// model spec, Resolution/save-time rules: an absent fallback is materialized
// as DuckDuckGo when R3 applies and the operator did not choose No fallback.
//
// The filesystem fixture uses a POSIX named pipe, supported on Linux/macOS.
// The first os.ReadFile sees valid JSON through its already-open descriptor.
// Before sending EOF, the writer atomically replaces the pathname with invalid
// JSON. The second config.LoadConfig must therefore fail, without a sleep,
// monkey patch, loader mock or production test seam. Windows is not exercised
// by this filesystem mechanism. GREEN and mutation probes belong to CHECK.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// configRereadFailureFixture replaces only the process-edge filesystem.
// join surfaces every writer error and waits for completion before cleanup.
func configRereadFailureFixture(t *testing.T, firstJSON string) (*restAPI, func()) {
	t.Helper()
	api := &restAPI{homePath: t.TempDir()}
	path := api.configPath()
	badPath := filepath.Join(api.homePath, "invalid-config.json")
	require.NoError(t, os.WriteFile(badPath, []byte(`{"version":`), 0o600))
	require.NoError(t, unix.Mkfifo(path, 0o600))

	done := make(chan error, 1)
	go func() {
		writer, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			done <- err
			return
		}
		n, writeErr := io.WriteString(writer, firstJSON)
		if writeErr == nil && n != len(firstJSON) {
			writeErr = io.ErrShortWrite
		}
		// EOF is withheld until the next open will see the invalid file.
		renameErr := os.Rename(badPath, path)
		closeErr := writer.Close()
		done <- errors.Join(writeErr, renameErr, closeErr)
	}()

	joined := false
	t.Cleanup(func() {
		if joined {
			return
		}
		// If an assertion returns before the reader opens the pipe, unblock
		// and join the writer rather than leave a live goroutine behind.
		unblock, err := os.OpenFile(path, os.O_RDWR|unix.O_NONBLOCK, 0)
		if err != nil {
			t.Errorf("could not unblock config fixture writer: %v", err)
			return
		}
		writerErr := <-done
		joined = true
		if err := errors.Join(writerErr, unblock.Close()); err != nil {
			t.Errorf("config fixture writer cleanup failed: %v", err)
		}
	})
	return api, func() {
		t.Helper()
		err := <-done
		joined = true
		require.NoError(t, err, "the filesystem swap must complete before judging the production behaviour")
	}
}

func TestIntegrationProviderUpdate_ConfigRereadFailureIsVisible(t *testing.T) {
	const loadError = "failed to detect config version: unexpected end of JSON input"
	// R3 needs usable defaults, not merely named providers. Dummy credentials
	// live only at the process edge; config.json holds references, never keys.
	t.Setenv("BRAVE_API_KEY", "fixture-brave-key")
	t.Setenv("TAVILY_API_KEY", "fixture-tavily-key")
	firstJSON := fmt.Sprintf(`{"version":%d,"tools":{"web":{"roles_migrated_at":"2026-09-30T00:00:00Z","default_provider":"brave","brave":{"enabled":true,"api_key_ref":"BRAVE_API_KEY"},"tavily":{"enabled":true,"api_key_ref":"TAVILY_API_KEY"},"duckduckgo":{"enabled":true}}}}`, config.CurrentVersion)
	active := true
	body := gen.IntegrationProviderUpdateRequest{Kind: "search", Active: &active}
	def, known := integrationDefByID("tavily")
	require.True(t, known, "the fixture must address a real integration provider")

	t.Run("readable_config_materializes_duckduckgo", func(t *testing.T) {
		api := &restAPI{homePath: t.TempDir()}
		require.NoError(t, os.WriteFile(api.configPath(), []byte(firstJSON), 0o600))
		fresh, err := config.LoadConfig(api.configPath())
		require.NoError(t, err)
		require.True(t, fresh.Tools.Web.UsableSearchProvider("brave"), "R3 requires a usable current default")
		require.True(t, fresh.Tools.Web.UsableSearchProvider(def.id), "R3 requires a usable saved default")
		require.True(t, fresh.Tools.Web.UsableSearchProvider("duckduckgo"), "R3 requires usable DuckDuckGo")
		var write integrationRoleWrite
		rejected := api.validateSearchIntegrationRoleWrite(httptest.NewRecorder(), def.id, def, body, &write)
		require.False(t, rejected, "a usable DuckDuckGo fallback is a valid default-role save")
		assert.True(t, write.materializeFallback, "spec save-time rule: an absent fallback must materialize DuckDuckGo")
	})

	t.Run("filesystem_control_fails_only_the_second_read", func(t *testing.T) {
		api, join := configRereadFailureFixture(t, firstJSON)
		state := api.readIntegrationRolesFileState()
		join()
		require.True(t, state.decided, "the first read must receive the valid role marker")
		require.Equal(t, "brave", state.rawDefault, "the first read must receive the valid default role")
		require.NotContains(t, state.web, "fallback_provider", "the fixture must reach the absent-fallback branch")
		_, err := config.LoadConfig(api.configPath())
		require.EqualError(t, err, loadError, "only the second read must fail with the injected JSON parse error")
	})

	t.Run("earlier_identical_read_rejects_with_visible_configuration_error", func(t *testing.T) {
		api, join := configRereadFailureFixture(t, firstJSON)
		w := httptest.NewRecorder()
		var write integrationRoleWrite
		fallbackBody := gen.IntegrationProviderUpdateRequest{Kind: "search", Fallback: &active}
		rejected := api.validateSearchIntegrationRoleWrite(w, def.id, def, fallbackBody, &write)
		join()
		require.True(t, rejected, "#1103's reference read error rejects the save")
		require.Equal(t, http.StatusInternalServerError, w.Code)
		var response gen.ErrorResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		assert.Equal(t, gen.ErrorResponse{Error: "could not read the current configuration"}, response)
	})

	t.Run("second_read_failure_is_logged_or_rejects_the_save", func(t *testing.T) {
		var logs bytes.Buffer
		previous := slog.Default()
		slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
		t.Cleanup(func() { slog.SetDefault(previous) })

		// Calibrate with the real production logging path cited by #1103,
		// then reset so that control cannot satisfy the regression.
		probeAPI := &restAPI{homePath: t.TempDir()}
		require.NoError(t, os.WriteFile(probeAPI.configPath(), []byte(`{"version":`), 0o600))
		_, resolved := probeAPI.integrationRoleSnapshot(integrationRolesFileState{})
		require.False(t, resolved, "the production logging control must encounter the same load error")
		var probe map[string]any
		require.NoError(t, json.Unmarshal(logs.Bytes(), &probe))
		require.Equal(t, "ERROR", probe["level"])
		require.Equal(t, loadError, probe["error"])
		require.Equal(t, probeAPI.configPath(), probe["path"])
		logs.Reset()

		api, join := configRereadFailureFixture(t, firstJSON)
		w := httptest.NewRecorder()
		var write integrationRoleWrite
		rejected := api.validateSearchIntegrationRoleWrite(w, def.id, def, body, &write)
		join()

		if rejected {
			// The issue explicitly permits the same visible 500 as the earlier
			// read instead of logging. An unrelated validation error is not enough.
			require.Equal(t, http.StatusInternalServerError, w.Code, "#1103: a config read failure must be a server error")
			var response gen.ErrorResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			assert.Equal(t, gen.ErrorResponse{Error: "could not read the current configuration"}, response)
		} else {
			captured := logs.String()
			var loggedErrors []string
			decoder := json.NewDecoder(strings.NewReader(captured))
			for {
				var entry map[string]any
				err := decoder.Decode(&entry)
				if errors.Is(err, io.EOF) {
					break
				}
				require.NoError(t, err, "captured log entries must be valid JSON")
				require.Contains(t, []string{"WARN", "ERROR"}, entry["level"], "the operator must see a warning/error, not only debug/info")
				if reason, ok := entry["error"].(string); ok {
					loggedErrors = append(loggedErrors, reason)
				}
			}
			assert.Equal(t, []string{loadError}, loggedErrors,
				"#1103: the DuckDuckGo fallback config re-read failed, but the save was not rejected and its error was not logged; rejected=%t status=%d body=%q captured logs: %s",
				rejected, w.Code, w.Body.String(), captured)
		}
	})
}
