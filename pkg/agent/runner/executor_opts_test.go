package runner

import (
	"bufio"
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestResolveCLIBinary(t *testing.T) {
	cases := []struct {
		name, cliPath, def, want string
	}{
		{"empty falls back to default", "", "claude", "claude"},
		{"whitespace falls back to default", "   ", "claude", "claude"},
		{"absolute path wins", "/usr/local/bin/claude", "claude", "/usr/local/bin/claude"},
		{"bare name wins", "claude-2", "claude", "claude-2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveCLIBinary(tc.cliPath, tc.def); got != tc.want {
				t.Errorf("resolveCLIBinary(%q,%q) = %q, want %q", tc.cliPath, tc.def, got, tc.want)
			}
		})
	}
}

func TestParseCLIArgs(t *testing.T) {
	cases := []struct {
		name, raw string
		want      []string
	}{
		{"empty", "", nil},
		{"simple flags", "--max-turns 5", []string{"--max-turns", "5"}},
		{"double quoted group", `--system "be terse"`, []string{"--system", "be terse"}},
		{"single quoted group", `--note 'a b c'`, []string{"--note", "a b c"}},
		{"escaped space", `--path a\ b`, []string{"--path", "a b"}},
		{"collapses whitespace", "  --a    --b  ", []string{"--a", "--b"}},
		{"equals form kept as one token", "--model=gpt", []string{"--model=gpt"}},
		// Robustness (review test gap): a malformed args string must not drop the
		// partial token silently — flush() emits what was accumulated.
		{"unterminated double quote keeps partial token", `--x "unclosed`, []string{"--x", "unclosed"}},
		{"trailing backslash keeps token", `--x a\`, []string{"--x", "a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseCLIArgs(tc.raw, "test")
			if len(got) != len(tc.want) {
				t.Fatalf("parseCLIArgs(%q) = %v (len %d), want %v (len %d)", tc.raw, got, len(got), tc.want, len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("parseCLIArgs(%q)[%d] = %q, want %q", tc.raw, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestParseCLIArgs_ShellMetacharsWarnNotReject confirms shell-metacharacters are
// tokenised literally (not rejected) — execve makes the literal pass safe.
func TestParseCLIArgs_ShellMetacharsWarnNotReject(t *testing.T) {
	got := parseCLIArgs("--flag $(whoami)", "test")
	// The token "$(whoami)" must survive verbatim — no command substitution.
	found := false
	for _, a := range got {
		if a == "$(whoami)" {
			found = true
		}
	}
	if !found {
		t.Fatalf("shell-metachar arg must be passed literally; got %v", got)
	}
}

func TestMergeEnvOverrides(t *testing.T) {
	base := []string{"PATH=/usr/bin", "ANTHROPIC_API_KEY=base"}

	t.Run("nil overrides returns base unchanged", func(t *testing.T) {
		got := mergeEnvOverrides(base, nil, "run")
		if len(got) != len(base) {
			t.Fatalf("got %d entries, want %d", len(got), len(base))
		}
	})

	t.Run("adds new key and replaces existing", func(t *testing.T) {
		got := mergeEnvOverrides(base, map[string]string{
			"DEBUG":             "1",        // new
			"ANTHROPIC_API_KEY": "override", // replace existing
		}, "run")
		m := envToMap(got)
		if m["DEBUG"] != "1" {
			t.Errorf("DEBUG = %q, want 1", m["DEBUG"])
		}
		if m["ANTHROPIC_API_KEY"] != "override" {
			t.Errorf("ANTHROPIC_API_KEY = %q, want override", m["ANTHROPIC_API_KEY"])
		}
		if m["PATH"] != "/usr/bin" {
			t.Errorf("PATH = %q, want /usr/bin (untouched)", m["PATH"])
		}
	})

	t.Run("protected OMNIPUS_ keys are dropped", func(t *testing.T) {
		got := mergeEnvOverrides(base, map[string]string{
			"OMNIPUS_MASTER_KEY": "leak",
			"OMNIPUS_AGENT_NAME": "spoof",
			"OMNIPUS_AGENT_TYPE": "spoof",
			"SAFE_VAR":           "ok",
		}, "run")
		m := envToMap(got)
		for _, k := range []string{"OMNIPUS_MASTER_KEY", "OMNIPUS_AGENT_NAME", "OMNIPUS_AGENT_TYPE"} {
			if _, present := m[k]; present {
				t.Errorf("protected key %q must NOT be set via env_overrides", k)
			}
		}
		if m["SAFE_VAR"] != "ok" {
			t.Errorf("non-protected SAFE_VAR must be applied; got %q", m["SAFE_VAR"])
		}
	})

	t.Run("empty key is skipped", func(t *testing.T) {
		// A whitespace-only / empty override key must be skipped, not emitted as a
		// bare "=value" entry that would corrupt the child env.
		got := mergeEnvOverrides(base, map[string]string{
			"":    "should-be-dropped",
			"   ": "also-dropped",
			"OK":  "kept",
		}, "run")
		for _, kv := range got {
			if strings.HasPrefix(kv, "=") || strings.HasPrefix(kv, " ") {
				t.Errorf("empty/blank override key produced a malformed entry: %q", kv)
			}
		}
		m := envToMap(got)
		if m["OK"] != "kept" {
			t.Errorf("non-empty key must be applied; OK = %q", m["OK"])
		}
		// No spurious growth beyond base + the single valid "OK" key.
		if len(got) != len(base)+1 {
			t.Errorf("expected base+1 entries, got %d (%v)", len(got), got)
		}
	})
}

// captureSlogWarnings installs a slog handler that records WARN+ messages for
// the duration of fn, restoring the previous default logger afterwards. It
// returns the captured log text.
func captureSlogWarnings(fn func()) string {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)
	fn()
	return buf.String()
}

// TestParseCLIArgs_ShellMetacharWarnFires asserts a WARN is actually emitted
// when cli_args carry genuine shell-control characters (review test gap). The
// narrowed shellInjectionChars set must NOT fire on ordinary `--flag=val`.
func TestParseCLIArgs_ShellMetacharWarnFires(t *testing.T) {
	out := captureSlogWarnings(func() {
		parseCLIArgs("--flag $(whoami)", "run-meta")
	})
	if !strings.Contains(out, "shell-metacharacters") {
		t.Errorf("expected a shell-metacharacter WARN for cli_args containing $(...), got: %q", out)
	}

	// A benign value with `=` (and glob-ish chars that are NOT shell-control) must
	// NOT trigger the warning after the narrowing fix.
	benign := captureSlogWarnings(func() {
		parseCLIArgs("--model=gpt-4 --path a/b*.txt", "run-benign")
	})
	if strings.Contains(benign, "shell-metacharacters") {
		t.Errorf("benign cli_args must NOT warn after narrowing shellInjectionChars; got: %q", benign)
	}
}

// TestMergeEnvOverrides_WarnPerProtectedKey asserts one WARN is emitted per
// offending protected key (the corrected doc contract), not silently skipped.
func TestMergeEnvOverrides_WarnPerProtectedKey(t *testing.T) {
	out := captureSlogWarnings(func() {
		mergeEnvOverrides([]string{"PATH=/usr/bin"}, map[string]string{
			"OMNIPUS_MASTER_KEY": "leak",
			"OMNIPUS_AGENT_NAME": "spoof",
		}, "run-prot")
	})
	got := strings.Count(out, "reserved Omnipus-internal variable")
	if got != 2 {
		t.Errorf("expected one WARN per offending protected key (2), got %d; log: %q", got, out)
	}
}

// TestExternalCLI_SpawnsWithExecutorConfig is the MAJ-5 end-to-end proof: a stub
// CLI placed at a custom path is spawned via opts.CLIPath, with cli_args appended
// to its argv and env_overrides merged into its environment. The stub dumps its
// own argv + env to files, which the test then asserts against.
func TestExternalCLI_SpawnsWithExecutorConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script")
	}

	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv.txt")
	envFile := filepath.Join(dir, "env.txt")
	// The stub records its argv (one per line) and full env, then emits a single
	// valid claude result line so the driver's stream parser terminates cleanly.
	body := "#!/bin/sh\n" +
		"for a in \"$@\"; do printf '%s\\n' \"$a\" >> '" + argvFile + "'; done\n" +
		"env > '" + envFile + "'\n" +
		`printf '%s\n' '{"type":"result","subtype":"success","result":"ok"}'` + "\n" +
		"exit 0\n"
	stub := filepath.Join(dir, "my-claude")
	if err := os.WriteFile(stub, []byte(body), 0o700); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	// IMPORTANT: do NOT override claudeBinName — the whole point of MAJ-5 is that
	// opts.CLIPath selects the binary. The default name stays "claude".
	d := NewClaudeDriver(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// A non-nil scrubbed base env so buildChildEnv does not inherit os.Environ();
	// env_overrides then layer on top.
	baseEnv := []string{"PATH=" + os.Getenv("PATH")}

	ch, err := d.Run(ctx, RunOptions{
		Input:        "do the thing",
		Env:          baseEnv,
		CLIPath:      stub, // cli_path: the binary that must actually run
		CLIArgs:      parseCLIArgs("--custom-flag value-x", "run"),
		EnvOverrides: map[string]string{"OMNIPUS_AGENT_NAME": "should-be-dropped", "CUSTOM_VAR": "custom-val"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	drain(t, ch)

	// (1) cli_path honored: the stub at the custom path actually ran (it wrote the
	// argv/env files).
	argvData, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("cli_path was not honored — stub did not run (argv file missing): %v", err)
	}
	argv := splitNonEmptyLines(string(argvData))

	// (2) cli_args appended: --custom-flag and value-x must be present in argv.
	if !containsSeq(argv, "--custom-flag", "value-x") {
		t.Errorf("cli_args not applied; stub argv = %v", argv)
	}

	// (3) env_overrides applied + protection: CUSTOM_VAR present; the protected
	// OMNIPUS_AGENT_NAME override was dropped.
	envMap := readEnvFile(t, envFile)
	if envMap["CUSTOM_VAR"] != "custom-val" {
		t.Errorf("env_overrides not applied; CUSTOM_VAR = %q", envMap["CUSTOM_VAR"])
	}
	if envMap["OMNIPUS_AGENT_NAME"] == "should-be-dropped" {
		t.Errorf("SECURITY: a protected OMNIPUS_* env override leaked into the child env")
	}
}

func envToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			m[kv[:eq]] = kv[eq+1:]
		}
	}
	return m
}

func splitNonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func containsSeq(argv []string, a, b string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == a && argv[i+1] == b {
			return true
		}
	}
	return false
}

func readEnvFile(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open env file: %v", err)
	}
	defer func() { _ = f.Close() }()
	out := make(map[string]string)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if eq := strings.IndexByte(line, '='); eq > 0 {
			out[line[:eq]] = line[eq+1:]
		}
	}
	return out
}
