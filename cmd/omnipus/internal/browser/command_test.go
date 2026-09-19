// Squad K: minimal test for the `omnipus browser provision` subcommand.
// Mirrors the patterns in cmd/omnipus/internal/{doctor,version}/command_test.go:
// asserts the cobra command tree, NOT the install behavior — the install
// itself is exercised on the UAT chroot as the CI execution receipt (per
// the brief's "VALIDATE the new command in the UAT chroot" mandate) and
// on real Go CI as the broader-install-path coverage.
package browser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBrowserCommand(t *testing.T) {
	cmd := NewBrowserCommand()

	require.NotNil(t, cmd)
	assert.Equal(t, "browser", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
	assert.NotEmpty(t, cmd.Long)
	assert.False(t, cmd.HasFlags())

	// provision is the only leaf today. The tree must hold exactly one
	// subcommand named "provision" so a CI invocation
	// (`omnipus browser provision ...`) resolves before the gateway /
	// start cobra dispatch.
	subs := cmd.Commands()
	require.Len(t, subs, 1, "browser command tree must hold exactly one subcommand today")
	assert.Equal(t, "provision", subs[0].Use)
	assert.NotNil(t, subs[0].RunE, "provision subcommand must have a RunE (not Run) so cobra propagates the install error as exit 1")
}
