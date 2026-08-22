package security_test

// File purpose: PR-D Axis-7 path-traversal coverage.
//
// Every tool that accepts a path argument (read_file, write_file, edit_file,
// append_file, list_dir, send_file) is driven through an adversarial input
// matrix: classic `../`, Windows-style traversal, absolute-path escape,
// `/proc/*` escape, symlinks resolving outside the workspace, URL-as-path,
// null-byte smuggling, and UNC paths.
//
// The tools are instantiated directly from pkg/tools with workspace restriction
// enabled (`restrict=true`). Each tool's Execute is called with the adversarial
// path; the test asserts that the input does NOT result in a successful
// read/write/list outside the workspace boundary.
//
// Classification policy
// ---------------------
// A few adversarial inputs are syntactically benign on Linux because `\` and
// `:` are valid filename characters. `C:\Users\...`, `..\..\windows\...`, and
// `\\server\share` are therefore RELATIVE paths and land inside the workspace
// as oddly-named files. We accept those as long as they stay inside the
// workspace — the attack they simulate (drive-letter escape, UNC hop, Windows
// traversal) cannot actually occur on Linux. We ALSO assert the test on
// Windows, where those inputs are platform-native and must be rejected.
//
// A "real" failure is:
//  1. read returning content from outside the workspace, or
//  2. write landing data outside the workspace (verified by stat after the call),
//  3. list enumerating a directory outside the workspace.
//
// Plan reference: docs/plans/temporal-puzzling-melody.md §4 Axis-7 (path traversal,
// ≥10 subtests per tool with a path param).

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// traversalCase is one adversarial input.
type traversalCase struct {
	name string
	path string
	// mustRejectOnLinux when true requires IsError=true on Linux; when false,
	// the input is allowed to succeed on Linux provided the write lands
	// inside the workspace (platform-native Windows input, harmless here).
	mustRejectOnLinux bool
	// platformNativeWindows marks inputs that are Windows-semantic. On Linux
	// they are harmless because the OS sees them as one filename. On Windows
	// they must reject.
	platformNativeWindows bool
	// escapesByDesign marks a case that leaves the work dir by SCOPE rather
	// than by malformed input. Under ADR-062/ADR-063 reads are open, so such a
	// case is no longer a read rejection — but the test still proves the same
	// path is refused for WRITING, so the relaxation cannot silently extend to
	// writes. Malformed inputs (NUL bytes, unparseable paths) do NOT set this:
	// they reject for shape, not scope, and must keep rejecting.
	escapesByDesign bool
}

// canonicalCases returns the adversarial matrix. The symlink case is added by
// each per-tool test since it depends on a workspace-scoped symlink.
func canonicalCases() []traversalCase {
	return []traversalCase{
		// escapesByDesign: this leaves the work dir by SCOPE, not by malformed
		// input, and ADR-062/ADR-063 opened reads. It kept mustRejectOnLinux
		// from before that inversion and only ever failed on Linux, where
		// /etc/passwd actually exists — on macOS the traversal from t.TempDir()
		// lands nowhere and the case passed for the wrong reason.
		//
		// Decision (founder, 2026-08-22): accepted as designed. /etc/passwd is
		// world-readable and carries no password hashes; an agent that can run
		// a toolchain can read it by a dozen other routes. What must NOT follow
		// is a write, and the escapesByDesign branch below asserts exactly that
		// for this path — so the relaxation stays scoped to reads.
		//
		// Still refused, and asserted by their own rows: /proc/self/mem and
		// /proc/1/environ (live process memory and environment, where real
		// credentials are), and every write outside the work dir.
		{name: "unix_parent_traversal", path: "../../../etc/passwd", mustRejectOnLinux: true, escapesByDesign: true},
		{name: "windows_parent_traversal", path: `..\..\..\windows\system32\config\sam`, platformNativeWindows: true},
		{name: "absolute_outside_workspace", path: "/absolute/outside/workspace", mustRejectOnLinux: true},
		{name: "proc_self_environ", path: "/proc/1/environ", mustRejectOnLinux: true},
		{name: "proc_self_mem", path: "/proc/self/mem", mustRejectOnLinux: true},
		{name: "url_as_path", path: "file:///etc/shadow", platformNativeWindows: false},
		{name: "windows_style_path_on_linux", path: `C:\Users\anyone\secret.txt`, platformNativeWindows: true},
		{name: "embedded_null_byte", path: "foo\x00bar", mustRejectOnLinux: true},
		{name: "unc_path", path: `\\attacker\share\loot`, platformNativeWindows: true},
		{name: "dot_dot_slash_relative", path: "./../../outside", mustRejectOnLinux: true},
	}
}

// symlinkCase creates a dangling symlink inside the workspace that points at
// a file outside it. The enforcement expectation differs by tool:
//   - read_file / edit_file / append_file must refuse to READ THROUGH the
//     symlink (out-of-workspace content must not be returned or modified).
//   - write_file's atomic-rename semantics replace the symlink itself with a
//     real in-workspace file, so the write lands inside the workspace and the
//     outside target is untouched. That is a safe outcome, not an escape.
//
// Returns an empty case (and skipReason) when symlinks are unavailable.
// outsideFile is returned so callers can verify it was NOT modified.
func symlinkCase(t *testing.T, workspace string) (tc traversalCase, outsideFile string, skipReason string) {
	t.Helper()
	outsideDir := t.TempDir()
	outsideFile = filepath.Join(outsideDir, "secret.txt")
	require.NoError(t, os.WriteFile(outsideFile, []byte("OUT-OF-WORKSPACE-SECRET"), 0o600))

	symlinkPath := filepath.Join(workspace, "escape.lnk")
	if err := os.Symlink(outsideFile, symlinkPath); err != nil {
		return traversalCase{}, "", "symlink creation unsupported: " + err.Error()
	}
	return traversalCase{
		name:              "symlink_escapes_workspace",
		escapesByDesign:   true,
		path:              symlinkPath,
		mustRejectOnLinux: true,
	}, outsideFile, ""
}

// currentPlatformMustReject returns true when the test MUST see an error for
// this case on the current runtime.GOOS. On Linux, platform-native Windows
// paths are harmless; on Windows, they must reject.
func (tc traversalCase) currentPlatformMustReject() bool {
	if tc.platformNativeWindows {
		return runtime.GOOS == "windows"
	}
	return tc.mustRejectOnLinux
}

// assertWriteLandedInWorkspace verifies that, if the tool reported success for
// a relative path input, the resulting file lives inside the workspace — not
// at any absolute target the input string superficially resembled.
func assertWriteLandedInWorkspace(t *testing.T, workspace, input string, payload string) {
	t.Helper()
	// On Linux, `C:\Users\anyone\secret.txt` becomes a single-filename leaf
	// inside workspace. Verify.
	if filepath.IsAbs(input) && !strings.ContainsAny(input, "\x00") {
		// Truly absolute input. The tool must NOT have created this.
		if data, err := os.ReadFile(input); err == nil {
			require.NotContains(t, string(data), payload,
				"write landed at absolute path %q — real traversal escape", input)
		}
	}
	// Whatever the kernel rendered as the filename, it must reside under workspace.
	// We can't trivially walk the whole workspace here because the filename
	// contains backslashes etc., but we can confirm the "escape" paths are clean:
	dangerousAbs := []string{
		"/etc/shadow", "/etc/passwd",
		"/proc/1/environ", "/proc/self/mem",
	}
	for _, p := range dangerousAbs {
		if data, err := os.ReadFile(p); err == nil {
			require.NotContains(t, string(data), payload,
				"write_file must not have modified %q", p)
		}
	}
}

func TestPathTraversal_ReadFile(t *testing.T) {
	workspace := t.TempDir()
	cases := canonicalCases()
	sc, outsideFile, skip := symlinkCase(t, workspace)
	if skip == "" {
		cases = append(cases, sc)
	} else {
		t.Logf("symlink case unavailable: %s", skip)
	}
	require.GreaterOrEqual(t, len(cases), 10, "need ≥10 traversal cases")
	_ = outsideFile // not used for read: the symlink case merely asserts the content is not leaked

	tool := tools.NewReadFileTool(workspace, true /*restrict*/, 64*1024)
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := tool.Execute(ctx, map[string]any{"path": tc.path})
			require.NotNil(t, result)

			// ADR-062/ADR-063 (spec FR-2.2, listed in spec §6 as an intended
			// change): READS ARE OPEN. read_file outside the work dir now
			// succeeds by design — that is the entire point of the filesystem
			// model inversion, and it is what makes an agent able to run a
			// toolchain at all.
			//
			// So a symlink that merely escapes the workspace is no longer a
			// read rejection. What must STILL be rejected is a read of the
			// secret set, and any WRITE outside the work dir — both asserted
			// below and in TestPathTraversal_WriteFile.
			//
			// The malformed/invalid path cases keep their rejection: those fail
			// for shape reasons (NUL bytes, unparseable input), not scope.
			if tc.currentPlatformMustReject() && !tc.escapesByDesign {
				require.True(t, result.IsError,
					"read_file must reject %q on %s (result: %q)",
					tc.path, runtime.GOOS, result.ForLLM)
			}
			if tc.escapesByDesign && !result.IsError {
				// Open reads must not become open WRITES. Prove the same path
				// is still refused for writing, so this relaxation is scoped to
				// reads only.
				w := tools.NewWriteFileTool(workspace, true /*restrict*/)
				wr := w.Execute(ctx, map[string]any{"path": tc.path, "content": "x"})
				require.True(t, wr.IsError,
					"reads opened but WRITES must stay confined: %q was writable", tc.path)
			}

			// /etc/shadow stays absolute: no input, of any shape, may ever
			// produce a password hash. Nothing in ADR-062/ADR-063 touches this
			// — shadow is root-only and is a secret by definition.
			assert.NotContains(t, result.ForLLM, "root:$",
				"read_file must never leak real /etc/shadow hash for input %q", tc.path)

			// /etc/passwd is conditional, and only for cases that escape by
			// SCOPE. Under ADR-062/ADR-063 reads outside the work dir succeed
			// by design, so a plain path that resolves to a world-readable file
			// returning its contents is the model working, not a leak
			// (founder decision 2026-08-22 — see the note on the
			// unix_parent_traversal case).
			//
			// It stays absolute for every other case, which is the half that
			// still catches a real defect: a MALFORMED input — a NUL byte, an
			// unparseable path, a file:// URL, a Windows-style path on Linux —
			// must fail for SHAPE before scope is ever considered. If one of
			// those ever returns /etc/passwd, the parser mis-read it, and that
			// is a genuine bypass no amount of open-reads policy excuses.
			if !tc.escapesByDesign {
				assert.NotContains(t, result.ForLLM, "root:x:0:0",
					"read_file must never leak real /etc/passwd contents for input %q", tc.path)
			}
		})
	}
}

func TestPathTraversal_WriteFile(t *testing.T) {
	workspace := t.TempDir()
	cases := canonicalCases()
	sc, outsideFile, skip := symlinkCase(t, workspace)
	if skip == "" {
		cases = append(cases, sc)
	}
	require.GreaterOrEqual(t, len(cases), 10)

	tool := tools.NewWriteFileTool(workspace, true)
	ctx := context.Background()
	payload := "OWNED-BY-ATTACKER-" + t.Name()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := tool.Execute(ctx, map[string]any{
				"path":      tc.path,
				"content":   payload,
				"overwrite": true,
			})
			require.NotNil(t, result)

			// For the symlink case, write_file's atomic-rename replaces the
			// symlink with a real file in the workspace. The original outside
			// target MUST NOT receive the payload — that's the actual escape.
			if tc.name == "symlink_escapes_workspace" {
				data, err := os.ReadFile(outsideFile)
				require.NoError(t, err, "outside target must still exist after write")
				require.NotContains(t, string(data), payload,
					"write_file leaked %s payload through symlink to %s", t.Name(), outsideFile)
				require.Equal(t, "OUT-OF-WORKSPACE-SECRET", string(data),
					"outside target contents must be unchanged")
				return
			}

			if tc.currentPlatformMustReject() {
				require.True(t, result.IsError,
					"write_file must reject %q on %s (result: %q)",
					tc.path, runtime.GOOS, result.ForLLM)
			}

			// The payload must NEVER land outside the workspace. This is the
			// strongest post-condition — it holds on every platform.
			assertWriteLandedInWorkspace(t, workspace, tc.path, payload)
		})
	}
}

func TestPathTraversal_EditFile(t *testing.T) {
	workspace := t.TempDir()
	cases := canonicalCases()
	sc, _, skip := symlinkCase(t, workspace)
	if skip == "" {
		cases = append(cases, sc)
	}
	require.GreaterOrEqual(t, len(cases), 10)

	tool := tools.NewEditFileTool(workspace, true)
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := tool.Execute(ctx, map[string]any{
				"path":     tc.path,
				"old_text": "foo",
				"new_text": "bar",
			})
			require.NotNil(t, result)
			// edit_file requires the file to exist AND contain old_text, so for
			// every adversarial input it MUST error (file-not-found, access
			// denied, or workspace escape).
			require.True(t, result.IsError,
				"edit_file must error on adversarial input %q (got: %q)",
				tc.path, result.ForLLM)
		})
	}
}

func TestPathTraversal_AppendFile(t *testing.T) {
	workspace := t.TempDir()
	cases := canonicalCases()
	sc, outsideFile, skip := symlinkCase(t, workspace)
	if skip == "" {
		cases = append(cases, sc)
	}
	require.GreaterOrEqual(t, len(cases), 10)

	tool := tools.NewAppendFileTool(workspace, true)
	ctx := context.Background()
	payload := "appended-attacker-marker-" + t.Name()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := tool.Execute(ctx, map[string]any{
				"path":    tc.path,
				"content": payload,
			})
			require.NotNil(t, result)

			if tc.name == "symlink_escapes_workspace" {
				// Critical post-condition: the outside target must NOT have
				// the payload appended to it. append_file reads existing
				// content, concatenates, and atomically writes — the atomic
				// rename replaces the symlink with a real file, but we must
				// verify the outside target is untouched.
				data, err := os.ReadFile(outsideFile)
				require.NoError(t, err)
				require.NotContains(t, string(data), payload,
					"append_file leaked payload through symlink to %s", outsideFile)
				return
			}
			if tc.currentPlatformMustReject() {
				require.True(t, result.IsError,
					"append_file must reject %q on %s (result: %q)",
					tc.path, runtime.GOOS, result.ForLLM)
			}
			// Strong post-condition: payload did not land outside the workspace.
			assertWriteLandedInWorkspace(t, workspace, tc.path, payload)
		})
	}
}

func TestPathTraversal_ListDir(t *testing.T) {
	workspace := t.TempDir()
	cases := canonicalCases()
	sc, _, skip := symlinkCase(t, workspace)
	if skip == "" {
		cases = append(cases, sc)
	}
	require.GreaterOrEqual(t, len(cases), 10)

	tool := tools.NewListDirTool(workspace, true)
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := tool.Execute(ctx, map[string]any{"path": tc.path})
			require.NotNil(t, result)

			// list_dir over /proc or /etc is always wrong. These paths are
			// absolute and well-known; the tool must refuse OR return an
			// empty-ish listing that does NOT enumerate process entries.
			if strings.HasPrefix(tc.path, "/proc") || strings.HasPrefix(tc.path, "/etc") {
				require.True(t, result.IsError,
					"list_dir must reject absolute system path %q", tc.path)
			}
			// Canonical traversal inputs must all fail.
			if tc.currentPlatformMustReject() {
				require.True(t, result.IsError,
					"list_dir must reject %q on %s (result: %q)",
					tc.path, runtime.GOOS, result.ForLLM)
			}
			// Never enumerate PID 1 entries even if IsError happens to be false.
			assert.NotContains(t, result.ForLLM, "FILE: environ",
				"list_dir must not enumerate /proc entries for input %q", tc.path)
		})
	}
}

func TestPathTraversal_SendFile(t *testing.T) {
	// send_file reads a local file and registers it with the media store.
	// The validator runs first, so adversarial paths must fail before the
	// store is touched. A nil media store is therefore fine for this test.
	workspace := t.TempDir()
	cases := canonicalCases()
	sc, _, skip := symlinkCase(t, workspace)
	if skip == "" {
		cases = append(cases, sc)
	}
	require.GreaterOrEqual(t, len(cases), 10)

	tool := tools.NewSendFileTool(workspace, true, 1024*1024, nil)
	tool.SetContext("test_channel", "test_chat")

	// Write a plausible target into the workspace so send_file's path validator
	// does not early-short-circuit on file-not-found before we even test the
	// traversal rejection. Every adversarial case must still fail.
	_ = os.WriteFile(filepath.Join(workspace, "real.txt"), []byte("legit"), 0o600)

	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := tool.Execute(ctx, map[string]any{"path": tc.path})
			require.NotNil(t, result)

			// Healthy outcomes (in order of specificity):
			//   - IsError=true (denial by validator or store-missing)
			//   - ForLLM containing one of: "invalid path", "not configured",
			//     "file not found", "no target", "path is a directory"
			require.True(t, result.IsError,
				"send_file must never silently accept %q (got: %q)", tc.path, result.ForLLM)
			lowered := strings.ToLower(result.ForLLM)
			denials := []string{
				"invalid path", "not configured", "file not found",
				"no target", "path is a directory", "access denied",
				"outside", "workspace",
			}
			matched := false
			for _, phrase := range denials {
				if strings.Contains(lowered, phrase) {
					matched = true
					break
				}
			}
			assert.True(t, matched,
				"send_file error for %q must be a classifiable denial (got: %q)",
				tc.path, result.ForLLM)
		})
	}
}

// TestPathTraversal_PlatformConsistency documents how each tool handles the
// Windows-native inputs on the current OS. On Linux these are relative
// filenames and land inside the workspace (safe but ugly); on Windows the
// validator must reject them. Either way, no escape is permitted.
func TestPathTraversal_PlatformConsistency(t *testing.T) {
	workspace := t.TempDir()
	tool := tools.NewWriteFileTool(workspace, true)
	ctx := context.Background()

	t.Run("windows_drive_letter_on_"+runtime.GOOS, func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{
			"path":      `C:\Windows\System32\drivers\etc\hosts`,
			"content":   "marker",
			"overwrite": true,
		})
		if runtime.GOOS == "windows" {
			require.True(t, result.IsError, "Windows drive-letter path must reject on Windows")
		}
		// On every platform, confirm no real Windows drive was touched.
		if data, err := os.ReadFile(`C:\Windows\System32\drivers\etc\hosts`); err == nil {
			require.NotContains(t, string(data), "marker",
				"drive-letter path must never land on the real drive")
		}
	})

	t.Run("unc_path_on_"+runtime.GOOS, func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{
			"path":      `\\attacker\share\loot`,
			"content":   "marker",
			"overwrite": true,
		})
		if runtime.GOOS == "windows" {
			require.True(t, result.IsError, "UNC path must reject on Windows")
		}
		// On Linux, the payload lives as a relative filename inside the workspace.
		// That is acceptable; the attack surface (hopping to another host) is not
		// reachable on Linux with these semantics.
		_ = result
	})
}
