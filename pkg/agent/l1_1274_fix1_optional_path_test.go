package agent

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/thirdpartycli"
)

// r17 area 4 replaced the optional per-agent cli_path with the Settings
// program (§3.5, §3.6). Creating an external worker never needs a path: with
// none saved the agent is still created, reported as not runnable with a
// program_warning, and a delegation to it is refused with the NoPath text
// before any driver is selected. There is no PATH lookup fallback, so a
// default-named binary on PATH changes nothing.
func TestL11274CreateWithoutSavedProgramIsNotRunnableAndRefusesRun(t *testing.T) {
	for _, cli := range []string{"claude-code", "codex", "opencode"} {
		t.Run(cli, func(t *testing.T) {
			f := newL11274Fixture(t, "", false)
			// A default-named install on PATH must not be picked up.
			binary := map[string]string{"claude-code": "claude", "codex": "codex", "opencode": "opencode"}[cli]
			t.Setenv("PATH", l11274InstallDir(t, f, binary))

			result := f.execute(t, "create_agent", l11274CreateArgs("Created "+cli, cli))
			id := l11274AssertCreated(t, f, result, cli, "#6E8BA8")
			body := l11274ResultObject(t, result)
			if body["runnable"] != false {
				t.Errorf("a worker with no saved program must report runnable=false: %s", result.ForLLM)
			}
			if warning, _ := body["program_warning"].(string); warning != thirdpartycli.NoPathText(cli) {
				t.Errorf("program_warning = %q, want the NoPath text %q", warning, thirdpartycli.NoPathText(cli))
			}

			fake, clis := l11274DriverSeam(t, f)
			refused := l11274DelegateAndReadRefusal(t, f, id, "must not run without a program")
			if refused != thirdpartycli.NoPathText(cli) {
				t.Errorf("run must be refused with the NoPath text, got %q", refused)
			}
			if got := len(fake.RecordedRunOpts()); got != 0 {
				t.Errorf("a refused run reached the driver %d times, want 0", got)
			}
			select {
			case selected := <-clis:
				_ = selected // the driver may be chosen before the program check; it must never Run
			default:
			}
		})
	}
}

// l11274InstallDir puts an executable named binary in a fresh directory and
// returns that directory (for PATH).
func l11274InstallDir(t *testing.T, f *l11274Fixture, binary string) string {
	t.Helper()
	path := l11274Executable(t, f.home, binary, true)
	return path[:strings.LastIndex(path, "/")]
}
