//go:build goolm && stdjson

package fspolicy

// Order 50 (kernel half) — TestProjectSkillPathGate_Kernel (S-111; Dataset
// D14 rows 12–13; C-25 "Kernel layer"). Oracle: skills-tools-redesign-spec.md —
//   D14#12: a `bash cat` on Linux with an enforcing kernel sandbox is
//        refused by the kernel layer (the gate is also rendered into the
//        kernel policy: on Linux by extending the existing deny enumeration
//        — fspolicy/secretset_kernel.go::KernelDenyEnumeration, today
//        limited to branches under $OMNIPUS_HOME — to mount branches, so a
//        gated folder is left out of the read grants).
//   D14#13: on an app-level-only path the refusal happens when the operand
//        is visible, and the platform is logged as app-level only — Warn
//        once per turn with the folder, never reported as kernel-enforced.
//
// RED: the kernel enumeration does not cover mount branches yet and the
// app-level-only logging does not exist. The seams below are those missing
// pieces; GREEN wires them to the real rendering and the per-turn log. Nil
// keeps the RED run loud (BLOCKED per row), never a skip.
import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProjectSkillPathGate_Kernel(t *testing.T) {
	t.Run("D14#12: the kernel policy refuses a gated project-skill folder on an enforcing Linux sandbox", func(t *testing.T) {
		if kernelDeniesGatedFolder == nil {
			t.Fatal("BLOCKED: the kernel-layer rendering of gated project-skill folders (the " +
				"KernelDenyEnumeration extension to mount branches) is not implemented — " +
				"required by S-111 and Dataset D14 row 12")
		}
		home := t.TempDir()
		mountRoot := filepath.Join(home, "W1")
		skillDir := filepath.Join(mountRoot, ".claude", "skills", "notes")
		require.NoError(t, os.MkdirAll(skillDir, 0o700))

		require.True(t, kernelDeniesGatedFolder(t, home, mountRoot, ".claude/skills/notes"),
			"D14#12: the gated folder must be denied at the kernel layer")
		require.False(t, kernelDeniesGatedFolder(t, home, mountRoot, "README.md"),
			"D14#12 positive control: an ordinary mount file stays readable")
	})

	t.Run("D14#13: an app-level-only path refuses when the operand is visible and is logged once per turn", func(t *testing.T) {
		if appLevelOnlyPathRefusal == nil {
			t.Fatal("BLOCKED: the app-level-only handling of the path gate (refusal when the " +
				"operand is visible, Warn once per turn naming the folder, never reported as " +
				"kernel-enforced) is not implemented — required by S-111 and Dataset D14 row 13")
		}
		home := t.TempDir()
		mountRoot := filepath.Join(home, "W1")
		skillDir := filepath.Join(mountRoot, ".claude", "skills", "notes")
		require.NoError(t, os.MkdirAll(skillDir, 0o700))

		refused, warnOnce := appLevelOnlyPathRefusal(t, home, mountRoot, ".claude/skills/notes/SKILL.md")
		require.True(t, refused, "D14#13: the visible operand is still refused at the app layer")
		require.True(t, warnOnce, "D14#13: the platform is logged as app-level only, once per turn")
	})
}

// The seams GREEN wires to the real kernel rendering and per-turn logging.
// Declared after the table so the RED failure above names the missing
// behaviour first.
var (
	kernelDeniesGatedFolder func(t testing.TB, home, mountRoot, relPath string) bool
	appLevelOnlyPathRefusal func(t testing.TB, home, mountRoot, relPath string) (refused, warnOnce bool)
)
