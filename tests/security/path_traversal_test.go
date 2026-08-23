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
// A "real" failure is (revised for ADR-062 — see the escapesByDesign note
// above canonicalCases, and issue #635):
//  1. read returning content from the CREDENTIAL DENY-LIST
//     (fspolicy.SecretEntriesAlways). An out-of-workspace read of an ordinary
//     file is NOT a failure — ADR-062 made reads open on purpose. This was
//     item 1 until 2026-08, when it still read "content from outside the
//     workspace"; that wording had been contradicted by shipped, accepted
//     design for ten days and turned CI red on Linux while passing on macOS.
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

	"github.com/elicify-ai/omnipus/pkg/config"
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

// canonicalCases classification, and why the scope-escaping rows carry
// escapesByDesign (issue #635, ADR-062 §7.3)
// ---------------------------------------------------------------------------
// ADR-062 inverted the filesystem model to reads-open: an out-of-work-dir READ
// is accepted design, and protection narrows to the fixed credential deny-list
// in fspolicy.SecretEntriesAlways. Five rows below leave the work dir by scope
// and nothing else, so under that model read_file does not — and must not —
// reject them:
//
//	unix_parent_traversal       ../../../etc/passwd
//	absolute_outside_workspace  /absolute/outside/workspace
//	proc_self_environ           /proc/1/environ
//	proc_self_mem               /proc/self/mem
//	dot_dot_slash_relative      ./../../outside
//
// Only the first of those was OBSERVED failing (CI run 32612801491, Linux:
// read_file returned the real /etc/passwd). The other four kept passing on
// Linux for reasons that have nothing to do with policy — the target does not
// exist, or the runner's uid cannot open it — and on macOS even the first one
// passed by accident, because t.TempDir() sits deep enough under /private/var
// that three levels up is not "/". A rejection that depends on the runner's
// filesystem layout and uid is not evidence of a boundary, so all five are
// reclassified together rather than only the row that happened to go red.
//
// The coverage they used to stand for has NOT been dropped. It is retargeted at
// the paths that ARE denied, per ADR-062 §7.3 ("the same attacks, retargeted at
// a denied credential path, must still fail — and the symlink and hardlink
// variants are the interesting ones against a path-based deny"):
// TestPathTraversal_SecretSetStillDenied. The reads-open contract itself — the
// behaviour that replaced the old rejection — is pinned by
// TestPathTraversal_ReadsOpenOutsideWorkspace.

// canonicalCases returns the adversarial matrix. The symlink case is added by
// each per-tool test since it depends on a workspace-scoped symlink.
func canonicalCases() []traversalCase {
	return []traversalCase{
		{name: "unix_parent_traversal", path: "../../../etc/passwd", mustRejectOnLinux: true, escapesByDesign: true},
		{name: "windows_parent_traversal", path: `..\..\..\windows\system32\config\sam`, platformNativeWindows: true},
		{name: "absolute_outside_workspace", path: "/absolute/outside/workspace", mustRejectOnLinux: true, escapesByDesign: true},
		{name: "proc_self_environ", path: "/proc/1/environ", mustRejectOnLinux: true, escapesByDesign: true},
		{name: "proc_self_mem", path: "/proc/self/mem", mustRejectOnLinux: true, escapesByDesign: true},
		{name: "url_as_path", path: "file:///etc/shadow", platformNativeWindows: false},
		{name: "windows_style_path_on_linux", path: `C:\Users\anyone\secret.txt`, platformNativeWindows: true},
		{name: "embedded_null_byte", path: "foo\x00bar", mustRejectOnLinux: true},
		{name: "unc_path", path: `\\attacker\share\loot`, platformNativeWindows: true},
		{name: "dot_dot_slash_relative", path: "./../../outside", mustRejectOnLinux: true, escapesByDesign: true},
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
	// A canary-populated $OMNIPUS_HOME makes the "must not leak a secret"
	// post-condition below mean something. Every path-taking tool derives its
	// carve-outs from $OMNIPUS_HOME at call time, so without this the matrix
	// would be asserting against whatever ~/.omnipus the runner happens to
	// have — or none at all.
	_, canary := secretCanaryHome(t)

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

			// The post-condition that survived the ADR-062 inversion.
			//
			// This used to assert on /etc/passwd and /etc/shadow content
			// ("root:x:0:0" / "root:$"). Under reads-open those are ordinary
			// readable files — the assertion contradicted the shipped model and
			// would have gone red on Linux for the same reason the rejection
			// above did. What is STILL absolute is the credential deny-list: no
			// adversarial input shape, on any platform, may return content from
			// the secret set. TestPathTraversal_SecretSetStillDenied aims the
			// same attacks directly at those paths; this is the matrix-wide
			// backstop for shapes nobody thought to aim.
			assert.NotContains(t, result.ForLLM, canary,
				"read_file must never return secret-set content for input %q", tc.path)
		})
	}
}

// resolvedTempDir returns t.TempDir() with symlinks resolved.
//
// Mixing a resolved path with an unresolved one is not cosmetic here, it
// silently changes what a test measures. On macOS t.TempDir() sits under /var,
// a symlink to /private/var. A filepath.Rel computed between an unresolved
// work dir and a resolved target yields a `../` chain with the wrong number of
// hops, and the resolver — which resolves the work dir before joining —
// lands it on a path like /private/private/var/... That path does not exist,
// so the read is refused as file-not-found and a deny-list assertion written
// as "IsError" would have gone GREEN without the deny-list ever being
// consulted. Measured, not hypothesised: the first run of
// TestPathTraversal_SecretSetStillDenied did exactly this, and only the
// "refused AS A CARVE-OUT" half of the oracle caught it.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return resolved
}

// secretCanaryHome provisions a temporary $OMNIPUS_HOME containing a real file
// for every entry of the ADR-062 §4.0 / fspolicy.SecretEntriesAlways secret
// set, each holding the returned canary marker, and points
// config.OmnipusHomeDir() at it for the duration of the test.
//
// It exists because the carve-out list is computed from $OMNIPUS_HOME on every
// call (tools.ResolveTurnFSPolicy -> fspolicy.EffectiveFSPolicy ->
// buildCarveOuts -> fspolicy.SecretPaths). Without an explicit home, a
// deny-list assertion in this package would be measuring the runner's own
// ~/.omnipus — present on a developer laptop, absent on CI — which is the
// class of accidental, environment-dependent pass that made the stale
// assertion in this file survive for ten days (issue #635).
//
// The returned home is realpath-resolved: on macOS t.TempDir() lives under
// /var, itself a symlink to /private/var, and every path the resolver reports
// back is resolved.
func secretCanaryHome(t *testing.T) (home, canary string) {
	t.Helper()
	raw := t.TempDir()
	canary = "OMNIPUS-SECRET-CANARY-DO-NOT-LEAK"

	writeSecret := func(rel string) {
		t.Helper()
		p := filepath.Join(raw, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
		require.NoError(t, os.WriteFile(p, []byte(canary+" "+rel+"\n"), 0o600))
	}
	// File-shaped entries, and one file inside each directory-shaped entry —
	// a directory carve-out is only meaningful if something is under it.
	for _, rel := range []string{
		"master.key",
		"credentials.json",
		"config.json",
		"cli.token",
		"auth.json",
		"config.json.bak-20260101",
		filepath.Join("entities", "agents", "mia.json"),
		filepath.Join("system", "audit.jsonl"),
		filepath.Join("backups", "full.tar"),
	} {
		writeSecret(rel)
	}

	t.Setenv(config.EnvHome, raw)

	resolved, err := filepath.EvalSymlinks(raw)
	require.NoError(t, err, "resolve the canary $OMNIPUS_HOME")
	return resolved, canary
}

// TestPathTraversal_ReadsOpenOutsideWorkspace pins the behaviour that REPLACED
// the rejection TestPathTraversal_ReadFile/unix_parent_traversal used to
// assert (ADR-062 §1: "Filesystem reads and execute default to ALLOW").
//
// Without this, reclassifying those rows as escapesByDesign would leave the
// reads-open half of the model with no positive coverage at all: the model
// could be silently reverted to confined reads and every remaining assertion
// in this file would still pass.
//
// The traversal target is a file this test creates in a sibling temp
// directory, reached by a genuine relative `../` path computed with
// filepath.Rel — deliberately NOT the literal "../../../etc/passwd" the stale
// row used. That literal only reaches a real file when t.TempDir() happens to
// be exactly three levels below "/", which is true on Linux and false on
// macOS; that layout dependency is precisely what let the stale assertion pass
// on one platform and fail on the other. This target exists on every platform,
// so the assertion means the same thing everywhere.
func TestPathTraversal_ReadsOpenOutsideWorkspace(t *testing.T) {
	secretCanaryHome(t)

	workspace := resolvedTempDir(t)
	outsideDir := resolvedTempDir(t)
	const content = "READS-ARE-OPEN-BY-DESIGN-ADR-062"
	outsideFile := filepath.Join(outsideDir, "ordinary.txt")
	require.NoError(t, os.WriteFile(outsideFile, []byte(content), 0o600))

	rel, err := filepath.Rel(workspace, outsideFile)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(rel, ".."),
		"the probe must be a real parent traversal, got %q", rel)

	ctx := context.Background()
	result := tools.NewReadFileTool(workspace, true /*restrict*/, 64*1024).
		Execute(ctx, map[string]any{"path": rel})
	require.NotNil(t, result)
	require.False(t, result.IsError,
		"ADR-062: a read that leaves the work dir by scope must SUCCEED (path %q, result %q)",
		rel, result.ForLLM)
	require.Contains(t, result.ForLLM, content,
		"the open read must actually return the file's contents")

	// The same relaxation must not have extended to writes (ADR-062 §5:
	// "Writes — workspace, /tmp, $TMPDIR, operator allowed_paths. Nothing
	// else."). Asserted here as well as in the matrix because this is the one
	// place the read is proven to have SUCCEEDED, so the write refusal is
	// being compared against a path that is demonstrably reachable.
	wr := tools.NewWriteFileTool(workspace, true /*restrict*/).
		Execute(ctx, map[string]any{"path": rel, "content": "x", "overwrite": true})
	require.True(t, wr.IsError,
		"reads opened but writes must stay confined: %q was writable", rel)
	after, err := os.ReadFile(outsideFile)
	require.NoError(t, err)
	require.Equal(t, content, string(after), "the outside file must be unmodified")
}

// TestPathTraversal_SecretSetStillDenied is the retarget ADR-062 §7.3
// prescribes for every path-traversal assertion the reads-open inversion
// invalidated: "the same attacks, retargeted at a denied credential path, must
// still fail — and the symlink and hardlink variants are the interesting ones
// against a path-based deny." §7.3 tabulates ten such tests;
// TestPathTraversal_ReadFile/unix_parent_traversal is an eleventh the ADR
// missed (issue #635), and this is its replacement.
//
// Deleting the stale rows without this would remove the only evidence the
// boundary was ever tested (ADR-062 §7.3, citing ADR-052 AC-6 checklist item
// 2).
//
// # What this measures, and on which platform
//
// This is the APP layer, not the kernel. read_file/write_file run inside the
// gateway process, and the deny is fspolicy.IsCarveOut, reached from
// tools.ResolvePath — portable Go with no Seatbelt or Landlock involvement. So
// a pass here is a pass for the same code Linux runs; the ADR-062 §7.1
// "[INFERRED] — unverified until run on a Linux host" caveat attaches to the
// KERNEL half (§4.2's Landlock sibling-granting walk), which no assertion in
// this file touches. The one genuinely platform-gated row is the hard-link
// case: fspolicy's link-count gate is unix-only by construction
// (linkcount_other.go), so it is skipped on Windows with that reason stated
// rather than silently asserted.
//
// # Oracle
//
// Every case asserts three things, because "IsError" alone is satisfied by a
// file that simply is not there — the accident that made the stale rows look
// green:
//
//  1. the target is readable by THIS process (proven with os.ReadFile first),
//     so a refusal can only have come from policy;
//  2. the refusal is classified as a carve-out denial (tools.ErrCarveOut's
//     message), not file-not-found and not merely outside-scope;
//  3. the canary content does not appear in the result.
//
// secretRetargetCase is one retargeted attack: the adversarial path shape the
// tool is given, and the real secret it resolves to. target is separate from
// path on purpose — the assertions read and re-read the SECRET (to prove it
// was reachable before the call and unchanged after), while the tool only ever
// sees the alias.
type secretRetargetCase struct {
	name   string
	path   string
	target string
	skip   string
}

func TestPathTraversal_SecretSetStillDenied(t *testing.T) {
	home, canary := secretCanaryHome(t)
	workspace := resolvedTempDir(t)

	masterKey := filepath.Join(home, "master.key")
	credentials := filepath.Join(home, "credentials.json")
	cliToken := filepath.Join(home, "cli.token")
	configBackup := filepath.Join(home, "config.json.bak-20260101")
	backupArchive := filepath.Join(home, "backups", "full.tar")

	// Relative traversal from the work dir up to the secret — the exact shape
	// of the retired "../../../etc/passwd" row, aimed at a path that is denied.
	relTraversal := func(target string) string {
		t.Helper()
		rel, err := filepath.Rel(workspace, target)
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(rel, ".."),
			"the probe must be a real parent traversal, got %q", rel)
		return rel
	}

	// Symlink planted inside the work dir. Against a path-based deny this is
	// the interesting variant (ADR-062 §7.3): the tool sees an in-workspace
	// path and only the realpath reveals the target.
	symlinkTo := func(name, target string) string {
		t.Helper()
		p := filepath.Join(workspace, name)
		if err := os.Symlink(target, p); err != nil {
			t.Skipf("symlink creation unsupported on %s: %v", runtime.GOOS, err)
		}
		return p
	}

	cases := []secretRetargetCase{
		{name: "relative_traversal_to_master_key", path: relTraversal(masterKey), target: masterKey},
		{name: "relative_traversal_to_credentials", path: relTraversal(credentials), target: credentials},
		{name: "absolute_master_key", path: masterKey, target: masterKey},
		{name: "absolute_cli_token", path: cliToken, target: cliToken},
		{name: "absolute_config_backup", path: configBackup, target: configBackup},
		{name: "absolute_backup_archive", path: backupArchive, target: backupArchive},
		{
			name:   "symlink_in_workspace_to_master_key",
			path:   symlinkTo("key.lnk", masterKey),
			target: masterKey,
		},
		{
			// Directory symlink: the leaf is spelled inside the work dir and
			// only an ancestor is the alias, so the deny cannot be decided
			// from the leaf's own name.
			name:   "dir_symlink_in_workspace_then_secret",
			path:   filepath.Join(symlinkTo("home.lnk", home), "credentials.json"),
			target: credentials,
		},
		{
			// Traversal THROUGH a workspace-local symlink: relative input,
			// symlinked ancestor, secret leaf.
			name:   "traversal_through_dir_symlink_to_backup_archive",
			path:   filepath.Join("home.lnk", "backups", "full.tar"),
			target: backupArchive,
		},
	}

	// Hard link to a FILE-shaped secret. Denied by device+inode identity
	// (fspolicy/pathidentity.go), not by the path — the alias has an entirely
	// in-workspace path. Unix only: the link-count gate the app layer uses
	// cannot be evaluated on Windows (fspolicy/linkcount_other.go), a residual
	// ADR-062-era code documents rather than closes.
	if runtime.GOOS != "windows" {
		hardlink := filepath.Join(workspace, "key.hardlink")
		if err := os.Link(masterKey, hardlink); err == nil {
			cases = append(cases, secretRetargetCase{
				name: "hardlink_in_workspace_to_master_key", path: hardlink, target: masterKey,
			})
		} else {
			t.Logf("hard-link case unavailable: %v", err)
		}

		aliasArchive := filepath.Join(workspace, "archive.hardlink")
		if err := os.Link(backupArchive, aliasArchive); err == nil {
			cases = append(cases, secretRetargetCase{
				name: "hardlink_in_workspace_into_backups_dir", path: aliasArchive, target: backupArchive,
			})
		} else {
			t.Logf("directory-shaped hard-link case unavailable: %v", err)
		}
	}

	require.GreaterOrEqual(t, len(cases), 9, "the retarget must cover at least the shapes the retired rows did")

	ctx := context.Background()
	readTool := tools.NewReadFileTool(workspace, true /*restrict*/, 64*1024)
	writeTool := tools.NewWriteFileTool(workspace, true /*restrict*/)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skip != "" {
				t.Skip(tc.skip)
			}

			// (1) Prove the target is readable by this process. Without this,
			// a green below is indistinguishable from "the file was not
			// there" — the accident that kept the stale rows passing.
			raw, err := os.ReadFile(tc.target)
			require.NoError(t, err, "the secret must be readable by the test process, or the denial proves nothing")
			require.Contains(t, string(raw), canary)

			result := readTool.Execute(ctx, map[string]any{"path": tc.path})
			require.NotNil(t, result)

			// (2) Refused, and refused AS A CARVE-OUT.
			require.True(t, result.IsError,
				"read_file must refuse the secret set under reads-open: %q (result: %q)", tc.path, result.ForLLM)
			require.Contains(t, result.ForLLM, "carve-out",
				"the refusal must be a carve-out denial (tools.ErrCarveOut), not file-not-found or outside-scope: %q", result.ForLLM)

			// (3) No content leaked.
			require.NotContains(t, result.ForLLM, canary,
				"read_file leaked secret-set content for %q", tc.path)

			// Writes to the same path must be refused too, and the secret must
			// be byte-identical afterwards — ADR-062 §4.1's rename/truncate
			// finding is why a read-only deny is not enough.
			wr := writeTool.Execute(ctx, map[string]any{"path": tc.path, "content": "CLOBBERED", "overwrite": true})
			require.NotNil(t, wr)
			require.True(t, wr.IsError,
				"write_file must refuse the secret set: %q (result: %q)", tc.path, wr.ForLLM)
			after, err := os.ReadFile(tc.target)
			require.NoError(t, err, "the secret must still exist after the refused write")
			require.Equal(t, string(raw), string(after), "the secret must be byte-identical after the refused write")
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
