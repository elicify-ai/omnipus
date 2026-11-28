package programs

// S106 account-check child and S105: run the real check with the sentinel at
// both process edges. This is the check's result/persistence sink boundary;
// agent transcript/background/notifier sinks are separate missing-runtime rows.
import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func assertAccountCheckSinks(t *testing.T) {
	t.Helper()
	const sentinel = "sentinel_9f3c2a7e5b1d" // revision-8 S130/D9, 21 bytes
	f := newCheckFixture(t, map[string]checkChildCase{sentinel: {Label: sentinel, Stderr: sentinel}})
	_, err := f.svc.Publish("github", sentinel, nil, nil)
	require.NoError(t, err)
	f.waitStarted(t, sentinel)
	conn := f.await(t, "github", "check_failed")
	require.Empty(t, conn.AccountLabel, "S105: neither the token nor FILTERED becomes an account label")
	require.Equal(t, "The account check printed the token instead of an account name. Fix the account check for GitHub.", conn.Message)
	persisted, err := os.ReadFile(f.state)
	require.NoError(t, err)
	view, err := json.Marshal(conn)
	require.NoError(t, err)
	for i := 0; i+8 <= len(sentinel); i++ {
		require.NotContains(t, string(persisted), sentinel[i:i+8], "no protected fragment enters persisted connection")
		require.NotContains(t, string(view), sentinel[i:i+8], "no protected fragment enters returned connection")
	}
}

func TestProgramAccountLabel_ActualCheck(t *testing.T) {
	t.Run("S105_masked_stdout_never_becomes_the_saved_label", func(t *testing.T) { assertAccountCheckSinks(t) })
}
