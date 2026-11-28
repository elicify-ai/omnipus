package agent

// Spec rev 8 S05/S31: real store failures must not turn into successful writes
// or prevent an otherwise ordinary child from running. The filesystem fault
// sits at the exported transaction hook, not inside the service or resolver.
import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/require"
)

func TestProgramStoreFailure_ActualChildren(t *testing.T) {
	t.Run("S31_store_unavailable_notice_echo_still_works", func(t *testing.T) {
		f := newProgramsTurnFixture(t)
		require.Equal(t, "16", programsChildCount(t, f, "GH_TOKEN", "s31-control"), "instrument: this agent's actual child received the saved token before the store failed")
		f.al.sysagentDeps.CredStore.Close()
		_, err := f.al.sysagentDeps.CredStore.Get("program.github")
		require.ErrorIs(t, err, credentials.ErrStoreLocked, "instrument: the real store now refuses reads")
		sid := f.mainSession("sales", "dev")
		report := filepath.Join(f.home, "workspaces", "sales", "work", "s31-count")
		command := fmt.Sprintf("echo ok; printenv GH_TOKEN | wc -c > %q", report)
		f.provider.setScript(sid, []providers.LLMResponse{wu5BashCall("s31-echo", command), wu5Stop("echo completed")})
		programsWebTurn(t, f, sid, "echo ok")
		f.waitForCalls(sid, 2, "S31's actual ordinary child")
		result := strings.ReplaceAll(f.provider.resultForCall(sid, "s31-echo"), "\r\n", "\n")
		first, _, _ := strings.Cut(result, "\n")
		require.Equal(t, "ok", first, "S31: the ordinary command still runs")
		require.Contains(t, result, "Connected programs were not loaded for this command: the credential store is unavailable.", "S31's exact spec notice")
		raw, err := os.ReadFile(report)
		require.NoError(t, err, "the child, not a denied tool, produced the count")
		require.Equal(t, "0", strings.TrimSpace(string(raw)), "S31: failed store reads supply no program variable")
	})

	t.Run("S05_failed_save_next_printenv_keeps_old_token", func(t *testing.T) {
		f := newProgramsTurnFixture(t)
		svc := programsTurnService(t, f)
		before := svc.Connection("github")
		require.Equal(t, int64(1), before.Generation)
		require.Equal(t, "octocat", before.AccountLabel)
		path := filepath.Join(f.home, "credentials.json")
		backup := path + ".s05-held"
		var held bool
		restore := sync.OnceFunc(func() {
			if held {
				require.NoError(t, os.Remove(path), "remove the fixture's empty rename blocker")
				require.NoError(t, os.Rename(backup, path), "restore the exact pre-write encrypted file")
			}
		})
		t.Cleanup(restore)
		previous := credentials.TransactHook
		t.Cleanup(func() { credentials.TransactHook = previous })
		var inject sync.Once
		credentials.TransactHook = func(stage string) {
			if stage != "after-read" {
				return
			}
			inject.Do(func() {
				require.NoError(t, os.Rename(path, backup))
				require.NoError(t, os.Mkdir(path, 0700))
				held = true
			})
		}
		_, err := svc.Publish("github", "tok_other_67890", nil, nil)
		credentials.TransactHook = previous
		require.True(t, held, "instrument: the actual transaction reached its in-lock read before the atomic rename was blocked")
		restore()
		require.Error(t, err, "S05: the real filesystem write failure must not be reported as saved")
		require.Equal(t, before, svc.Connection("github"), "S05: failed save preserves generation 1 and octocat")
		require.Equal(t, "16", programsChildCount(t, f, "GH_TOKEN", "s05-after-failure"), "S05: the next real child still uses the original fifteen-byte token plus newline")
	})
}
