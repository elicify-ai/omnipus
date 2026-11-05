package skills

// r15 §9.5 / S-5.20–23: external input, real menu scan/activation capture.
// Oversized helpers degrade versioning, not valid instructions; a file that
// grows at the real open boundary still cannot defeat the instruction bound.
// Probe-fired and consumed-byte controls reject a disconnected instrument.
import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstructionGrowth_MenuAndActivationBounded(t *testing.T) {
	const limit = 262144
	for _, surface := range []string{"menu", "activation"} {
		t.Run(surface, func(t *testing.T) {
			root := t.TempDir()
			dir := writeSkillFixture(t, root, "foo")
			path := filepath.Join(dir, "SKILL.md")
			oldOpen, oldConsumed := CaptureOpenProbe, CaptureConsumedProbe
			defer func() { CaptureOpenProbe, CaptureConsumedProbe = oldOpen, oldConsumed }()
			fired := false
			var growErr error
			var consumed int64
			CaptureOpenProbe = func(_, p string) {
				if p != path || fired {
					return
				}
				fired = true
				// Happens synchronously at the promised real I/O boundary, not
				// on an unscheduled goroutine or after a cached size check.
				f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					growErr = err
					return
				}
				_, growErr = f.WriteString(strings.Repeat("G", limit+1))
				if err := f.Close(); growErr == nil {
					growErr = err
				}
			}
			CaptureConsumedProbe = func(p string, n int64) {
				if p == path && n > consumed {
					consumed = n
				}
			}
			if surface == "menu" {
				for _, row := range NewSkillsLoader("", root, "").ListSkills() {
					assert.NotEqual(t, "foo", row.ID, "growing oversized instruction omitted")
				}
			} else {
				text, err := LoadForActivation(root, "foo", LoadActivationOptions{})
				require.ErrorIs(t, err, ErrSkillInstructionLimit)
				assert.Empty(t, text, "oversized instruction body never leaks")
			}
			require.True(t, fired, "BLOCKED: instruction open-boundary instrumentation missing — r15 §9.5")
			require.NoError(t, growErr)
			require.Greater(t, consumed, int64(0), "real bounded reads must be observable")
			assert.LessOrEqual(t, consumed, int64(limit+65536), "every observed bounded-read counter, including metadata/capture/fallback, stays within limit plus one buffer")
			manifests, err := filepath.Glob(filepath.Join(SkillHistoryRoot(root), "foo", "versions", "*.json"))
			require.NoError(t, err)
			assert.Empty(t, manifests, "oversized instructions record no baseline")
		})
	}
}

func TestActivation_OversizedHelperStillLoadsBoundedInstructions(t *testing.T) {
	root := t.TempDir()
	dir := writeSkillFixture(t, root, "foo")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "huge.bin"), make([]byte, 10485761), 0o644))
	text, err := LoadForActivation(root, "foo", LoadActivationOptions{EditAllowed: true})
	require.NoError(t, err, "helpers over capture limit degrade version availability, not instruction loading")
	assert.Contains(t, text, "last change: unknown (changed outside Omnipus)", "§9.5's truthful degraded-load state, not an invented error code")
	assert.NotContains(t, text, `revision="`, "an aborted capture certifies no revision")
	assert.NotContains(t, text, `version="`, "an aborted capture supplies no version")
	// The real-session companion TestSkillDegradedActivation_ClearsSeenAndMarksLocal
	// checks has_local_changes and the next mutation's SKILL_NOT_READ through
	// registered tools; this renderer has no session input and cannot prove seen.
	assert.Contains(t, text, "fixture skill foo")
	assert.Contains(t, text, "Body of foo.", "valid instruction body is actually loaded, not just its header")
	assert.Contains(t, text, "huge.bin")
	manifests, err := filepath.Glob(filepath.Join(SkillHistoryRoot(root), "foo", "versions", "*.json"))
	require.NoError(t, err)
	assert.Empty(t, manifests, "failed helper capture commits no version")
}

func TestInstructionResolution_ActivationUsesCapturedDomain(t *testing.T) {
	for _, target := range []string{"included.md", ".versions/excluded.md", ".omnipus-identity", "../outside.md", "missing.md"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			dir := writeSkillFixture(t, root, "foo")
			p := filepath.Join(dir, target)
			if target != "missing.md" {
				require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
				require.NoError(t, os.WriteFile(p, []byte(validSkillMD("foo", "linked instructions", "CAPTURED-INSTRUCTION-CANARY")), 0o644))
			}
			require.NoError(t, os.Remove(filepath.Join(dir, "SKILL.md")))
			require.NoError(t, os.Symlink(target, filepath.Join(dir, "SKILL.md")))
			opens := 0
			old := CaptureOpenProbe
			defer func() { CaptureOpenProbe = old }()
			CaptureOpenProbe = func(_, path string) {
				if filepath.Clean(path) == filepath.Clean(p) {
					opens++
				}
			}
			text, err := LoadForActivation(root, "foo", LoadActivationOptions{})
			if target == "included.md" {
				require.NoError(t, err)
				assert.Contains(t, text, "CAPTURED-INSTRUCTION-CANARY")
				assert.Greater(t, opens, 0, "included-target read positive control")
			} else {
				require.True(t, errors.Is(err, ErrSkillInstructionUnresolved), "excluded/external/dangling target must be refused: %v", err)
				assert.Contains(t, err.Error(), target)
				assert.NotContains(t, text, "CAPTURED-INSTRUCTION-CANARY")
				assert.Zero(t, opens, "target bytes are outside captured content and never opened")
			}
		})
	}
}
