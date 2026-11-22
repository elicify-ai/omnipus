package officepreview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/stretchr/testify/require"
)

// OwnRoot returns a fresh folder strictly inside Omnipus's Linux own folder
// for the running test executable (component.OwnFolder of the resolved test
// binary, simulated GOOS "linux"), so a registered fixture passes the §3.9
// own-folder rule on hosted Linux (spec "Existing tests affected"). On every
// other system the rule does not apply and a plain per-test temp folder is
// returned. The folder is removed at cleanup; distinct calls get distinct
// folders, so sibling-name rows create their siblings inside one OwnRoot.
func OwnRoot(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	resolved, err := filepath.EvalSymlinks(self)
	require.NoError(t, err)
	own := component.OwnFolder(resolved, "linux")
	if own == "" {
		return t.TempDir()
	}
	// The own folder of a test binary does not exist yet — the packaged
	// layout below share/omnipus ships with the product, not with a test
	// run. Create it (inside the test's own build/scratch tree, including
	// AtPackagedExecutable's isolated <iso>/Application/Contents tree, whose
	// own share/omnipus is exactly this calculation), then make per-call
	// folders inside it.
	require.NoError(t, os.MkdirAll(own, 0700))
	root, err := os.MkdirTemp(own, "trusted-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

// TrustedOffice builds a process-edge fixture, NOT an official Office install.
// Its executable is fingerprinted; the harmless resource is deliberately not:
// AD-4 protects Office libraries/configuration through global writer exclusion.
// The independent manifest recipe is SHA256("name hex-digest\n").
func TrustedOffice(t *testing.T, home, root string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(root, 0700))
	resolvedRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	program := []byte("#!/bin/sh\nexit 73\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "soffice"), program, 0700))
	digest := sha256.Sum256(program)
	hexDigest := hex.EncodeToString(digest[:])
	listDigest := sha256.Sum256([]byte("soffice " + hexDigest + "\n"))
	manifest := component.Manifest{
		Schema: 1, Component: component.KindOffice, Version: "26.8.1",
		Platform: runtime.GOOS + "-" + runtime.GOARCH, Executable: "soffice",
		Files: map[string]string{"soffice": hexDigest}, FileListSHA256: hex.EncodeToString(listDigest[:]),
		Trust: component.TrustOperatorRegistered, Root: resolvedRoot,
		RegisteredBy: "test-operator", RegisteredAt: "2026-10-31T00:00:00Z",
	}
	dir := component.RegisteredDir(home, component.KindOffice)
	require.NoError(t, os.MkdirAll(dir, 0700))
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0600))
	dependency := filepath.Join(root, "unhashed-resource.txt")
	require.NoError(t, os.WriteFile(dependency, []byte("original resource"), 0600))
	resolver, err := component.NewResolver(home)
	require.NoError(t, err)
	resolved, problem := resolver.Resolve(component.KindOffice)
	require.Nil(t, problem, "positive control: fixture must pass actual component fingerprint verification")
	require.Equal(t, resolvedRoot, resolved.Root)
	return dependency
}

// PackagedOfficeFixture stages the same fixture recipe as TrustedOffice, as
// a PACKAGED component: the manifest is written at the slot root itself with
// TrustPackaged and no root, and no operator-registered manifest is ever
// written. The packaged slots are not registration locations (OF-1), and the
// own-folder check applies only after a manifest is marked
// operator-registered (OF-4) — TrustedOffice's positive-control resolve
// would otherwise correctly refuse the registered manifest it staged at a
// slot outside the own folder (observed on the merged code:
// omnipus-scratch/office-preview-of/logs/f1-probe.log). The manifest fields
// are exactly what the packaged-slot rows' previous registered→packaged
// conversion produced; the positive control is the caller's "populated slot
// is a genuinely verified component" resolve after this write, identical to
// the renderer rows. Returns the unhashed resource path, as TrustedOffice
// does.
func PackagedOfficeFixture(t *testing.T, root string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(root, 0700))
	program := []byte("#!/bin/sh\nexit 73\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "soffice"), program, 0700))
	digest := sha256.Sum256(program)
	hexDigest := hex.EncodeToString(digest[:])
	listDigest := sha256.Sum256([]byte("soffice " + hexDigest + "\n"))
	manifest := component.Manifest{
		Schema: 1, Component: component.KindOffice, Version: "26.8.1",
		Platform: runtime.GOOS + "-" + runtime.GOARCH, Executable: "soffice",
		Files: map[string]string{"soffice": hexDigest}, FileListSHA256: hex.EncodeToString(listDigest[:]),
		Trust: component.TrustPackaged, Root: "",
		RegisteredBy: "test-operator", RegisteredAt: "2026-10-31T00:00:00Z",
	}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "manifest.json"), data, 0600))
	dependency := filepath.Join(root, "unhashed-resource.txt")
	require.NoError(t, os.WriteFile(dependency, []byte("original resource"), 0600))
	return dependency
}

// WriterRoots invokes the REAL enumeration on both commissioned versions:
// 3ecdd3a3e has (home,cfg), 264bd7bc7 adds the grants argument. The type
// switch is solely a signature bridge for identical red/green assertions.
// It does not calculate, filter, or supply the implementation's answer.
func WriterRoots(home string, cfg *config.Config) []component.WriteRoot {
	switch fn := any(preview.ConfiguredWriteRoots).(type) {
	case func(string, *config.Config) []component.WriteRoot:
		return fn(home, cfg)
	case func(string, *config.Config, []fspolicy.PathGrant) []component.WriteRoot:
		return fn(home, cfg, nil)
	default:
		panic("officepreview: unsupported ConfiguredWriteRoots signature")
	}
}

// RequireWriterRefusal checks the public per-call path, not just a root-list
// string. The harmless fixture returns 73 if wrongly executed; a converter
// error does NOT satisfy this assertion. No shipped installation is modified.
func RequireWriterRefusal(t *testing.T, svc *preview.Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	adm, err := svc.Admit(ctx, true)
	require.NoError(t, err)
	defer adm.Release()
	_, err = svc.Preview(ctx, adm, preview.Request{Name: "harmless.docx", Ext: ".docx", Data: []byte("authorized fixture input"), Default: true})
	require.Error(t, err, "AD-4: an agent-writable Office component must not run")
	message := strings.ToLower(err.Error())
	require.True(t, strings.Contains(message, "agent can write") || strings.Contains(message, "unsafe location"), "must refuse for writer exclusion, not an unrelated spawn/input error: %s", message)
	require.NotEqual(t, "ready", svc.ComponentStatus().Office, "global status must disable it for every caller")
}
