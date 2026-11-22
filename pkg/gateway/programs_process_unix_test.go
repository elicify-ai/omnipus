//go:build linux || darwin

package gateway

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func pgAssertCheckProcessGone(t *testing.T, pidFile string) {
	t.Helper()
	raw, err := os.ReadFile(pidFile)
	require.NoError(t, err, "the fake recorded its pid")
	var pid int
	_, err = fmt.Sscanf(strings.TrimSpace(string(raw)), "%d", &pid)
	require.NoError(t, err)
	require.Positive(t, pid)
	require.ErrorIs(t, unix.Kill(-pid, 0), unix.ESRCH, "the check process group is gone")
}
