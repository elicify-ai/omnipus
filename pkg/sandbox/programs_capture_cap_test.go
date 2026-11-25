package sandbox_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/programs"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/require"
)

const captureSentinel = "sentinel_9f3c2a7e5b1d"

// S-103: real child -> programs.Attach -> recording sink -> actual foreground
// cappedBuffer at 80 bytes. Removing ONLY Attach's masking must leak a proper
// ten-byte fragment into this instrument and fail; no direct NewMaskingWriter.
// This proves the composition, not that every agent launch uses it (S-106).
func TestProgramsAttach_ForegroundCaptureCap(t *testing.T) {
	for _, row := range []struct {
		name          string
		prefix, limit int
	}{
		{"S103_eighty_byte_cap", 70, 80},
		{"S103_delivered_inside_normal_limit", 1000, 64000},
	} {
		t.Run(row.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProgramsAttach_CaptureChild$", fmt.Sprint(row.prefix))
			cmd.Env = []string{"OMNIPUS_S1_CAPTURE_CHILD=1"}
			stdout := sandbox.NewProgramTestCapture(row.limit)
			stderr := sandbox.NewProgramTestCapture(row.limit)
			cmd.Stdout, cmd.Stderr = stdout, stderr
			require.NoError(t, programs.Attach(cmd, programs.ResolvedPrograms{Env: []string{"GH_TOKEN=" + captureSentinel}, Tokens: []string{captureSentinel}}))
			err := cmd.Run()
			require.NoError(t, err, "Attach must inject GH_TOKEN and install source masking; stderr=%q", stderr.Entered())
			for label, out := range map[string]string{"entered cap": stdout.Entered(), "captured": stdout.Captured()} {
				for i := 0; i+8 <= len(captureSentinel); i++ {
					require.NotContains(t, out, captureSentinel[i:i+8], label)
				}
				require.Contains(t, out, "[FILTERED]", label)
			}
			require.Equal(t, strings.Repeat("a", row.prefix)+"[FILTERED]END", stdout.Entered())
			if row.limit == 64000 {
				require.Equal(t, stdout.Entered(), stdout.Captured())
			}
		})
	}
}

// Positive control for the instrument: a raw source demonstrably leaves the
// first ten protected bytes inside the same real eighty-byte cap. This is not
// an executed implementation mutation (that remains independent CHECK).
func TestProgramsAttach_CapInstrument(t *testing.T) {
	capture := sandbox.NewProgramTestCapture(80)
	raw := strings.Repeat("a", 70) + captureSentinel
	n, err := capture.Write([]byte(raw))
	require.NoError(t, err)
	require.Equal(t, len(raw), n)
	require.Equal(t, raw, capture.Entered())
	require.Contains(t, capture.Captured(), captureSentinel[:10])
	require.NotContains(t, capture.Captured(), captureSentinel)
}

// A process-edge fake emits its actual injected variable, without shell or
// platform-specific executable syntax. The test process always exits/joined by
// CommandContext.Run; it cannot leave a grandchild behind.
func TestProgramsAttach_CaptureChild(t *testing.T) {
	if os.Getenv("OMNIPUS_S1_CAPTURE_CHILD") != "1" {
		return
	}
	value := os.Getenv("GH_TOKEN")
	if value != captureSentinel {
		fmt.Fprint(os.Stderr, "program variable not injected")
		os.Exit(9)
	}
	prefix := 70
	if os.Args[len(os.Args)-1] == "1000" {
		prefix = 1000
	}
	_, err := fmt.Fprint(os.Stdout, strings.Repeat("a", prefix)+value+"END")
	if err != nil {
		os.Exit(8)
	}
	os.Exit(0)
}
