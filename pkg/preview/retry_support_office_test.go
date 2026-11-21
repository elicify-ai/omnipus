package preview

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/preview/launcher"
	"github.com/stretchr/testify/require"
)

// Shared unchanged helpers moved from the Unix process-edge tests so T-78
// also compiles on Windows without enabling the old Unix-only tree tests.
type fakeOfficeReceipt struct {
	Args  []string `json:"args"`
	Env   []string `json:"env"`
	PID   int      `json:"pid"`
	Child int      `json:"child"`
}

func fakeOfficeCall(t *testing.T, home, behavior string) (officeConversion, *call) {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	start, err := processStart(os.Getpid())
	require.NoError(t, err)
	c, err := newCall(home, os.Getpid(), start)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(c.dir, "behavior"), []byte(behavior), 0600))
	return officeConversion{self: self, executable: self, root: filepath.Dir(self), call: c}, c
}

func init() {
	if len(os.Args) < 2 {
		return
	}
	if os.Args[1] == "__office-retry-grandchild" {
		if os.WriteFile("retry-child-ready", []byte(strconv.Itoa(os.Getpid())), 0600) != nil {
			os.Exit(70)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	if runtime.GOOS == "windows" {
		switch os.Args[1] {
		case launcher.ModeArg:
			os.Exit(launcher.Main(os.Args[2:]))
		case "--headless":
			behavior, err := os.ReadFile("behavior")
			if err != nil || !strings.HasPrefix(string(behavior), "retry_") {
				os.Exit(80)
			}
			os.Exit(officeRetryProcess(string(behavior)))
		}
	}
}
