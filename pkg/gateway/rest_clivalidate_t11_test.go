// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for T11 slice A, person-only Check CLI (slim spec §3.2, §8 FR-4 and FR-5).
//
// Oracles come from the spec, not from a run of the current route:
//   - CLI-token, development-bypass, and no-user requests are 403 before the
//     body is read, and nothing starts (FR-4, C-A1).
//   - A signed-in person whose candidate fails CheckProgramPath gets reason
//     path-not-allowed, nothing starts, and the message names the failed check
//     (FR-5, C-A2). Domain refusals stay on HTTP 200, which is the route's
//     existing convention; 403 is only the person gate.
//   - An allowed candidate is launched, the version comes back, and
//     resolved_path is the final file (FR-5 "launches the returned file").
//
// REAL: the registered POST /api/v1/system/cli-validate chain and, on an
// allowed candidate, the real version handshake.
// MOCK: none. cliTestConnection is wrapped only to count calls; the original
// handshake still runs. The candidate is a Go-built program, per the spec's
// Windows rule (no shell script).
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

const (
	t11MarkerText = "t11-marker-sentinel"
	t11Version    = "1.2.3"
)

// t11ReadCounter counts body bytes actually read. Spec §3.2: the person gate
// returns 403 before reading the body.
type t11ReadCounter struct {
	r io.Reader
	n int
}

func (c *t11ReadCounter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func t11ValidateMux(t *testing.T) (*restAPI, *http.ServeMux, string) {
	t.Helper()
	api := newTestRestAPIWithHome(t)
	t.Setenv("OMNIPUS_HOME", api.homePath)
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	return api, mux, api.homePath
}

func t11Person(t *testing.T, home string) (token string, cfg *config.Config) {
	t.Helper()
	token = "omnipus_" + strings.Repeat("c", 64)
	cfg = &config.Config{
		Gateway: config.GatewayConfig{
			Users: []config.UserConfig{{
				Username:  "t11-person",
				TokenHash: mustBcryptHash(t, token),
			}},
		},
		Agents: config.AgentsConfig{Defaults: config.AgentDefaults{Home: home}},
	}
	return token, cfg
}

func t11CLIToken(t *testing.T, home string) (token string, cfg *config.Config) {
	t.Helper()
	token = "omnipus_" + strings.Repeat("e", 64)
	cfg = &config.Config{
		Gateway: config.GatewayConfig{
			CLIToken: &config.TokenEntry{Hash: mustBcryptHash(t, token)},
		},
		Agents: config.AgentsConfig{Defaults: config.AgentDefaults{Home: home}},
	}
	return token, cfg
}

func t11CountSpawns(t *testing.T) *int {
	t.Helper()
	n := 0
	orig := cliTestConnection
	cliTestConnection = func(ctx context.Context, cli, cliPath string) runner.ConnectionTestResult {
		n++
		return orig(ctx, cli, cliPath)
	}
	t.Cleanup(func() { cliTestConnection = orig })
	return &n
}

func t11PostValidate(t *testing.T, mux *http.ServeMux, cfg *config.Config, token, cliPath, ip string, counter *t11ReadCounter) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"cli": "claude-code", "cli_path": cliPath})
	require.NoError(t, err)
	var body io.Reader = bytes.NewReader(payload)
	if counter != nil {
		counter.r = bytes.NewReader(payload)
		counter.n = 0
		body = counter
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/cli-validate", body)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.RemoteAddr = ip
	req = req.WithContext(context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, cfg))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func t11DecodeValidate(t *testing.T, w *httptest.ResponseRecorder) gen.CliValidateResponse {
	t.Helper()
	var resp gen.CliValidateResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), "body=%s", w.Body.String())
	return resp
}

func t11DetailHasOne(t *testing.T, resp gen.CliValidateResponse, needles ...string) {
	t.Helper()
	require.NotNil(t, resp.Detail, "spec §3.2: the refusal message names the failed check")
	require.NotContains(t, *resp.Detail, t11MarkerText, "the message must not carry file contents")
	low := strings.ToLower(*resp.Detail)
	for _, n := range needles {
		if strings.Contains(low, strings.ToLower(n)) {
			return
		}
	}
	t.Fatalf("spec §3.2: detail %q must name the failed check (one of %v)", *resp.Detail, needles)
}

func t11AssertNotSpawned(t *testing.T, marker string, spawns int) {
	t.Helper()
	require.Zero(t, spawns, "spec FR-4/FR-5: nothing starts")
	_, err := os.Stat(marker)
	require.ErrorIs(t, err, fs.ErrNotExist, "marker %s must be absent (the program must not have started)", marker)
}

func t11AssertPathRefused(t *testing.T, w *httptest.ResponseRecorder, marker string, spawns int, needles ...string) {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code,
		"path-not-allowed is the classified reason on the existing 200 body, not an HTTP error; body=%s", w.Body.String())
	resp := t11DecodeValidate(t, w)
	require.Equal(t, gen.CliValidateResponseReason("path-not-allowed"), resp.Reason,
		"body=%s spawns=%d", w.Body.String(), spawns)
	require.False(t, resp.Ok, "a refused candidate is not ok")
	require.Nil(t, resp.Version, "nothing ran, so there is no version")
	t11DetailHasOne(t, resp, needles...)
	t11AssertNotSpawned(t, marker, spawns)
}

// t11BuildMarkerProgram builds a Go program that writes marker and prints a
// version. Spec §8: Windows fakes are Go programs, not shell scripts. The same
// shape is used on Unix so the fixture does not depend on a shell.
func t11BuildMarkerProgram(t *testing.T, marker string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o700))
	src := "package main\nimport \"os\"\nfunc main() {\n" +
		"\t_ = os.WriteFile(" + t11JSONString(marker) + ", []byte(" + t11JSONString(t11MarkerText) + "), 0o644)\n" +
		"\t_, _ = os.Stdout.WriteString(" + t11JSONString("claude "+t11Version+"\n") + ")\n}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module t11fake\n\ngo 1.22\n"), 0o644))
	out := filepath.Join(dir, "fakecli")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = dir
	env := make([]string, 0, len(os.Environ())+1)
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "GOFLAGS=") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "CGO_ENABLED=0")
	built, err := cmd.CombinedOutput()
	require.NoError(t, err, "fixture go build: %s", built)
	return out
}

func t11JSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func t11CopyExe(t *testing.T, src, dst string, mode os.FileMode) {
	t.Helper()
	in, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(dst), 0o700))
	require.NoError(t, os.WriteFile(dst, in, mode))
	require.NoError(t, os.Chmod(dst, mode))
}

func t11ClearMarker(t *testing.T, marker string) {
	t.Helper()
	err := os.Remove(marker)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("clear marker: %v", err)
	}
}

// TestCliValidate_NonPersonRefusedZeroSpawn is FR-4. A CLI token and a
// development-bypass request are 403 before the body is read, and the
// candidate program does not start. A signed-in person is not 403.
func TestCliValidate_NonPersonRefusedZeroSpawn(t *testing.T) {
	_, mux, home := t11ValidateMux(t)
	marker := filepath.Join(t.TempDir(), "marker")
	prog := t11BuildMarkerProgram(t, marker)
	spawns := t11CountSpawns(t)

	t.Run("cli token is 403 before the body and starts nothing", func(t *testing.T) {
		t11ClearMarker(t, marker)
		*spawns = 0
		token, cfg := t11CLIToken(t, home)
		var reads t11ReadCounter
		w := t11PostValidate(t, mux, cfg, token, prog, "198.51.100.60:1", &reads)
		require.Equal(t, http.StatusForbidden, w.Code, "spec FR-4 / C-A1; body=%s", w.Body.String())
		require.Zero(t, reads.n, "spec §3.2: 403 before reading the body")
		t11AssertNotSpawned(t, marker, *spawns)
	})

	t.Run("development bypass is 403 before the body and starts nothing", func(t *testing.T) {
		t11ClearMarker(t, marker)
		*spawns = 0
		cfg := &config.Config{
			Gateway: config.GatewayConfig{DevModeBypass: true},
			Agents:  config.AgentsConfig{Defaults: config.AgentDefaults{Home: home}},
		}
		var reads t11ReadCounter
		w := t11PostValidate(t, mux, cfg, "", prog, "198.51.100.61:1", &reads)
		require.Equal(t, http.StatusForbidden, w.Code, "spec FR-4 / C-A1; body=%s", w.Body.String())
		require.NotEqual(t, http.StatusServiceUnavailable, w.Code, "this is the person gate (403), not RequireNotBypass (503)")
		require.Zero(t, reads.n, "spec §3.2: 403 before reading the body")
		t11AssertNotSpawned(t, marker, *spawns)
	})

	t.Run("signed-in person is not forbidden", func(t *testing.T) {
		// Empty path stays the existing missing-binary short-circuit. The spec
		// does not repeal it; it adds the person gate in front.
		token, cfg := t11Person(t, home)
		w := t11PostValidate(t, mux, cfg, token, "", "198.51.100.62:1", nil)
		require.NotEqual(t, http.StatusForbidden, w.Code, "a signed-in person is the caller the route accepts; body=%s", w.Body.String())
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		resp := t11DecodeValidate(t, w)
		require.Equal(t, gen.CliValidateResponseReasonMissingBinary, resp.Reason)
	})
}

// TestCliValidate_SharedCheckBeforeSpawn is FR-5. The shared file check runs
// before the handshake. Forbidden candidates do not start. An allowed
// candidate launches the resolved file.
func TestCliValidate_SharedCheckBeforeSpawn(t *testing.T) {
	_, mux, home := t11ValidateMux(t)
	marker := filepath.Join(t.TempDir(), "marker")
	prog := t11BuildMarkerProgram(t, marker)
	token, cfg := t11Person(t, home)
	spawns := t11CountSpawns(t)
	const ip = "198.51.100.70:1"

	refuse := func(t *testing.T, path string, needles ...string) {
		t.Helper()
		t11ClearMarker(t, marker)
		*spawns = 0
		w := t11PostValidate(t, mux, cfg, token, path, ip, nil)
		t11AssertPathRefused(t, w, marker, *spawns, needles...)
	}

	t.Run("program inside omnipus home", func(t *testing.T) {
		dst := filepath.Join(home, "t11-fakecli")
		t11CopyExe(t, prog, dst, 0o755)
		refuse(t, dst, "home")
	})

	t.Run("program inside a workspace folder", func(t *testing.T) {
		const id = "t11ws"
		require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{ID: id, Name: "T11"}))
		work, err := workspace.SafeWorkDir(home, id)
		require.NoError(t, err)
		dst := filepath.Join(work, "fakecli")
		t11CopyExe(t, prog, dst, 0o755)
		refuse(t, dst, "home", "workspace")
	})

	t.Run("program inside a workspace mount", func(t *testing.T) {
		const id = "t11mount"
		require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{ID: id, Name: "T11 mount"}))
		host := t.TempDir()
		mount, _, err := workspace.CreateMount(home, id, "extra", host)
		require.NoError(t, err)
		dst := filepath.Join(mount.HostPath, "fakecli")
		t11CopyExe(t, prog, dst, 0o755)
		refuse(t, dst, "mount")
	})

	if runtime.GOOS != "windows" {
		t.Run("link into a workspace", func(t *testing.T) {
			const id = "t11link"
			require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{ID: id, Name: "T11 link"}))
			work, err := workspace.SafeWorkDir(home, id)
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(work, 0o700))
			inside := filepath.Join(work, "fakecli")
			t11CopyExe(t, prog, inside, 0o755)
			outDir := t.TempDir()
			require.NoError(t, os.Chmod(outDir, 0o700))
			link := filepath.Join(outDir, "codex-x")
			require.NoError(t, os.Symlink(inside, link))
			refuse(t, link, "link")
		})

		t.Run("middle link of the chain sits in a workspace", func(t *testing.T) {
			const id = "t11chain"
			require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{ID: id, Name: "T11 chain"}))
			work, err := workspace.SafeWorkDir(home, id)
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(work, 0o700))
			finalDir := t.TempDir()
			require.NoError(t, os.Chmod(finalDir, 0o700))
			final := filepath.Join(finalDir, "fakecli")
			t11CopyExe(t, prog, final, 0o755)
			mid := filepath.Join(work, "mid")
			require.NoError(t, os.Symlink(final, mid))
			outDir := t.TempDir()
			require.NoError(t, os.Chmod(outDir, 0o700))
			outer := filepath.Join(outDir, "outer")
			require.NoError(t, os.Symlink(mid, outer))
			refuse(t, outer, "link")
		})

		t.Run("file writable by others", func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.Chmod(dir, 0o700))
			dst := filepath.Join(dir, "fakecli")
			t11CopyExe(t, prog, dst, 0o707)
			refuse(t, dst, "writable")
		})

		t.Run("parent directory writable by others", func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.Chmod(dir, 0o707))
			dst := filepath.Join(dir, "fakecli")
			t11CopyExe(t, prog, dst, 0o755)
			refuse(t, dst, "writable")
		})

		t.Run("not executable", func(t *testing.T) {
			dir := t.TempDir()
			dst := filepath.Join(dir, "fakecli")
			t11CopyExe(t, prog, dst, 0o644)
			refuse(t, dst, "executable")
		})
	}

	t.Run("relative path", func(t *testing.T) {
		dir := t.TempDir()
		dst := filepath.Join(dir, "fakecli")
		t11CopyExe(t, prog, dst, 0o755)
		wd, err := os.Getwd()
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(wd) })
		require.NoError(t, os.Chdir(dir))
		name := "fakecli"
		if runtime.GOOS == "windows" {
			name = "fakecli.exe"
			require.NoError(t, os.Rename(dst, filepath.Join(dir, name)))
		}
		refuse(t, name, "absolute")
	})

	t.Run("directory", func(t *testing.T) {
		refuse(t, t.TempDir(), "regular")
	})

	t.Run("allowed program returns the version and the resolved file", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0o700))
		dst := filepath.Join(dir, "fakecli")
		t11CopyExe(t, prog, dst, 0o755)
		final, err := filepath.EvalSymlinks(dst)
		require.NoError(t, err)
		t11ClearMarker(t, marker)
		*spawns = 0
		w := t11PostValidate(t, mux, cfg, token, dst, ip, nil)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		resp := t11DecodeValidate(t, w)
		require.NotEqual(t, gen.CliValidateResponseReason("path-not-allowed"), resp.Reason, "an allowed file must run; body=%s", w.Body.String())
		require.NotNil(t, resp.Version)
		require.Equal(t, t11Version, *resp.Version, "spec FR-5 / BDD-50: the version of the launched file")
		require.NotNil(t, resp.ResolvedPath)
		require.Equal(t, final, *resp.ResolvedPath, "callers launch the checked resolved file, not a second path")
		require.Equal(t, 1, *spawns)
		_, err = os.Stat(marker)
		require.NoError(t, err, "the launched program must have written the marker")
	})

	if runtime.GOOS != "windows" {
		t.Run("allowed symlink launches the final file", func(t *testing.T) {
			// Spec gap, stated: a symlink's own mode is 0777 on Unix. The spec
			// still says an accepted link launches the resolved file, so a link
			// whose target and parent directories are not other-writable is allowed.
			dir := t.TempDir()
			require.NoError(t, os.Chmod(dir, 0o700))
			dst := filepath.Join(dir, "fakecli")
			t11CopyExe(t, prog, dst, 0o755)
			link := filepath.Join(dir, "link")
			require.NoError(t, os.Symlink(dst, link))
			final, err := filepath.EvalSymlinks(link)
			require.NoError(t, err)
			t11ClearMarker(t, marker)
			*spawns = 0
			w := t11PostValidate(t, mux, cfg, token, link, ip, nil)
			require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
			resp := t11DecodeValidate(t, w)
			require.NotNil(t, resp.ResolvedPath)
			require.Equal(t, final, *resp.ResolvedPath, "the returned path is the final file, not the link")
			require.NotNil(t, resp.Version)
			require.Equal(t, t11Version, *resp.Version)
			require.Equal(t, 1, *spawns)
		})
	}
}
