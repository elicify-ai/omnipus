package agent

// Receiver/observation instrument ONLY. A plain installed-Git client uses a
// test-owned environment helper to prove the external fixture can accept an
// authenticated smart push. This does not exercise AgentLoop admission, Resolve
// or production GitSupport and MUST NOT replace the still-failing S20 row.
import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProgramGitHelper_HTTPSReceiverInstrument(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Fatal("BLOCKED: native Windows HTTPS Git receiver/observer instrument is not implemented; the POSIX helper cannot certify that platform")
	}
	git, err := exec.LookPath("git")
	require.NoError(t, err)
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg")
	require.NoError(t, os.MkdirAll(filepath.Join(xdg, "git"), 0700))
	receiver := newProgramsGitReceiver(t, git, home)
	proxy := programsGitLocalProxy(t, receiver.server.Listener.Addr().String())
	global := filepath.Join(home, ".gitconfig")
	for key, value := range map[string]string{
		"s20.observer": "global-control", "http.sslCAInfo": receiver.cert, "http.proxy": proxy,
	} {
		programsGitSetup(t, git, home, "", "config", "--file", global, key, value)
	}
	programsGitSetup(t, git, home, "", "config", "--file", filepath.Join(xdg, "git", "config"), "s20.xdg", "xdg-control")
	project := t.TempDir()
	programsGitSetup(t, git, home, "", "init", project)
	require.NoError(t, os.WriteFile(filepath.Join(project, "s20.txt"), []byte("S20 receiver positive control\n"), 0600))
	programsGitSetup(t, git, home, project, "add", "s20.txt")
	programsGitSetup(t, git, home, project, "-c", "user.name=Daniel Piatkowski", "-c", "user.email=10800669+daniel-piatkowski-ai@users.noreply.github.com", "commit", "-m", "S20 receiver instrument")
	source := programsGitSetup(t, git, home, project, "rev-parse", "HEAD")
	programsGitSetup(t, git, home, project, "remote", "add", "origin", "https://github.com/s20.git")
	observed := programsGitObserver(t, git, project)
	// This canned helper belongs only to the receiver's test instrument. It
	// cannot prove that the product installed its own environment-only helper.
	helper := `!f() { test "$1" = get || exit 0; printf 'username=x-access-token\npassword=%s\n' "$GH_TOKEN"; }; f`
	for _, args := range [][]string{{"--version"}, {"push", "origin", "HEAD:refs/heads/s20"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = project
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_CONFIG_HOME=" + xdg,
			"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GH_TOKEN=tok_valid_12345",
			"GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=credential.https://github.com.helper", "GIT_CONFIG_VALUE_0=",
			"GIT_CONFIG_KEY_1=credential.https://github.com.helper", "GIT_CONFIG_VALUE_1=" + helper}
		out, runErr := cmd.CombinedOutput()
		require.NoError(t, runErr, "plain-Git receiver instrument: %s", out)
	}
	users, packs, rejected := receiver.state.snapshot()
	require.NotEmpty(t, users)
	for _, user := range users {
		require.Equal(t, "x-access-token", user)
	}
	require.Equal(t, 1, packs)
	require.Positive(t, rejected, "instrument: actual Git first receives the authentication challenge")
	require.Equal(t, source, programsGitSetup(t, git, home, "", "--git-dir", receiver.repo, "rev-parse", "refs/heads/s20"))
	programsGitObserveArguments(t, observed)
	programsGitObserveConfig(t, observed, home, project, filepath.Dir(receiver.repo))
	t.Log("RECEIVER INSTRUMENT ONLY: installed Git transferred a real pack/ref over verified TLS with exact synthetic account authentication; config/URL/argv readers positively exercised. Product S20 admission is not certified by this control.")
}
