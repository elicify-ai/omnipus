package omnpkg_test

// RED T-2 — TestRemap_NewIDNeverCollides (spec TDD plan order 12, property).
// Spec: FR-031 (the new agent ID MUST be a fresh UUID, different from the
// source ID and from every existing ID); BDD "Confirmed import creates the
// agent" ("new_id is a UUID different from the source ID and from every ID
// present before"); ADR D5 (the remap component owns the table; property
// test: "no imported ID equals any ID that existed before").

import (
	"regexp"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

var uuidShape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func TestRemap_NewIDNeverCollides(t *testing.T) {
	t.Run("FR-031 property: 1000 fresh IDs are unique, UUID-shaped, outside taken", func(t *testing.T) {
		taken := map[string]bool{
			testSourceID:                           true, // the source ID itself
			testSourceID2:                          true,
			"11111111-2222-3333-4444-555555555555": true,
			"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee": true,
		}
		seen := map[string]bool{}
		for i := 0; i < 1000; i++ {
			id, err := omnpkg.FreshAgentID(taken)
			require.NoError(t, err)
			require.Regexp(t, uuidShape, id, "the new ID is a UUID")
			require.False(t, taken[id], "the new ID never equals a taken ID (source or pre-existing)")
			require.False(t, seen[id], "the new IDs never collide with each other")
			seen[id] = true
		}
	})

	t.Run("FR-031 the source ID alone is enough to force a different ID", func(t *testing.T) {
		// Generate until the source ID is "used up": with the source in the
		// taken set the very first draw must differ from it.
		taken := map[string]bool{testSourceID: true}
		id, err := omnpkg.FreshAgentID(taken)
		require.NoError(t, err)
		require.NotEqual(t, testSourceID, id)
		require.False(t, taken[id], "FreshAgentID does not mutate the caller's set")
	})
}
