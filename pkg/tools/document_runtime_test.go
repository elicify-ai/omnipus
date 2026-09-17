package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/documentruntime"
)

func TestBashDocumentRuntimeUsesPrefixAndWorkerCache(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX shell")
	}
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	layout, err := documentruntime.ResolveLayout(filepath.Join(workspace, "data"), documentruntime.ManifestRevision, "mia")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(layout.Bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(layout.Cache, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(layout.Bin, "python")
	script := "#!/bin/sh\nprintf 'python=%s\\ncache=%s\\nskills=%s\\nargs=%s\\n' \"$0\" \"$XDG_CACHE_HOME\" \"$OMNIPUS_DOCUMENT_SKILLS\" \"$*\"\n"
	if err = os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	tool, err := NewExecToolWithDeps(workspace, true, nil, ExecToolDeps{GodMode: false})
	if err != nil {
		t.Fatal(err)
	}
	tool.SetDocumentRuntime(layout, false)
	command := strings.Join(documentruntime.ProbeArgv(layout), " ")
	result := tool.Execute(bashCtx(t), map[string]any{"command": command})
	if result.IsError || !strings.Contains(result.ForLLM, "python="+fake) || !strings.Contains(result.ForLLM, "cache="+layout.Cache) || !strings.Contains(result.ForLLM, "skills="+layout.Skills) || !strings.Contains(result.ForLLM, "--format all") {
		t.Fatalf("result=%+v", result)
	}
}

func TestDocumentProbeFormatFlagDoesNotRelaxDiskWipeGuard(t *testing.T) {
	if got := applyDenyPatterns("python -m omnipus_document_probe --format all", defaultDenyPatterns, nil); got != "" {
		t.Fatalf("probe format flag blocked: %s", got)
	}
	for _, command := range []string{"format C:", "echo ready; mkfs /dev/x", "sudo diskpart wipe"} {
		if got := applyDenyPatterns(command, defaultDenyPatterns, nil); got == "" {
			t.Fatalf("disk wipe command allowed: %q", command)
		}
	}
}

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

func TestBashDocumentRuntimeFinalizeCommandIsAdminOnlyAndReportsIncompleteSetup(t *testing.T) {
	workspace := t.TempDir()
	layout, err := documentruntime.ResolveLayout(filepath.Join(workspace, "data"), documentruntime.ManifestRevision, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = documentruntime.ProvisionFirstParty(layout); err != nil {
		t.Fatal(err)
	}

	worker, err := NewExecToolWithDeps(workspace, true, nil, ExecToolDeps{GodMode: false})
	if err != nil {
		t.Fatal(err)
	}
	worker.SetDocumentRuntime(layout, false)
	result := worker.Execute(bashCtx(t), map[string]any{"command": documentruntime.FinalizeCommand})
	if !result.IsError || !strings.Contains(result.ForLLM, "restricted to Admin") {
		t.Fatalf("worker result=%+v", result)
	}

	admin, err := NewExecToolWithDeps(workspace, true, nil, ExecToolDeps{GodMode: false})
	if err != nil {
		t.Fatal(err)
	}
	admin.SetDocumentRuntime(layout, true)
	result = admin.Execute(bashCtx(t), map[string]any{"command": documentruntime.FinalizeCommand})
	if !result.IsError || !strings.Contains(result.ForLLM, "setup incomplete") || !strings.Contains(result.ForLLM, "python") {
		t.Fatalf("incomplete setup result=%+v", result)
	}

	for _, path := range []string{filepath.Join(layout.Bin, "python"), filepath.Join(layout.Bin, "node"), filepath.Join(layout.Bin, "soffice")} {
		if err = os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	result = admin.Execute(bashCtx(t), map[string]any{"command": documentruntime.FinalizeCommand})
	if result.IsError || !strings.Contains(result.ForLLM, `"status":"ready"`) {
		t.Fatalf("finalized setup result=%+v", result)
	}
}

func TestBashDocumentRuntimeFinalizeRejectsManifestSymlinkOutsideManagedPrefix(t *testing.T) {
	workspace := t.TempDir()
	layout, err := documentruntime.ResolveLayout(filepath.Join(workspace, "data"), documentruntime.ManifestRevision, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = documentruntime.ProvisionFirstParty(layout); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "manifest.json")
	manifestBytes, err := os.ReadFile(layout.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(external, manifestBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(layout.Manifest); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(external, layout.Manifest); err != nil {
		t.Fatal(err)
	}

	admin, err := NewExecToolWithDeps(workspace, true, nil, ExecToolDeps{GodMode: false})
	if err != nil {
		t.Fatal(err)
	}
	admin.SetDocumentRuntime(layout, true)
	result := admin.Execute(bashCtx(t), map[string]any{"command": documentruntime.FinalizeCommand})
	if !result.IsError || !strings.Contains(result.ForLLM, "manifest unavailable") {
		t.Fatalf("external manifest symlink result=%+v", result)
	}
	if strings.Contains(result.ForLLM, external) {
		t.Fatalf("error leaked outside manifest path: %q", result.ForLLM)
	}
}

func TestBashDocumentRuntimeFinalizeRejectsOversizedManifest(t *testing.T) {
	workspace := t.TempDir()
	layout, err := documentruntime.ResolveLayout(filepath.Join(workspace, "data"), documentruntime.ManifestRevision, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(layout.Manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(layout.Manifest, []byte(`{"revision":"`+strings.Repeat("a", 2<<20)+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	admin, err := NewExecToolWithDeps(workspace, true, nil, ExecToolDeps{GodMode: false})
	if err != nil {
		t.Fatal(err)
	}
	admin.SetDocumentRuntime(layout, true)
	result := admin.Execute(bashCtx(t), map[string]any{"command": documentruntime.FinalizeCommand})
	if !result.IsError || !strings.Contains(result.ForLLM, "exceeds") {
		t.Fatalf("oversized manifest result=%+v", result)
	}
}

func TestBashDescriptionPublishesExactAdminFinalizeCommand(t *testing.T) {
	tool, err := NewExecToolWithDeps(t.TempDir(), true, nil, ExecToolDeps{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tool.Description(), documentruntime.FinalizeCommand) {
		t.Fatalf("bash description does not publish %q", documentruntime.FinalizeCommand)
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
	if err := os.MkdirAll(layout.Cache, 0o755); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	tool, err := NewExecToolWithDeps(workspace, false, nil, ExecToolDeps{GodMode: false})
	if err != nil {
		t.Fatal(err)
	}
	tool.SetDocumentRuntime(layout, false)
	result := tool.Execute(bashCtx(t), map[string]any{"command": strings.Join(documentruntime.ProbeArgv(layout), " "), "timeout_seconds": float64(120)})
	if result.IsError || !strings.Contains(result.ForLLM, `"ok": true`) {
		t.Fatalf("actual worker probe failed: %+v", result)
	}
}
