//go:build windows

package programs

// FR005/F5: exercise the real account-check process-tree owner with a native
// child/grandchild and no taskkill on PATH. This is a cleanup-component fixture,
// not gateway/Service timeout certification. GOOS=windows vet is compile-only;
// a native Windows run is required before any runtime claim.
import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestProgramCheck_WindowsTreeRegression(t *testing.T) {
	if role := os.Getenv("OMNIPUS_S1_TREE_ROLE"); role != "" {
		programsWindowsTreeChild(t, role, os.Getenv("OMNIPUS_S1_TREE_DIR"))
		return
	}
	for _, action := range []string{"stop", "release"} {
		t.Run("F5_"+action+"_terminates_native_descendant", func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestProgramCheck_WindowsTreeRegression$", "-test.count=1")
			cmd.Env = append(os.Environ(), "OMNIPUS_S1_TREE_ROLE=root", "OMNIPUS_S1_TREE_DIR="+dir, "GH_TOKEN=tok_valid_12345", "PATH=")
			setProcessGroup(cmd)
			require.NoError(t, cmd.Start())
			t.Cleanup(func() {
				stopProcessGroup(cmd.Process)
				releaseProcessGroup(cmd.Process)
				_ = cmd.Wait()
			})
			require.NoError(t, startProcessGroup(cmd), "real native check-tree ownership must succeed, never silently degrade")
			// Permit the root to fork only once the real Job Object owns it.
			// This barrier controls the external fixture, not the cleanup code.
			require.NoError(t, os.WriteFile(filepath.Join(dir, "fork"), nil, 0600))
			descendant := programsWindowsTreePID(t, dir, "descendant")
			for _, name := range []string{"root", "descendant"} {
				count, readErr := os.ReadFile(filepath.Join(dir, name+".count"))
				require.NoError(t, readErr)
				require.Equal(t, "16", string(count), "instrument: %s inherited the S25 fifteen-byte token plus printenv newline, without printing it", name)
			}
			rootHandle, rootErr := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
			require.NoError(t, rootErr)
			defer windows.CloseHandle(rootHandle)
			descendantHandle, childErr := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(descendant))
			require.NoError(t, childErr)
			defer windows.CloseHandle(descendantHandle)
			for _, handle := range []windows.Handle{rootHandle, descendantHandle} {
				state, probeErr := windows.WaitForSingleObject(handle, 0)
				require.NoError(t, probeErr)
				require.Equal(t, uint32(windows.WAIT_TIMEOUT), state, "instrument: both observed processes were alive before cleanup")
			}
			if action == "stop" {
				stopProcessGroup(cmd.Process)
			} else {
				releaseProcessGroup(cmd.Process)
			}
			for _, handle := range []windows.Handle{rootHandle, descendantHandle} {
				state, probeErr := windows.WaitForSingleObject(handle, 5000)
				require.NoError(t, probeErr)
				require.Equal(t, uint32(windows.WAIT_OBJECT_0), state, "FR005: cleanup must terminate parent AND its actual token-bearing descendant, without taskkill")
			}
		})
	}
}

func programsWindowsTreePID(t *testing.T, dir, role string) int {
	t.Helper()
	var pid int
	require.Eventually(t, func() bool {
		raw, readErr := os.ReadFile(filepath.Join(dir, role+".pid"))
		if readErr != nil {
			return false
		}
		parsed, parseErr := strconv.Atoi(string(raw))
		pid = parsed
		return parseErr == nil && pid > 0
	}, 5*time.Second, 5*time.Millisecond, "native external fixture must actually start %s", role)
	return pid
}

func programsWindowsTreeChild(t *testing.T, role, dir string) {
	t.Helper()
	require.NotEmpty(t, dir)
	require.Contains(t, []string{"root", "descendant"}, role)
	require.NoError(t, os.WriteFile(filepath.Join(dir, role+".count"), []byte(strconv.Itoa(len(os.Getenv("GH_TOKEN"))+1)), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, role+".pid"), []byte(strconv.Itoa(os.Getpid())), 0600))
	if role == "root" {
		require.Eventually(t, func() bool {
			_, statErr := os.Stat(filepath.Join(dir, "fork"))
			return statErr == nil
		}, 5*time.Second, 5*time.Millisecond, "parent must establish real native tree ownership before the fixture forks")
		child := exec.Command(os.Args[0], "-test.run=^TestProgramCheck_WindowsTreeRegression$", "-test.count=1")
		// The same native executable inherits the real parent's environment.
		child.Env = append(os.Environ(), "OMNIPUS_S1_TREE_ROLE=descendant")
		require.NoError(t, child.Start())
		_ = child.Wait() // The real job cleanup, not a fixture timer, ends it.
		return
	}
	select {} // External descendant stays alive until the real cleanup kills it.
}
