package tools

// Spec rev8 S88/FR013/FR014. Boundary authorized by the chief on 2026-11-23:
// actual ExecTool/Resolve/probe/Attach/child in a disposable non-evidence dir.
// This is NOT a full-loop git-push-through-evidence-guard acceptance claim.
import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/programs"
	"github.com/stretchr/testify/require"
)

func TestProgramGitEnv_RealExecBoundary(t *testing.T) {
	t.Run("S88_new_git_gets_the_helper", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Fatal("BLOCKED: S88's native Windows git executable fixture is not implemented; this POSIX process-edge fake cannot certify Windows FR014")
		}
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("OMNIPUS_HOME", home)
		for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS"} {
			t.Setenv(name, "")
			require.NoError(t, os.Unsetenv(name))
		}
		store := credentials.NewStore(filepath.Join(home, "credentials.json"))
		require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x49}, 32)))
		t.Cleanup(store.Close)
		_, err := store.CommitProgramCredential(programs.TokenReference("github"), "tok_valid_12345", nil)
		require.NoError(t, err)
		cfg := &config.Config{Agents: config.AgentsConfig{List: []config.AgentConfig{{ID: "dev", Programs: []string{"github"}}}}}
		cfg.Programs.WebChatAllowed = true
		bin := filepath.Join(home, "bin")
		require.NoError(t, os.Mkdir(bin, 0700))
		body := `#!/bin/sh
if [ "$#" -eq 1 ] && [ "$1" = --version ]; then printf 'git version 2.43.0\n'; exit 0; fi
[ "$#" -eq 1 ] && [ "$1" = push ] || exit 81
[ "$GH_TOKEN" = tok_valid_12345 ] || exit 82
[ "$GIT_CONFIG_COUNT" = 2 ] || exit 83
[ "$GIT_CONFIG_KEY_0" = credential.https://github.com.helper ] || exit 84
[ "$GIT_CONFIG_KEY_1" = credential.https://github.com.helper ] || exit 85
[ -z "$GIT_CONFIG_VALUE_0" ] || exit 86
case "$GIT_CONFIG_VALUE_1" in *tok_valid_12345*) exit 87;; esac
helper=${GIT_CONFIG_VALUE_1#?}
answer=$(printf 'protocol=https\nhost=github.com\n\n' | /bin/sh -c "$helper get") || exit 88
expected=$(printf 'username=x-access-token\npassword=%s\n' "$GH_TOKEN")
[ "$answer" = "$expected" ] || exit 89
for operation in store erase; do
 answer=$(printf 'protocol=https\nhost=github.com\n\n' | /bin/sh -c "$helper $operation") || exit 90
 [ -z "$answer" ] || exit 91
done
printf 'S88_GIT_EXECUTED'
`
		require.NoError(t, os.WriteFile(filepath.Join(bin, "git"), []byte(body), 0700))
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		project := filepath.Join(home, "ordinary-project")
		require.NoError(t, os.Mkdir(project, 0700))
		// The source resolves the actual encrypted store and trusted test turn;
		// it does not fabricate a TurnPrograms or helper environment.
		source := func(ctx context.Context, private []string) *programs.TurnPrograms {
			p, resolveErr := programs.Resolve(cfg, store, nil, programs.TurnRequest{AgentID: ToolAgentID(ctx), Provenance: ToolTurnProvenance(ctx), PrivateVariables: private})
			require.NoError(t, resolveErr)
			return p
		}
		tool, err := NewExecToolWithDeps(project, true, cfg, ExecToolDeps{Programs: source})
		require.NoError(t, err)
		origin := NewTurnOrigin()
		origin.AddProvenance(bus.NewProvenance(bus.OriginWeb))
		ctx := WithTurnOrigin(WithAgentID(context.Background(), "dev"), origin)
		result := tool.Execute(ctx, map[string]any{"command": "git push"})
		require.False(t, result.IsError, "real ExecTool composition/child boundary failed: %s", result.ContentForLLM())
		require.Equal(t, "S88_GIT_EXECUTED", result.ContentForLLM(), "S88: real ExecTool child received count2, both managed entries, and get-only helper semantics; full-loop guarded-project admission remains a separate UAT row")
	})
}
