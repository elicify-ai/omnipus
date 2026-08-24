// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// writeFakeCopilotCLI writes an executable stand-in for the GitHub Copilot CLI
// that records its argv and working directory, then replays a canned stdout /
// stderr / exit code. It pins the CLI contract this provider was written
// against (@github/copilot 1.0.80) so a flag change is caught here rather than
// against the operator's live subscription.
func writeFakeCopilotCLI(t *testing.T, stdout, stderr string, exitCode int) (script, argsFile, cwdFile string) {
	t.Helper()
	dir := t.TempDir()
	script = filepath.Join(dir, "copilot")
	argsFile = filepath.Join(dir, "args.txt")
	cwdFile = filepath.Join(dir, "cwd.txt")

	body := "#!/bin/bash\n" +
		"printf '%s\\n' \"$@\" > " + copilotShellQuote(argsFile) + "\n" +
		"pwd > " + copilotShellQuote(cwdFile) + "\n"
	if stdout != "" {
		body += "cat <<'OMNIPUS_EOF'\n" + stdout + "\nOMNIPUS_EOF\n"
	}
	if stderr != "" {
		body += "cat >&2 <<'OMNIPUS_EOF'\n" + stderr + "\nOMNIPUS_EOF\n"
	}
	body += "exit " + strconv.Itoa(exitCode) + "\n"

	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script, argsFile, cwdFile
}

func copilotShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI uses a #!/bin/bash shebang with no Windows equivalent (see #113)")
	}
}

// TestCopilotCliProvider_ParsesOutput covers the CLI's suppressed-mode output
// (`-s`: the agent's response text and nothing else) becoming an LLMResponse.
func TestCopilotCliProvider_ParsesOutput(t *testing.T) {
	p := &CopilotCliProvider{}

	t.Run("plain text", func(t *testing.T) {
		resp, err := p.parseOutput("  Hello from the Copilot CLI.\n\n")
		if err != nil {
			t.Fatalf("parseOutput() error: %v", err)
		}
		if resp.Content != "Hello from the Copilot CLI." {
			t.Errorf("Content = %q, want %q", resp.Content, "Hello from the Copilot CLI.")
		}
		if resp.FinishReason != "stop" {
			t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "stop")
		}
		if len(resp.ToolCalls) != 0 {
			t.Errorf("ToolCalls = %d, want 0", len(resp.ToolCalls))
		}
		// The CLI reports no token accounting in suppressed mode. Unknown must
		// stay unknown — a zeroed UsageInfo would be reported as a real 0-token
		// turn by the cost tracker.
		if resp.Usage != nil {
			t.Errorf("Usage = %+v, want nil (the CLI reports no usage)", resp.Usage)
		}
	})

	t.Run("tool call in text", func(t *testing.T) {
		out := "Let me read that file.\n" +
			`{"tool_calls":[{"id":"call_1","type":"function",` +
			`"function":{"name":"read_file","arguments":"{\"path\":\"/tmp/a.txt\"}"}}]}`
		resp, err := p.parseOutput(out)
		if err != nil {
			t.Fatalf("parseOutput() error: %v", err)
		}
		if len(resp.ToolCalls) != 1 {
			t.Fatalf("ToolCalls = %d, want 1", len(resp.ToolCalls))
		}
		if resp.ToolCalls[0].Function.Name != "read_file" {
			t.Errorf("tool name = %q, want %q", resp.ToolCalls[0].Function.Name, "read_file")
		}
		if resp.FinishReason != "tool_calls" {
			t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "tool_calls")
		}
		if strings.Contains(resp.Content, "tool_calls") {
			t.Errorf("Content still carries the tool-call JSON: %q", resp.Content)
		}
		if resp.Content != "Let me read that file." {
			t.Errorf("Content = %q, want %q", resp.Content, "Let me read that file.")
		}
	})

	t.Run("empty output", func(t *testing.T) {
		resp, err := p.parseOutput("")
		if err != nil {
			t.Fatalf("parseOutput() error: %v", err)
		}
		if resp.Content != "" {
			t.Errorf("Content = %q, want empty", resp.Content)
		}
		if resp.FinishReason != "stop" {
			t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "stop")
		}
	})
}

// TestCopilotCliProvider_InvokesVerifiedFlags pins the exact non-interactive
// contract of @github/copilot 1.0.80, each flag confirmed against the published
// binary: `-p PROMPT -s --allow-all-tools --no-ask-user --no-color --log-level
// none -C WORKSPACE [--model MODEL]`.
func TestCopilotCliProvider_InvokesVerifiedFlags(t *testing.T) {
	skipOnWindows(t)

	script, argsFile, cwdFile := writeFakeCopilotCLI(t, "ok", "", 0)
	workspace := t.TempDir()

	p := &CopilotCliProvider{command: script, workspace: workspace}
	resp, err := p.Chat(context.Background(),
		[]Message{{Role: "user", Content: "ping"}}, nil, "claude-sonnet-4.6", nil)
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("Content = %q, want %q", resp.Content, "ok")
	}

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("reading captured args: %v", err)
	}
	args := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")

	wantExact := []string{
		"-p", "ping",
		"-s",
		"--allow-all-tools",
		"--no-ask-user",
		"--no-color",
		"--log-level", "none",
		"-C", workspace,
		"--model", "claude-sonnet-4.6",
	}
	if len(args) != len(wantExact) {
		t.Fatalf("argv = %q, want exactly %q", args, wantExact)
	}
	for i := range wantExact {
		if args[i] != wantExact[i] {
			t.Errorf("argv[%d] = %q, want %q (full argv %q)", i, args[i], wantExact[i], args)
		}
	}

	gotCwd, err := os.ReadFile(cwdFile)
	if err != nil {
		t.Fatalf("reading captured cwd: %v", err)
	}
	// macOS /var -> /private/var symlink, and bash `pwd` reports the logical
	// path: resolve both sides before comparing.
	wantCwd, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	haveCwd, err := filepath.EvalSymlinks(strings.TrimSpace(string(gotCwd)))
	if err != nil {
		t.Fatal(err)
	}
	if haveCwd != wantCwd {
		t.Errorf("cwd = %q, want %q", haveCwd, wantCwd)
	}
}

// TestCopilotCliProvider_OmitsModelFlagWhenUnset proves the CLI is left to pick
// its own default model when the row names none.
func TestCopilotCliProvider_OmitsModelFlagWhenUnset(t *testing.T) {
	skipOnWindows(t)

	script, argsFile, _ := writeFakeCopilotCLI(t, "ok", "", 0)
	p := &CopilotCliProvider{command: script}

	if _, err := p.Chat(context.Background(),
		[]Message{{Role: "user", Content: "ping"}}, nil, "", nil); err != nil {
		t.Fatalf("Chat() error: %v", err)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "--model") {
		t.Errorf("argv carries a --model flag with no model configured: %q", string(raw))
	}
}

// TestCopilotCliProvider_MissingBinary is the FR-009 `disconnected` precursor:
// with no `copilot` on this machine the provider fails with a sentinel the
// gateway can map, not an opaque exec error.
func TestCopilotCliProvider_MissingBinary(t *testing.T) {
	p := &CopilotCliProvider{command: filepath.Join(t.TempDir(), "definitely-not-copilot")}

	_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "", nil)
	if err == nil {
		t.Fatal("Chat() with a missing binary returned no error")
	}
	if !errors.Is(err, ErrCopilotCLINotFound) {
		t.Errorf("error = %v, want it to wrap ErrCopilotCLINotFound", err)
	}
}

// TestCopilotCliProvider_MissingBinaryOnEmptyPATH covers the real operator
// case: the default `copilot` name with nothing resolvable on PATH.
func TestCopilotCliProvider_MissingBinaryOnEmptyPATH(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if CopilotCLIAvailable("") {
		t.Fatal("CopilotCLIAvailable() = true with an empty PATH")
	}
	p := NewCopilotCliProvider("")
	_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "", nil)
	if !errors.Is(err, ErrCopilotCLINotFound) {
		t.Errorf("error = %v, want it to wrap ErrCopilotCLINotFound", err)
	}
}

// TestCopilotCliProvider_NonZeroExitSurfacesStderr pins the spec's error
// wording: `copilot cli error: <stderr>`.
func TestCopilotCliProvider_NonZeroExitSurfacesStderr(t *testing.T) {
	skipOnWindows(t)

	script, _, _ := writeFakeCopilotCLI(t, "", "not signed in: run `copilot login`", 1)
	p := &CopilotCliProvider{command: script}

	_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "", nil)
	if err == nil {
		t.Fatal("Chat() returned no error on a non-zero exit")
	}
	if !strings.HasPrefix(err.Error(), "copilot cli error: ") {
		t.Errorf("error = %q, want the %q prefix", err.Error(), "copilot cli error: ")
	}
	if !strings.Contains(err.Error(), "run `copilot login`") {
		t.Errorf("error = %q, want the CLI's stderr carried through", err.Error())
	}
}

// TestCopilotCliProvider_ContextCancel proves a cancelled turn reports the
// context error, not a subprocess kill.
func TestCopilotCliProvider_ContextCancel(t *testing.T) {
	skipOnWindows(t)

	dir := t.TempDir()
	script := filepath.Join(dir, "copilot")
	if err := os.WriteFile(script, []byte("#!/bin/bash\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := &CopilotCliProvider{command: script}
	_, err := p.Chat(ctx, []Message{{Role: "user", Content: "hi"}}, nil, "", nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

// TestCopilotCliProvider_BuildPromptFlattensConversation covers the single
// prompt string the CLI accepts — it has no system-prompt flag.
func TestCopilotCliProvider_BuildPromptFlattensConversation(t *testing.T) {
	p := &CopilotCliProvider{}

	single := p.buildPrompt([]Message{{Role: "user", Content: "just this"}}, nil)
	if single != "just this" {
		t.Errorf("single-message prompt = %q, want %q", single, "just this")
	}

	full := p.buildPrompt([]Message{
		{Role: "system", Content: "Be terse."},
		{Role: "user", Content: "What is 2+2?"},
		{Role: "assistant", Content: "4"},
		{Role: "tool", ToolCallID: "call_9", Content: "42"},
	}, nil)
	for _, want := range []string{
		"## System Instructions", "Be terse.", "## Task",
		"What is 2+2?", "Assistant: 4", "[Tool Result for call_9]: 42",
	} {
		if !strings.Contains(full, want) {
			t.Errorf("prompt %q missing %q", full, want)
		}
	}
}

func TestCopilotCliProvider_GetDefaultModel(t *testing.T) {
	if got := NewCopilotCliProvider("").GetDefaultModel(); got != "github-copilot" {
		t.Errorf("GetDefaultModel() = %q, want %q", got, "github-copilot")
	}
}

// TestCopilotCliProvider_FactoryDispatchByCliKind is FR-003's dispatch rule:
// a catalog row's cli_kind selects the subprocess constructor — `codex` →
// CodexCliProvider, `copilot` → CopilotCliProvider (X-14).
func TestCopilotCliProvider_FactoryDispatchByCliKind(t *testing.T) {
	t.Run("copilot", func(t *testing.T) {
		p, err := NewCliProviderForKind(catalog.CLIKindCopilot, "/tmp/ws", "")
		if err != nil {
			t.Fatalf("NewCliProviderForKind(copilot) error: %v", err)
		}
		cp, ok := p.(*CopilotCliProvider)
		if !ok {
			t.Fatalf("provider = %T, want *CopilotCliProvider", p)
		}
		if cp.workspace != "/tmp/ws" {
			t.Errorf("workspace = %q, want %q", cp.workspace, "/tmp/ws")
		}
		if cp.command != CopilotCLICommand {
			t.Errorf("command = %q, want %q", cp.command, CopilotCLICommand)
		}
	})

	t.Run("copilot with cli_path override", func(t *testing.T) {
		p, err := NewCliProviderForKind(catalog.CLIKindCopilot, "/tmp/ws", "/opt/bin/copilot")
		if err != nil {
			t.Fatalf("NewCliProviderForKind() error: %v", err)
		}
		if got := p.(*CopilotCliProvider).command; got != "/opt/bin/copilot" {
			t.Errorf("command = %q, want the cli_path override", got)
		}
	})

	t.Run("codex", func(t *testing.T) {
		p, err := NewCliProviderForKind(catalog.CLIKindCodex, "/tmp/ws", "")
		if err != nil {
			t.Fatalf("NewCliProviderForKind(codex) error: %v", err)
		}
		if _, ok := p.(*CodexCliProvider); !ok {
			t.Fatalf("provider = %T, want *CodexCliProvider", p)
		}
	})

	t.Run("unknown kind", func(t *testing.T) {
		if _, err := NewCliProviderForKind("gemini", "/tmp/ws", ""); err == nil {
			t.Fatal("NewCliProviderForKind() accepted an unknown cli_kind")
		}
	})
}

// TestCopilotCliProvider_FactoryCreatesFromConfig proves a configured
// `github-copilot` row resolves to the subprocess driver through the ordinary
// CreateProviderFromConfig path.
func TestCopilotCliProvider_FactoryCreatesFromConfig(t *testing.T) {
	p, modelID, err := CreateProviderFromConfig(&config.ModelConfig{
		ModelName: "copilot",
		Model:     "claude-sonnet-4.6",
		Provider:  "github-copilot",
		Home:      "/tmp/copilot-ws",
	})
	if err != nil {
		t.Fatalf("CreateProviderFromConfig() error: %v", err)
	}
	if _, ok := p.(*CopilotCliProvider); !ok {
		t.Fatalf("provider = %T, want *CopilotCliProvider", p)
	}
	if modelID != "claude-sonnet-4.6" {
		t.Errorf("modelID = %q, want %q", modelID, "claude-sonnet-4.6")
	}
	if !IsKnownProtocol("github-copilot") {
		t.Error("IsKnownProtocol(github-copilot) = false; the row would be rejected before it reaches the factory")
	}
}

// --- sign-in status (ADR-068 FR-009) ---------------------------------------

// TestCopilotCliSignIn_States drives the four FR-009 outcomes over a fake
// `copilot` that replays each state the real CLI can produce. The
// not-signed-in case replays the VERIFIED stderr of @github/copilot 1.0.80
// ("Error: No authentication information found." + the /login guidance),
// captured by running the published binary with no credential.
func TestCopilotCliSignIn_States(t *testing.T) {
	skipOnWindows(t)

	const realNotSignedInStderr = `Error: No authentication information found.

Copilot can be authenticated with GitHub using an OAuth Token or a Fine-Grained Personal Access Token.

To authenticate, you can use any of the following methods:
  * Start 'copilot' and run the '/login' command
  * Set the COPILOT_GITHUB_TOKEN, GH_TOKEN, or GITHUB_TOKEN environment variable
  * Run 'gh auth login' to authenticate with the GitHub CLI`

	cases := []struct {
		name     string
		stdout   string
		stderr   string
		exitCode int
		want     CopilotSignInState
	}{
		{"signed in", "ok", "", 0, CopilotSignedIn},
		{"not signed in", "", realNotSignedInStderr, 1, CopilotNotSignedIn},
		{"expired session", "", "Error: your Copilot session has expired. Run `copilot login` again.", 1, CopilotSignInExpired},
		{"revoked token", "", "Error: Bad credentials (401)", 1, CopilotSignInExpired},
		{"unrecognised failure", "", "Error: something else went wrong", 1, CopilotNotSignedIn},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script, argsFile, _ := writeFakeCopilotCLI(t, tc.stdout, tc.stderr, tc.exitCode)

			got := CopilotSignIn(context.Background(), script, "")
			if got.State != tc.want {
				t.Errorf("state = %q, want %q (detail %q)", got.State, tc.want, got.Detail)
			}
			// FR-009: Omnipus never decodes or holds the CLI's token, so no
			// expiry is ever reported for a cli_login provider.
			if got.ExpiresAt != nil {
				t.Errorf("ExpiresAt = %v, want nil", got.ExpiresAt)
			}

			raw, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatalf("reading captured args: %v", err)
			}
			// The check must never leave the operator's instructions or logs in
			// play, and must never ask the human a question.
			for _, want := range []string{"-p", "-s", "--allow-all-tools", "--no-ask-user", "--no-custom-instructions"} {
				if !strings.Contains(string(raw), want) {
					t.Errorf("sign-in check argv missing %q: %q", want, string(raw))
				}
			}
		})
	}
}

// TestCopilotCliSignIn_MissingBinary is the FR-009 `disconnected` case: the CLI
// is not installed, which is about the machine, not the account.
func TestCopilotCliSignIn_MissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	got := CopilotSignIn(context.Background(), "", "")
	if got.State != CopilotCLIMissing {
		t.Errorf("state = %q, want %q", got.State, CopilotCLIMissing)
	}
	if CopilotCLIMissingHint == "" {
		t.Error("CopilotCLIMissingHint must carry the operator hint")
	}
}

// TestCopilotCliSignIn_RunsInWorkspace proves the check never runs against
// whatever directory the gateway happens to have as its cwd.
func TestCopilotCliSignIn_RunsInWorkspace(t *testing.T) {
	skipOnWindows(t)

	script, argsFile, cwdFile := writeFakeCopilotCLI(t, "ok", "", 0)
	ws := t.TempDir()

	if got := CopilotSignIn(context.Background(), script, ws); got.State != CopilotSignedIn {
		t.Fatalf("state = %q, want %q", got.State, CopilotSignedIn)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "-C\n"+ws) {
		t.Errorf("argv missing -C %s: %q", ws, string(raw))
	}
	gotCwd, err := os.ReadFile(cwdFile)
	if err != nil {
		t.Fatal(err)
	}
	have, err := filepath.EvalSymlinks(strings.TrimSpace(string(gotCwd)))
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(ws)
	if err != nil {
		t.Fatal(err)
	}
	if have != want {
		t.Errorf("cwd = %q, want %q", have, want)
	}
}
