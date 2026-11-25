package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/require"
)

// Setup never receives the managed token. Commands address only disposable
// fixture repositories, with host-global/system Git configuration excluded.
func programsGitSetup(t *testing.T, git, home, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(git, args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "fixture git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

type programsGitObservation struct {
	argv, config, trace string
}

func programsGitObserver(t *testing.T, git, project string) programsGitObservation {
	t.Helper()
	observed := programsGitObservation{
		argv: filepath.Join(project, "s20-argv"), config: filepath.Join(project, "s20-config-observed"),
		trace: filepath.Join(project, "s20-trace2"),
	}
	bin := t.TempDir()
	// Observe then exec the installed binary, including the genuine version
	// probe. Never synthesize a Git result, helper or credential configuration.
	script := fmt.Sprintf(`#!/bin/sh
export GIT_CONFIG_NOSYSTEM=1 GIT_TERMINAL_PROMPT=0
export GIT_TRACE2_EVENT=%q
printf '%%s\000' git "$@" >> %q || exit 71
%q config --list --show-origin >> %q || exit 72
exec %q "$@"
`, observed.trace, observed.argv, git, observed.config, git)
	require.NoError(t, os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return observed
}

func programsGitMountedTurn(t *testing.T, f *wu5Fixture, id, cwd, command string) string {
	t.Helper()
	sid := f.mainSession("sales", "dev")
	call := wu5BashCall(id, command)
	call.ToolCalls[0].Arguments["cwd"] = cwd
	f.provider.setScript(sid, []providers.LLMResponse{call, wu5Stop("Git transfer probe complete")})
	input := programsWebInput()
	input.SessionID, input.ChatID = sid, sid
	input.Content = "push the fixture commit from the approved project"
	// Name the addressed workspace explicitly, as the trusted server carrier
	// does. This supplies identity/context, never a fabricated mount or grant.
	input.Metadata = map[string]string{"workspace_id": "sales"}
	_, _, err := f.al.processMessage(context.Background(), input)
	require.NoError(t, err)
	f.waitForCalls(sid, 2, id+" actual mounted-project tool result")
	return f.provider.resultForCall(sid, id)
}

func programsGitObserveArguments(t *testing.T, observed programsGitObservation) {
	t.Helper()
	argv, err := os.ReadFile(observed.argv)
	require.NoError(t, err)
	words := strings.Split(string(argv), "\x00")
	require.Contains(t, words, "--version", "instrument: the actual version probe used this installed Git observer")
	require.Contains(t, words, "push")
	require.Contains(t, words, "HEAD:refs/heads/s20")
	require.False(t, strings.Contains(string(argv), "tok_valid_12345"), "token entered observed Git argv")

	trace, err := os.Open(observed.trace)
	require.NoError(t, err)
	defer func() { require.NoError(t, trace.Close()) }()
	scanner := bufio.NewScanner(trace)
	var args []string
	remote, helper := false, false
	for scanner.Scan() {
		// not-wire-format: installed Git's local trace2, not a gateway carrier.
		var event struct {
			Event string   `json:"event"`
			Argv  []string `json:"argv"`
		}
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &event))
		if event.Event != "start" && event.Event != "child_start" {
			continue
		}
		args = append(args, event.Argv...)
		for _, arg := range event.Argv {
			remote = remote || strings.Contains(arg, "remote-https")
			helper = helper || (strings.Contains(arg, "x-access-token") && strings.Contains(arg, "GH_TOKEN") && strings.Contains(arg, "get"))
		}
	}
	require.NoError(t, scanner.Err())
	require.True(t, remote, "instrument: trace2 observed the real HTTPS transport subprocess")
	require.True(t, helper, "instrument: trace2 observed actual configured credential helper argv")
	require.Contains(t, args, "https://github.com/s20.git", "instrument: remote URL argv is recorded")
	for _, arg := range args {
		require.False(t, strings.Contains(arg, "tok_valid_12345"), "token entered a traced subprocess argv")
	}
}

func programsGitObserveConfig(t *testing.T, observed programsGitObservation, roots ...string) {
	t.Helper()
	config, err := os.ReadFile(observed.config)
	require.NoError(t, err)
	text := string(config)
	require.Contains(t, text, "s20.observer=global-control", "instrument: global config reader is active")
	require.Contains(t, text, "s20.xdg=xdg-control", "instrument: XDG config reader is active")
	require.Contains(t, text, "remote.origin.url=https://github.com/s20.git", "instrument: repository config/remote reader is active")
	require.False(t, strings.Contains(text, "tok_valid_12345"), "token entered observed Git configuration")
	require.NotContains(t, strings.ToLower(text), "include.path=", "fixture must not leave additional config files outside its observation roots")
	require.NotContains(t, strings.ToLower(text), "includeif.", "fixture must not leave conditional config files outside its observation roots")
	seen := map[string]bool{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() || (d.Name() != "config" && d.Name() != ".gitconfig") {
				return nil
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			seen[path] = true
			require.False(t, strings.Contains(string(body), "tok_valid_12345"), "token entered a fixture Git config file")
			return nil
		})
		require.NoError(t, err)
	}
	require.GreaterOrEqual(t, len(seen), 4, "instrument: global, XDG, source and destination configs must really be read")
}
