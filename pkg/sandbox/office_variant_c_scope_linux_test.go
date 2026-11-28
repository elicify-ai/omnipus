//go:build linux

package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T-118 / FR-061: actual per-turn/tool construction seams, for both
// read models, including bash Auto's network rendering. Pre-existing broad
// /tmp writes and open-model reads are NOT new converter additions.
func TestOfficeVariantC_T118_TurnAndToolPoliciesDoNotGainConverterAdditions(t *testing.T) {
	home := officeVariantCScopeHome(t)
	work := filepath.Join(home, "workspace")
	require.NoError(t, os.Mkdir(work, 0700))
	old, installed := RegisteredTurnPolicyBase()
	defer func() {
		if installed {
			RegisterTurnPolicyBase(&old)
		} else {
			RegisterTurnPolicyBase(nil)
		}
	}()
	for _, model := range []FilesystemModel{FilesystemModelConfined, FilesystemModelOpen} {
		t.Run(string(model), func(t *testing.T) {
			for _, auto := range []bool{false, true} {
				RegisterTurnPolicyBase(&TurnPolicyInput{HomePath: home, Model: model, NetworkAutoDeny: auto})
				policy, err := KernelPolicyForTurn(fspolicy.FSPolicy{WorkDir: work, Scope: fspolicy.FSScopeConfined})
				require.NoError(t, err)
				require.NotNil(t, policy, "instrument: exercise the real registered tool seam")
				officeAssertNoConverterAdditions(t, *policy)
			}
		})
	}
}

// q1bConverterOnlyReads are the founder Q1 B paths (OFFICE-Q1B-READS.md,
// "The three extra reads"). They belong to the Linux confined boot set for
// the converter child, not to a per-turn or tool policy. Hardcoded from that
// decision, not read from confinedBootExtraReads.
var q1bConverterOnlyReads = []string{
	"/etc/passwd",
	"/usr/share/fonts",
	"/usr/local/share/fonts",
}

func officePathIsQ1BConverterRead(path string) bool {
	clean := filepath.Clean(path)
	for _, banned := range q1bConverterOnlyReads {
		if clean == banned || strings.HasPrefix(clean, banned+"/") {
			return true
		}
	}
	return false
}

func officeAssertNoConverterAdditions(t *testing.T, policy SandboxPolicy) {
	t.Helper()
	for _, rules := range [][]PathRule{policy.FilesystemRules, policy.AllowedBranches} {
		for _, rule := range rules {
			assert.Zero(t, rule.Access & ^(AccessRead|AccessWrite|AccessExecute), "T-118: new converter-only access leaked into turn/tool rule %s", rule.Path)
			assert.False(t, officePathIsQ1BConverterRead(rule.Path),
				"T-118: Q1 B converter-only read %s leaked into turn/tool policy; none of /etc/passwd, /usr/share/fonts, /usr/local/share/fonts (or a path under them) is allowed", rule.Path)
		}
	}
}

func officeVariantCScopeHome(t *testing.T) string {
	t.Helper()
	base := os.Getenv("OMNIPUS_TEST_OFFICE_DATA_ROOT")
	if base == "" {
		var err error
		base, err = os.Getwd()
		require.NoError(t, err)
	}
	resolved, err := filepath.EvalSymlinks(base)
	require.NoError(t, err)
	require.False(t, resolved == "/tmp" || strings.HasPrefix(resolved, "/tmp/"), "O2: scope home must be outside /tmp")
	home, err := os.MkdirTemp(resolved, "office-turn-scope-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(home)) })
	t.Setenv("TMPDIR", home)
	return home
}
