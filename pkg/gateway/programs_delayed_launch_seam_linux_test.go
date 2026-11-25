//go:build linux && s1seams

package gateway

// S139/FR047: actual person saves/reservations/check children/shared audit.
// IN_OPEN on the private executable observes kernel exec admission even when
// a canceled late process dies before writing its first userspace marker.
import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/testseam"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgramCheck_ActualDelayedLaunch(t *testing.T) {
	t.Run("S139_delayed_launch_cannot_stop_the_newer_check", func(t *testing.T) {
		f := pgPersonAPI(t)
		pgSupersededAuditInstrument(t, f)
		dir := t.TempDir()
		ready, release := filepath.Join(dir, "b-started"), filepath.Join(dir, "release-b")
		body := fmt.Sprintf(`case "$GH_TOKEN" in
 tok_new_654321) printf 'account-b\n';;
 tok_valid_12345) printf 'account-a\n';;
 *) exit 91;;
esac
printf 'B_CHECK_RUNNING' > %q
while [ ! -f %q ]; do sleep 0.01; done
`, ready, release)
		pgInstallFakeGh(t, f, body, "")
		program, err := exec.LookPath("gh")
		require.NoError(t, err)
		watch := newOpenWatch(t, []string{program})
		paused, launch := make(chan struct{}), make(chan struct{})
		releaseLaunch := sync.OnceFunc(func() { close(launch) })
		t.Cleanup(testseam.SetBeforeCheckLaunch(func(id string, generation int64) {
			if id == "github" && generation == 1 {
				close(paused)
				<-launch
			}
		}))
		t.Cleanup(func() {
			releaseLaunch()
			assert.NoError(t, os.WriteFile(release, []byte("release"), 0600))
			f.api.programService().Wait()
		})
		require.Equal(t, "not_connected", pgConnection(t, pgProgramEntryByID(t, f, "github"))["state"])
		a := pgProgramsSessionCall(t, f, http.MethodPut, "/api/v1/programs/github/credential", `{"value":"tok_valid_12345"}`)
		require.Equal(t, http.StatusOK, a.Code, "actual person paste A: %s", a.Body.String())
		select {
		case <-paused:
		case <-time.After(5 * time.Second):
			t.Fatal("instrument: A's actual reserved launch did not enter the pre-process barrier")
		}
		pending := pgConnection(t, pgProgramEntryByID(t, f, "github"))
		require.Equal(t, float64(1), pending["generation"], "actual A committed generation1 before the barrier")
		require.Equal(t, "checking", pending["state"])
		require.Zero(t, watch.drain(t), "instrument: A is paused before even the kernel opens its executable")
		require.Zero(t, f.ghStarted(), "instrument: no child has run yet")

		b := pgProgramsSessionCall(t, f, http.MethodPut, "/api/v1/programs/github/credential", `{"value":"tok_new_654321","expected_generation":1}`)
		require.Equal(t, http.StatusOK, b.Code, "actual person paste B must advance while A's launch is paused: %s", b.Body.String())
		require.Eventually(t, func() bool {
			p, readErr := os.ReadFile(ready)
			return readErr == nil && string(p) == "B_CHECK_RUNNING"
		}, 5*time.Second, 10*time.Millisecond, "instrument: the real B check started and is held")
		require.Greater(t, watch.drain(t), 0, "instrument: this SAME kernel executable-open observer sees the actual B start")
		require.Equal(t, 1, f.ghStarted(), "only B reached its first userspace instruction while A was held")
		pending = pgConnection(t, pgProgramEntryByID(t, f, "github"))
		require.Equal(t, float64(2), pending["generation"])
		require.Equal(t, "checking", pending["state"])
		releaseLaunch()
		require.NoError(t, os.WriteFile(release, []byte("release"), 0600))
		f.api.programService().Wait() // Join BOTH actual launch goroutines, not a scheduler delay.

		lateOpens := watch.drain(t)
		assert.Zero(t, lateOpens, "S139: A's superseded launch must start no process; a kernel executable-open still counts if cancellation kills it before its own start marker")
		final := pgConnection(t, pgProgramEntryByID(t, f, "github"))
		assert.Equal(t, "connected", final["state"], "S139: late A must not stop B or leave generation2 checking")
		assert.Equal(t, float64(2), final["generation"])
		assert.Equal(t, "account-b", final["account_label"])
		// A (generation 1) never ran and reports no result; B (generation 2) is the one
		// finished check (ruling H50, ruling-check-delta item 5).
		pgAssertSupersededAudit(t, f, pgSupersededWant{
			results:    []pgResultWant{{generation: 2, revision: f.api.programService().Connection("github").CheckRevision, outcome: "connected", reason: "account_named"}},
			superseded: []int64{1},
			unreported: []int64{1},
		})
		t.Logf("actual A reserved/paused; actual B started; both launches joined; late_executable_opens=%d final_generation=%v final_state=%v", lateOpens, final["generation"], final["state"])
	})
}
