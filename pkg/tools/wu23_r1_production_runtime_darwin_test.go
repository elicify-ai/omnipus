//go:build darwin

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/environmentsetup"
	fixture "github.com/elicify-ai/omnipus/tests/l1_red/protectedfixture"
)

// F1 / D3 A5/A6: this must reach the real callers, not a hand-populated
// TurnPolicyInput.AllowRoots that bypasses the production handoff defect.
func TestWU23R1_ProductionBashRuntimeAllowances_Seatbelt(t *testing.T) {
	if wu23R1Isolated(t) {
		return
	}
	f := wu23R1Fixture(t)
	rt, shared := wu23R1PublishedRuntime(t, f)
	layout := wu23R1DocumentLayout(t, f)
	docBin := filepath.Join(layout.Bin, "wu23-r1-document")
	fixture.Write(t, docBin, "#!/bin/sh\nprintf 'DOCUMENT_EXEC=ok\\n'\n")
	wu23R1OwnerWritable(t, docBin)
	tool := wu23R1Exec(t, f, false)
	tool.SetDocumentRuntime(layout)
	doc := tool.documentEnvTurn(f.Work)
	if !doc.ok {
		t.Fatalf("provisioned document runtime did not resolve: %s", doc.notice)
	}
	fixture.Write(t, filepath.Join(doc.layout.Cache, "before.txt"), "cache-before")
	other := filepath.Join(f.Home, "cache", "documents", "other", "private.txt")
	fixture.Write(t, other, fixture.Sentinel)
	policy, err := tool.turnKernelPolicy(wu23Context(f), f.Work, rt, doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	script := `cat "$1" >/dev/null || exit 90
printf 'WORKSPACE_READ=ok\n'
if "$2" 2>/dev/null; then printf 'SHARED_REACH=ok\n'; else printf 'SHARED_REACH=denied\n'; fi
if "$3" 2>/dev/null; then printf 'DOCUMENT_REACH=ok\n'; else printf 'DOCUMENT_REACH=denied\n'; fi
if { printf bad > "$2"; } 2>/dev/null; then printf 'SHARED_WRITE=allowed\n'; else printf 'SHARED_WRITE=denied\n'; fi
if { printf bad > "$3"; } 2>/dev/null; then printf 'DOCUMENT_WRITE=allowed\n'; else printf 'DOCUMENT_WRITE=denied\n'; fi
cat "$4/before.txt" >/dev/null || exit 91
printf cache-after > "$4/after.txt" || exit 92
printf 'CACHE_RW=ok\n'
if cat "$5" >/dev/null 2>/dev/null; then printf 'OTHER_CACHE=allowed\n'; else printf 'OTHER_CACHE=denied\n'; fi`
	out := wu23R1PolicyChild(t, policy, script, filepath.Join(f.Work, "allowed.txt"), shared, docBin, doc.layout.Cache, other)
	t.Logf("actual turnKernelPolicy/augmentKernelPolicy child receipt: %q", out)
	t.Run("same_child_workspace_and_cache_controls", func(t *testing.T) {
		if !strings.Contains(out, "WORKSPACE_READ=ok\n") || !strings.Contains(out, "CACHE_RW=ok\n") {
			t.Fatalf("production child lost independent allowed controls: %q", out)
		}
		data, readErr := os.ReadFile(filepath.Join(doc.layout.Cache, "after.txt"))
		if readErr != nil || string(data) != "cache-after" {
			t.Fatalf("own cache actual write=%q err=%v", data, readErr)
		}
	})
	want := "WORKSPACE_READ=ok\nSHARED_EXEC=ok\nSHARED_REACH=ok\nDOCUMENT_EXEC=ok\nDOCUMENT_REACH=ok\nSHARED_WRITE=denied\nDOCUMENT_WRITE=denied\nCACHE_RW=ok\nOTHER_CACHE=denied\n"
	if out != want {
		t.Errorf("D3 production runtime handoff output=%q want=%q", out, want)
	}
}

// Chief Q2 A: setup has a generic-runtime input, not a document-layer input.
// Its child must execute already-published runtimes while retaining only its
// selected prefix/cache/tmp writes. Real storage allocates the install area.
func TestWU23R1_ProductionSetupRuntimeAllowances_Seatbelt(t *testing.T) {
	if wu23R1Isolated(t) {
		return
	}
	f := wu23R1Fixture(t)
	rt, shared := wu23R1PublishedRuntime(t, f)
	area, err := storageSeamStore{}.BeginInstall(f.Home, f.Work, string(environmentsetup.ScopeWorkspace))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if abortErr := area.Abort(); abortErr != nil {
			t.Errorf("release setup area: %v", abortErr)
		}
	})
	tool := NewEnvironmentSetupTool(EnvironmentSetupToolDeps{Home: f.Home, AgentWorkDir: f.AgentHome, Store: storageSeamStore{}})
	lim, err := tool.buildSetupLimits(wu23Context(f), area, f.Work, rt, 20)
	if err != nil {
		t.Fatal(err)
	}
	script := `cat "$1" >/dev/null || exit 90
printf 'WORKSPACE_READ=ok\n'
if "$2" 2>/dev/null; then printf 'SHARED_REACH=ok\n'; else printf 'SHARED_REACH=denied\n'; fi
if { printf bad > "$2"; } 2>/dev/null; then printf 'SHARED_WRITE=allowed\n'; else printf 'SHARED_WRITE=denied\n'; fi
printf prefix-control > "$3/installed.txt" || exit 91
printf cache-control > "$4/cache.txt" || exit 92
printf tmp-control > "$5/temp.txt" || exit 93
printf 'INSTALL_CACHE_TMP=ok\n'
if cat "$6" >/dev/null 2>/dev/null; then printf 'INTERNAL=allowed\n'; else printf 'INTERNAL=denied\n'; fi`
	out := wu23R1PolicyChild(t, lim.KernelPolicy, script, filepath.Join(f.Work, "allowed.txt"), shared, area.Prefix(), area.Cache(), area.Tmp(), filepath.Join(f.Home, "credentials.json"))
	t.Logf("actual buildSetupLimits child receipt: %q", out)
	t.Run("same_child_install_controls", func(t *testing.T) {
		for path, want := range map[string]string{
			filepath.Join(area.Prefix(), "installed.txt"): "prefix-control",
			filepath.Join(area.Cache(), "cache.txt"):      "cache-control",
			filepath.Join(area.Tmp(), "temp.txt"):         "tmp-control",
		} {
			data, readErr := os.ReadFile(path)
			if readErr != nil || string(data) != want {
				t.Fatalf("setup exact allowed bytes=%q want=%q err=%v", data, want, readErr)
			}
		}
	})
	want := "WORKSPACE_READ=ok\nSHARED_EXEC=ok\nSHARED_REACH=ok\nSHARED_WRITE=denied\nINSTALL_CACHE_TMP=ok\nINTERNAL=denied\n"
	if out != want {
		t.Errorf("D3 setup runtime handoff output=%q want=%q", out, want)
	}
}
