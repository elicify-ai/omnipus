package documentruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

func TestResolveLayoutVersionedAbsolutePrefix(t *testing.T) {
	root := t.TempDir()
	got, err := ResolveLayout(root, ManifestRevision, "mia")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "toolchains", "documents", ManifestRevision)
	if got.Prefix != want || !filepath.IsAbs(got.Manifest) || !strings.HasSuffix(got.Cache, filepath.Join("documents", "mia")) {
		t.Fatalf("layout=%+v want prefix %s", got, want)
	}
	for _, bad := range []struct{ root, rev, id string }{{"relative", ManifestRevision, "mia"}, {root, "latest", "mia"}, {root, ManifestRevision, "../mia"}} {
		if _, err := ResolveLayout(bad.root, bad.rev, bad.id); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func TestChildEnvironmentPrependsRuntimeAndDropsSecrets(t *testing.T) {
	l, _ := ResolveLayout(t.TempDir(), ManifestRevision, "gp")
	got := ChildEnvironment([]string{"PATH=/usr/bin", "TMPDIR=/host/temp", "OSL_SOCKET_PATH=/host/socket", "LANG=en_US.UTF-8", "OPENAI_API_KEY=secret", "OMNIPUS_MASTER_KEY=master"}, l)
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "PATH="+l.Bin+string(os.PathListSeparator)+"/usr/bin") || !strings.Contains(joined, "PYTHONPATH="+filepath.Join(l.Lib, "python")) || !strings.Contains(joined, "TMPDIR="+l.Cache) || !strings.Contains(joined, "OSL_SOCKET_PATH=.") || strings.Contains(joined, "/host/temp") || strings.Contains(joined, "/host/socket") || strings.Contains(joined, "secret") || strings.Contains(joined, "master") {
		t.Fatalf("environment=%v", got)
	}
}

func TestApplySandboxAccessScopesDocumentIPCToAuthorizedWorkDir(t *testing.T) {
	root := t.TempDir()
	l, _ := ResolveLayout(root, ManifestRevision, "mia")
	work := filepath.Join(root, "workspaces", "ws", "work")
	base := sandbox.SandboxPolicy{FilesystemRules: []sandbox.PathRule{{Path: work, Access: sandbox.AccessRead | sandbox.AccessWrite}}}
	got, err := ApplySandboxAccess(base, l, work)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.UnixSocketRules) != 1 || got.UnixSocketRules[0].Path != work || !got.UnixSocketRules[0].Bind || !got.UnixSocketRules[0].Connect {
		t.Fatalf("unix socket rules=%+v", got.UnixSocketRules)
	}
	if _, err := ApplySandboxAccess(base, l, root); err == nil {
		t.Fatal("granted document IPC to parent of writable directory")
	}
	if _, err := ApplySandboxAccess(base, l, filepath.Join(root, "outside")); err == nil {
		t.Fatal("granted document IPC outside an existing writable policy path")
	}
}

func TestProvisionInstallsSkillsAndSandboxAccessIsWorkerOnly(t *testing.T) {
	l, _ := ResolveLayout(t.TempDir(), ManifestRevision, "mia")
	manifest, err := ProvisionFirstParty(l)
	if err != nil {
		t.Fatal(err)
	}
	if err = VerifyAssets(l.Prefix, manifest.Assets); err != nil {
		t.Fatal(err)
	}
	for _, id := range DocumentSkillIDs {
		for _, rel := range []string{"SKILL.md", "LICENSE"} {
			if _, err = os.Stat(filepath.Join(l.Skills, id, rel)); err != nil {
				t.Fatalf("%s/%s: %v", id, rel, err)
			}
		}
	}
	// A base rule that overlaps the managed prefix must not give agents write
	// access to it — even when their ordinary workspace policy says otherwise.
	base := sandbox.SandboxPolicy{FilesystemRules: []sandbox.PathRule{{Path: filepath.Dir(filepath.Dir(l.Prefix)), Access: sandbox.AccessRead | sandbox.AccessWrite}}}
	worker, err := ApplySandboxAccess(base, l, l.Cache)
	if err != nil {
		t.Fatal(err)
	}
	if len(worker.FilesystemRules) < 3 {
		t.Fatalf("filesystem rules=%v", worker.FilesystemRules)
	}
	if worker.FilesystemRules[0].Access&sandbox.AccessWrite != 0 || worker.FilesystemRules[1].Access&sandbox.AccessWrite != 0 {
		t.Fatalf("write access to the managed prefix survived: %v", worker.FilesystemRules)
	}
	if worker.FilesystemRules[2].Access&sandbox.AccessWrite == 0 {
		t.Fatalf("worker cache must stay writable: %v", worker.FilesystemRules)
	}
	// A tampered asset is detected by checksum.
	if err := os.WriteFile(filepath.Join(l.Skills, DocumentSkillIDs[0], "SKILL.md"), []byte("mutated"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyAssets(l.Prefix, manifest.Assets); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("mismatch err=%v", err)
	}
}

func TestVerifyAssetsRejectsSymlinkOutsideManagedPrefix(t *testing.T) {
	prefix := t.TempDir()
	external := filepath.Join(t.TempDir(), "secret.txt")
	secret := []byte("outside managed runtime")
	if err := os.WriteFile(external, secret, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(prefix, "asset.txt")); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(secret)
	err := VerifyAssets(prefix, []Asset{{Path: "asset.txt", SHA256: hex.EncodeToString(sum[:])}})
	if err == nil {
		t.Fatal("outside-prefix symlink was accepted as a managed asset")
	}
	if strings.Contains(err.Error(), external) {
		t.Fatalf("error leaked outside asset path: %v", err)
	}
}

func TestProvisionFirstPartyIsProbeableBeforeDependencies(t *testing.T) {
	layout, _ := ResolveLayout(t.TempDir(), ManifestRevision, "mia")
	manifest, err := ProvisionFirstParty(layout)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Assets) < 20 {
		t.Fatalf("assets=%d, want packaged helpers and knowledge", len(manifest.Assets))
	}
	if _, err := os.Stat(filepath.Join(layout.Lib, "python", "omnipus_document_probe", "__main__.py")); err != nil {
		t.Fatal(err)
	}
	if err := VerifyAssets(layout.Prefix, manifest.Assets); err != nil {
		t.Fatal(err)
	}
	// The manifest names the dependency paths the skills expect, but nothing
	// requires them to exist at provisioning time: a probe run before the
	// agent installs anything reports missing components instead of falling
	// through to host tooling.
	if _, err := os.Stat(manifest.Python); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("provisioning must not fabricate dependencies: %v", err)
	}
}

func TestProvisionFirstPartyExistingManifestCreatesCallingWorkerCache(t *testing.T) {
	root := t.TempDir()
	first, _ := ResolveLayout(root, ManifestRevision, "mia")
	if _, err := ProvisionFirstParty(first); err != nil {
		t.Fatal(err)
	}
	second, _ := ResolveLayout(root, ManifestRevision, "gp")
	if _, err := os.Stat(second.Cache); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second worker cache exists before its provisioning call: %v", err)
	}
	if _, err := ProvisionFirstParty(second); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(second.Cache)
	if err != nil || !info.IsDir() {
		t.Fatalf("second worker cache was not created on existing-manifest path: info=%v err=%v", info, err)
	}
}
