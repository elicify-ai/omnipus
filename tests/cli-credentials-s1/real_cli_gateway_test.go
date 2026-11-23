package clicredentialss1_test

import (
	"net/http"
	"os/exec"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/programs"
	"github.com/elicify-ai/omnipus/tests/testutil/programstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These are actual app.Main CLI/gateway processes, not re-executed Store-only
// test children. The signal-file seam only pauses the real CLI transaction.
func TestProgramsRealCLIAndGateway(t *testing.T) {
	bin := buildActualCLI(t)
	t.Run("S136_actual_CLI_held_read_gateway_paste_release", func(t *testing.T) {
		f := newRealGatewayFixture(t, bin)
		before := cliStoreSnapshot(t, f)
		cli, release := startHeldCLI(t, f)
		t.Cleanup(release)
		f.clearAdmission(t)
		paste := startActualPaste(f)
		t.Cleanup(func() { release(); paste.wait(t) })
		f.awaitAdmission(t)
		select {
		case <-paste.done:
			t.Fatalf("S136 paste returned while actual CLI was held after-read: status=%d err=%v", paste.result.status, paste.err)
		case <-time.After(time.Second):
		}
		require.Equal(t, before, cliStoreSnapshot(t, f), "while CLI is paused, token/generation/metadata/receipts are unchanged")
		release()
		require.NoError(t, cli.wait(t), "%s", cli.output.String())
		w := paste.wait(t)
		require.Equal(t, http.StatusOK, w.status, "%s", w.body)
		f.awaitConnected(t, 2, "hubot")
		after := cliStoreSnapshot(t, f)
		ref := programs.TokenReference("github")
		require.Equal(t, "y", after.Values["OTHER"], "the actual CLI write survived")
		require.Equal(t, "tok_new_67890", after.Values[ref], "the person's actual paste survived")
		require.Equal(t, int64(2), after.Generations[ref])
		require.Equal(t, before.Metadata[ref].Recipient, after.Metadata[ref].Recipient, "the program-owned binding survives replacement")
		require.Equal(t, before.Receipts, after.Receipts, "both real historical receipt carriers survive the stale-writer hazard")
		removeStoreRef(&before, ref)
		removeStoreRef(&after, ref)
		removeStoreRef(&after, "OTHER")
		require.Equal(t, before, after, "no unrelated value/private selection/options/inventory/receipt changed")
	})
	t.Run("S146_actual_CLI_busy_nonzero_no_late_write", func(t *testing.T) {
		// The negative and within-bound positive use separate fresh homes.
		f := newRealGatewayFixture(t, bin)
		before := cliStoreSnapshot(t, f)
		holder, release := startHeldCLI(t, f)
		t.Cleanup(release)
		start := time.Now()
		subject := startActualCLI(t, bin, f.env, "credentials", "set", "TARGET", "x")
		err := subject.wait(t)
		elapsed := time.Since(start)
		var exit *exec.ExitError
		assert.ErrorAs(t, err, &exit, "busy is a real nonzero CLI exit, not a JSON error with process success")
		if exit != nil {
			assert.NotZero(t, exit.ExitCode())
		}
		assert.Contains(t, subject.output.String(), "The credential store is busy. Try again.", "the spec's truthful busy message reaches the operator")
		assert.GreaterOrEqual(t, elapsed, 4500*time.Millisecond)
		assert.LessOrEqual(t, elapsed, 5500*time.Millisecond)
		time.Sleep(max(time.Duration(0), 6*time.Second-time.Since(start)))
		release()
		require.NoError(t, holder.wait(t))
		time.Sleep(2 * time.Second)
		after := cliStoreSnapshot(t, f)
		require.Equal(t, "y", after.Values["OTHER"])
		removeStoreRef(&after, "OTHER")
		require.Equal(t, before, after, "busy CLI writes nothing, even two seconds after admission becomes possible")

		positive := newRealGatewayFixture(t, bin)
		shortHolder, shortRelease := startHeldCLI(t, positive)
		t.Cleanup(shortRelease)
		accepted := startActualCLI(t, bin, positive.env, "credentials", "set", "TARGET", "x")
		time.Sleep(time.Second)
		shortRelease()
		require.NoError(t, shortHolder.wait(t))
		require.NoError(t, accepted.wait(t), "%s", accepted.output.String())
		state := cliStoreSnapshot(t, positive)
		require.Equal(t, "x", state.Values["TARGET"], "the same real CLI succeeds when released within the bound")
		require.Equal(t, "y", state.Values["OTHER"])
	})
	t.Run("S146_crossprocess_gateway_503_no_check_no_late_write", func(t *testing.T) {
		f := newRealGatewayFixture(t, bin)
		before := cliStoreSnapshot(t, f)
		starts := f.checkStarts(t)
		holder, release := startHeldCLI(t, f)
		t.Cleanup(release)
		start := time.Now()
		w := f.call(t, http.MethodPut, "/api/v1/programs/github/credential", gen.ProgramCredentialRequest{Value: "tok_new_67890"})
		elapsed := time.Since(start)
		assert.Equal(t, http.StatusServiceUnavailable, w.status, "%s", w.body)
		assert.Contains(t, string(w.body), "The credential store is busy. Try again.")
		assert.GreaterOrEqual(t, elapsed, 4500*time.Millisecond)
		assert.LessOrEqual(t, elapsed, 5500*time.Millisecond)
		assert.Equal(t, before, cliStoreSnapshot(t, f), "failed paste has not published any token/generation/metadata/receipt")
		assert.Equal(t, starts, f.checkStarts(t), "a rejected paste starts no account check")
		time.Sleep(max(time.Duration(0), 6*time.Second-time.Since(start)))
		release()
		require.NoError(t, holder.wait(t))
		time.Sleep(2 * time.Second)
		after := cliStoreSnapshot(t, f)
		require.Equal(t, "y", after.Values["OTHER"])
		removeStoreRef(&after, "OTHER")
		require.Equal(t, before, after, "nothing lands late after the 503")
		require.Equal(t, starts, f.checkStarts(t), "no late check either")
		f.awaitConnected(t, 1, "octocat")

		positive := newRealGatewayFixture(t, bin)
		shortHolder, shortRelease := startHeldCLI(t, positive)
		t.Cleanup(shortRelease)
		positive.clearAdmission(t)
		paste := startActualPaste(positive)
		t.Cleanup(func() { shortRelease(); paste.wait(t) })
		positive.awaitAdmission(t)
		time.Sleep(time.Second)
		shortRelease()
		require.NoError(t, shortHolder.wait(t))
		accepted := paste.wait(t)
		require.Equal(t, http.StatusOK, accepted.status, "%s", accepted.body)
		positive.awaitConnected(t, 2, "hubot")
		require.Equal(t, "y", cliStoreSnapshot(t, positive).Values["OTHER"])
	})
}

type actualPaste struct {
	done   chan struct{}
	result actualHTTPResult
	err    error
}

func startActualPaste(f *realGatewayFixture) *actualPaste {
	p := &actualPaste{done: make(chan struct{})}
	go func() {
		p.result, p.err = f.do(http.MethodPut, "/api/v1/programs/github/credential", gen.ProgramCredentialRequest{Value: "tok_new_67890"})
		close(p.done)
	}()
	return p
}

func (p *actualPaste) wait(t *testing.T) actualHTTPResult {
	t.Helper()
	select {
	case <-p.done:
		require.NoError(t, p.err)
		return p.result
	case <-time.After(12 * time.Second):
		t.Fatal("actual paste did not return within the fixture join budget")
		return actualHTTPResult{}
	}
}

func cliStoreSnapshot(t *testing.T, f *realGatewayFixture) programstore.State {
	t.Helper()
	return programstore.Snapshot(t, f.store, f.canary, "s136-program-receipt")
}

func removeStoreRef(state *programstore.State, ref string) {
	delete(state.Values, ref)
	delete(state.Metadata, ref)
	delete(state.Generations, ref)
	kept := state.Inventory[:0]
	for _, item := range state.Inventory {
		if item.Reference != ref {
			kept = append(kept, item)
		}
	}
	state.Inventory = kept
}
