//go:build goolm && stdjson && linux

// RED regression for a non-terminal character device on `omnipus import`
// (epic #1094 S1a, T-6, audit finding F1).
//
// Oracle: export-import-s1-spec.md revision 7, BDD "Declined or
// non-interactive without --yes" — when stdin is not a terminal and --yes
// is absent, the import prints "Import cancelled", exits 1, and imports
// nothing. A strings.Reader or a pipe never reaches the *os.File character
// device check, and /dev/null returns EOF, which also cancels. /dev/zero is
// a character device, is not a terminal, and never returns a newline, so a
// guard that treats every character device as a terminal reads without bound.

package pkgcmd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// nonTerminalHangBound is a hard stop for a command that keeps reading
// stdin. It is not a timing tolerance: a correct cancel returns as soon as
// the preview is printed. No sleep is used to wait for success.
const nonTerminalHangBound = 2 * time.Second

// TestPkgCmd_NonTerminalCharDeviceCancelsWithoutYes fails while confirm
// treats every character device as a terminal. The parent kills the child
// at nonTerminalHangBound so a buggy read of /dev/zero cannot hang the
// package or grow without a limit.
func TestPkgCmd_NonTerminalCharDeviceCancelsWithoutYes(t *testing.T) {
	if os.Getenv("OMNIPUS_T6_STDIN_CHILD") == "1" {
		nonTerminalCharDeviceChild(t)
		return
	}
	nonTerminalCharDeviceParent(t)
}

func nonTerminalCharDeviceParent(t *testing.T) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestPkgCmd_NonTerminalCharDeviceCancelsWithoutYes$", "-test.v", "-test.count=1")
	cmd.Env = append(os.Environ(), "OMNIPUS_T6_STDIN_CHILD=1")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	require.NoError(t, cmd.Start())

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("non-terminal stdin without --yes must cancel with exit 1; child exit %v\n%s", err, out.String())
		}
	case <-time.After(nonTerminalHangBound):
		_ = cmd.Process.Kill()
		<-done
		t.Fatalf("hung: /dev/zero is not a terminal and --yes is absent, but import did not cancel within %s and kept reading stdin\n%s",
			nonTerminalHangBound, out.String())
	}
}

func nonTerminalCharDeviceChild(t *testing.T) {
	dev, err := os.Open("/dev/zero")
	require.NoError(t, err)
	t.Cleanup(func() { _ = dev.Close() })
	info, err := dev.Stat()
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeCharDevice, "instrument: /dev/zero must be a character device")
	_, termErr := unix.IoctlGetTermios(int(dev.Fd()), unix.TCGETS)
	require.Error(t, termErr, "instrument: /dev/zero must not be a terminal")

	dir := t.TempDir()
	file := defaultImportSetup(t, dir)
	f := newFakeGateway(t).
		withWorkspaces(map[string]any{"id": fixtureResearchWorkspaceID, "name": "Research", "status": "active"}).
		servePreview(previewBody(t, nil))

	stdout, stderr, runErr := runCommandFile(t, importCmd(f, Options{Token: "tok"}), dev,
		"import", file, "--workspace", "Research")

	require.Equal(t, 1, exitCodeOf(t, runErr), "BDD: non-terminal stdin without --yes exits 1; stderr: %s", stderr)
	assert.Contains(t, stdout, "Import cancelled", "BDD: the owner is told the import was cancelled")
	assert.Equal(t, 0, f.counts().applies, "nothing is imported")
}

// TestPkgCmd_NonTerminalAffirmativeCancelsWithoutYes is the case /dev/zero
// does not prove. confirm must cancel when stdin is a non-terminal file whose
// contents are an affirmative answer. A guard that only rejects an endless
// device, then reads a normal file, would import.
func TestPkgCmd_NonTerminalAffirmativeCancelsWithoutYes(t *testing.T) {
	dev, err := os.CreateTemp(t.TempDir(), "stdin-yes-")
	require.NoError(t, err)
	_, err = dev.WriteString("y\n")
	require.NoError(t, err)
	_, err = dev.Seek(0, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dev.Close() })
	info, err := dev.Stat()
	require.NoError(t, err)
	require.Zero(t, info.Mode()&os.ModeCharDevice, "instrument: the affirmative answer is a regular file")
	_, termErr := unix.IoctlGetTermios(int(dev.Fd()), unix.TCGETS)
	require.Error(t, termErr, "instrument: the file must not be a terminal")

	dir := t.TempDir()
	file := defaultImportSetup(t, dir)
	f := newFakeGateway(t).
		withWorkspaces(map[string]any{"id": fixtureResearchWorkspaceID, "name": "Research", "status": "active"}).
		servePreview(previewBody(t, nil))

	stdout, stderr, runErr := runCommandFile(t, importCmd(f, Options{Token: "tok"}), dev,
		"import", file, "--workspace", "Research")

	require.Equal(t, 1, exitCodeOf(t, runErr), "BDD: non-terminal stdin without --yes exits 1 even when it contains y; stderr: %s", stderr)
	assert.Contains(t, stdout, "Import cancelled", "BDD: the owner is told the import was cancelled")
	assert.Equal(t, 0, f.counts().applies, "an affirmative answer on a non-terminal must not import")
}

// runCommandFile is runCommand with a real *os.File for stdin, so confirm's
// terminal check sees the file mode instead of an in-memory reader.
func runCommandFile(t *testing.T, cmd *cobra.Command, in *os.File, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetIn(in)
	cmd.SetArgs(args)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	err := cmd.ExecuteContext(ctx)
	return stdout.String(), stderr.String(), err
}
