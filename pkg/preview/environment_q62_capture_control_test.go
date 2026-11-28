package preview

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/childenv"
	"github.com/elicify-ai/omnipus/pkg/preview/protocol"
	"github.com/stretchr/testify/require"
)

// Calibrate the SAME final-target receipt builder and protocol target used by
// the production-path tests. An empty/fabricated capture must fail this control.
// This deliberately runs with a generic inherited list, never production approval.
func TestPreviewQ62_CaptureInstrumentSeesInheritedSentinels(t *testing.T) {
	q62PlantParentSentinels(t)
	self, err := os.Executable()
	require.NoError(t, err)
	var input bytes.Buffer
	req := protocol.Request{Op: protocol.OpText, FirstPage: 1, LastPage: 1, MaxInput: protocol.MaxInputPDF}
	require.NoError(t, protocol.WriteRequest(&input, req, bytes.NewReader([]byte("q62 control")), int64(len("q62 control"))))
	expected := childenv.Filter()
	cmd := exec.Command(self, "__q62-protocol-env", "capture-control")
	cmd.Env = append([]string{}, expected...)
	cmd.Stdin = &input
	raw, err := cmd.Output()
	require.NoError(t, err)
	response, err := protocol.ReadResponse(bytes.NewReader(raw), req, nil)
	require.NoError(t, err)
	require.Len(t, response.Text, 1)
	var receipt q62TargetReceipt
	require.NoError(t, json.Unmarshal([]byte(response.Text[0].Text), &receipt))
	require.Equal(t, "final-target", receipt.Stage)
	require.Equal(t, "capture-control", receipt.Program)
	require.Positive(t, receipt.PID)
	require.NotEqual(t, os.Getpid(), receipt.PID)
	if runtime.GOOS == "windows" {
		// The controlled generic list omits this key; Go adds it on Windows.
		expected = append(expected, "SYSTEMROOT="+os.Getenv("SYSTEMROOT"))
	}
	q62AssertEnvironment(t, receipt.Env, expected)
	for _, entry := range []string{"USER=q62-parent-user", "OMNIPUS_CHILD_Q62_SENTINEL=q62-pass-through"} {
		require.Contains(t, receipt.Env, entry)
	}
}
