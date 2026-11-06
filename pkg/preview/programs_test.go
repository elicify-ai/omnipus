//go:build darwin || linux

package preview

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/preview/launcher"
	"github.com/stretchr/testify/require"
)

// The test executable hosts the REAL launcher, and acts as the external
// converter only at the process edge. Conversion/limits/records stay real.
func init() {
	if len(os.Args) < 2 {
		return
	}
	if os.Args[1] == launcher.ModeArg {
		os.Exit(launcher.Main(os.Args[2:]))
	}
	if os.Args[1] == "--headless" {
		os.Exit(fakeOfficeProcess())
	}
}

func fakeOfficeProcess() int {
	behavior, err := os.ReadFile("behavior")
	if err != nil {
		return 80
	}
	if strings.HasPrefix(string(behavior), "retry_") {
		return officeRetryProcess(string(behavior))
	}
	if strings.HasPrefix(string(behavior), "scratch_") {
		return scratchLinkOfficeProcess()
	}
	receipt := fakeOfficeReceipt{Args: os.Args[1:], Env: os.Environ(), PID: os.Getpid()}
	if string(behavior) == "tree" {
		child := exec.Command("/bin/sleep", "30")
		if err = child.Start(); err != nil {
			return 81
		}
		receipt.Child = child.Process.Pid
		data, merr := json.Marshal(receipt)
		if merr != nil {
			return 82
		}
		if err = os.WriteFile("receipt.json", data, 0600); err != nil {
			return 83
		}
		if err = child.Wait(); err != nil {
			return 84
		}
		return 0
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return 85
	}
	if err = os.WriteFile("receipt.json", data, 0600); err != nil {
		return 86
	}
	out := filepath.Join("out", "document.pdf")
	switch string(behavior) {
	case "valid":
		if err = os.WriteFile(out, []byte("%PDF-fixture"), 0600); err != nil {
			return 87
		}
	case "junk_zero", "junk_nonzero":
		if err = os.WriteFile(out, []byte("not a PDF"), 0600); err != nil {
			return 88
		}
		if string(behavior) == "junk_nonzero" {
			return 7
		}
	case "empty":
		if err = os.WriteFile(out, nil, 0600); err != nil {
			return 89
		}
	case "marker":
		_, _ = os.Stdout.WriteString("convert document.xlsx as a Calc document -> document.pdf using filter calc_pdf_Export\n")
	}
	return 0
}

// T-06 / MV-2: exact child argv and environment (captured inside the real
// launched process); parent credentials and variables must not leak.
func TestPreviewConverter_ArgvAndEnvironmentGolden(t *testing.T) {
	t.Setenv("OFFICE_PREVIEW_PARENT_SECRET", "must-not-reach-child")
	conv, c := fakeOfficeCall(t, t.TempDir(), "valid")
	defer c.finish()
	conv.registry = filepath.Join(c.dir, "registry space")
	pdf, _, err := conv.run(context.Background(), []byte("input"), ".xlsx")
	require.NoError(t, err)
	require.Equal(t, []byte("%PDF-fixture"), pdf)
	raw, err := os.ReadFile(filepath.Join(c.dir, "receipt.json"))
	require.NoError(t, err)
	var receipt fakeOfficeReceipt
	require.NoError(t, json.Unmarshal(raw, &receipt))
	wantArgs := []string{"--headless", "--invisible", "--nologo", "--nodefault", "--norestore", "--nolockcheck", "-env:UserInstallation=file://" + filepath.ToSlash(filepath.Join(c.dir, "profile")), "--convert-to", "pdf", "--outdir", filepath.Join(c.dir, "out"), filepath.Join(c.dir, "in", "document.xlsx")}
	require.Equal(t, wantArgs, receipt.Args)
	wantEnv := []string{"PATH=/usr/bin:/bin", "LANG=en_US.UTF-8", "HOME=" + filepath.Join(c.dir, "home"), "TMPDIR=" + filepath.Join(c.dir, "tmp")}
	if c.socketDir != "" {
		wantEnv = append(wantEnv, "OSL_SOCKET_PATH="+c.socketDir, "URE_MORE_SERVICES=<file://"+strings.ReplaceAll(filepath.ToSlash(conv.registry), " ", "%20")+">*")
	}
	sort.Strings(wantEnv)
	sort.Strings(receipt.Env)
	require.Equal(t, wantEnv, receipt.Env)
	copied, err := os.ReadFile(filepath.Join(c.dir, "in", "document.xlsx"))
	require.NoError(t, err)
	require.Equal(t, []byte("input"), copied)
}

// T-07 / FR-011: success messages and exit 0 cannot replace a readable PDF.
func TestPreviewConverter_RejectsJunkEmptyAndMarkerOnlyOutput(t *testing.T) {
	for _, behavior := range []string{"junk_zero", "junk_nonzero", "empty", "marker"} {
		t.Run(behavior, func(t *testing.T) {
			conv, c := fakeOfficeCall(t, t.TempDir(), behavior)
			defer c.finish()
			pdf, _, err := conv.run(context.Background(), []byte("input"), ".docx")
			require.Nil(t, pdf)
			require.Error(t, err)
			require.True(t, strings.HasPrefix(err.Error(), "conversion failed: LibreOffice produced no readable PDF"), "MV-1 error: %v", err)
			_, statErr := os.Stat(filepath.Join(c.dir, "receipt.json"))
			require.NoError(t, statErr, "the negative-control converter really executed")
		})
	}
}

func assertPreviewTreeEnd(t *testing.T, home, mode string) {
	t.Helper()
	conv, c := fakeOfficeCall(t, home, "tree")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	defer c.finish()
	done := make(chan error, 1)
	go func() { _, _, err := conv.run(ctx, []byte("input"), ".docx"); done <- err }()
	var receipt fakeOfficeReceipt
	require.Eventually(t, func() bool {
		raw, err := os.ReadFile(filepath.Join(c.dir, "receipt.json"))
		return err == nil && json.Unmarshal(raw, &receipt) == nil
	}, time.Second, 5*time.Millisecond, "converter and grandchild must reach the running state")
	require.True(t, processAlive(receipt.PID))
	require.True(t, processAlive(receipt.Child), "positive control: grandchild was alive before cancellation")
	record := readPreviewOwner(t, c)
	require.Equal(t, "running", record.State)
	require.NotNil(t, record.Launcher)
	require.Equal(t, receipt.PID, record.Launcher.PID)
	if mode == "stop" {
		cancel()
	}
	select {
	case err := <-done:
		if mode == "stop" {
			require.EqualError(t, err, "stopped")
		} else {
			require.EqualError(t, err, "timed out after 120 s")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("converter did not return after cancellation")
	}
	require.Eventually(t, func() bool { return !processAlive(receipt.PID) && !processAlive(receipt.Child) }, 5*time.Second, 10*time.Millisecond, "AD-6: converter and grandchild must both be gone")
	c.finish()
	_, err := os.Stat(c.dir)
	require.ErrorIs(t, err, os.ErrNotExist)
	if c.socketDir != "" {
		_, err = os.Stat(c.socketDir)
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}

func TestPreviewConverter_DeadlineKillsWholeTree(t *testing.T) {
	assertPreviewTreeEnd(t, t.TempDir(), "deadline")
}
func TestPreviewConverter_StopKillsWholeTree(t *testing.T) {
	assertPreviewTreeEnd(t, t.TempDir(), "stop")
}

// T-13: exactly fifty real child lifecycles, ten deadlines and ten Stops.
// The thirty successes use a process-edge fake, not real Office rendering.
func TestPreviewScratch_FiftyRunsLeaveNoFoldersOrProcesses(t *testing.T) {
	home := t.TempDir()
	for i := 0; i < 50; i++ {
		if i < 10 {
			assertPreviewTreeEnd(t, home, "deadline")
			continue
		}
		if i < 20 {
			assertPreviewTreeEnd(t, home, "stop")
			continue
		}
		conv, c := fakeOfficeCall(t, home, "valid")
		pdf, _, err := conv.run(context.Background(), []byte("input"), ".docx")
		require.NoError(t, err)
		require.Equal(t, []byte("%PDF-fixture"), pdf)
		raw, err := os.ReadFile(filepath.Join(c.dir, "receipt.json"))
		require.NoError(t, err)
		var receipt fakeOfficeReceipt
		require.NoError(t, json.Unmarshal(raw, &receipt))
		require.False(t, processAlive(receipt.PID))
		c.finish()
	}
	entries, err := os.ReadDir(filepath.Join(home, "tmp", "preview"))
	require.NoError(t, err)
	require.Empty(t, entries)
}
