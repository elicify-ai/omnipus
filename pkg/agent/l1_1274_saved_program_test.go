package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/thirdpartycli"
)

// r17 area 4: create_agent has no cli_path argument. An agent that sends one
// is refused naming the property, and nothing is created.
func TestL11274CreateRefusesAnAgentSuppliedCLIPath(t *testing.T) {
	for _, cli := range []string{"claude-code", "codex", "opencode"} {
		t.Run(cli, func(t *testing.T) {
			f := newL11274Fixture(t, "", false)
			path := l11274Executable(t, f.home, cli, true)
			args := l11274CreateArgs("Rejected worker", cli)
			args["cli_path"] = path
			before, _, err := agentstore.New(f.home).List()
			if err != nil {
				t.Fatal(err)
			}
			result := f.execute(t, "create_agent", args)
			if result == nil || !result.IsError || !strings.Contains(result.ForLLM, "cli_path") {
				t.Fatalf("an agent-supplied cli_path must be refused naming the property, got %+v", result)
			}
			l11274AssertAbsent(t, f, "rejected-worker")
			after, _, err := agentstore.New(f.home).List()
			if err != nil || len(after) != len(before) {
				t.Errorf("refused create changed the agent roster: before=%d after=%d err=%v", len(before), len(after), err)
			}
		})
	}
}

// r17 §3.6: a saved program that fails its checks refuses the run with the
// CheckFailed text naming the check and the saved path, before the driver.
func TestL11274SavedProgramFailingChecksRefusesRun(t *testing.T) {
	for _, cli := range []string{"claude-code", "codex", "opencode"} {
		for _, class := range []string{"nonexistent", "directory", "non-executable"} {
			t.Run(cli+"/"+class, func(t *testing.T) {
				f := newL11274Fixture(t, "", false)
				valid := l11274Executable(t, f.home, cli, true)
				path, check := filepath.Join(filepath.Dir(valid), "missing-"+cli), thirdpartycli.CheckNotFound
				switch class {
				case "directory":
					path, check = filepath.Dir(valid), thirdpartycli.CheckNotRegularFile
				case "non-executable":
					path, check = l11274Executable(t, f.home, cli, false), thirdpartycli.CheckNotExecutable
				}
				if _, err := os.Lstat(path); class != "nonexistent" && err != nil {
					t.Fatalf("SETUP: fixture %s: %v", class, err)
				}
				f.setProgram(t, cli, path)
				result := f.execute(t, "create_agent", l11274CreateArgs("Created "+cli, cli))
				id := l11274AssertCreated(t, f, result, cli, "#6E8BA8")
				if warning, _ := l11274ResultObject(t, result)["program_warning"].(string); !strings.Contains(warning, check) {
					t.Errorf("program_warning must name the %s check, got %q", check, warning)
				}
				fake, _ := l11274DriverSeam(t, f)
				refused := l11274DelegateAndReadRefusal(t, f, id, "must not run a bad program")
				if !strings.Contains(refused, check) || !l11274ReasonNamesPath(refused, path) {
					t.Errorf("run refusal must name check %q and path %q, got %q", check, path, refused)
				}
				if got := len(fake.RecordedRunOpts()); got != 0 {
					t.Errorf("a refused run reached the driver %d times, want 0", got)
				}
			})
		}
	}
}

// l11274DelegateAndReadRefusal delegates to the worker and returns the
// Omnipus-authored program refusal (r17 section 3.6 step 5, chief ruling A):
// the delegation itself is refused as the parent's tool error BEFORE any child
// session is created, so no child record exists and the driver is never reached.
func l11274DelegateAndReadRefusal(t *testing.T, f *l11274Fixture, id, task string) string {
	t.Helper()
	result := l11274Delegate(t, f, id, task, true)
	if result == nil || !result.IsError {
		t.Fatalf("an unrunnable external worker must be refused as the delegation tool error, got %+v", result)
	}
	records, err := f.al.GetSessionLifecycleStore().List(session.LifecycleFilter{AgentID: id})
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range records {
		t.Errorf("a refused delegation created child session %q for %s", rec.SessionID, id)
	}
	return result.ForLLM
}
