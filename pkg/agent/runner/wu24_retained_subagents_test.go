package runner

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// This is the external-process version fixture only. The normal test binary
// has a different name and normal test arguments, so this cannot bypass test
// collection or assertions. Copying the test executable avoids a POSIX-only
// shell fixture: the same credential pre-check runs on all supported OSes.
func init() {
	name := filepath.Base(os.Args[0])
	if (name == "wu24-codex-version" || name == "wu24-codex-version.exe") &&
		len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("codex" + "-cli 0.4.0")
		os.Exit(0)
	}
}

// WU-24 KEEP: third-party workers are not model providers. Their real dispatch
// gate and constructors must survive removal of the subprocess model adapters.
func TestWU24ExternalSubagentDrivers(t *testing.T) {
	for _, tc := range []struct {
		cli  string
		want ExternalAgentRunner
	}{
		{"claude-code", (*ClaudeDriver)(nil)},
		{"codex", (*CodexDriver)(nil)},
		{"opencode", (*OpencodeDriver)(nil)},
	} {
		t.Run(tc.cli, func(t *testing.T) {
			kind, err := ResolveDispatch(&config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: tc.cli})
			if err != nil || kind != DispatchKindExternalCLI {
				t.Fatalf("retained worker %q dispatch = (%q, %v), want external-cli", tc.cli, kind, err)
			}
			driver, err := NewDriver(tc.cli, nil)
			if err != nil {
				t.Fatalf("retained worker %q constructor failed: %v", tc.cli, err)
			}
			if reflect.TypeOf(driver) != reflect.TypeOf(tc.want) || reflect.ValueOf(driver).IsNil() {
				t.Fatalf("retained worker %q = %T, want non-nil %T", tc.cli, driver, tc.want)
			}
		})
	}
}

func wu24VersionBinary(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable for the process fixture: %v", err)
	}
	name := "wu24-codex-version"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	source, err := os.Open(executable)
	if err != nil {
		t.Fatalf("open process fixture source: %v", err)
	}
	defer source.Close()
	destination, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatalf("create process fixture: %v", err)
	}
	_, copyErr := io.Copy(destination, source)
	closeErr := destination.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("copy process fixture: copy=%v close=%v", copyErr, closeErr)
	}
	return path
}

// WU-24 KEEP amendment: Codex's third-party-worker sign-in pre-check must
// remain alive even when its old model-provider credential reader disappears.
// Only the version executable is a fixture; admission and on-disk credential
// discovery remain real. The negative control prevents an unconditional ready
// result from manufacturing a green.
func TestWU24CodexSubagentSignInPrecheck(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OPENAI_API_KEY", "")
	codexHome := filepath.Join(home, ".codex")
	t.Setenv("CODEX_HOME", codexHome)
	binary := wu24VersionBinary(t)

	missing := TestConnectionWithPath(t.Context(), "codex", binary)
	if missing.OK || missing.Reason != ReasonUnauthenticated || missing.CLIVersion != "0.4.0" {
		t.Fatalf("without a login file = %+v, want unauthenticated after version 0.4.0", missing)
	}

	if err := os.Mkdir(codexHome, 0o700); err != nil {
		t.Fatalf("create temporary Codex home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"),
		[]byte(`{"tokens":{"access_token":"wu24-fixture-token","account_id":"wu24-fixture-account"}}`), 0o600); err != nil {
		t.Fatalf("write temporary external-worker login: %v", err)
	}
	ready := TestConnectionWithPath(t.Context(), "codex", binary)
	if !ready.OK || ready.Reason != ReasonOK || ready.CLIVersion != "0.4.0" {
		t.Fatalf("with a login file = %+v, want ready with version 0.4.0", ready)
	}
}
