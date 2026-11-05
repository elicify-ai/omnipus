package fspolicy

// r15 §6.2, FR-012, S-2.5–S-2.7. Leaf tests supplement, never replace,
// the actual ExecTool child tests in pkg/tools. No permission-only oracle.
import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkillsKernel_RuntimeExemptionIsExactlyOneValidLevel(t *testing.T) {
	home := t.TempDir()
	for _, tc := range []struct {
		rel  string
		drop bool
	}{
		{"skills/a", false},
		{"skills/" + strings.Repeat("a", 64), false},
		{"skills/My-Skill", false}, // §17.2 D-1 permits uppercase syntax.
		{"skills", true},
		{".", true},
		{"skills/foo/child", true},
		{"skills/.staging", true},
		{"skills/.swap", true},
		{"skills/foo.replace-backup", true},
		{"skills/a--b", true},
		{"skills/" + strings.Repeat("a", 65), true},
		{"sessions", true},
		{"entities/agents", true},
	} {
		t.Run(tc.rel, func(t *testing.T) {
			assert.Equal(t, tc.drop, runtimeAllowanceOverridesInventory(home, filepath.Join(home, filepath.FromSlash(tc.rel))),
				"§6.2 grants exactly one non-dot valid-slug level, never parents, descendants or inventory")
		})
	}
}

func TestSkillsKernel_EnumerationFailureCannotReadmitSkills(t *testing.T) {
	home := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(home, []byte("enumeration must fail even as root"), 0o600))
	_, _, err := KernelDenyEnumeration(home, filepath.Join(home, "agents", "self"), []KernelBranch{
		{Path: filepath.Join(home, "skills", "foo"), Runtime: true},
	})
	require.Error(t, err, "real directory enumeration fails closed on ENOTDIR, not a root-bypassable chmod fixture")
	assert.Contains(t, SecretPaths(home), filepath.Join(home, "skills"), "§6.2 fallback must retain the whole skills root")
}
