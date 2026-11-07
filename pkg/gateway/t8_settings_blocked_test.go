//go:build !windows

package gateway

// r17 §3 / D-6. Real registered routes, login cookie, generated validation,
// config.json and filesystem. No replacement handler or path-validator mock.
import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThirdPartySettings_PathPresetAndPersonOnly(t *testing.T) {
	t.Run("9.1 initial rows and 9.26 detection never executes", func(t *testing.T) {
		f := t8SettingsAPI(t)
		dir := t.TempDir()
		marker := filepath.Join(dir, "was-executed")
		// CHECK F4: an explicit link makes resolved-path power independent of
		// whether this host's temporary directory happens to be a symlink.
		target := filepath.Join(t.TempDir(), "codex-real")
		require.NoError(t, os.WriteFile(target, []byte("#!/bin/sh\nprintf launched > '"+marker+"'\n"), 0o755))
		bin := filepath.Join(dir, "codex")
		require.NoError(t, os.Symlink(target, bin))
		t.Setenv("PATH", dir)
		rows := t8SettingsRows(t, f.view(t))
		for _, cli := range []string{"claude-code", "codex", "opencode"} {
			row := t8SettingsRow(t, rows, cli)
			assert.Equal(t, "", row["path"])
			assert.Equal(t, "", row["preset_args"])
		}
		detected, ok := t8SettingsRow(t, rows, "codex")["detected"].(map[string]any)
		require.True(t, ok)
		// CliDetectEntry.path is fully resolved, including the explicit link.
		resolved, err := filepath.EvalSymlinks(target)
		require.NoError(t, err)
		require.NotEqual(t, bin, resolved, "instrument: link and target differ even with a canonical TMPDIR")
		assert.Equal(t, bin, detected["link_path"])
		assert.Equal(t, resolved, detected["path"])
		_, err = os.Stat(marker)
		require.ErrorIs(t, err, os.ErrNotExist, "detection must never exec a candidate")
		var disk map[string]any
		require.NoError(t, json.Unmarshal(f.disk(t), &disk))
		assert.NotContains(t, string(f.disk(t)), "\"detected\"", "detection is never persisted")
	})
	t.Run("9.2 replacement and 9.6 entered link preserved", func(t *testing.T) {
		f := t8SettingsAPI(t)
		dir := t.TempDir()
		program := t8Program(t, dir, "codex-v1", 0o755)
		link := filepath.Join(dir, "codex")
		require.NoError(t, os.Symlink(program, link))
		w := f.call(t, http.MethodPut, t8SettingsRoute, t8SettingsBody("codex", link, "--model a"), "person")
		require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
		rows := t8SettingsRows(t, f.view(t))
		row := t8SettingsRow(t, rows, "codex")
		assert.Equal(t, link, row["path"])
		assert.Equal(t, "--model a", row["preset_args"])
		// PUT is whole-section replacement: omitted CLI keys clear, no revision.
		w = f.call(t, http.MethodPut, t8SettingsRoute, t8SettingsBody("opencode", "", "--flag ok"), "person")
		require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
		rows = t8SettingsRows(t, f.view(t))
		row = t8SettingsRow(t, rows, "codex")
		assert.Equal(t, "", row["path"])
		assert.Equal(t, "", row["preset_args"])
		assert.Equal(t, "--flag ok", t8SettingsRow(t, rows, "opencode")["preset_args"])
	})
	t.Run("9.20 preset failures and character boundaries", func(t *testing.T) {
		for _, tc := range []struct{ value, check, reason string }{
			{"x\ny", "contains_line_break", "they must be one line"}, {"x\ry", "contains_line_break", "they must be one line"},
			{"x\x00y", "contains_nul", "they contain a NUL character"}, {strings.Repeat("x", 4097), "too_long", "they are longer than 4,096 characters"},
		} {
			t.Run(tc.check+"-"+string([]rune(tc.value)[0]), func(t *testing.T) {
				f := t8SettingsAPI(t)
				f.expectRejected(t, t8SettingsBody("codex", "", tc.value), "codex", "preset_args", "cli_preset_invalid", tc.check, "", "The parameters for Codex were not saved: "+tc.reason+".")
			})
		}
		for _, n := range []int{0, 1, 4095, 4096} {
			t.Run(fmt.Sprintf("accepted-%d", n), func(t *testing.T) {
				f := t8SettingsAPI(t)
				preset := strings.Repeat("界", n)
				w := f.call(t, http.MethodPut, t8SettingsRoute, t8SettingsBody("codex", "", preset), "person")
				require.Equal(t, http.StatusOK, w.Code, "character limit, not UTF-8 bytes: %s", w.Body.String())
				assert.Equal(t, preset, t8SettingsRow(t, t8SettingsRows(t, f.view(t)), "codex")["preset_args"])
			})
		}
	})
	t.Run("9.21 all rows validate before any save and fixed validation order", func(t *testing.T) {
		f := t8SettingsAPI(t)
		program := t8Program(t, t.TempDir(), "codex", 0o755)
		clis := map[string]any{"codex": map[string]any{"path": program, "preset_args": "--model ok"}, "opencode": map[string]any{"path": "relative", "preset_args": "bad\nline"}}
		body := map[string]any{"clis": clis}
		f.expectRejected(t, body, "opencode", "path", "cli_path_invalid", "not_absolute", "relative", "The program path for OpenCode was not saved: it must be an absolute path.")
		clis["claude-code"] = map[string]any{"path": "first-relative", "preset_args": "bad\nline"}
		f.expectRejected(t, body, "claude-code", "path", "cli_path_invalid", "not_absolute", "first-relative", "The program path for Claude Code was not saved: it must be an absolute path.")
	})
	t.Run("9.22 unknown fields and no revision token", func(t *testing.T) {
		for _, body := range []any{map[string]any{"clis": map[string]any{}, "revision": "not-a-token"}, map[string]any{"clis": map[string]any{"unknown-cli": map[string]any{"path": "", "preset_args": ""}}}, map[string]any{"clis": map[string]any{"codex": map[string]any{"path": ""}}}} {
			f := t8SettingsAPI(t)
			before := f.disk(t)
			w := f.call(t, http.MethodPut, t8SettingsRoute, body, "person")
			require.Equal(t, http.StatusBadRequest, w.Code, "generated schema rejects unknown/missing fields: %s", w.Body.String())
			assert.Equal(t, before, f.disk(t))
			assert.NotEqual(t, http.StatusConflict, w.Code)
		}
	})
	t.Run("9.23 real session versus bearer versus missing session", func(t *testing.T) {
		f := t8SettingsAPI(t)
		for _, method := range []string{http.MethodGet, http.MethodPut} {
			for _, tc := range []struct {
				actor  string
				status int
			}{{"none", 401}, {"token", 403}} {
				before := f.disk(t)
				w := f.call(t, method, t8SettingsRoute, t8SettingsBody("codex", "", ""), tc.actor)
				assert.Equal(t, tc.status, w.Code, "%s %s: %s", method, tc.actor, w.Body.String())
				assert.Equal(t, before, f.disk(t))
			}
		}
	})
	t.Run("9.27 audit old and new path and preset", func(t *testing.T) {
		f := t8SettingsAPI(t)
		oldProgram := t8Program(t, t.TempDir(), "codex-old", 0o755)
		newProgram := t8Program(t, t.TempDir(), "codex-new", 0o755)
		for _, v := range []struct{ path, preset string }{{oldProgram, "--model prior"}, {newProgram, "--model chosen"}} {
			w := f.call(t, http.MethodPut, t8SettingsRoute, t8SettingsBody("codex", v.path, v.preset), "person")
			require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
		}
		for _, tc := range []struct{ event, old, new string }{
			{"settings.third_party_agents.path_changed", oldProgram, newProgram},
			{"settings.third_party_agents.preset_changed", "--model prior", "--model chosen"},
		} {
			entries := readAuditEntries(t, f.auditDir, tc.event)
			require.Len(t, entries, 2)
			// r17 prescribes the values, not the names of the old/new keys.
			// Exact nonempty scalar membership rejects a name-only event without
			// imposing a JSON key spelling the spec never chose.
			values := t8AuditScalars(entries[1])
			assert.Contains(t, values, tc.old)
			assert.Contains(t, values, tc.new)
			assert.Contains(t, values, "codex")
			assert.Contains(t, values, "t8-person")
		}
		w := f.call(t, http.MethodPut, t8SettingsRoute, t8SettingsBody("codex", newProgram, "--model chosen"), "person")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Len(t, readAuditEntries(t, f.auditDir, "settings.third_party_agents.path_changed"), 2)
		assert.Len(t, readAuditEntries(t, f.auditDir, "settings.third_party_agents.preset_changed"), 2)
	})
}

func TestThirdPartySettings_D6_Dataset(t *testing.T) {
	for _, tc := range []struct{ id, kind, check string }{
		{"D6.1", "executable", ""}, {"D6.2 / 9.3", "relative", "not_absolute"}, {"D6.3 / 9.5", "directory", "not_regular_file"},
		{"D6.4 / 9.5", "no-exec", "not_executable"}, {"D6.5 / 9.4", "workspace", "agent_writable_location"}, {"D6.6", "home", "agent_writable_location"},
		// Founder Q13 A / Q2b supersedes only these permission-based refusals.
		{"D6.7 / 9.5", "file-world-write", ""}, {"D6.8 / 9.5", "parent-world-write", ""},
		{"D6.9 / 9.6", "symlink", ""}, {"D6.10", "relative-target", ""}, {"D6.11 / 9.7", "link-world-write", ""},
		{"D6.12 / 9.8", "workspace-middle", "agent_writable_location"}, {"D6.13", "dangling", "not_found"}, {"D6.14", "loop", "not_found"},
		{"D6.15", "group-write", ""}, {"D6.16", "empty", ""},
	} {
		t.Run(tc.id, func(t *testing.T) {
			f := t8SettingsAPI(t)
			dir := t.TempDir()
			entered := t8Program(t, dir, "program", 0o755)
			at := entered
			switch tc.kind {
			case "relative":
				entered = "bin/codex"
				at = entered
			case "directory":
				entered = dir
				at = entered
			case "no-exec":
				require.NoError(t, os.Chmod(entered, 0o644))
			case "file-world-write":
				require.NoError(t, os.Chmod(entered, 0o757))
			case "parent-world-write":
				require.NoError(t, os.Chmod(dir, 0o777))
			case "group-write":
				require.NoError(t, os.Chmod(dir, 0o775))
			case "home":
				entered = t8Program(t, f.api.homePath, "program", 0o755)
				at = entered
			case "workspace":
				ws := seedLibraryWorkspace(t, f.api, "T8 writable workspace")
				entered = t8Program(t, workDir(f.api, ws), "program", 0o755)
				at = entered
			case "symlink", "relative-target", "link-world-write", "workspace-middle", "dangling", "loop":
				target := entered
				entered = filepath.Join(dir, "link")
				at = entered
				if tc.kind == "relative-target" {
					target = "program"
				}
				if tc.kind == "dangling" {
					target = filepath.Join(dir, "missing")
				}
				if tc.kind == "loop" {
					target = entered
				}
				if tc.kind == "workspace-middle" {
					ws := seedLibraryWorkspace(t, f.api, "T8 middle link")
					workspaceDir := workDir(f.api, ws)
					require.NoError(t, os.MkdirAll(workspaceDir, 0o755), "fixture: materialize the workspace that owns the middle link")
					middle := filepath.Join(workspaceDir, "middle")
					require.NoError(t, os.Symlink(target, middle))
					target = middle
					at = middle
				}
				require.NoError(t, os.Symlink(target, entered))
				if tc.kind == "link-world-write" {
					require.NoError(t, os.Chmod(dir, 0o777))
				}
			case "empty":
				entered = ""
			}
			if tc.check != "" {
				reasons := map[string]string{"not_absolute": "it must be an absolute path", "not_found": "nothing exists at that path (or a link in it is broken)", "not_regular_file": "it is not a regular file", "not_executable": "it is not executable for the Omnipus user", "agent_writable_location": "it is inside a folder agents can write to"}
				f.expectRejected(t, t8SettingsBody("codex", entered, ""), "codex", "path", "cli_path_invalid", tc.check, at, "The program path for Codex was not saved: "+reasons[tc.check]+".")
			} else {
				w := f.call(t, http.MethodPut, t8SettingsRoute, t8SettingsBody("codex", entered, "--flag ok"), "person")
				require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
				assert.Equal(t, entered, t8SettingsRow(t, t8SettingsRows(t, f.view(t)), "codex")["path"])
			}
		})
	}
	t.Run("D6.17 path character length", func(t *testing.T) {
		f := t8SettingsAPI(t)
		for _, n := range []int{4096, 4097} {
			value := "/" + strings.Repeat("x", n-1)
			before := f.disk(t)
			w := f.call(t, http.MethodPut, t8SettingsRoute, t8SettingsBody("codex", value, ""), "person")
			require.Equal(t, http.StatusBadRequest, w.Code, "%s", w.Body.String())
			var out map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
			if n == 4096 {
				assert.Equal(t, "cli_path_invalid", out["code"])
			} else {
				assert.NotEqual(t, "cli_path_invalid", out["code"], "4097 is rejected by the schema before the filesystem check")
			}
			assert.Equal(t, before, f.disk(t))
		}
	})
	t.Run("D6.18 unknown CLI key", func(t *testing.T) {
		f := t8SettingsAPI(t)
		before := f.disk(t)
		w := f.call(t, http.MethodPut, t8SettingsRoute, t8SettingsBody("unknown-cli", "", ""), "person")
		assert.Equal(t, http.StatusBadRequest, w.Code, "%s", w.Body.String())
		assert.Equal(t, before, f.disk(t))
	})
	t.Run("D6.20 detection key mapping and 9.1 link versus resolved target", func(t *testing.T) {
		f := t8SettingsAPI(t)
		dir := t.TempDir()
		target := t8Program(t, dir, "versioned", 0o755)
		link := filepath.Join(dir, "claude")
		require.NoError(t, os.Symlink(target, link))
		t.Setenv("PATH", dir)
		rows := t8SettingsRows(t, f.view(t))
		assert.NotContains(t, rows, "claude")
		det, ok := t8SettingsRow(t, rows, "claude-code")["detected"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, link, det["link_path"])
		resolved, err := filepath.EvalSymlinks(target)
		require.NoError(t, err)
		assert.Equal(t, resolved, det["path"])
	})
}

func t8AuditScalars(v any) []string {
	var out []string
	switch x := v.(type) {
	case string:
		out = append(out, x)
	case map[string]any:
		for _, child := range x {
			out = append(out, t8AuditScalars(child)...)
		}
	case []any:
		for _, child := range x {
			out = append(out, t8AuditScalars(child)...)
		}
	}
	return out
}
