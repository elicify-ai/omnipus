// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build linux

package components_test

// T-129 (office-preview spec §3.8 BR-2.7; FR-066; B-88; founder Q100 A) —
// the restart hint after a successful registration on confined Linux.
//
// No new production symbol is needed: the hint is printed by the existing
// `omnipus office register` / `omnipus pdf-renderer register` commands, so
// unlike T-122…T-128 this file compiles today and its RED is a FAILING
// ASSERTION on the pre-change code (the second stdout line is missing).
//
// Oracles are the spec's exact texts:
//
//	line 1 (existing): Registered <kind> <version> from <root>
//	line 2 (BR-2.7):   Stop and start Omnipus (for example with your service manager) to use this component; the in-app restart is not enough in the confined sandbox, which lets the running gateway use only components installed before it started.
//
// Line 2 follows a successful registration when the configuration's
// `sandbox.filesystem_model` is confined; the open model, a failed
// registration print it never; the exit code is unchanged (0); the command
// restarts nothing.
//
// The "configuration cannot be read" case is settled by the spec amendment
// (02f1aeca3, BR-2.7/B-88/T-129): registration is refused loudly before any
// hint, because configuredWriteRoots then reports a write root that covers
// everything and the AD-4 writer exclusion fails closed — a non-zero exit
// status, an error naming the unreadable configuration, no Registered line
// and no restart line. TestT129_UnreadableConfigRegistrationIsRefusedLoudly
// pins exactly that.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/app/internal/components"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

const t129Hint = "Stop and start Omnipus (for example with your service manager) to use this component; the in-app restart is not enough in the confined sandbox, which lets the running gateway use only components installed before it started."

// t129Home points OMNIPUS_HOME at a fresh test home and writes the named
// configuration: "confined"/"open" write a valid current-version config.json
// with that sandbox.filesystem_model; "corrupt" writes an unparseable one
// (the configuration cannot be read).
func t129Home(t *testing.T, model string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_CONFIG", filepath.Join(home, "config.json"))
	var raw []byte
	switch model {
	case "corrupt":
		raw = []byte("{ this is not json")
	default:
		raw, _ = json.Marshal(map[string]any{
			"version": 1,
			"sandbox": map[string]any{"filesystem_model": model},
		})
	}
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), raw, 0o600))
	return home
}

// t129OwnFolder stages the default program path's own folder (§3.9 fixture
// move, sanctioned; team-lead 2026-11-21 — this file is not on the spec's
// candidate list but fails the same way): the register command runs the
// live Linux own-folder check on the running program, so the fixtures it
// must ACCEPT live inside dir(resolved test binary)/../share/omnipus.
// Assertions are untouched; only the fixture roots relocate.
func t129OwnFolder(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	resolved, err := filepath.EvalSymlinks(self)
	require.NoError(t, err)
	own := filepath.Clean(filepath.Join(filepath.Dir(resolved), "..", "share", "omnipus"))
	require.NoError(t, os.MkdirAll(own, 0o755))
	t.Cleanup(func() { _ = os.RemoveAll(own) })
	return own
}

// t129RealPath returns the fixture path as the command records it
// (register.go::realDir resolves symlinks before printing "from <root>").
func t129RealPath(t *testing.T, p string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(p)
	require.NoError(t, err)
	return resolved
}

// t129OfficeFixture builds the Linux LibreOffice layout registration accepts
// (program/soffice, program/soffice.bin, program/versionrc with a supported
// 26.8.x product version — the same shape component's own layout tests use),
// outside the data folder, and returns (folder, expectedVersion).
func t129OfficeFixture(t *testing.T) (string, string) {
	t.Helper()
	root := filepath.Join(t129OwnFolder(t), "t129-office")
	program := filepath.Join(root, "program")
	require.NoError(t, os.MkdirAll(program, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(program, "soffice"), []byte("t129 fixture program"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(program, "soffice.bin"), []byte("t129 fixture program"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(program, "versionrc"), []byte("ProductVersion=26.8.1.1\n"), 0o644))
	return root, "26.8.1.1"
}

// t129RendererFixture builds an unpacked renderer release folder: a packaged
// manifest plus the fingerprinted program, per AD-4. The digest formula is
// the documented one ("sorted \"<path> <sha256>\\n\" lines",
// component.go::FileListDigest), written out by hand so the fixture never
// calls the code under test for its oracle.
func t129RendererFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t129OwnFolder(t), "t129-renderer")
	require.NoError(t, os.MkdirAll(root, 0o755))
	program := filepath.Join(root, "omnipus-pdfium")
	require.NoError(t, os.WriteFile(program, []byte("t129 fixture renderer"), 0o755))
	raw, err := os.ReadFile(program)
	require.NoError(t, err)
	sum := sha256.Sum256(raw)
	files := map[string]string{"omnipus-pdfium": hex.EncodeToString(sum[:])}
	listSum := sha256.Sum256([]byte("omnipus-pdfium " + files["omnipus-pdfium"] + "\n"))
	manifest := map[string]any{
		"schema":           1,
		"component":        "pdf-renderer",
		"version":          "129.0+t129",
		"build_id":         "t129-build",
		"protocol_version": 1,
		"platform":         runtime.GOOS + "-" + runtime.GOARCH,
		"executable":       "omnipus-pdfium",
		"files":            files,
		"file_list_sha256": hex.EncodeToString(listSum[:]),
		"trust":            "packaged",
	}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "manifest.json"), data, 0o600))
	return root
}

// t129RunRegister runs one register command and returns its stdout, stderr
// and Execute error.
func t129RunRegister(t *testing.T, command string, folder string) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	var cmd *cobra.Command
	switch command {
	case "office":
		cmd = components.NewOfficeCommand()
	case "pdf-renderer":
		cmd = components.NewPDFRendererCommand()
	default:
		t.Fatalf("t129: unknown command %q", command)
	}
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"register", folder})
	execErr := cmd.Execute()
	return out.String(), errOut.String(), execErr
}

func TestT129_OfficeRegisterPrintsRestartHintWhenConfined(t *testing.T) {
	home := t129Home(t, "confined")
	folder, version := t129OfficeFixture(t)
	root := t129RealPath(t, folder)
	out, _, execErr := t129RunRegister(t, "office", folder)
	require.NoError(t, execErr, "T-129: a successful registration keeps exit status 0")

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2, "T-129: exactly the Registered line and the BR-2.7 hint; got %q", out)
	require.Equal(t, "Registered office "+version+" from "+root, lines[0], "T-129: the existing Registered line is unchanged")
	require.Equal(t, t129Hint, lines[1], "T-129: the BR-2.7 hint follows, word for word")

	// The registration landed in the protected data directory.
	require.FileExists(t, filepath.Join(home, "system", "components", "office", "manifest.json"))
}

func TestT129_RendererRegisterPrintsRestartHintWhenConfined(t *testing.T) {
	t129Home(t, "confined")
	folder := t129RendererFixture(t)
	root := t129RealPath(t, folder)
	out, _, execErr := t129RunRegister(t, "pdf-renderer", folder)
	require.NoError(t, execErr, "T-129: a successful registration keeps exit status 0")

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2, "T-129: exactly the Registered line and the BR-2.7 hint; got %q", out)
	require.True(t, strings.HasSuffix(lines[0], " from "+root),
		"T-129: the existing Registered line is unchanged; got %q", lines[0])
	require.Equal(t, t129Hint, lines[1], "T-129: the BR-2.7 hint follows, word for word")
}

// TestT129_OpenModelPrintsOnlyTheRegisteredLine: under the open model nothing
// extra is printed, exit status unchanged.
func TestT129_OpenModelPrintsOnlyTheRegisteredLine(t *testing.T) {
	t129Home(t, "open")
	folder, version := t129OfficeFixture(t)
	root := t129RealPath(t, folder)
	out, _, execErr := t129RunRegister(t, "office", folder)
	require.NoError(t, execErr)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 1, "T-129: under the open model nothing extra is printed; got %q", out)
	require.Equal(t, "Registered office "+version+" from "+root, lines[0])
}

// TestT129_FailedRegistrationPrintsNeitherLine: a folder that is no
// LibreOffice install is refused; no Registered line, no hint, the error is
// returned.
func TestT129_FailedRegistrationPrintsNeitherLine(t *testing.T) {
	t129Home(t, "confined")
	notOffice := t.TempDir()
	out, errOut, execErr := t129RunRegister(t, "office", notOffice)
	require.Error(t, execErr, "T-129: a failed registration returns its error")
	require.NotContains(t, out, "Registered ", "T-129: no Registered line after a failed registration")
	require.NotContains(t, out, t129Hint, "T-129: no hint after a failed registration")
	require.NotEmpty(t, errOut, "T-129: the refusal is reported on stderr")
}

// TestT129_UnreadableConfigRegistrationIsRefusedLoudly pins the compound
// behavior reachable for "the configuration cannot be read": AD-4's writer
// exclusion fails closed (configuredWriteRoots returns the unknown-authority
// root), so the registration is refused and neither line leaks. See this
// file's header note: BR-2.7's "the line is printed as well" sentence cannot
// co-occur with a successful registration while that refusal stands.
func TestT129_UnreadableConfigRegistrationIsRefusedLoudly(t *testing.T) {
	t129Home(t, "corrupt")
	folder, _ := t129OfficeFixture(t)
	out, errOut, execErr := t129RunRegister(t, "office", folder)
	require.Error(t, execErr, "T-129: an unreadable configuration refuses the registration (AD-4 fails closed)")
	require.NotContains(t, out, "Registered ", "T-129: no Registered line when the registration is refused")
	require.NotContains(t, out, t129Hint, "T-129: the hint never leaks after a failed registration")
	require.NotEmpty(t, errOut, "T-129: the refusal names the unreadable configuration on stderr")
}
