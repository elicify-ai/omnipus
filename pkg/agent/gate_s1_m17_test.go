package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// S1 final-gate M17: convenience wrappers that only tests call must not live in
// production files. They are defined in testonly_wrappers_test.go; this proof
// keeps the non-test sources free of them (a production caller would have to be
// added deliberately, together with removing the name from this list).
func TestNoTestOnlyWrappersInProductionFiles(t *testing.T) {
	decls := packageDeclNames(t, ".")
	// Control: the production claim-carrying forms the wrappers delegate to exist.
	for _, kept := range []string{"completeSteeredTurnForExecution", "deliverSteeredCompletionForExecution",
		"startTaskNowViaLauncherAs", "mintTaskLifecycleRecordFor", "admitBoundConnectorInputCtx", "writeErrorTranscriptCore",
		"resolveMediaRefsWithOffload"} {
		require.True(t, decls[kept], "control: production keeps %s", kept)
	}
	for _, only := range []string{"completeSteeredTurn", "deliverSteeredCompletion", "startTaskNowViaLauncher",
		"mintTaskLifecycleRecord", "admitBoundConnectorInput", "writeErrorTranscriptWithAbandonment", "resolveMediaRefs"} {
		require.False(t, decls[only], "%s is test-only: define it in a _test file, not in production", only)
	}
}
