package gateway

// S-113/R09: never promote named-route probing to independent writer coverage.
import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProgramCredential_RouteSweep_MethodInstrument(t *testing.T) {
	const p = "/api/v1/programs/"
	methods := []string{http.MethodPut, http.MethodPost, http.MethodDelete}
	handled := map[string]bool{}
	for _, m := range methods {
		handled[p+"|"+m] = true
	}
	require.Empty(t, pgMissingSweepRows([]string{p}, handled, nil, nil), "all required methods are present")
	delete(handled, p+"|"+http.MethodDelete)
	require.Equal(t, []string{p + "|" + http.MethodDelete}, pgMissingSweepRows([]string{p}, handled, nil, nil), "PUT and POST cannot cover missing DELETE")
	path, _, row := pgSweepTarget(p, http.MethodDelete)
	require.True(t, row)
	require.Equal(t, "/api/v1/programs/github/credential", path, "disconnect is not entry deletion (S-53/S-113)")
}

func TestProgramCredential_RouteSweep_MissingBoundaries(t *testing.T) {
	t.Run("independent_registered_route_to_writer_inventory", func(t *testing.T) {
		// H46 ruling: the real two-edition mux census and source-typed writer
		// enumeration replace only S113's last And. This exact-name row now
		// guards mandatory build/name selection, never their runtime outcome.
		pgRequireS113Inventory(t)
	})
	t.Run("all_concrete_schema_paths_and_live_revisions", func(t *testing.T) {
		pgConcreteProgramsPayloads(t)
	})
}
