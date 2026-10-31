//go:build darwin

package tools

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/environmentsetup"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	fixture "github.com/elicify-ai/omnipus/tests/l1_red/protectedfixture"
)

func wu23R1Quote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func wu23R1BashSuccess(t *testing.T, result *ToolResult, want string) {
	t.Helper()
	if result.IsError || result.ExitCode == nil || *result.ExitCode != 0 || result.ForLLM != want {
		t.Fatalf("real next bash command: output=%q exit=%v error=%t want=%q / exit 0", result.ForLLM, result.ExitCode, result.IsError, want)
	}
}

func wu23R1WorkspaceInstallRun(t *testing.T, god bool) {
	t.Helper()
	if wu23R1Isolated(t) {
		return
	}
	f := wu23R1Fixture(t)
	bash := wu23R1Exec(t, f, god)
	ctx := wu23Context(f)
	t.Run("real_bash_workspace_control", func(t *testing.T) {
		wu23R1BashSuccess(t, wu23R1Bash(t, bash, ctx, "printf 'WORKSPACE_CONTROL=ok\\n'"), "WORKSPACE_CONTROL=ok\n")
	})
	// Omit scope on purpose: the founder required the DEFAULT workspace path.
	command := `set -eu
mkdir -p "$OMNIPUS_ENV_PREFIX/bin"
printf '#!/bin/sh\nprintf "WORKSPACE_PROGRAM=ok\\n"\n' > "$OMNIPUS_ENV_PREFIX/bin/wu23-r1-workspace"
chmod 700 "$OMNIPUS_ENV_PREFIX/bin/wu23-r1-workspace"`
	setup, id, completion := wu23R1Install(t, f, god, command, "")
	if completion.IsError {
		t.Fatalf("default workspace install must succeed under god=%t: %s", god, completion.ForLLM)
	}
	prefix := filepath.Join(f.Work, ".omnipus", "env")
	path := filepath.Join(prefix, "bin", "wu23-r1-workspace")
	data, err := os.ReadFile(path)
	wantProgram := "#!/bin/sh\nprintf \"WORKSPACE_PROGRAM=ok\\n\"\n"
	if err != nil || string(data) != wantProgram {
		t.Fatalf("default workspace install location/bytes=%q want=%q err=%v", data, wantProgram, err)
	}
	poll, pollResult := pollAction(t, setup, ctx, id)
	if pollResult.IsError || poll.Status != "done" || poll.ExitCode != 0 {
		t.Fatalf("real setup did not publish truthful completion: %+v %s", poll, pollResult.ForLLM)
	}
	read := setup.Execute(ctx, map[string]any{"action": "read", "session_id": id})
	if read.IsError || !strings.Contains(read.ForLLM, prefix) {
		t.Fatalf("default scope must remain workspace in real session output: %s", read.ForLLM)
	}
	// Bare program name, not a manually supplied PATH or direct Derive input.
	wu23R1BashSuccess(t, wu23R1Bash(t, bash, ctx, "wu23-r1-workspace"), "WORKSPACE_PROGRAM=ok\n")
}

func wu23R1SharedInstallRun(t *testing.T, god bool) {
	t.Helper()
	if wu23R1Isolated(t) {
		return
	}
	f := wu23R1Fixture(t)
	wu23R1UnlockPublished(t, f.Home)
	bash := wu23R1Exec(t, f, god)
	ctx := wu23Context(f)
	t.Run("real_bash_workspace_control", func(t *testing.T) {
		wu23R1BashSuccess(t, wu23R1Bash(t, bash, ctx, "printf 'WORKSPACE_CONTROL=ok\\n'"), "WORKSPACE_CONTROL=ok\n")
	})
	command := `set -eu
mkdir -p "$OMNIPUS_ENV_PREFIX/bin"
cat > "$OMNIPUS_ENV_PREFIX/bin/wu23-r1-installed-shared" <<'PROGRAM'
#!/bin/sh
if [ "${1-}" = --probe-write ]; then
  if { printf bad > "$0"; } 2>/dev/null; then printf 'SHARED_WRITE=allowed\n'; else printf 'SHARED_WRITE=denied\n'; fi
  exit 0
fi
printf 'SHARED_PROGRAM=ok\n'
PROGRAM
chmod 700 "$OMNIPUS_ENV_PREFIX/bin/wu23-r1-installed-shared"`
	setup, id, completion := wu23R1Install(t, f, god, command, "shared")
	if completion.IsError {
		t.Fatalf("real shared install must succeed under god=%t: %s", god, completion.ForLLM)
	}
	rt, err := environmentsetup.RuntimeEnvPaths(f.Home, f.Work)
	if err != nil || len(rt.Shared) != 1 {
		t.Fatalf("completed shared install not published/discovered: %+v err=%v", rt.Shared, err)
	}
	poll, pollResult := pollAction(t, setup, ctx, id)
	if pollResult.IsError || poll.Status != "done" || poll.ExitCode != 0 {
		t.Fatalf("real shared publication not truthful: %+v %s", poll, pollResult.ForLLM)
	}
	program := filepath.Join(rt.Shared[0].Dir, "bin", "wu23-r1-installed-shared")
	wu23R1OwnerWritable(t, program)
	wu23R1BashSuccess(t, wu23R1Bash(t, bash, ctx, "wu23-r1-installed-shared"), "SHARED_PROGRAM=ok\n")
	// The installed real child probes its own $0, so neither an ordinary
	// permission deny nor a command-text refusal can false-green the kernel.
	wu23R1BashSuccess(t, wu23R1Bash(t, bash, ctx, "wu23-r1-installed-shared --probe-write"), "SHARED_WRITE=denied\n")
}

func wu23R1DocumentToolsRun(t *testing.T, god bool) {
	t.Helper()
	if wu23R1Isolated(t) {
		return
	}
	f := wu23R1Fixture(t)
	layout := wu23R1DocumentLayout(t, f)
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("BLOCKED: real Python interpreter unavailable — required by founder's document-tool execution test: %v", err)
	}
	// A real launcher delegates to the actual interpreter. It never prints a
	// fabricated document result; the bundled ZIP/XML inspector does the work.
	fixture.Write(t, filepath.Join(layout.Bin, "python"), "#!/bin/sh\nprintf 'managed-prefix\\n' > \"$XDG_CACHE_HOME/managed-launcher.txt\" || exit 90\nexec "+wu23R1Quote(python)+" \"$@\"\n")
	wu23R1OwnerWritable(t, filepath.Join(layout.Bin, "python"))
	input := filepath.Join(f.Work, "input.docx")
	original := wu23R1DocumentArtifact(t, input)
	inspector := filepath.Join(layout.Skills, "elicify-office", "scripts", "inspect_docx.py")
	want := map[string]any{"paragraphs": float64(2), "tables": float64(1), "revision_marks": float64(0), "field_instructions": float64(0), "external_relationships": []any{}, "media_parts": []any{}}
	t.Run("real_unsandboxed_inspector_control", func(t *testing.T) {
		cmd := exec.Command(python, "-I", inspector, input)
		cmd.Env = append(sandbox.ScrubGatewayEnv(), "HOME="+f.Outside)
		out, runErr := cmd.CombinedOutput()
		if runErr != nil || !reflect.DeepEqual(wu23R1DocumentJSON(t, string(out)), want) {
			t.Fatalf("real inspector fixture/oracle control failed: err=%v output=%s", runErr, out)
		}
	})
	bash := wu23R1Exec(t, f, god)
	bash.SetDocumentRuntime(layout)
	ctx := wu23Context(f)
	t.Run("real_bash_workspace_control", func(t *testing.T) {
		wu23R1BashSuccess(t, wu23R1Bash(t, bash, ctx, "printf 'WORKSPACE_CONTROL=ok\\n'"), "WORKSPACE_CONTROL=ok\n")
	})
	// The helper is reached from the actual document environment, not a host
	// PATH fallback or fake interpreter. The input is a literal Word package.
	code := `import os,runpy,sys; cache=os.environ["XDG_CACHE_HOME"]; open(os.path.join(cache,"document-cache.txt"),"w").write("document-cache"); assert open(os.path.join(cache,"document-cache.txt")).read()=="document-cache"; sys.argv=["inspect_docx.py","input.docx"]; runpy.run_path(os.path.join(os.environ["OMNIPUS_DOCUMENT_SKILLS"],"elicify-office","scripts","inspect_docx.py"),run_name="__main__")`
	result := wu23R1Bash(t, bash, ctx, "python -I -c "+wu23R1Quote(code))
	if result.IsError || result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("real managed document tool must execute under god=%t: %s", god, result.ForLLM)
	}
	if got := wu23R1DocumentJSON(t, result.ForLLM); !reflect.DeepEqual(got, want) {
		t.Errorf("bundled document inspector actual=%#v want=%#v from literal XML", got, want)
	}
	launcher, launcherErr := os.ReadFile(filepath.Join(f.Work, documentCacheDirName, "managed-launcher.txt"))
	if launcherErr != nil || string(launcher) != "managed-prefix\n" {
		t.Errorf("document command fell through to a host interpreter instead of executing managed prefix: bytes=%q err=%v", launcher, launcherErr)
	}
	cache := filepath.Join(f.Work, documentCacheDirName, "document-cache.txt")
	data, readErr := os.ReadFile(cache)
	if readErr != nil || string(data) != "document-cache" {
		t.Errorf("real document cache write=%q err=%v", data, readErr)
	}
	data, readErr = os.ReadFile(input)
	if readErr != nil || !bytes.Equal(data, original) {
		t.Errorf("read-only document tool changed source package: err=%v", readErr)
	}
}

func TestWU23R1_WorkspaceInstallThenBash_Normal_Seatbelt(t *testing.T) {
	wu23R1WorkspaceInstallRun(t, false)
}
func TestWU23R1_WorkspaceInstallThenBash_God_Seatbelt(t *testing.T) {
	wu23R1WorkspaceInstallRun(t, true)
}
func TestWU23R1_SharedInstallThenBash_Normal_Seatbelt(t *testing.T) { wu23R1SharedInstallRun(t, false) }
func TestWU23R1_SharedInstallThenBash_God_Seatbelt(t *testing.T)    { wu23R1SharedInstallRun(t, true) }
func TestWU23R1_DocumentToolsRun_Normal_Seatbelt(t *testing.T)      { wu23R1DocumentToolsRun(t, false) }
func TestWU23R1_DocumentToolsRun_God_Seatbelt(t *testing.T)         { wu23R1DocumentToolsRun(t, true) }
