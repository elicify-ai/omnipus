//go:build darwin

package preview_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"syscall"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/require"
)

func recheckKernelRoot(t *testing.T) string {
	t.Helper()
	parent := os.Getenv("OMNIPUS_TEST_SECURITY_ROOT")
	if parent == "" {
		t.Fatal("BLOCKED: real Seatbelt authority proof needs OMNIPUS_TEST_SECURITY_ROOT outside TMPDIR — W2-S02 R2/R5")
	}
	require.NoError(t, os.MkdirAll(parent, 0700))
	base, err := os.MkdirTemp(parent, "recheck-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(base)) })
	return base
}

func TestWriteExclusionSeesSymlinkToComponentSubtree(t *testing.T) {
	for _, form := range []string{"authority_alias_to_program", "registered_root_alias"} {
		t.Run(form, func(t *testing.T) {
			base, home, work := recheckKernelRoot(t), t.TempDir(), t.TempDir()
			t.Setenv("OMNIPUS_HOME", home)
			root := filepath.Join(base, "office")
			registration := root
			if form == "registered_root_alias" {
				require.NoError(t, os.MkdirAll(root, 0700))
				registration = filepath.Join(base, "component-alias")
				require.NoError(t, os.Symlink(root, registration))
			}
			resource := officepreview.TrustedOffice(t, home, registration)
			program := filepath.Join(root, "program")
			require.NoError(t, os.MkdirAll(program, 0700))
			target := filepath.Join(program, "dependency.txt")
			require.NoError(t, os.Rename(resource, target))
			alias := filepath.Join(base, "writer-alias")
			require.NoError(t, os.Symlink(program, alias))
			actual, err := os.Stat(program)
			require.NoError(t, err)
			aliased, err := os.Stat(alias)
			require.NoError(t, err)
			require.True(t, os.SameFile(actual, aliased))
			input := sandbox.TurnPolicyInput{HomePath: home, Model: sandbox.FilesystemModelOpen}
			policy := fspolicy.FSPolicy{WorkDir: work}
			output, denied := securitySeatbeltWrite(t, sandbox.DeriveKernelPolicy(policy, input), target)
			require.Error(t, denied, "without authority the real confined write must fail: %s", output)
			input.AllowedPaths = []string{alias}
			output, allowed := securitySeatbeltWrite(t, sandbox.DeriveKernelPolicy(policy, input), filepath.Join(alias, "dependency.txt"))
			require.NoError(t, allowed, "alias authority really permits the write: %s", output)
			data, err := os.ReadFile(target)
			require.NoError(t, err)
			require.Equal(t, "kernel authority wrote", string(data))
			cfg := &config.Config{}
			cfg.Sandbox.AllowedPaths = []string{alias}
			officepreview.RequireWriterRefusal(t, securityWriterService(t, home, func() *config.Config { return cfg }))
		})
	}
}

// Probe the exact test volume, not an OS-name assumption. An unsupported
// volume is a loud platform gap; it is never silently counted as certified.
func recheckAliasProbe(t *testing.T, base string) {
	t.Helper()
	for _, names := range [][2]string{{"CaseProbe", "caseprobe"}, {"CaféProbe", "CaféProbe"}} {
		original, alias := filepath.Join(base, names[0]), filepath.Join(base, names[1])
		require.NoError(t, os.WriteFile(original, []byte("identity control"), 0600))
		a, err := os.Stat(original)
		require.NoError(t, err)
		b, err := os.Stat(alias)
		if err != nil || !os.SameFile(a, b) {
			t.Fatalf("BLOCKED: test volume does not support alias %q -> %q — explicit W2-S02 R2 platform gap: %v", names[0], names[1], err)
		}
		t.Logf("volume probe: %q and %q identify the same file", names[0], names[1])
	}
}

func TestWriteExclusionPrefixUsesFilesystemAliases(t *testing.T) {
	for _, form := range []string{"NFC_root_NFD_write_ASCII_stem", "NFD_root_ASCII_stem", "case_prefix", "symlinked_parent_ASCII_stem"} {
		t.Run(form, func(t *testing.T) {
			base, home, work := t.TempDir(), t.TempDir(), t.TempDir()
			base, err := filepath.EvalSymlinks(base)
			require.NoError(t, err)
			t.Setenv("OMNIPUS_HOME", home)
			recheckAliasProbe(t, base)
			rootName, writeName, stem := "Café", "Café", "Cafe"
			switch form {
			case "NFD_root_ASCII_stem":
				rootName = "Café"
			case "case_prefix":
				rootName, writeName, stem = "OFFICE7", "office7", "office"
			}
			root := filepath.Join(base, rootName)
			target := officepreview.TrustedOffice(t, home, root)
			writeBase := base
			if form == "symlinked_parent_ASCII_stem" {
				writeBase = filepath.Join(t.TempDir(), "parent-alias")
				require.NoError(t, os.Symlink(base, writeBase))
			}
			alias := filepath.Join(writeBase, writeName)
			a, err := os.Stat(root)
			require.NoError(t, err)
			b, err := os.Stat(alias)
			require.NoError(t, err)
			require.True(t, os.SameFile(a, b), "the actual component, not a similarly named folder, is writable")
			// The real file guard requires lexical AND physical permission.
			// Both explicit ASCII stems refer to this same proven component;
			// neither a lexical-only nor a physical-only pattern can serve as
			// an accepted-write control when the parent itself is a symlink.
			patterns := []string{"^" + regexp.QuoteMeta(filepath.Join(base, stem))}
			if writeBase != base {
				patterns = append(patterns, "^"+regexp.QuoteMeta(filepath.Join(writeBase, stem)))
			}
			securityWrite(t, work, filepath.Join(alias, filepath.Base(target)), patterns...)
			data, err := os.ReadFile(target)
			require.NoError(t, err)
			require.Equal(t, "authority really wrote", string(data))
			cfg := &config.Config{}
			cfg.Tools.AllowWritePaths = patterns
			officepreview.RequireWriterRefusal(t, securityWriterService(t, home, func() *config.Config { return cfg }))
		})
	}
}

// This child has an actual inherited kernel policy and keeps its already
// authorized descriptor open. Two readiness messages prove the writer is
// alive before AND after the authority-source fault; no timing guess is used.
func recheckHeldWriter(t *testing.T, policy sandbox.SandboxPolicy, target string) func() {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	script := `exec 3>"$1" || exit 71
printf 'captured authority wrote' >&3
printf 'READY\n'
IFS= read -r gate || exit 0
printf ' surviving authority wrote' >&3
printf 'WROTE\n'
IFS= read -r gate || exit 0
`
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script, "held-writer", target)
	backend := sandbox.NewSeatbeltBackend()
	require.True(t, backend.Available(), "BLOCKED: real retained-kernel-authority control requires Seatbelt")
	require.NoError(t, backend.ApplyToCmd(cmd, policy))
	input, err := cmd.StdinPipe()
	require.NoError(t, err)
	output, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_, _ = fmt.Fprint(input, "finish\nfinish\n")
		_ = input.Close()
		waitErr := cmd.Wait()
		cancel()
		require.NoError(t, waitErr, "held writer must terminate cleanly; never leave a child running")
	})
	reader := bufio.NewReader(output)
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "READY\n", line)
	return func() {
		_, err := fmt.Fprintln(input, "continue")
		require.NoError(t, err)
		line, err := reader.ReadString('\n')
		require.NoError(t, err)
		require.Equal(t, "WROTE\n", line)
		data, err := os.ReadFile(target)
		require.NoError(t, err)
		require.Equal(t, "captured authority wrote surviving authority wrote", string(data))
	}
}

// T-69 / S2 amendment (2026-11-05): listed but unreadable authority still
// fails closed. A removed record with a surviving writer is now the explicit
// §3.2 boundary item 1; dropped_record was removed by spec, not made to pass.
func TestWriteExclusionRejectsUnknownAuthorityWithLiveWriter(t *testing.T) {
	for _, fault := range []string{"malformed_record", "record_is_directory", "mount_list_error", "identity_error"} {
		t.Run(fault, func(t *testing.T) {
			base, home, work := recheckKernelRoot(t), t.TempDir(), t.TempDir()
			t.Setenv("OMNIPUS_HOME", home)
			root := filepath.Join(base, "office")
			target := officepreview.TrustedOffice(t, home, root)
			mountRoot := root
			if fault == "identity_error" {
				mountRoot = filepath.Join(base, "mount-alias")
				require.NoError(t, os.Symlink(root, mountRoot))
			}
			require.NoError(t, os.MkdirAll(workspace.MountStoreDir(home), 0700))
			record, err := workspace.MountStorePath(home, "writer-workspace")
			require.NoError(t, err)
			raw, err := json.Marshal(map[string]any{"workspace_id": "writer-workspace", "mounts": []workspace.Mount{{Name: "office", HostPath: mountRoot}}})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(record, raw, 0600))
			require.Equal(t, []string{mountRoot}, workspace.AllowedMountRoots(home, "writer-workspace"))
			ctx := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "retained-writer"), "writer-workspace")
			captured, err := tools.ResolveTurnFSPolicy(ctx, work, true)
			require.NoError(t, err)
			require.Contains(t, captured.AllowedRoots, mountRoot)
			input := sandbox.TurnPolicyInput{HomePath: home, Model: sandbox.FilesystemModelOpen}
			output, denied := securitySeatbeltWrite(t, sandbox.DeriveKernelPolicy(fspolicy.FSPolicy{WorkDir: work}, input), target)
			require.Error(t, denied, "no-authority control must deny: %s", output)
			continueWriter := recheckHeldWriter(t, sandbox.DeriveKernelPolicy(captured, input), target)
			data, err := os.ReadFile(target)
			require.NoError(t, err)
			require.Equal(t, "captured authority wrote", string(data))
			switch fault {
			case "malformed_record":
				require.NoError(t, os.WriteFile(record, []byte("{malformed"), 0600))
			case "record_is_directory":
				require.NoError(t, os.Remove(record))
				require.NoError(t, os.Mkdir(record, 0700))
			case "mount_list_error":
				dir := workspace.MountStoreDir(home)
				require.NoError(t, os.Rename(dir, dir+"-unavailable"))
				require.NoError(t, os.WriteFile(dir, []byte("not a directory"), 0600))
			case "identity_error":
				require.NoError(t, os.Remove(mountRoot))
				require.NoError(t, os.Symlink(mountRoot, mountRoot))
				_, err := os.Stat(mountRoot)
				require.ErrorIs(t, err, syscall.ELOOP)
			}
			continueWriter()
			cfg := &config.Config{}
			officepreview.RequireWriterRefusal(t, securityWriterService(t, home, func() *config.Config { return cfg }))
		})
	}
}
