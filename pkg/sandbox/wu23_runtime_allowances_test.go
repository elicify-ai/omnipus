//go:build linux || darwin

package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/environmentsetup"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	fixture "github.com/elicify-ai/omnipus/tests/l1_red/protectedfixture"
)

// D3 A5/A6 and F01: a real published generation and document prefix must be
// subtracted BEFORE denial, never appended too late and dropped by Landlock.
func wu23RuntimeAllowances(t *testing.T) {
	f := fixture.New(t)
	target, err := environmentsetup.BeginInstall(f.Home, f.Work, environmentsetup.ScopeShared)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := target.Abort(); err != nil {
			t.Errorf("close shared installation: %v", err)
		}
	})
	runtimeBin := filepath.Join(target.Prefix(), "bin", "wu23-runtime")
	fixture.Write(t, runtimeBin, "#!/bin/sh\nprintf RUNTIME_ALLOWED")
	if err := os.Chmod(runtimeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := target.Commit(); err != nil {
		t.Fatal(err)
	}
	rt, err := environmentsetup.RuntimeEnvPaths(f.Home, f.Work)
	if err != nil || len(rt.ReadExec) != 1 {
		t.Fatalf("published generation view: %v err=%v", rt.ReadExec, err)
	}
	// The document fixture follows the Layout paths read in runtime.go::ResolveLayout.
	// Its readiness is not under test; these are the already-resolved A6 inputs.
	// Importing documentruntime here would cycle (it imports sandbox).
	docPrefix := filepath.Join(f.Home, "toolchains", "documents", strings.Repeat("a", 64))
	docCache := filepath.Join(f.Home, "cache", "documents", "self")
	docBin := filepath.Join(docPrefix, "bin", "wu23-document")
	fixture.Write(t, docBin, "#!/bin/sh\nprintf DOCUMENT_ALLOWED")
	if err := os.Chmod(docBin, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture.Write(t, filepath.Join(docCache, "old.txt"), "cache-control")
	otherCache := filepath.Join(f.Home, "cache", "documents", "other", "private.txt")
	fixture.Write(t, otherCache, fixture.Sentinel)
	authored, err := fspolicy.EffectiveFSPolicy(t.Context(), f.AgentHome, f.Work, true, f.Home, "self", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	in := TurnPolicyInput{HomePath: f.Home, Model: FilesystemModelOpen}
	var roots []fixture.Allowance
	for _, path := range rt.ReadExec {
		roots = append(roots, fixture.Allowance{Path: path})
	}
	roots = append(roots, fixture.Allowance{Path: docPrefix}, fixture.Allowance{Path: docCache, Writable: true})
	fixture.SetAllowRoots(t, &in, "AllowRoots", roots)
	policy := DeriveKernelPolicy(authored, in)
	// Keep the existing augmentation step. The test catches denial that was
	// already computed without seeing these exact grants.
	for _, root := range roots {
		access := AccessRead | AccessExecute
		if root.Writable {
			access = AccessRead | AccessWrite
		}
		policy.FilesystemRules = append(policy.FilesystemRules, PathRule{Path: root.Path, Access: access})
	}
	cmd := wu23KernelCommand(t, policy, []string{"/bin/sh", "-c", `cat "$1" >/dev/null || exit 90
"$2" || exit 91
printf '\n'
"$3" || exit 92
printf '\n'
printf cache > "$4/new.txt" || exit 93
printf 'ALLOW_CONTROLS=ok\n'
if printf bad > "$2" 2>/dev/null; then printf 'RUNTIME_WRITE=allowed\n'; else printf 'RUNTIME_WRITE=denied\n'; fi
if printf bad > "$3" 2>/dev/null; then printf 'DOCUMENT_WRITE=allowed\n'; else printf 'DOCUMENT_WRITE=denied\n'; fi
if cat "$5" >/dev/null 2>/dev/null; then printf 'OTHER_CACHE=allowed\n'; else printf 'OTHER_CACHE=denied\n'; fi`, "wu23", filepath.Join(f.Work, "allowed.txt"), runtimeBin, docBin, docCache, otherCache})
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("D3 runtime/document positive child failed: %v output=%s", err, out)
	}
	want := "RUNTIME_ALLOWED\nDOCUMENT_ALLOWED\nALLOW_CONTROLS=ok\nRUNTIME_WRITE=denied\nDOCUMENT_WRITE=denied\nOTHER_CACHE=denied\n"
	if string(out) != want {
		t.Errorf("D3 child output=%q want=%q", out, want)
	}
	b, err := os.ReadFile(filepath.Join(docCache, "new.txt"))
	if err != nil || string(b) != "cache" {
		t.Fatalf("document cache actual bytes=%q err=%v", b, err)
	}
}
