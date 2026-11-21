//go:build goolm && stdjson

package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// FR-042: both operator command families expose registration and removal.
func TestOfficeComponentCommands_RegisterAndUnregister(t *testing.T) {
	for _, family := range []string{"office", "pdf-renderer"} {
		for _, verb := range []string{"register", "unregister"} {
			t.Run(family+"/"+verb, func(t *testing.T) {
				root := NewRootCommand()
				cmd, rest, err := root.Find([]string{family, verb})
				require.NoError(t, err)
				require.Empty(t, rest)
				require.Equal(t, "omnipus "+family+" "+verb, cmd.CommandPath())
			})
		}
	}
}

// Test-only command-tree control, NOT an implementation mutation or CHECK.
// A missing family must not still resolve to its registration command.
func TestOfficeComponentCommands_ControlDetectsRemovedFamily(t *testing.T) {
	for _, family := range []string{"office", "pdf-renderer"} {
		t.Run(family, func(t *testing.T) {
			root := NewRootCommand()
			cmd, _, err := root.Find([]string{family})
			require.NoError(t, err)
			require.Equal(t, "omnipus "+family, cmd.CommandPath())
			root.RemoveCommand(cmd)
			got, _, findErr := root.Find([]string{family, "register"})
			if findErr == nil {
				require.NotEqual(t, "omnipus "+family+" register", got.CommandPath(), "removed family falsely resolved")
			}
		})
	}
}
