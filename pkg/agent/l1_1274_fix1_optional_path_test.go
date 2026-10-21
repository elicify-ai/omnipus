package agent

import (
	"path/filepath"
	"testing"
)

// Oracle: chief F04 / documented optional path rule. Creation must accept
// omission AND explicit empty path, save that request, and delegate without
// converting it into an invented install path. The runner's real default-name
// selection and isolated PATH lookup are independently tested in pkg/agent/runner.
func TestL11274OptionalCLIPathCreatesAndDelegatesEachWorker(t *testing.T) {
	for _, cli := range []string{"claude-code", "codex", "opencode"} {
		for _, supplied := range []string{"omitted", "empty"} {
			t.Run(cli+"/"+supplied, func(t *testing.T) {
				f := newL11274Fixture(t, "", false)
				// Independent documented default names, not the output of the
				// resolver under test. Only the test install is on this PATH.
				binary := map[string]string{"claude-code": "claude", "codex": "codex", "opencode": "opencode"}[cli]
				install := l11274Executable(t, f.home, binary, true)
				t.Setenv("PATH", filepath.Dir(install))
				args := l11274CreateArgs("Created "+cli, cli, "")
				if supplied == "omitted" {
					delete(args, "cli_path")
				}
				result := f.execute(t, "create_agent", args)
				id := l11274AssertCreated(t, f, result, cli, "", "#6E8BA8")
				l11274AssertExternalDelegate(t, f, id, cli, "")
			})
		}
	}
}
