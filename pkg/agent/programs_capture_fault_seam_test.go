//go:build s1seams

package agent

// Spec rev8 S103/FR020 and S130/D9 row7. The supplied test-build seams observe
// the actual pre-cap writer and fault the actual background pipe read edge.
// No standalone cap/masker or manufactured tool result stands in for the flow.
import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/testseam"
	"github.com/stretchr/testify/require"
)

func TestProgramMasking_ActualCaptureSeam(t *testing.T) {
	t.Run("S103_Dev_exec_reaches_instrumented_cap", func(t *testing.T) {
		f := newProgramsTurnFixtureToken(t, programsBackgroundSentinel, func(cfg *config.Config) { cfg.Tools.FilterSensitiveData = false })
		// Resolve the actual tool's one-time git-capability probe before arming
		// the command recorder. Do not strip an unexpected prefix from captured
		// bytes or relax the exact S103 oracle to accommodate unrelated output.
		require.Equal(t, "S103_CAPTURE_CONTROL", programsForeground(t, f, "s103-warmup", "printf 'S103_CAPTURE_CONTROL'"))
		var mu sync.Mutex
		var stdout, stderr bytes.Buffer
		t.Cleanup(testseam.SetForegroundCapture(80, func(stream string, p []byte) {
			mu.Lock()
			defer mu.Unlock()
			switch stream {
			case "stdout":
				stdout.Write(p)
			case "stderr":
				stderr.Write(p)
			}
		}))
		// S103's exact original boundary: seventy harmless bytes, then the
		// twenty-one-byte sentinel. Its first ten raw bytes would fit cap80.
		command := "printf '%s' '" + strings.Repeat("a", 70) + "'; printf '%s' \"$GH_TOKEN\""
		result := programsForeground(t, f, "s103-real-cap", command)
		mu.Lock()
		captured, capturedErr := stdout.String(), stderr.String()
		mu.Unlock()
		want := strings.Repeat("a", 70) + "[FILTERED]"
		require.Equal(t, want, captured, "S103: exact bytes entering the real cap, not just the delivered output")
		require.Empty(t, capturedErr, "instrument: this actual child writes only stdout")
		require.Equal(t, want, result, "S103: actual cap80 admits the seventy harmless bytes and ten-byte replacement")
		programsNoTokenWindow(t, programsBackgroundSentinel, captured, "S103 actual pre-cap recording sink")
		t.Log("actual Dev foreground child; configured cap=80; exact pre-cap stdout=70 harmless bytes plus replacement; no protected token window")
	})
}

func TestProgramMasking_ActualReadFaultSeam(t *testing.T) {
	t.Run("D9_row7_readerror_stdout10_background_read", func(t *testing.T) {
		f, sid, events := programsBackgroundFixture(t, programsBackgroundSentinel)
		work := filepath.Join(f.home, "workspaces", "sales", "work")
		require.NoError(t, os.MkdirAll(work, 0700))
		marker := filepath.Join(work, "h45-child-ran")
		type observedFault struct {
			session, stream string
			read            int64
		}
		var mu sync.Mutex
		var faults []observedFault
		fault := errors.New("S130 D9 row7 injected non-EOF pipe read error")
		t.Cleanup(testseam.SetBackgroundPipeReadFault(func(child, stream string, read int64) error {
			if stream != "stdout" || read < 10 {
				return nil
			}
			mu.Lock()
			defer mu.Unlock()
			faults = append(faults, observedFault{child, stream, read})
			return fault
		}))
		// No stdout control bytes precede the specified ten original bytes.
		// Child admission/execution is proved through a separate harmless file.
		command := fmt.Sprintf("printf '%%.10s' \"$GH_TOKEN\"; printf 'H45_CHILD_RAN' > %q", marker)
		child := programsStartBackground(t, f, sid, "d9-read-fault-start", command)
		completion := programsCompletion(t, events, sid)
		rawMarker, err := os.ReadFile(marker)
		require.NoError(t, err, "instrument: the actual child ran after writing its original ten bytes")
		require.Equal(t, "H45_CHILD_RAN", string(rawMarker))
		mu.Lock()
		actual := append([]observedFault(nil), faults...)
		mu.Unlock()
		require.Equal(t, []observedFault{{child, "stdout", 10}}, actual, "D9 row7: exactly one real non-EOF read error, at ten bytes, on this actual returned session")
		read, raw := programsBashAction(t, f, sid, "d9-read-fault-read", map[string]any{"action": "read", "session_id": child})
		require.Equal(t, "[FILTERED]", read.Output, "D9 row7: actual registered background read, not empty absence or a timeout/kill surrogate")
		programsNoTokenWindow(t, programsBackgroundSentinel, raw, "D9 row7 actual registered background JSON read")
		programsNoTokenWindow(t, programsBackgroundSentinel, completion.Content, "D9 row7 actual completion callback")
		t.Log("actual original stdout10 child; real pipe-read fault observed once at10; registered same-session read=[FILTERED]")
	})
}
