package officepreview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/stretchr/testify/require"
)

// AtPackagedExecutable re-executes exactly this named test serially under an
// isolated executable location. Its packaged slots are ALL in a test-owned
// tree, including macOS's ../../../ candidate. It never changes the live
// executable, a shared build folder or an installed component. The waiting
// parent forwards the child's real failure; this is not an environment skip.
func AtPackagedExecutable(t *testing.T) bool {
	t.Helper()
	const marker = "OMNIPUS_TEST_OFFICE_PACKAGED_EXECUTABLE"
	if os.Getenv(marker) == t.Name() {
		return true
	}
	root := t.TempDir()
	exe := filepath.Join(root, "Application", "Contents", "MacOS", "omnipus.test")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	require.NoError(t, os.MkdirAll(filepath.Dir(exe), 0700))
	self, err := os.Executable()
	require.NoError(t, err)
	if err := os.Link(self, exe); err != nil {
		in, openErr := os.Open(self)
		require.NoError(t, openErr)
		defer in.Close()
		out, createErr := os.OpenFile(exe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		require.NoError(t, createErr)
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		require.NoError(t, copyErr)
		require.NoError(t, closeErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.v", "-test.count=1", "-test.run=^"+regexp.QuoteMeta(t.Name())+"$")
	cmd.Env = append(os.Environ(), marker+"="+t.Name())
	data, err := cmd.CombinedOutput()
	t.Logf("isolated executable %s\n%s", exe, data)
	require.NoError(t, err, "the same test at the isolated packaged location must pass; child assertions above are the evidence")
	return false
}

func RegisteredRendererFixture(t *testing.T, home, root string, register bool) {
	t.Helper()
	require.NoError(t, os.MkdirAll(root, 0700))
	realRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	files := map[string]string{}
	exe, lib := "omnipus-pdfium", "libpdfium.so"
	if runtime.GOOS == "darwin" {
		lib = "libpdfium.dylib"
	}
	if runtime.GOOS == "windows" {
		exe, lib = "omnipus-pdfium.exe", "pdfium.dll"
	}
	for _, name := range []string{exe, lib} {
		data := []byte("harmless fixture, never executed: " + name)
		require.NoError(t, os.WriteFile(filepath.Join(root, name), data, 0700))
		sum := sha256.Sum256(data)
		files[name] = hex.EncodeToString(sum[:])
	}
	m := component.Manifest{Schema: 1, Component: component.KindPDFRenderer, Version: "test-fixture", BuildID: "test-fixture-v1", ProtocolVersion: 1, Platform: runtime.GOOS + "-" + runtime.GOARCH, Executable: exe, Files: files, FileListSHA256: component.FileListDigest(files), Trust: component.TrustPackaged}
	dir := root
	if register {
		m.Root, m.Trust = realRoot, component.TrustOperatorRegistered
		dir = component.RegisteredDir(home, component.KindPDFRenderer)
	}
	require.NoError(t, os.MkdirAll(dir, 0700))
	data, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0600))
}

// Expected strings below are literal §3.2 oracles, not calls to the product's
// formatting code. Keeping them separate catches a wrong formatter branch.
func ProgramMountRefusal(name, folder string) string {
	return fmt.Sprintf("that folder cannot be mounted: it is, contains or lies inside the %s program folder (%s), and agents can never get write access there. Mount a folder that does not include it.", name, folder)
}

func ProgramShellRefusal(path, name, folder string) string {
	return fmt.Sprintf("Command blocked by safety guard (preview program folder): %s — agents can never get write access to the %s program folder %s.", path, name, folder)
}

// ConfigEntryRefusal is §3.2's save/reload text for an entry Omnipus CAN
// reduce to a folder. Kept here as a literal so a wrong formatter branch in
// any writer fails against the same independent sentence.
func ConfigEntryRefusal(field, entry, name, folder string) string {
	return fmt.Sprintf("config error: %s entry %q would give agents write access to the %s program folder %s; agents can never get write access there.", field, entry, name, folder)
}

// ConfigUnreducibleRefusal is §3.2's text for an entry Omnipus cannot reduce.
// The hint is generic (^/<folder>(?:/\|$)), not the specific folder.
func ConfigUnreducibleRefusal(field, entry string) string {
	return fmt.Sprintf("config error: %s entry %q: Omnipus cannot tell which folders it covers, so it may include a document preview program folder; write it as ^/<folder>(?:/\\|$).", field, entry)
}

// RegisteredOfficeExecutable registers a verified LibreOffice manifest whose
// program is exePath (the caller's test binary in T-88, so preview.Service
// really launches it through run-limited), fingerprinted exactly like
// TrustedOffice. Returns the resolved root.
func RegisteredOfficeExecutable(t *testing.T, home, root, exePath string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(root, 0700))
	resolvedRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	exeName := filepath.Base(exePath)
	installed := filepath.Join(resolvedRoot, exeName)
	if installed != exePath {
		if err := os.Link(exePath, installed); err != nil {
			in, openErr := os.Open(exePath)
			require.NoError(t, openErr)
			defer in.Close()
			out, createErr := os.OpenFile(installed, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
			require.NoError(t, createErr)
			_, copyErr := io.Copy(out, in)
			closeErr := out.Close()
			require.NoError(t, copyErr)
			require.NoError(t, closeErr)
		}
	}
	data, err := os.ReadFile(installed)
	require.NoError(t, err)
	sum := sha256.Sum256(data)
	files := map[string]string{exeName: hex.EncodeToString(sum[:])}
	manifest := component.Manifest{
		Schema: 1, Component: component.KindOffice, Version: "test-fixture",
		Platform: runtime.GOOS + "-" + runtime.GOARCH, Executable: exeName,
		Files: files, FileListSHA256: component.FileListDigest(files),
		Trust: component.TrustOperatorRegistered, Root: resolvedRoot,
		RegisteredBy: "test-operator", RegisteredAt: "2026-11-06T00:00:00Z",
	}
	dir := component.RegisteredDir(home, component.KindOffice)
	require.NoError(t, os.MkdirAll(dir, 0700))
	raw, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0600))
	return resolvedRoot
}
