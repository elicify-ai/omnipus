package agent

// Spec rev8 S84 / D-3 row13: one visible git word grants BOTH programs'
// sites, and real git resolves each host through the correct managed helper.
import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/programs"
	"github.com/stretchr/testify/require"
)

func TestProgramNetwork_ActualSharedGitWord(t *testing.T) {
	t.Run("S84_shared_program_word_per_site_helper", func(t *testing.T) {
		programsIsolateGit(t)
		realGit, err := exec.LookPath("git")
		require.NoError(t, err, "actual installed git is required to test credential-helper behavior")
		x := newProgramsNetworkFixture(t, false, true, false)
		cfg := x.f.al.GetConfig()
		cfg.Programs.Entries = append(cfg.Programs.Entries, config.ProgramEntryConfig{
			ID: "gitlab", Kind: "custom", DisplayName: "GitLab", ProgramWords: []string{"git"}, TokenVariables: []string{"GITLAB_TOKEN"}, NetworkSites: []string{"gitlab.com"}, Git: &config.ProgramGitConfig{Host: "gitlab.com"},
			IdentityCheck: &config.ProgramIdentityCheckConfig{ProgramWord: "git", Args: []string{"--version"}, TimeoutSeconds: 20, MaxOutputBytes: 4096},
		})
		cfg.Agents.List[0].Programs = []string{"github", "gitlab"}
		bin := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))[0]
		// This is only the remote executable edge. It invokes INSTALLED git for
		// credential lookup, using the real child's exact managed environment;
		// no helper string, token map or git config entry is synthesized here.
		body := fmt.Sprintf(`#!/bin/sh
if [ "$#" -eq 1 ] && [ "$1" = --version ]; then printf 'git version 2.43.0\n'; exit 0; fi
[ "$#" -eq 2 ] && [ "$1" = ls-remote ] && [ "$2" = https://gitlab.com/o/r ] || exit 81
[ "$GH_TOKEN" = tok_valid_12345 ] || exit 82
[ "$GITLAB_TOKEN" = tok_gitlab_67890 ] || exit 83
for host in gitlab.com github.com; do
 answer=$(printf 'protocol=https\nhost=%%s\n\n' "$host" | %q credential fill) || exit 84
 case "$host" in gitlab.com) expected_token=$GITLAB_TOKEN;; github.com) expected_token=$GH_TOKEN;; esac
 actual_user=$(printf '%%s\n' "$answer" | grep '^username=')
 actual_password=$(printf '%%s\n' "$answer" | grep '^password=')
 [ "$actual_user" = username=x-access-token ] || exit 85
 [ "$actual_password" = "password=$expected_token" ] || exit 86
done
printf '%%s' "${HTTPS_PROXY:-}" > %q
while [ ! -f %q ]; do sleep 0.02; done
printf 'S84_BOTH_SITE_HELPERS_RAN'
`, realGit, x.child, x.release)
		require.NoError(t, os.WriteFile(filepath.Join(bin, "git"), []byte(body), 0700))
		svc := programs.NewService(x.f.al.GetConfig, func() (*credentials.Store, error) { return x.f.al.sysagentDeps.CredStore, nil }, filepath.Join(x.f.home, "programs", "connections.json"))
		_, err = svc.Publish("gitlab", "tok_gitlab_67890", nil, nil)
		require.NoError(t, err)
		svc.Wait()
		require.Equal(t, "connected", svc.Connection("gitlab").State, "S84 Given: the custom entry is really saved and checked")
		require.Equal(t, "connected", svc.Connection("github").State, "S84 Given: the original checked GitHub connection survives")
		finish := x.start(t, "git ls-remote https://gitlab.com/o/r")
		token := x.childToken(t, true)
		require.Empty(t, x.approver.snapshot(), "S84: the shared word automatically grants the union; no approval")
		x.probe(t, token, "gitlab.com", true)
		x.probe(t, token, "github.com", true)
		x.probe(t, token, "api.github.com", true)
		x.probe(t, token, "example.org", false)
		x.probe(t, "", "gitlab.com", false)
		require.Equal(t, "S84_BOTH_SITE_HELPERS_RAN", finish(), "S84: actual child validated both exact host-specific credentials without exposing them")
		x.probe(t, token, "gitlab.com", false)
		x.probe(t, token, "github.com", false)
		x.probe(t, token, "api.github.com", false)
		require.Empty(t, x.approver.snapshot(), "S84: no delayed/second approval")
		t.Log("actual shared git word: both host helpers used correct separate saved tokens; GitLab/GitHub grants allowed during the real child and revoked after it")
	})
}
