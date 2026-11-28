//go:build linux

package preview

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const functionalProbeArg = "__office-functional-probe"

type functionalProbeResult struct {
	Errno string `json:"errno"`
	Text  string `json:"text"`
}

func init() {
	if len(os.Args) == 4 && os.Args[1] == functionalProbeArg {
		result := functionalDoProbe(os.Args[2], os.Args[3])
		if json.NewEncoder(os.Stdout).Encode(result) != nil {
			os.Exit(70)
		}
		os.Exit(0)
	}
}

func functionalDoProbe(operation, path string) functionalProbeResult {
	var err error
	result := functionalProbeResult{}
	switch operation {
	case "socket":
		var listener net.Listener
		listener, err = net.Listen("unix", path)
		if err == nil {
			err = listener.Close() // Also removes only the helper's own socket.
		}
	case "file":
		var file *os.File
		file, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			err = file.Close()
		}
	case "directory":
		err = os.Mkdir(path, 0700)
	case "symlink":
		err = os.Symlink("planted", path)
	case "link":
		err = os.Link(path+"-source", path)
	case "read":
		var raw []byte
		raw, err = os.ReadFile(path)
		result.Text = string(raw)
	case "list":
		_, err = os.ReadDir(path)
	case "remove":
		err = os.Remove(path)
	default:
		err = fmt.Errorf("unknown functional probe: %q", operation)
	}
	switch {
	case err == nil:
		result.Errno = "ok"
	case errors.Is(err, unix.EACCES):
		result.Errno = "EACCES"
	case errors.Is(err, unix.EPERM):
		result.Errno = "EPERM"
	default:
		result.Errno = err.Error() // Missing objects/unrelated failures cannot pass a denial.
	}
	return result
}

// The optional self override re-points the probe at a specific copy of the
// test binary: the real T12x enforced image must exec a probe copy under the
// home, because the per-turn rules cover the home but not the original
// binary's location (the default stays os.Executable for every other user).
func functionalRunProbe(t *testing.T, hooks *spawnHooks, operation, path, work string, selfOverride ...string) functionalProbeResult {
	t.Helper()
	var self string
	var err error
	if len(selfOverride) > 0 {
		self = selfOverride[0]
	} else {
		self, err = os.Executable()
		require.NoError(t, err)
	}
	cmd := exec.Command(self, functionalProbeArg, operation, path)
	cmd.Dir = work
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	// A real pipe, like the production launcher. Nil stdin makes os/exec
	// open /dev/null on the restricted spawn thread, outside the D8c list.
	cmd.Stdin = bytes.NewReader(nil)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	var release func()
	if hooks == nil {
		err = cmd.Start()
	} else {
		require.Empty(t, hooks.unconfined, "T-118: unconfined probes are not evidence")
		require.NotNil(t, hooks.start, "T-118: actual Linux start hook required")
		release, err = hooks.start(cmd)
	}
	require.NoError(t, err, "probe start: %s", output.String())
	if release != nil {
		defer release()
	}
	require.NoError(t, cmd.Wait(), "instrument: helper must complete its operation and report it: %s", output.String())
	var result functionalProbeResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result), "instrument: no missing/malformed receipt: %s", output.String())
	return result
}
