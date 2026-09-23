package tools

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/documentruntime"
	"github.com/elicify-ai/omnipus/pkg/environmentsetup"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// writeFakeManagedPython materializes a fake managed toolchain python that
// reports its own path plus the environment the runtime injected.
func writeFakeManagedPython(t *testing.T, binDir string) {
	t.Helper()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf 'python=%s\\ncache=%s\\nskills=%s\\nargs=%s\\n' \"$0\" \"$XDG_CACHE_HOME\" \"$OMNIPUS_DOCUMENT_SKILLS\" \"$*\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "python"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// provisionLayout resolves a layout under home and provisions first-party
// assets (probe + skills + manifest) without external dependencies.
func provisionLayout(t *testing.T, home, workerID string) documentruntime.Layout {
	t.Helper()
	layout, err := documentruntime.ResolveLayout(home, documentruntime.ManifestRevision, workerID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = documentruntime.ProvisionFirstParty(layout); err != nil {
		t.Fatal(err)
	}
	return layout
}

// TestBashDocumentRuntimeUsesPrefixAndPerTurnCache pins the ES-FR-04
// environment composition for a wired runtime: the shared read-only prefix
// provides Bin/Skills, while the writable cache is resolved PER TURN inside
// the authorized workspace root — never the construction-time data root.
func TestBashDocumentRuntimeUsesPrefixAndPerTurnCache(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX shell")
	}
	home := t.TempDir()
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	layout := provisionLayout(t, home, "mia")
	writeFakeManagedPython(t, layout.Bin)
	tool, err := NewExecToolWithDeps(workspace, true, nil, ExecToolDeps{GodMode: false})
	if err != nil {
		t.Fatal(err)
	}
	tool.SetDocumentRuntime(layout)
	command := strings.Join(documentruntime.ProbeArgv(layout), " ")
	result := tool.Execute(bashCtx(t), map[string]any{"command": command})
	if result.IsError {
		t.Fatalf("probe must run: %+v", result)
	}
	wantCache := filepath.Join(workspace, ".omnipus-document-cache")
	if !strings.Contains(result.ForLLM, "python="+filepath.Join(layout.Bin, "python")) ||
		!strings.Contains(result.ForLLM, "cache="+wantCache) ||
		!strings.Contains(result.ForLLM, "skills="+layout.Skills) ||
		!strings.Contains(result.ForLLM, "--format all") {
		t.Fatalf("result=%+v", result)
	}
	if strings.Contains(result.ForLLM, "document runtime is not ready") {
		t.Fatalf("provisioned runtime without converter registry must not be reported broken: %+v", result)
	}
}

// TestBashDocumentRuntimeCacheFollowsTurnWorkspace pins GS-05: one agent
// serving two workspaces must observe a different writable cache per turn,
// derived from the turn's authorized root, with an unchanged shared prefix.
func TestBashDocumentRuntimeCacheFollowsTurnWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX shell")
	}
	home := t.TempDir()
	agentHome := filepath.Join(home, "agents", "mia")
	wsA := filepath.Join(home, "workspaces", "ws-a", "work")
	wsB := filepath.Join(home, "workspaces", "ws-b", "work")
	for _, dir := range []string{agentHome, wsA, wsB} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	layout := provisionLayout(t, home, "mia")
	tool, err := NewExecToolWithDeps(agentHome, true, nil, ExecToolDeps{GodMode: false})
	if err != nil {
		t.Fatal(err)
	}
	tool.SetDocumentRuntime(layout)
	command := `printf 'cache=%s home=%s\n' "$XDG_CACHE_HOME" "$HOME"`
	for _, tc := range []struct{ name, ws string }{{"workspace-a", wsA}, {"workspace-b", wsB}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := WithTurnWorkspaceDir(WithAgentID(context.Background(), "mia"), tc.ws)
			result := tool.Execute(ctx, map[string]any{"command": command})
			if result.IsError {
				t.Fatalf("result=%+v", result)
			}
			wantCache := filepath.Join(tc.ws, ".omnipus-document-cache")
			if !strings.Contains(result.ForLLM, "cache="+wantCache) ||
				!strings.Contains(result.ForLLM, "home="+wantCache) {
				t.Fatalf("turn cache must follow the turn workspace %s: %+v", tc.ws, result)
			}
		})
	}
}

// TestBashWorkspaceEnvPackagesReachCommandEnvironment pins ES-FR-04's
// "workspace packages ... available to every permitted native agent": the
// workspace env's conventional bin directory (.omnipus/env/bin — storage's
// current generic contract; the recipe-era .omnipus/env/venv and /node
// layouts are retired) precedes the managed document prefix on PATH so an
// agent's own installed packages win over the shared toolchain, and the
// inherited host PATH survives as the tail.
func TestBashWorkspaceEnvPackagesReachCommandEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX shell")
	}
	home := t.TempDir()
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	layout := provisionLayout(t, home, "mia")
	writeFakeManagedPython(t, layout.Bin)

	// The conventional generic bin directory storage discovers (one level
	// under the env root: bin/ on Unix, Scripts/ on Windows).
	wsBin := filepath.Join(workspace, ".omnipus", "env", "bin")
	writeFakeManagedPython(t, wsBin)

	tool, err := NewExecToolWithDeps(workspace, true, nil, ExecToolDeps{GodMode: false})
	if err != nil {
		t.Fatal(err)
	}
	tool.SetDocumentRuntime(layout)
	// Host-tail oracle: capture the process PATH BEFORE the composition runs;
	// the documented layering (shell_env.go) makes that value the composed
	// PATH's tail, so a dropped tail (mutation M4) cannot pass. Deliberately
	// NOT a t.Setenv marker: narrowing the process PATH breaks hardened_exec's
	// own LookPath("sh") for the spawn — the instrument must not remove the
	// very shell it is probing.
	hostPath := os.Getenv("PATH")
	if hostPath == "" {
		t.Skip("host PATH is empty; the tail oracle needs a non-empty PATH")
	}
	result := tool.Execute(bashCtx(t), map[string]any{"command": `printf 'path=%s\n' "$PATH"`})
	if result.IsError {
		t.Fatalf("result=%+v", result)
	}
	wsPython := filepath.Join(wsBin, "python")
	probe := tool.Execute(bashCtx(t), map[string]any{"command": strings.Join(documentruntime.ProbeArgv(layout), " ")})
	if probe.IsError || !strings.Contains(probe.ForLLM, "python="+wsPython) {
		t.Fatalf("workspace bin python must win over the managed prefix: %+v", probe)
	}

	pathValue := extractEnvVar(result.ForLLM, "path=")
	if pathValue == "" {
		t.Fatalf("env report missing: %+v", result)
	}
	wsIdx := strings.Index(pathValue, wsBin)
	docIdx := strings.Index(pathValue, layout.Bin)
	if wsIdx < 0 || docIdx < 0 || wsIdx > docIdx {
		t.Fatalf("workspace bin must precede the managed prefix on PATH: %q", pathValue)
	}
	if !strings.HasSuffix(pathValue, hostPath) {
		t.Fatalf("inherited host PATH must survive as the tail after the composed segments: %q", pathValue)
	}

	// Without a workspace env the composed PATH carries no env segment.
	empty, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result = tool.Execute(WithTurnWorkspaceDir(bashCtx(t), empty), map[string]any{"command": `printf 'path=%s\n' "$PATH"`})
	if result.IsError || strings.Contains(extractEnvVar(result.ForLLM, "path="), ".omnipus/env") {
		t.Fatalf("absent workspace env must not inject PATH segments: %+v", result)
	}
}

// TestBashSharedGenerationReachableReadExecOnly pins ES-FR-03's shared-runtime
// rule: an app-managed shared generation — published through storage's
// lifecycle API so the fixture cannot drift from the runtime reader — is
// reachable on PATH and enters the kernel sandbox READ+EXECUTE only: an agent
// can run it, never update it. A second, unrelated generation stays reachable
// and both precede the host PATH tail (additive publication, coordinator
// review 2026-09-18).
func TestBashSharedGenerationReachableReadExecOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX shell")
	}
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	unlockPublished(t, home)
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// Two independent arbitrary generations with different executable names —
	// no catalogue, just storage's generic lifecycle API, so the fixture
	// cannot drift from the runtime reader's layout.
	genBins := make([]string, 0, 2)
	for range 2 {
		target, beginErr := environmentsetup.BeginInstall(home, "", environmentsetup.ScopeShared)
		if beginErr != nil {
			t.Fatalf("begin shared install: %v", beginErr)
		}
		bin := filepath.Join(target.Prefix(), "bin")
		writeFakeManagedPython(t, bin)
		if _, commitErr := target.Commit(); commitErr != nil {
			t.Fatalf("commit shared generation: %v", commitErr)
		}
		genBins = append(genBins, bin)
	}

	tool, err := NewExecToolWithDeps(workspace, true, nil, ExecToolDeps{GodMode: false})
	if err != nil {
		t.Fatal(err)
	}
	result := tool.Execute(bashCtx(t), map[string]any{"command": `printf 'path=%s\n' "$PATH"`})
	if result.IsError {
		t.Fatalf("result=%+v", result)
	}
	pathValue := extractEnvVar(result.ForLLM, "path=")
	for _, bin := range genBins {
		if !strings.Contains(pathValue, bin) {
			t.Fatalf("shared generation bin %q must be on PATH: %q", bin, pathValue)
		}
	}
	_, hostTail, _ := strings.Cut(pathValue, genBins[len(genBins)-1])
	if hostTail == "" {
		t.Fatalf("shared generation bins must precede the inherited host PATH: %q", pathValue)
	}

	sandbox.RegisterTurnPolicyBase(&sandbox.TurnPolicyInput{HomePath: home, Model: sandbox.FilesystemModelOpen})
	t.Cleanup(func() { sandbox.RegisterTurnPolicyBase(nil) })
	ctx := WithTurnWorkspaceDir(WithAgentID(context.Background(), "mia"), workspace)
	rtLayer, rtErr := runtimeEnvTurn(workspace)
	if rtErr != nil {
		t.Fatalf("resolve runtime env: %v", rtErr)
	}
	policy, err := tool.turnKernelPolicy(ctx, workspace, rtLayer, documentEnvLayer{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, bin := range genBins {
		genDir := filepath.Dir(bin)
		found := false
		for _, rule := range policy.FilesystemRules {
			if pathOverlaps(rule.Path, genDir) {
				found = true
				if rule.Access&sandbox.AccessRead == 0 || rule.Access&sandbox.AccessExecute == 0 {
					t.Fatalf("shared generation rule must grant read+execute: %+v", rule)
				}
			}
		}
		if !found {
			t.Fatalf("shared generation %s missing from kernel policy rules: %+v", genDir, policy.FilesystemRules)
		}
		for _, rule := range policy.FilesystemRules {
			if grantsWriteOver(rule, genDir) {
				t.Fatalf("rule grants WRITE over the read-only shared generation: %+v", rule)
			}
		}
	}
}

// TestBashDocumentCacheSymlinkRefused pins the cache-path confinement fix: a
// pre-existing symlink at <turn root>/.omnipus-document-cache must be refused
// (never followed), the command still runs with the truthful notice, and —
// the escalation the fix exists for — the kernel policy must not grant write
// through the link to its outside target.
func TestBashDocumentCacheSymlinkRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixture needs POSIX")
	}
	home := t.TempDir()
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	layout := provisionLayout(t, home, "mia")
	outside := filepath.Join(home, "outside-target")
	if mkdirErr := os.MkdirAll(outside, 0o755); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	if linkErr := os.Symlink(outside, filepath.Join(workspace, ".omnipus-document-cache")); linkErr != nil {
		t.Fatal(linkErr)
	}
	tool, err := NewExecToolWithDeps(workspace, true, nil, ExecToolDeps{GodMode: false})
	if err != nil {
		t.Fatal(err)
	}
	tool.SetDocumentRuntime(layout)

	result := tool.Execute(bashCtx(t), map[string]any{"command": "printf should-run-anyway"})
	if result.IsError || !strings.Contains(result.ForLLM, "should-run-anyway") {
		t.Fatalf("refused cache must not block the command: %+v", result)
	}
	if !strings.Contains(result.ForLLM, "document runtime is not ready") {
		t.Fatalf("refused cache must be reported truthfully: %+v", result)
	}
	// No injection: the broken layer composes no managed entries.
	envResult := tool.Execute(bashCtx(t), map[string]any{"command": `printf 'path=%s cache=%s\n' "$PATH" "$XDG_CACHE_HOME"`})
	if envResult.IsError {
		t.Fatalf("env report failed: %+v", envResult)
	}
	if strings.Contains(extractEnvVar(envResult.ForLLM, "path="), layout.Bin) {
		t.Fatalf("refused cache must keep the managed prefix out of PATH: %+v", envResult)
	}
	if extractEnvVar(envResult.ForLLM, "cache=") != "" {
		t.Fatalf("refused cache must not set cache env: %+v", envResult)
	}

	// The escalation path: the per-turn policy must not grant write through
	// the link. The doc layer executeRun derives is the refused one.
	sandbox.RegisterTurnPolicyBase(&sandbox.TurnPolicyInput{HomePath: home, Model: sandbox.FilesystemModelOpen})
	t.Cleanup(func() { sandbox.RegisterTurnPolicyBase(nil) })
	ctx := WithTurnWorkspaceDir(WithAgentID(context.Background(), "mia"), workspace)
	resolved, err := tool.resolveCWD(ctx, nil, workspace)
	if err != nil {
		t.Fatal(err)
	}
	docLayer := tool.documentEnvTurn(workspace)
	if docLayer.ok {
		t.Fatalf("symlinked cache must refuse readiness: %+v", docLayer)
	}
	rtLayer, rtErr := runtimeEnvTurn(workspace)
	if rtErr != nil {
		t.Fatalf("resolve runtime env: %v", rtErr)
	}
	policy, err := tool.turnKernelPolicy(ctx, resolved, rtLayer, docLayer, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range policy.FilesystemRules {
		if rule.Path == outside && rule.Access&sandbox.AccessWrite != 0 {
			t.Fatalf("kernel policy grants write on the symlink target outside the turn root: %+v", rule)
		}
	}
	for _, rule := range policy.FilesystemRules {
		if grantsWriteOver(rule, outside) {
			t.Fatalf("a rule carries write over the outside symlink target: %+v", rule)
		}
	}
}

// TestBashGenericRuntimeStateReportedNotSilent covers the runtime-side review
// note: corrupt or unresolvable installed-runtimes state must produce a
// truthful notice (a silent fall-through could run a host program of the same
// name), while unrelated commands keep running.
func TestBashGenericRuntimeStateReportedNotSilent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX shell and permission bits")
	}
	t.Run("broken generation marker", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv(config.EnvHome, home)
		unlockPublished(t, home)
		workspace, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		// Two generations: the first stays valid and on PATH; the second loses
		// its marker and must disappear behind a truthful notice.
		first, err := environmentsetup.BeginInstall(home, "", environmentsetup.ScopeShared)
		if err != nil {
			t.Fatalf("begin shared install: %v", err)
		}
		writeFakeManagedPython(t, filepath.Join(first.Prefix(), "bin"))
		firstPub, err := first.Commit()
		if err != nil {
			t.Fatalf("commit shared generation: %v", err)
		}
		validBin := filepath.Join(firstPub.Dir, "bin")

		second, err := environmentsetup.BeginInstall(home, "", environmentsetup.ScopeShared)
		if err != nil {
			t.Fatalf("begin second shared install: %v", err)
		}
		writeFakeManagedPython(t, filepath.Join(second.Prefix(), "bin"))
		published, err := second.Commit()
		if err != nil {
			t.Fatalf("commit second shared generation: %v", err)
		}
		// Removing the top-level files destroys the install marker storage
		// wrote at Commit — readFileLimited then fails, which storage
		// classifies as a broken (non-fatal) generation. The published tree
		// is read-only (0555 dirs / 0444 files), so the generation DIR needs
		// its write bit back before anything inside can be unlinked.
		if chmodErr := os.Chmod(published.Dir, 0o755); chmodErr != nil {
			t.Fatal(chmodErr)
		}
		entries, err := os.ReadDir(published.Dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.Type().IsRegular() {
				marker := filepath.Join(published.Dir, entry.Name())
				if markerErr := os.Chmod(marker, 0o644); markerErr != nil {
					t.Fatal(markerErr)
				}
				if rmErr := os.Remove(marker); rmErr != nil {
					t.Fatal(rmErr)
				}
			}
		}

		tool, err := NewExecToolWithDeps(workspace, true, nil, ExecToolDeps{GodMode: false})
		if err != nil {
			t.Fatal(err)
		}
		result := tool.Execute(bashCtx(t), map[string]any{"command": `printf 'path=%s\n' "$PATH"`})
		if result.IsError {
			t.Fatalf("command must run despite the corrupt generation: %+v", result)
		}
		if !strings.Contains(result.ForLLM, "unreadable or corrupt") {
			t.Fatalf("corrupt generation must be reported truthfully: %+v", result)
		}
		pathValue := extractEnvVar(result.ForLLM, "path=")
		if !strings.Contains(pathValue, validBin) {
			t.Fatalf("the valid generation must stay on PATH: %q", pathValue)
		}
		if strings.Contains(pathValue, published.Dir) {
			t.Fatalf("the corrupt generation must be absent from PATH: %q", pathValue)
		}
	})

	t.Run("unresolvable env root", func(t *testing.T) {
		workspace, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		// A symlink loop at <workspace>/.omnipus makes os.Stat on the env root
		// fail with ELOOP — a hard RuntimeEnvPaths error, not "missing state".
		if linkErr := os.Symlink(filepath.Join(workspace, ".omnipus"), filepath.Join(workspace, ".omnipus")); linkErr != nil {
			t.Fatal(linkErr)
		}
		tool, err := NewExecToolWithDeps(workspace, true, nil, ExecToolDeps{GodMode: false})
		if err != nil {
			t.Fatal(err)
		}
		result := tool.Execute(bashCtx(t), map[string]any{"command": "printf should-run-anyway"})
		if result.IsError {
			t.Fatalf("command must run despite unresolvable runtime state: %+v", result)
		}
		if !strings.Contains(result.ForLLM, "could not be resolved") {
			t.Fatalf("unresolvable state must be reported truthfully: %+v", result)
		}
	})
}

// unlockPublished makes a committed (read-only) shared generation tree
// removable again before t.TempDir cleanup: storage's Commit locks the tree
// to 0555/0444, which would otherwise fail RemoveAll.
func unlockPublished(t *testing.T, root string) {
	t.Helper()
	t.Cleanup(func() {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			// Best-effort unlock: a path the walk could not stat is skipped,
			// and the walk continues so later paths still get unlocked.
			if err == nil {
				if d.IsDir() {
					_ = os.Chmod(path, 0o755)
				} else {
					_ = os.Chmod(path, 0o644)
				}
			}
			return nil
		})
	})
}

// grantsWriteOver reports whether any sandbox rule carries WRITE over path.
func grantsWriteOver(rule sandbox.PathRule, path string) bool {
	if rule.Access&sandbox.AccessWrite == 0 {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(rule.Path), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// extractEnvVar pulls var=value back out of a printf report line.
func extractEnvVar(output, prefix string) string {
	for _, line := range strings.Split(output, "\n") {
		if after, ok := strings.CutPrefix(line, prefix); ok {
			return strings.TrimRight(after, "\r")
		}
	}
	return ""
}

// TestBashDocumentRuntimeCorruptRegistryRunsCommandsWithNotice is the
// coordinator-corrected readiness design: a corrupt OPTIONAL Office
// installation must not indiscriminately block unrelated bash commands. The
// command runs on the host environment while the result truthfully reports
// the broken managed runtime — the fallback is loud, never silent.
func TestBashDocumentRuntimeCorruptRegistryRunsCommandsWithNotice(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX shell")
	}
	home := t.TempDir()
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	layout := provisionLayout(t, home, "mia")
	registry := documentruntime.ConverterServiceRegistryDir(layout)
	if mkdirErr := os.MkdirAll(registry, 0o755); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	if writeErr := os.WriteFile(filepath.Join(registry, "services.rdb"), []byte(`<components>`), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	tool, err := NewExecToolWithDeps(workspace, true, nil, ExecToolDeps{GodMode: false})
	if err != nil {
		t.Fatal(err)
	}
	tool.SetDocumentRuntime(layout)
	result := tool.Execute(bashCtx(t), map[string]any{"command": "printf should-run-anyway"})
	if result.IsError || !strings.Contains(result.ForLLM, "should-run-anyway") {
		t.Fatalf("unrelated command must still run on a broken optional runtime: %+v", result)
	}
	if !strings.Contains(result.ForLLM, "document runtime is not ready") {
		t.Fatalf("broken managed runtime must be reported truthfully: %+v", result)
	}
	// The broken managed environment must NOT be injected: the managed python
	// is absent from PATH composition while the runtime is corrupt.
	envResult := tool.Execute(bashCtx(t), map[string]any{"command": `printf 'path=%s cache=%s\n' "$PATH" "$XDG_CACHE_HOME"`})
	if envResult.IsError {
		t.Fatalf("env report failed: %+v", envResult)
	}
	if strings.Contains(extractEnvVar(envResult.ForLLM, "path="), layout.Bin) {
		t.Fatalf("broken managed prefix must not enter PATH: %+v", envResult)
	}
	if extractEnvVar(envResult.ForLLM, "cache=") != "" {
		t.Fatalf("broken managed runtime must not set cache env: %+v", envResult)
	}
	if !strings.Contains(envResult.ForLLM, "document runtime is not ready") {
		t.Fatalf("notice must accompany every command while broken: %+v", envResult)
	}
}

// TestBashDocumentRuntimeCorruptRegistryNoticeAcrossSpawnModes extends the
// corrected readiness design to every spawn mode: foreground/background and
// god-mode/sandboxed all run the command and all carry the honest notice.
func TestBashDocumentRuntimeCorruptRegistryNoticeAcrossSpawnModes(t *testing.T) {
	for _, tc := range []struct {
		name                string
		godMode, background bool
	}{
		{"unrestricted-foreground", true, false},
		{"unrestricted-background", true, true},
		{"sandbox-background", false, true},
		{"sandbox-foreground", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			workspace, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			layout := provisionLayout(t, home, "mia")
			registry := documentruntime.ConverterServiceRegistryDir(layout)
			if mkdirErr := os.MkdirAll(registry, 0o755); mkdirErr != nil {
				t.Fatal(mkdirErr)
			}
			if writeErr := os.WriteFile(filepath.Join(registry, "services.rdb"), []byte(`<components>`), 0o644); writeErr != nil {
				t.Fatal(writeErr)
			}
			tool, err := NewExecToolWithDeps(workspace, true, nil, ExecToolDeps{GodMode: tc.godMode})
			if err != nil {
				t.Fatal(err)
			}
			tool.sessionManager = &SessionManager{sessions: make(map[string]*ProcessSession)}
			t.Cleanup(func() { tool.sessionManager.KillAll() })
			tool.SetDocumentRuntime(layout)
			result := tool.Execute(bashCtx(t), map[string]any{"command": "echo marker > started.txt", "run_in_background": tc.background})
			if !strings.Contains(result.ForLLM, "document runtime is not ready") {
				t.Errorf("every spawn mode must carry the truthful notice: %+v", result)
			}
			if tc.background && len(tool.sessionManager.List()) != 1 {
				t.Errorf("background command must still register its session: %+v", tool.sessionManager.List())
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(workspace, "started.txt")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("command must still execute in mode %s (no marker written)", tc.name)
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}

func TestDocumentRuntimeSocketPolicyUsesResolvedWorkspaceCWD(t *testing.T) {
	home := t.TempDir()
	agentHome := filepath.Join(home, "agents", "mia")
	workspace := filepath.Join(home, "workspaces", "shared", "work")
	subdir := filepath.Join(workspace, "renders")
	for _, dir := range []string{agentHome, subdir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	layout, err := documentruntime.ResolveLayout(filepath.Join(home, "runtime"), documentruntime.ManifestRevision, "mia")
	if err != nil {
		t.Fatal(err)
	}
	tool, err := NewExecToolWithDeps(agentHome, true, nil, ExecToolDeps{GodMode: false})
	if err != nil {
		t.Fatal(err)
	}
	tool.SetDocumentRuntime(layout)
	sandbox.RegisterTurnPolicyBase(&sandbox.TurnPolicyInput{HomePath: home, Model: sandbox.FilesystemModelOpen})
	t.Cleanup(func() { sandbox.RegisterTurnPolicyBase(nil) })

	ctx := WithTurnWorkspaceDir(WithAgentID(context.Background(), "mia"), workspace)
	for _, tc := range []struct{ name, cwd string }{{"workspace root", ""}, {"workspace subdirectory", "renders"}} {
		t.Run(tc.name, func(t *testing.T) {
			resolved, err := tool.resolveCWD(ctx, map[string]any{"cwd": tc.cwd}, workspace)
			if err != nil {
				t.Fatal(err)
			}
			rtLayer, rtErr := runtimeEnvTurn(workspace)
			if rtErr != nil {
				t.Fatalf("resolve runtime env: %v", rtErr)
			}
			policy, err := tool.turnKernelPolicy(ctx, resolved, rtLayer, documentEnvLayer{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(policy.UnixSocketRules) != 1 || policy.UnixSocketRules[0].Path != resolved {
				t.Fatalf("socket rules=%+v, want resolved cwd %q", policy.UnixSocketRules, resolved)
			}
			if policy.UnixSocketRules[0].Path == agentHome {
				t.Fatalf("socket policy incorrectly used agent home %q", agentHome)
			}
		})
	}
}

// TestDocumentProbeFormatFlagDoesNotRelaxDiskWipeGuard used to pin that the
// document probe's own `--format all` flag never tripped the disk-wipe deny
// pattern (`format`/`mkfs`/`diskpart`), and that real disk-wipe commands
// still did. ADR-091 D2 deletes the whole regex block-list layer those
// patterns lived in — disk-wipe-by-device-name is one of the categories D2's
// own text names as accepted, undefended residual risk ("neither filesystem
// nor network operations... nothing in this ADR covers them"), so there is
// nothing left for this test to pin: the probe's format flag was never
// blocked by anything else, and neither is a real disk-wipe command anymore.
// See ADR-091 D2/D8's own accepted-risk language, not a gap introduced here.

func TestDocumentSkillLoadPublishesAuthorizedRuntimeRoot(t *testing.T) {
	layout, err := documentruntime.ResolveLayout(t.TempDir(), documentruntime.ManifestRevision, "mia")
	if err != nil {
		t.Fatal(err)
	}
	tool := NewSkillTool(5)
	tool.SetResolver(func(_ context.Context, slug string) SkillLoadOutcome {
		return SkillLoadOutcome{Status: SkillLoadLoaded, Content: "instructions"}
	}, func(context.Context, string) bool { return true }, nil)
	tool.SetDocumentRuntime(layout)
	result := tool.Execute(bashCtx(t), map[string]any{"name": "elicify-docx"})
	wantRoot := filepath.Join(layout.Skills, "elicify-docx")
	if result.IsError || !strings.Contains(result.ForLLM, wantRoot) || !strings.Contains(result.ForLLM, layout.Manifest) {
		t.Fatalf("result=%+v", result)
	}
}

// TestBashDescriptionDoesNotPublishAdminFinalize pins the GS-02 removal: the
// Admin-only finalization route is gone from the agent-facing description.
func TestBashDescriptionDoesNotPublishAdminFinalize(t *testing.T) {
	tool, err := NewExecToolWithDeps(t.TempDir(), true, nil, ExecToolDeps{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tool.Description(), "omnipus-document-runtime finalize") ||
		strings.Contains(tool.Description(), "Admin can publish") {
		t.Fatalf("bash description still publishes the retired Admin finalize route: %q", tool.Description())
	}
}

// TestBashFinalizeCommandIsNoLongerIntercepted proves the former magic
// command now goes through the ordinary command path: no structured Admin
// setup result, no special-cased success — an ordinary command failure.
func TestBashFinalizeCommandIsNoLongerIntercepted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX shell")
	}
	home := t.TempDir()
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	layout := provisionLayout(t, home, "admin")
	tool, err := NewExecToolWithDeps(workspace, true, nil, ExecToolDeps{GodMode: false})
	if err != nil {
		t.Fatal(err)
	}
	tool.SetDocumentRuntime(layout)
	result := tool.Execute(bashCtx(t), map[string]any{"command": "omnipus-document-runtime finalize"})
	if strings.Contains(result.ForLLM, `"status":"ready"`) ||
		strings.Contains(result.ForLLM, "restricted to Admin") ||
		strings.Contains(result.ForLLM, "setup incomplete") {
		t.Fatalf("finalize command must not be intercepted any more: %+v", result)
	}
}

func TestBashDocumentRuntimeActualProbe(t *testing.T) {
	prefix := os.Getenv("OMNIPUS_DOCUMENT_RUNTIME_E2E_PREFIX")
	if prefix == "" {
		t.Skip("set OMNIPUS_DOCUMENT_RUNTIME_E2E_PREFIX to a provisioned manifest prefix")
	}
	dataRoot := filepath.Dir(filepath.Dir(filepath.Dir(prefix)))
	layout, err := documentruntime.ResolveLayout(dataRoot, filepath.Base(prefix), "mia-e2e")
	if err != nil {
		t.Fatal(err)
	}
	if mkdirErr := os.MkdirAll(layout.Cache, 0o755); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	workspace := t.TempDir()
	tool, err := NewExecToolWithDeps(workspace, false, nil, ExecToolDeps{GodMode: false})
	if err != nil {
		t.Fatal(err)
	}
	tool.SetDocumentRuntime(layout)
	result := tool.Execute(bashCtx(t), map[string]any{"command": strings.Join(documentruntime.ProbeArgv(layout), " "), "timeout_seconds": float64(120)})
	if result.IsError || !strings.Contains(result.ForLLM, `"ok": true`) {
		t.Fatalf("actual worker probe failed: %+v", result)
	}
}
