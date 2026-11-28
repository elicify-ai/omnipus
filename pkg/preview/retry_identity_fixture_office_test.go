package preview

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// CI 5461130a6 / AD-7 / Q62: the converter's granted folders do not include
// /proc. A process-edge fake must not need that forbidden metadata either.
// Deny only the OS metadata reader; all profile/input/receipt/PDF file I/O and
// the existing owner-seen barrier remain real. This is a portable instrument
// control, NOT a claim of executing Linux Landlock on macOS.
func TestOfficeRetryFixture_DoesNotReadConfinedProcessMetadata(t *testing.T) {
	original, err := os.Getwd()
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { require.NoError(t, os.Chdir(original)) })
	for _, folder := range []string{"profile/user", "in", "out"} {
		require.NoError(t, os.MkdirAll(filepath.FromSlash(folder), 0700))
	}
	require.NoError(t, os.WriteFile(filepath.FromSlash("profile/user/registrymodifications.xcu"), []byte(profileSettings), 0600))
	require.NoError(t, os.WriteFile(filepath.FromSlash("in/document.docx"), []byte("fixture input"), 0600))
	require.NoError(t, os.WriteFile("retry-owner-seen-1", []byte("ok"), 0600))
	reads := 0
	code := officeRetryProcessWithStartReader("retry_valid", func(pid int) (string, error) {
		reads++
		return "", &os.PathError{Op: "open", Path: fmt.Sprintf("/proc/%d/stat", pid), Err: os.ErrPermission}
	})
	require.Equal(t, 0, code, "a confined converter fake must succeed without /proc; metadata reads=%d", reads)
	require.Zero(t, reads, "process identity is observed by the gateway-side barrier, never by the confined fake")
	raw, err := os.ReadFile("retry-start-1.json")
	require.NoError(t, err)
	var receipt officeRetryReceipt
	require.NoError(t, json.Unmarshal(raw, &receipt))
	require.Equal(t, os.Getpid(), receipt.PID)
	require.Empty(t, receipt.Start, "the target does not invent its own process start")
	require.Empty(t, receipt.LauncherStart, "the parent must read the actual kernel identity before release")
	pdf, err := os.ReadFile(filepath.FromSlash("out/document.pdf"))
	require.NoError(t, err)
	require.Equal(t, officeRetryPDF(), pdf)
}
