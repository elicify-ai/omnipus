// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build linux

package preview

// T-125 (b), T-126 and T-128 (a)–(d) — office-preview spec §3.8 (BR-2,
// BR-2.4, BR-2.5; FR-065, FR-066; founder Q100 A), the REAL Linux rows of
// job `office-preview-real` (scripts/office-preview-real/real-tests.txt —
// a wiring note; scripts/ is not a test-author file).
//
// These tests build the gateway's startup rules WITH THE PRODUCTION CODE,
// exactly as the spec mandates for T-126: the §3.8 grant helper in
// pkg/preview/component (component.ConfinementStartupGrants) and the new
// boot sibling sandbox.DefaultBootPolicyForModel. Both do not exist yet, so
// this file does not compile on 24f2f912c — the spec's own RED staging
// (§13 note: "the new function and helper are introduced as stubs first if
// needed"): the developer lands compiling stubs, and from that moment every
// red here is a failing assertion carrying the real permission failure, not
// a compile failure. The pure-policy RED that compiles today lives in
// pkg/sandbox and pkg/preview/component.
//
// Oracles are the spec's texts and page counts, never a converter run: the
// BR-2.5 after-start sentences, StatusReady + "packaged: <slot>" sources,
// FX-DOCX6 → 6 pages with its source strings, the 30-page FX-PDF30 → 1
// page image for page "3", EACCES for the agent's derived policy, and the
// forced-re-exec machinery the T-114/Q1 B rows already use (the production
// reExecProcess code path is unix.Exec of the pre-resolved executable with
// the original argv and environment, pkg/gateway/rest_gateway_restart.go).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/tests/officepreview/realprograms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const (
	// Role dispatch for the forced re-exec, per test (set by the image that
	// applies the startup rules, read by the re-exec'd image).
	t126RoleEnv = "OMNIPUS_TEST_BOOTGRANT_ROLE"
	// Every staged artifact of one orchestration lives under this root, so
	// the re-exec'd image (and the unconfined helper) find it by env.
	t126BaseEnv = "OMNIPUS_TEST_BOOTGRANT_BASE"

	// t126HelperArg is the unconfined helper process's argv marker. The
	// helper is spawned by the pre-enforcement image and performs the
	// post-start manipulations the enforced image cannot: installing a
	// component into the prefix, replacing one in place, and running
	// RegisterOffice (T-128 a–c follow the spec's "separate, unconfined
	// process" pattern).
	t126HelperArg = "__boot-grant-helper"

	// t126ExecProbeArg is the exec probe's argv marker: the child execs the
	// path in argv[2] and reports the permission outcome on its exit code,
	// because functional_probe's probe has no exec operation.
	t126ExecProbeArg = "__boot-grant-exec-probe"
)

// Exec-probe exit codes (an errno cannot cross exec; the runner maps them
// back to labels).
const (
	t126ProbeStarted = 0
	t126ProbeEACCES  = 13
	t126ProbeEPERM   = 1
	t126ProbeOther   = 125
)

// BR-2.5 after-start sentences, word for word from the spec (T-128 row).
const (
	t128OfficeAfterStart = "Office preview was installed or registered after Omnipus started. Stop and start Omnipus (for example with your service manager) to use it; the in-app restart is not enough in the confined sandbox."
	t128RendererText     = "PDF renderer was installed or registered after Omnipus started; stop and start Omnipus (for example with your service manager) to use it; the in-app restart is not enough in the confined sandbox"
)

func init() {
	if len(os.Args) == 3 && os.Args[1] == t126ExecProbeArg {
		os.Exit(t126DoExecProbe(os.Args[2]))
	}
	if len(os.Args) == 3 && os.Args[1] == t126HelperArg {
		t126HelperMain(os.Args[2])
		os.Exit(0)
	}
}

// t126DoExecProbe starts the program at path. The exit code says whether the
// program STARTED (permission to execute granted), not how the program fared.
func t126DoExecProbe(path string) int {
	cmd := exec.Command(path)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "exec probe: %v\n", err)
		switch {
		case errors.Is(err, unix.EACCES):
			return t126ProbeEACCES
		case errors.Is(err, unix.EPERM):
			return t126ProbeEPERM
		default:
			return t126ProbeOther
		}
	}
	// Started: its own exit status is the program's business.
	_ = cmd.Wait()
	return t126ProbeStarted
}

// t126RunExecProbe mirrors functionalRunProbe for the exec probe: the child
// goes through hooks (the agent shell spawn path) or runs plain (nil hooks,
// the unconfined control). Returns "ok", "EACCES", "EPERM" or the failure.
func t126RunExecProbe(t *testing.T, hooks *spawnHooks, path, work string) string {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(self, t126ExecProbeArg, path)
	cmd.Dir = work
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	cmd.Stdin = nil
	var release func()
	if hooks == nil {
		err = cmd.Start()
		if err == nil {
			release = func() {}
		}
	} else {
		require.Empty(t, hooks.unconfined, "T-125(b): unconfined probes are not evidence")
		require.NotNil(t, hooks.start, "T-125(b): the actual Linux start hook is required")
		release, err = hooks.start(cmd)
	}
	require.NoError(t, err, "probe start: %v", err)
	if release != nil {
		defer release()
	}
	runErr := cmd.Wait()
	if runErr == nil {
		return "ok"
	}
	var exitErr *exec.ExitError
	require.ErrorAs(t, runErr, &exitErr, "instrument: exec probe must report a status")
	switch exitErr.ExitCode() {
	case t126ProbeStarted:
		return "ok"
	case t126ProbeEACCES:
		return "EACCES"
	case t126ProbeEPERM:
		return "EPERM"
	default:
		return fmt.Sprintf("other(exit=%d)", exitErr.ExitCode())
	}
}

// t126Layout is one orchestration's staged world.
type t126Layout struct {
	Base        string // scratch root (outside /tmp and the data folder)
	Prefix      string // P
	Home        string // the gateway home (outside /tmp)
	Bin         string // P/bin/omnipus (copy of the test binary = the program file)
	OfficeSlot  string // P/share/omnipus/office (present or absent per scenario)
	PdfiumSlot  string // P/share/omnipus/pdfium
	OfficeSrc   string // pristine real LibreOffice (staged outside P)
	RendererSrc string // pristine real renderer release folder
}

// t126PinBase returns the staging root, mirroring functionalPinDataOutsideTmp.
func t126PinBase(t *testing.T) string {
	t.Helper()
	functionalPinDataOutsideTmp(t)
	base := os.Getenv("OMNIPUS_TEST_OFFICE_DATA_ROOT")
	if base == "" {
		var err error
		base, err = os.Getwd()
		require.NoError(t, err)
	}
	return base
}

// t126RequirePrefixOutside asserts, before anything else, that P is outside
// /opt, /usr, /tmp, $TMPDIR and the gateway home — the spec's T-126 guard:
// those are already granted (or writable), and the test could not see the
// defect from inside them.
func t126RequirePrefixOutside(t *testing.T, prefix, home string) {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(prefix)
	require.NoError(t, err)
	banned := []string{"/opt", "/usr"}
	for _, b := range banned {
		rb, bErr := filepath.EvalSymlinks(b)
		require.NoError(t, bErr)
		require.False(t, resolved == rb || strings.HasPrefix(resolved, rb+string(os.PathSeparator)),
			"T-126: prefix %s must lie outside %s, or the test cannot see the defect", prefix, b)
	}
	tmp, tmpErr := filepath.EvalSymlinks("/tmp")
	require.NoError(t, tmpErr)
	require.False(t, resolved == tmp || strings.HasPrefix(resolved, tmp+string(os.PathSeparator)),
		"T-126: prefix %s must lie outside /tmp", prefix)
	if tmpdir := os.Getenv("TMPDIR"); tmpdir != "" {
		if rt, err := filepath.EvalSymlinks(tmpdir); err == nil {
			require.False(t, resolved == rt || strings.HasPrefix(resolved, rt+string(os.PathSeparator)),
				"T-126: prefix %s must lie outside $TMPDIR (%s)", prefix, tmpdir)
		}
	}
	rh, hErr := filepath.EvalSymlinks(home)
	require.NoError(t, hErr)
	require.False(t, resolved == rh || strings.HasPrefix(resolved, rh+string(os.PathSeparator)),
		"T-126: prefix %s must lie outside the test's home %s", prefix, home)
}

// t126StagePrefix builds the T-126 world: P with its packaged slots (per the
// flags), P/bin/omnipus as a copy of the test binary, and a home outside
// /tmp. The office slot's manifest is written by the production writer; the
// renderer release folder ships its own packaged manifest.
func t126StagePrefix(t *testing.T, withOffice, withRenderer bool) *t126Layout {
	t.Helper()
	officeSrc, rendererSrc := realprograms.Roots(t, true, true)
	base, err := os.MkdirTemp(t126PinBase(t), "bootgrant-")
	require.NoError(t, err)
	layout := &t126Layout{
		Base:        base,
		Prefix:      filepath.Join(base, "prefix"),
		Home:        filepath.Join(base, "home"),
		OfficeSrc:   officeSrc,
		RendererSrc: rendererSrc,
		Bin:         filepath.Join(base, "prefix", "bin", "omnipus"),
	}
	layout.OfficeSlot = filepath.Join(layout.Prefix, "share", "omnipus", "office")
	layout.PdfiumSlot = filepath.Join(layout.Prefix, "share", "omnipus", "pdfium")
	t126RequirePrefixOutside(t, layout.Prefix, layout.Home)
	require.NoError(t, os.MkdirAll(filepath.Join(layout.Prefix, "bin"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(layout.Prefix, "share", "omnipus"), 0o755))
	require.NoError(t, os.MkdirAll(layout.Home, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(layout.Home, "scratch"), 0o700))
	self, err := os.Executable()
	require.NoError(t, err)
	q1bCopyFile(t, self, layout.Bin, 0o755)
	if withOffice {
		require.NoError(t, os.MkdirAll(layout.OfficeSlot, 0o755))
		functionalCopyTree(t, officeSrc, layout.OfficeSlot)
		require.NoError(t, component.WritePackagedOfficeManifest(layout.OfficeSlot, component.PackagedOfficeOptions{
			GOOS: "linux", GOARCH: runtime.GOARCH,
		}))
	} else {
		// The after-start scenarios need the pristine tree outside P.
		pristine := filepath.Join(base, "install-office")
		require.NoError(t, os.MkdirAll(pristine, 0o755))
		functionalCopyTree(t, officeSrc, pristine)
		layout.OfficeSrc = pristine
	}
	if withRenderer {
		require.NoError(t, os.MkdirAll(layout.PdfiumSlot, 0o755))
		functionalCopyTree(t, rendererSrc, layout.PdfiumSlot)
	} else {
		pristine := filepath.Join(base, "install-pdfium")
		require.NoError(t, os.MkdirAll(pristine, 0o755))
		functionalCopyTree(t, rendererSrc, pristine)
		layout.RendererSrc = pristine
		require.FileExists(t, filepath.Join(pristine, "manifest.json"),
			"BLOCKED: the real renderer fixture is an unpacked release folder and must carry its packaged manifest.json")
	}
	if withRenderer {
		require.FileExists(t, filepath.Join(layout.PdfiumSlot, "manifest.json"),
			"BLOCKED: the real renderer release folder must carry its packaged manifest.json")
	}
	return layout
}

// t126GrantsAndPolicy builds the startup rules with the production code,
// exactly as the spec's T-126 mandates (BR-2.2): the §3.8 grant helper, then
// the new boot sibling (nil allowed paths — the Linux exec-path seed is
// empty).
func t126GrantsAndPolicy(t *testing.T, layout *t126Layout) ([]string, *sandbox.SandboxPolicy) {
	t.Helper()
	resolver := &component.Resolver{Home: layout.Home, Executable: layout.Bin}
	grants, grantNotes := component.ConfinementStartupGrants(resolver)
	t.Logf("§3.8: startup grants=%v notes=%v", grants, grantNotes)
	policy := sandbox.DefaultBootPolicyForModel(sandbox.FilesystemModelConfined, layout.Home, nil, nil, nil, nil, grants)
	return grants, &policy
}

// t126ApplyAndReexec applies the startup rules to a locked thread and
// re-execs through the production reExecProcess code path (unix.Exec of the
// pre-resolved executable with the original argv and environment). Only
// returns on failure.
func t126ApplyAndReexec(t *testing.T, layout *t126Layout, grants []string, policy *sandbox.SandboxPolicy, role string) {
	t.Helper()
	require.NoError(t, os.Setenv("TMPDIR", filepath.Join(layout.Home, "scratch")))
	require.NoError(t, os.Setenv(t126RoleEnv, role))
	require.NoError(t, os.Setenv(t126BaseEnv, layout.Base))
	runtime.LockOSThread()
	backend, ok := sandbox.NewLinuxBackend()
	require.True(t, ok, "BLOCKED: enforcing Linux Landlock unavailable — required by §3.8; an unconfined run fails, never skips")
	require.NoError(t, backend.ApplyWithMode(*policy, sandbox.ModeEnforce))
	require.True(t, backend.EnforcingChildren(), "§3.8: no unconfined acceptance")
	err := unix.Exec(layout.Bin, os.Args, os.Environ())
	t.Fatalf("§3.8 forced re-exec of %s: %v", layout.Bin, err)
}

// t126InstallStartupRules is the re-exec'd image's first breath, and it does
// what the gateway does: the spec's T-126 and T-128(d) have the new image
// APPLY its own startup rules, and BR-2.5 needs the grant record —
// ConfinementStartupGrants → DefaultBootPolicyForModel →
// RecordStartupGrants → ApplyWithMode → component.SetStartupGrants, the
// gateway's exact sequence (pkg/gateway/sandbox_apply.go). A fresh backend
// object enforces nothing until ApplyWithMode runs.
func t126InstallStartupRules(t *testing.T, layout *t126Layout) {
	t.Helper()
	require.Equal(t, layout.Base, os.Getenv(t126BaseEnv), "instrument: staged base carried through the re-exec")
	backend, ok := sandbox.NewLinuxBackend()
	require.True(t, ok, "BLOCKED: enforcing Linux Landlock unavailable — required by §3.8; an unconfined run fails, never skips")
	grants, policy := t126GrantsAndPolicy(t, layout)
	rec, recordNotes := RecordStartupGrants(*policy, grants)
	require.NoError(t, backend.ApplyWithMode(*policy, sandbox.ModeEnforce))
	require.True(t, backend.EnforcingChildren(), "§3.8: the re-exec'd image must re-apply its own startup rules and enforce them (no unconfined acceptance)")
	component.SetStartupGrants(rec)
	t.Logf("§3.8: startup rules re-applied and the BR-2.5 record installed; record notes=%v", recordNotes)
}

// t126SpawnHelper starts the unconfined helper process with an ops file and
// does not wait for it: the enforced image drives it through trigger files.
func t126SpawnHelper(t *testing.T, layout *t126Layout, ops []t126HelperOp) {
	t.Helper()
	raw, err := json.Marshal(ops)
	require.NoError(t, err)
	opsPath := filepath.Join(layout.Base, "helper-ops.json")
	require.NoError(t, os.WriteFile(opsPath, raw, 0o600))
	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(self, t126HelperArg, opsPath)
	cmd.Env = append(os.Environ(), t126BaseEnv+"="+layout.Base)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start(), "T-128: the unconfined helper must start")
}

type t126HelperOp struct {
	Op      string `json:"op"`      // install-office | install-renderer | replace-office | register-office
	Trigger string `json:"trigger"` // file the enforced image creates
	Done    string `json:"done"`    // file the helper creates
}

// t126HelperMain is the helper process body: unconfined (spawned before any
// enforcement), it performs each op when its trigger appears.
func t126HelperMain(opsPath string) {
	raw, err := os.ReadFile(opsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "helper: %v\n", err)
		os.Exit(64)
	}
	var ops []t126HelperOp
	if err := json.Unmarshal(raw, &ops); err != nil {
		fmt.Fprintf(os.Stderr, "helper: %v\n", err)
		os.Exit(64)
	}
	base := os.Getenv(t126BaseEnv)
	deadline := time.Now().Add(6 * time.Minute)
	for _, op := range ops {
		for !t126FileExists(filepath.Join(base, op.Trigger)) {
			if time.Now().After(deadline) {
				fmt.Fprintf(os.Stderr, "helper: trigger %s never appeared\n", op.Trigger)
				os.Exit(65)
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err := t126PerformOp(base, op.Op); err != nil {
			fmt.Fprintf(os.Stderr, "helper: %s: %v\n", op.Op, err)
			os.Exit(66)
		}
		if err := os.WriteFile(filepath.Join(base, op.Done), []byte("done\n"), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "helper: %v\n", err)
			os.Exit(67)
		}
	}
}

func t126FileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func t126PerformOp(base, op string) error {
	switch op {
	case "install-office":
		return os.Rename(filepath.Join(base, "install-office"), filepath.Join(base, "prefix", "share", "omnipus", "office"))
	case "install-renderer":
		return os.Rename(filepath.Join(base, "install-pdfium"), filepath.Join(base, "prefix", "share", "omnipus", "pdfium"))
	case "replace-office":
		slot := filepath.Join(base, "prefix", "share", "omnipus", "office")
		if err := os.Rename(slot, filepath.Join(base, "replaced-office")); err != nil {
			return err
		}
		if err := os.MkdirAll(slot, 0o755); err != nil {
			return err
		}
		return copyDir(filepath.Join(base, "install-office"), slot)
	case "register-office":
		_, err := component.RegisterOffice(component.RegisterOptions{
			Home:     filepath.Join(base, "home"),
			Folder:   filepath.Join(base, "install-office"),
			Operator: "t128-helper",
		})
		return err
	default:
		return fmt.Errorf("unknown helper op %q", op)
	}
}

// copyDir copies a directory tree (test staging only; the source install is
// never edited).
func copyDir(source, target string) error {
	return filepath.WalkDir(source, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(target, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o755)
	})
}

func t126Trigger(t *testing.T, layout *t126Layout, name string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(layout.Base, name), []byte("go\n"), 0o600))
}

func t126AwaitDone(t *testing.T, layout *t126Layout, name string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for !t126FileExists(filepath.Join(layout.Base, name)) {
		if time.Now().After(deadline) {
			t.Fatalf("T-128: helper never finished %s", name)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestT125B_RealAgentPolicyNeverReachesTheGrants (T-125 b, BR-2.4): under
// T-126's startup rules applied to the whole process, a child started
// through the agent shell spawn path with the agent's DERIVED PER-TURN
// policy cannot read the office slot's manifest nor execute the renderer
// program (EACCES both); the same two actions by an unconfined control
// process succeed.
func TestT125B_RealAgentPolicyNeverReachesTheGrants(t *testing.T) {
	layout := t126StagePrefix(t, true, true)
	if os.Getenv(t126RoleEnv) == "" {
		// Unconfined controls, before any rule exists.
		manifest := filepath.Join(layout.OfficeSlot, "manifest.json")
		controlRead := functionalRunProbe(t, nil, "read", manifest, layout.Home)
		require.Equal(t, "ok", controlRead.Errno, "T-125(b) control: the manifest is readable before the rules")
		rendererProgram := t126RendererProgram(t, layout)
		controlExec := t126RunExecProbe(t, nil, rendererProgram, layout.Home)
		require.Equal(t, "ok", controlExec, "T-125(b) control: the renderer program starts unconfined")
		require.NoError(t, os.WriteFile(filepath.Join(layout.Base, "control-ok"), []byte(controlRead.Errno+","+controlExec+"\n"), 0o600))
		grants, policy := t126GrantsAndPolicy(t, layout)
		t126ApplyAndReexec(t, layout, grants, policy, "child")
	}
	// Enforced image: prove the controls really ran, then deny.
	raw, err := os.ReadFile(filepath.Join(layout.Base, "control-ok"))
	require.NoError(t, err, "T-125(b): the unconfined controls must have run before the rules")
	require.Equal(t, "ok,ok\n", string(raw), "T-125(b): unconfined controls must succeed")
	t126InstallStartupRules(t, layout)

	work := filepath.Join(layout.Home, "scratch")
	agentPolicy, err := sandbox.KernelPolicyForTurn(fspolicy.FSPolicy{WorkDir: work, Scope: fspolicy.FSScopeConfined})
	require.NoError(t, err, "T-125(b): the real tool seam must produce the agent's per-turn policy")
	hooks := spawnHooks{start: func(cmd *exec.Cmd) (func(), error) {
		return sandbox.StartLockedWithPolicyHeld(cmd, agentPolicy)
	}}
	manifest := filepath.Join(layout.OfficeSlot, "manifest.json")
	deniedRead := functionalRunProbe(t, &hooks, "read", manifest, work)
	t.Logf("T-125(b): agent-policy manifest read=%s", deniedRead.Errno)
	require.Equal(t, "EACCES", deniedRead.Errno,
		"T-125(b): the agent's derived per-turn policy must deny the startup-granted office slot (BR-2.4)")

	rendererProgram := t126RendererProgram(t, layout)
	deniedExec := t126RunExecProbe(t, &hooks, rendererProgram, work)
	t.Logf("T-125(b): agent-policy renderer exec=%s", deniedExec)
	require.Equal(t, "EACCES", deniedExec,
		"T-125(b): the agent's derived per-turn policy must deny executing the startup-granted renderer program (BR-2.4)")
}

// t126RendererProgram returns the renderer program's absolute path from the
// staged slot's manifest.
func t126RendererProgram(t *testing.T, layout *t126Layout) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(layout.PdfiumSlot, "manifest.json"))
	require.NoError(t, err)
	var manifest struct {
		Executable string `json:"executable"`
	}
	require.NoError(t, json.Unmarshal(raw, &manifest), "instrument: renderer manifest must name its executable")
	require.NotEmpty(t, manifest.Executable)
	return filepath.Join(layout.PdfiumSlot, filepath.FromSlash(manifest.Executable))
}

// TestT126_RealPackagedComponentsPreviewUnderStartupGrants (BR-2; B-81…B-83):
// both components resolve packaged from P's slots and real previews work
// under the startup rules: A2 (FX-DOCX6) → 6 pages with its source strings,
// A5 (FX-PDF30, page "3") → 1 page image.
func TestT126_RealPackagedComponentsPreviewUnderStartupGrants(t *testing.T) {
	layout := t126StagePrefix(t, true, true)
	if os.Getenv(t126RoleEnv) == "" {
		grants, policy := t126GrantsAndPolicy(t, layout)
		t126ApplyAndReexec(t, layout, grants, policy, "child")
	}
	t126InstallStartupRules(t, layout)
	resolver := &component.Resolver{Home: layout.Home, Executable: layout.Bin}
	office, problem := resolver.Resolve(component.KindOffice)
	require.Nil(t, problem, "T-126: the packaged office slot must resolve")
	require.NotNil(t, office)
	assert.Equal(t, "packaged: "+filepath.Clean(layout.OfficeSlot), office.Source,
		"T-126: the office component must come from P's packaged slot")
	renderer, rendererProblem := resolver.Resolve(component.KindPDFRenderer)
	require.Nil(t, rendererProblem, "T-126: the packaged renderer slot must resolve")
	require.NotNil(t, renderer)
	assert.Equal(t, "packaged: "+filepath.Clean(layout.PdfiumSlot), renderer.Source,
		"T-126: the renderer component must come from P's packaged slot")

	svc, err := New(Config{Home: layout.Home, Self: layout.Bin, Resolver: resolver})
	require.NoError(t, err)
	t126PreviewA2(t, svc, "T-126")
	t126PreviewA5(t, svc, "T-126")
}

// TestT128A_InstalledAfterStartIsDeniedForBoth (BR-2.5 a): start with no
// component in P; after the rules are applied, the unconfined helper puts
// LibreOffice there, then the renderer. Both previews return the exact
// BR-2.5 sentences, and the converter never starts.
func TestT128A_InstalledAfterStartIsDeniedForBoth(t *testing.T) {
	layout := t126StagePrefix(t, false, false)
	if os.Getenv(t126RoleEnv) == "" {
		t126SpawnHelper(t, layout, []t126HelperOp{
			{Op: "install-office", Trigger: "a-office.trg", Done: "a-office.done"},
			{Op: "install-renderer", Trigger: "a-renderer.trg", Done: "a-renderer.done"},
		})
		grants, policy := t126GrantsAndPolicy(t, layout)
		t126ApplyAndReexec(t, layout, grants, policy, "child")
	}
	t126InstallStartupRules(t, layout)
	resolver := &component.Resolver{Home: layout.Home, Executable: layout.Bin}
	svc, err := New(Config{Home: layout.Home, Self: layout.Bin, Resolver: resolver})
	require.NoError(t, err)

	starts := 0
	testHookConverterStart = func(converterStart) { starts++ }
	t.Cleanup(func() { testHookConverterStart = nil })
	rendererStarts := 0
	testHookRendererStart = func(converterStart) { rendererStarts++ }
	t.Cleanup(func() { testHookRendererStart = nil })

	t126Trigger(t, layout, "a-office.trg")
	t126AwaitDone(t, layout, "a-office.done")
	res, previewErr := t126PreviewOffice(t, svc)
	require.Error(t, previewErr, "T-128(a): the after-start office component must not be used")
	require.EqualError(t, previewErr, t128OfficeAfterStart,
		"T-128(a): the office denial text must be the BR-2.5 sentence word for word; got %q", previewErr)
	require.Nil(t, res)
	require.Zero(t, starts, "T-128(a): the converter must never start for an after-start component")

	t126Trigger(t, layout, "a-renderer.trg")
	t126AwaitDone(t, layout, "a-renderer.done")
	res, _ = t126PreviewPDF(t, svc, 3, 3)
	require.NotNil(t, res, "T-128(a): the PDF source itself still previews")
	require.Equal(t, "renderer_damaged", res.RenderReason, "T-128(a): the renderer denial reason")
	require.Equal(t, t128RendererText, res.RenderDetail,
		"T-128(a): the renderer denial text must be the BR-2.5 sentence word for word; got %q", res.RenderDetail)
	require.Empty(t, res.Pages, "T-128(a): no page image from an after-start renderer")
	require.Zero(t, rendererStarts, "T-128(a): the renderer must never start for an after-start component")
}

// TestT128B_RegisteredAfterStartIsDenied (BR-2.5 b): the helper runs
// RegisterOffice from a separate, unconfined process after the start; the
// same Office sentence follows.
func TestT128B_RegisteredAfterStartIsDenied(t *testing.T) {
	layout := t126StagePrefix(t, false, true)
	if os.Getenv(t126RoleEnv) == "" {
		t126SpawnHelper(t, layout, []t126HelperOp{
			{Op: "register-office", Trigger: "b-register.trg", Done: "b-register.done"},
		})
		grants, policy := t126GrantsAndPolicy(t, layout)
		t126ApplyAndReexec(t, layout, grants, policy, "child")
	}
	t126InstallStartupRules(t, layout)
	resolver := &component.Resolver{Home: layout.Home, Executable: layout.Bin}
	svc, err := New(Config{Home: layout.Home, Self: layout.Bin, Resolver: resolver})
	require.NoError(t, err)
	starts := 0
	testHookConverterStart = func(converterStart) { starts++ }
	t.Cleanup(func() { testHookConverterStart = nil })

	t126Trigger(t, layout, "b-register.trg")
	t126AwaitDone(t, layout, "b-register.done")
	_, previewErr := t126PreviewOffice(t, svc)
	require.Error(t, previewErr, "T-128(b): the after-start registration must not be used")
	require.EqualError(t, previewErr, t128OfficeAfterStart,
		"T-128(b): the office denial text must be the BR-2.5 sentence word for word; got %q", previewErr)
	require.Zero(t, starts, "T-128(b): the converter must never start")
}

// TestT128C_ReplaceInPlaceIsDenied (BR-2.5 c): the component is present at
// the start and granted; the helper replaces it in place after the start
// (rename away, copy back); the same Office sentence follows. A preview
// before the replacement must succeed — (a)–(c) are no pass when nothing
// was confined.
func TestT128C_ReplaceInPlaceIsDenied(t *testing.T) {
	layout := t126StagePrefix(t, true, true)
	if os.Getenv(t126RoleEnv) == "" {
		t126SpawnHelper(t, layout, []t126HelperOp{
			{Op: "replace-office", Trigger: "c-replace.trg", Done: "c-replace.done"},
		})
		grants, policy := t126GrantsAndPolicy(t, layout)
		t126ApplyAndReexec(t, layout, grants, policy, "child")
	}
	t126InstallStartupRules(t, layout)
	resolver := &component.Resolver{Home: layout.Home, Executable: layout.Bin}
	svc, err := New(Config{Home: layout.Home, Self: layout.Bin, Resolver: resolver})
	require.NoError(t, err)

	// Before the replacement the granted component must really work.
	t126PreviewA2(t, svc, "T-128(c) pre-replacement control")
	starts := 0
	testHookConverterStart = func(converterStart) { starts++ }
	t.Cleanup(func() { testHookConverterStart = nil })

	t126Trigger(t, layout, "c-replace.trg")
	t126AwaitDone(t, layout, "c-replace.done")
	_, previewErr := t126PreviewOffice(t, svc)
	require.Error(t, previewErr, "T-128(c): the replaced-in-place component must not be used")
	require.EqualError(t, previewErr, t128OfficeAfterStart,
		"T-128(c): the office denial text must be the BR-2.5 sentence word for word; got %q", previewErr)
	require.Positive(t, starts, "T-128(c) instrument: the pre-replacement control must have started the converter once")
}

// TestT128D_RestartFromRestrictedThreadKeepsGrants (BR-2.5 d): components
// present at the first start; the first image applies its startup rules,
// proves the restarting thread is restricted, and re-execs itself through
// the production reExecProcess code path on that thread; the new image
// applies its own startup rules; A2 → 6 pages, A5 → 1 page image.
func TestT128D_RestartFromRestrictedThreadKeepsGrants(t *testing.T) {
	layout := t126StagePrefix(t, true, true)
	switch role := os.Getenv(t126RoleEnv); role {
	case "":
		proof := filepath.Join(layout.Base, "thread-proof")
		require.NoError(t, os.WriteFile(proof, []byte("readable before the rules\n"), 0o600))
		runtime.LockOSThread()
		backend, ok := sandbox.NewLinuxBackend()
		require.True(t, ok, "BLOCKED: enforcing Linux Landlock unavailable — required by §3.8")
		_, policy := t126GrantsAndPolicy(t, layout)
		require.NoError(t, backend.ApplyWithMode(*policy, sandbox.ModeEnforce))
		require.True(t, backend.EnforcingChildren(), "§3.8: no unconfined acceptance")
		// The restarting thread is restricted: a readable file outside the
		// startup rules must fail with EACCES on THIS thread.
		_, readErr := os.ReadFile(proof)
		require.Error(t, readErr, "T-128(d) instrument: the thread must be restricted before the restart")
		require.ErrorIs(t, readErr, unix.EACCES,
			"T-128(d) instrument: the restriction proof must be a permission failure, not a missing file")
		require.NoError(t, os.Setenv("TMPDIR", filepath.Join(layout.Home, "scratch")))
		require.NoError(t, os.Setenv(t126RoleEnv, "restart-second"))
		require.NoError(t, os.Setenv(t126BaseEnv, layout.Base))
		// reExecProcess's code path, on the proven-restricted thread.
		if err := unix.Exec(layout.Bin, os.Args, os.Environ()); err != nil {
			t.Fatalf("T-128(d) restart re-exec: %v", err)
		}
	case "restart-second":
		t126InstallStartupRules(t, layout)
		resolver := &component.Resolver{Home: layout.Home, Executable: layout.Bin}
		svc, err := New(Config{Home: layout.Home, Self: layout.Bin, Resolver: resolver})
		require.NoError(t, err)
		t126PreviewA2(t, svc, "T-128(d) after restart")
		t126PreviewA5(t, svc, "T-128(d) after restart")
	default:
		t.Fatalf("unknown role %q", role)
	}
}

// t126PreviewOffice previews FX-DOCX6 (A2) and asserts the spec's 6 pages
// and source strings.
func t126PreviewA2(t *testing.T, svc *Service, label string) {
	t.Helper()
	for _, doc := range functionalDocuments(t) {
		if doc.ID != "FX-DOCX6" {
			continue
		}
		res := t126PreviewDoc(t, svc, doc, label)
		require.Equal(t, 6, res.PageCount, "%s: A2 is a 6-page source fixture", label)
		var text strings.Builder
		for _, page := range res.Text {
			text.WriteString(page.Text)
			text.WriteByte(' ')
		}
		joined := strings.Join(strings.Fields(text.String()), " ")
		for _, expected := range doc.Text {
			assert.Contains(t, joined, expected, "%s: A2 must carry its source text %q", label, expected)
		}
		return
	}
	t.Fatalf("instrument: FX-DOCX6 missing from the source fixtures")
}

// t126PreviewA5 previews FX-PDF30 page "3" and asserts 1 page image.
func t126PreviewA5(t *testing.T, svc *Service, label string) {
	t.Helper()
	res, err := t126PreviewPDF(t, svc, 3, 3)
	require.NoError(t, err, "%s: A5 page 3 must render", label)
	require.NotNil(t, res)
	require.Len(t, res.Pages, 1, "%s: A5 page \"3\" is exactly 1 page image", label)
	require.Equal(t, 3, res.Pages[0].Number, "%s: the rendered page is page 3", label)
	require.NotEmpty(t, res.Pages[0].PNG, "%s: the page image must be real bytes", label)
}

func t126PreviewDoc(t *testing.T, svc *Service, doc functionalDocument, label string) *Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), CallTimeout)
	defer cancel()
	adm, err := svc.Admit(ctx, true)
	require.NoError(t, err, "%s: admit", label)
	defer adm.Release()
	result, previewErr := svc.Preview(ctx, adm, Request{Name: label + doc.Ext, Ext: doc.Ext, Data: doc.Data, Default: true})
	require.NoError(t, previewErr, "%s: the granted component must really convert", label)
	require.NotNil(t, result, "%s: a real conversion returns a result", label)
	assert.Empty(t, result.Unconfined, "%s: unconfined conversion is not acceptance", label)
	return result
}

func t126PreviewOffice(t *testing.T, svc *Service) (*Result, error) {
	t.Helper()
	for _, doc := range functionalDocuments(t) {
		if doc.ID != "FX-DOCX6" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), CallTimeout)
		defer cancel()
		adm, err := svc.Admit(ctx, true)
		require.NoError(t, err, "T-128: admit")
		defer adm.Release()
		return svc.Preview(ctx, adm, Request{Name: "t128" + doc.Ext, Ext: doc.Ext, Data: doc.Data, Default: true})
	}
	t.Fatalf("instrument: FX-DOCX6 missing from the source fixtures")
	return nil, nil
}

func t126PreviewPDF(t *testing.T, svc *Service, first, last int) (*Result, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), CallTimeout)
	defer cancel()
	adm, err := svc.Admit(ctx, true)
	require.NoError(t, err, "T-126: admit")
	defer adm.Release()
	return svc.Preview(ctx, adm, Request{Name: "fx-pdf30.pdf", Ext: ".pdf", Data: t126PDF(t, 30), First: first, Last: last})
}

// t126PDF builds FX-PDF30: a 30-page PDF whose page count is the source's,
// not a converter observation. The shapes follow the independently built
// one-page PDF of retry_office_test.go.
func t126PDF(t *testing.T, pages int) []byte {
	t.Helper()
	require.Positive(t, pages)
	var objects []string
	objects = append(objects, "<< /Type /Catalog /Pages 2 0 R >>")
	kids := make([]string, 0, pages)
	for i := 1; i <= pages; i++ {
		kids = append(kids, fmt.Sprintf("%d 0 R", 2+i))
	}
	objects = append(objects, fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", pages, strings.Join(kids, " ")))
	for i := 1; i <= pages; i++ {
		contentRef := 2 + pages + i
		objects = append(objects, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 100] /Contents %d 0 R >>", contentRef))
	}
	for i := 1; i <= pages; i++ {
		objects = append(objects, fmt.Sprintf("<< /Length 0 >>\nstream\n\nendstream"))
	}
	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, object := range objects {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return []byte(b.String())
}
