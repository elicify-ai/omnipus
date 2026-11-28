package preview

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/preview/launcher"
	"github.com/elicify-ai/omnipus/pkg/preview/protocol"
	"github.com/stretchr/testify/require"
)

// AD-7 / FR-009 / MV-2: final converter targets, including the exit-81 retry,
// rather than merely the launcher command before Prepare/Start execute.
func TestPreviewQ62_FinalTargetConverterEnvironment(t *testing.T) {
	if !q62Prime(t) {
		return
	}
	q62PlantParentSentinels(t)
	for _, tc := range []struct {
		name     string
		registry bool
		starts   int
	}{
		{"without_registry", false, 1}, {"with_registry", true, 1}, {"retry", true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			behavior := "retry_valid"
			if tc.starts == 2 {
				behavior = "retry_81_once"
			}
			conv, c := fakeOfficeCall(t, t.TempDir(), behavior)
			defer c.finish()
			if tc.registry {
				conv.registry = filepath.Join(c.dir, "registry space")
				require.NoError(t, os.MkdirAll(conv.registry, 0700))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			pdf, unconfined, err := officeRetryRunWithOwnerBarriers(t, conv, ctx, c, conv.executable, []byte("q62 input"), ".docx", nil)
			require.NoError(t, err)
			require.Equal(t, officeRetryPDF(), pdf)
			require.Empty(t, unconfined, "instrument: real platform hooks run, not an unconfined stand-in")
			receipts := officeRetryReceipts(t, c)
			require.Len(t, receipts, tc.starts, "instrument: exact number of final-target entries")
			expected := q62ConverterExpected(t, c, conv.registry)
			for _, receipt := range receipts {
				require.NotEqual(t, os.Getpid(), receipt.PID, "receipt comes from the target process")
				q62AssertEnvironment(t, receipt.Env, expected)
			}
			if tc.starts == 2 {
				require.NotEqual(t, receipts[0].PID, receipts[1].PID)
				require.Equal(t, receipts[0].Env, receipts[1].Env, "MV-2: retry forwards the identical closed list")
			}
		})
	}
}

// MV-2 now includes BOTH renderer and extractor; their final environments are
// empty on Unix and exactly SYSTEMROOT on Windows, never a generic scrub list.
func TestPreviewQ62_FinalTargetProtocolEnvironment(t *testing.T) {
	if !q62Prime(t) {
		return
	}
	q62PlantParentSentinels(t)
	self, err := os.Executable()
	require.NoError(t, err)
	for _, program := range []string{"renderer", "extractor"} {
		t.Run(program, func(t *testing.T) {
			start, err := processStart(os.Getpid())
			require.NoError(t, err)
			c, err := newCall(t.TempDir(), os.Getpid(), start)
			require.NoError(t, err)
			defer c.finish()
			p := protocolProgram{self: self, target: self, args: []string{"__q62-protocol-env", program}, call: c, maxInput: protocol.MaxInputPDF,
				confine: confinement{readExec: []string{filepath.Dir(self)}, readWrite: []string{c.dir}}}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			response, unconfined, err := p.run(ctx, protocol.Request{Op: protocol.OpText, FirstPage: 1, LastPage: 1}, []byte("q62 input"), nil)
			require.NoError(t, err)
			require.Empty(t, unconfined)
			require.Len(t, response.Text, 1)
			require.Equal(t, program, response.Meta.BuildID)
			var receipt q62TargetReceipt
			require.NoError(t, json.Unmarshal([]byte(response.Text[0].Text), &receipt))
			require.Equal(t, "final-target", receipt.Stage)
			require.Equal(t, program, receipt.Program)
			require.NotEqual(t, os.Getpid(), receipt.PID)
			expected := []string{}
			if runtime.GOOS == "windows" {
				expected = []string{"SYSTEMROOT=" + os.Getenv("SYSTEMROOT")}
			}
			q62AssertEnvironment(t, receipt.Env, expected)
		})
	}
}

func TestPreviewQ62_CommandEnvironmentOwnership(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  []string
	}{{"nil", nil}, {"empty", []string{}}, {"populated", []string{"ONE=original"}}} {
		t.Run(tc.name, func(t *testing.T) {
			source := tc.env
			cmd, err := launcher.Command(launcher.Spec{Self: "test-executable", Target: "test-target", Env: source})
			require.NoError(t, err)
			require.NotNil(t, cmd.Env, "nil input must not reopen inheritance")
			q62AssertEnvironment(t, cmd.Env, tc.env)
			if len(source) > 0 {
				source[0] = "ONE=source-changed"
				require.Equal(t, []string{"ONE=original"}, cmd.Env)
				cmd.Env[0] = "ONE=command-changed"
				require.Equal(t, []string{"ONE=source-changed"}, source)
			}
		})
	}
}
