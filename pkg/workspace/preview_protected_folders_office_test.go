//go:build darwin || linux

package workspace_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This is the real registration entry point in a second process, not a
// changed return value or a fabricated protected-folder lookup.
func init() {
	if len(os.Args) != 4 || os.Args[1] != "__office-preview-register-renderer" {
		return
	}
	_, err := component.RegisterRenderer(component.RegisterOptions{Home: os.Args[2], Folder: os.Args[3], Operator: "test-operator-second-process"})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func officeProtectedMount(t *testing.T, target, home string) {
	t.Helper()
	_, _, err := workspace.CheckMountTarget(target, home)
	assert.ErrorIs(t, err, workspace.ErrMountRefused, "T-89: granting this target must refuse overlap with a protected program folder")
}

func TestOfficePreviewProtectsEveryProgramFolder(t *testing.T) {
	if !officepreview.AtPackagedExecutable(t) {
		return
	}
	for _, kind := range []component.Kind{component.KindOffice, component.KindPDFRenderer} {
		for _, installed := range []bool{false, true} {
			t.Run(fmt.Sprintf("packaged_%s_installed_%t", kind, installed), func(t *testing.T) {
				home := t.TempDir()
				t.Setenv(config.EnvHome, home)
				resolver, err := component.NewResolver(home)
				require.NoError(t, err)
				exeDir := filepath.Dir(resolver.Executable)
				slot := "office"
				if kind == component.KindPDFRenderer {
					slot = "pdfium"
				}
				expected := []string{filepath.Join(exeDir, "..", slot), filepath.Join(exeDir, "..", "share", "omnipus", slot)}
				if runtime.GOOS == "darwin" {
					expected[1] = filepath.Join(exeDir, "..", "..", "..", slot)
				}
				require.Equal(t, expected, resolver.PackagedCandidates(kind), "§10 independent two-slot layout; a dropped candidate must not silently shrink test coverage")
				for index, root := range expected {
					t.Run(fmt.Sprintf("slot_%d", index), func(t *testing.T) {
						require.NoError(t, os.MkdirAll(root, 0700))
						// Empty means no component installed, not an invalid mount
						// target. Every case uses a real existing directory.
						require.NoError(t, os.RemoveAll(filepath.Join(root, "manifest.json")))
						if installed {
							if kind == component.KindPDFRenderer {
								officepreview.RegisteredRendererFixture(t, home, root, false)
							} else {
								officepreview.TrustedOffice(t, home, root)
								path := filepath.Join(component.RegisteredDir(home, kind), "manifest.json")
								raw, err := os.ReadFile(path)
								require.NoError(t, err)
								var m component.Manifest
								require.NoError(t, json.Unmarshal(raw, &m))
								m.Root, m.Trust = "", component.TrustPackaged
								raw, err = json.Marshal(m)
								require.NoError(t, err)
								require.NoError(t, os.WriteFile(filepath.Join(root, "manifest.json"), raw, 0600))
								require.NoError(t, os.Remove(path))
							}
							_, problem := resolver.Resolve(kind)
							require.Nil(t, problem, "instrument: populated slot is a genuinely verified component")
						}
						officeProtectedMount(t, root, home)
						require.NoError(t, os.RemoveAll(root))
					})
				}
			})
		}
	}
	t.Run("registered_roots_and_links", func(t *testing.T) {
		for _, kind := range []component.Kind{component.KindOffice, component.KindPDFRenderer} {
			t.Run(string(kind), func(t *testing.T) {
				home, root := t.TempDir(), t.TempDir()
				if kind == component.KindOffice {
					officepreview.TrustedOffice(t, home, root)
				} else {
					officepreview.RegisteredRendererFixture(t, home, root, true)
				}
				officeProtectedMount(t, root, home)
				alias := filepath.Join(t.TempDir(), "alias")
				require.NoError(t, os.Symlink(root, alias))
				officeProtectedMount(t, alias, home)
			})
		}
	})
	t.Run("fresh_second_process_registration", func(t *testing.T) {
		home, root := t.TempDir(), t.TempDir()
		officepreview.RegisteredRendererFixture(t, home, root, false)
		_, _, err := workspace.CheckMountTarget(root, home)
		require.NoError(t, err, "instrument: not protected before operator registration")
		self, err := os.Executable()
		require.NoError(t, err)
		cmd := exec.Command(self, "__office-preview-register-renderer", home, root)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "actual registration child: %s", out)
		resolver, err := component.NewResolver(home)
		require.NoError(t, err)
		resolved, problem := resolver.Resolve(component.KindPDFRenderer)
		require.Nil(t, problem)
		require.Equal(t, component.TrustOperatorRegistered, resolved.Manifest.Trust)
		officeProtectedMount(t, root, home)
	})
	t.Run("unreadable_registration_names_no_folder", func(t *testing.T) {
		home, root := t.TempDir(), t.TempDir()
		officepreview.RegisteredRendererFixture(t, home, root, true)
		path := filepath.Join(component.RegisteredDir(home, component.KindPDFRenderer), "manifest.json")
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.Mkdir(path, 0700)) // reliably unreadable as a file, even under root
		resolver, err := component.NewResolver(home)
		require.NoError(t, err)
		resolved, problem := resolver.Resolve(component.KindPDFRenderer)
		require.Nil(t, resolved)
		require.NotNil(t, problem)
		require.Equal(t, component.StatusDamaged, problem.Status)
		_, _, err = workspace.CheckMountTarget(root, home)
		require.NoError(t, err, "unreadable manifest names no protected root; never use a stale cached root")
	})
	t.Run("undecidable_registered_root_refuses", func(t *testing.T) {
		home, root := t.TempDir(), t.TempDir()
		officepreview.RegisteredRendererFixture(t, home, root, true)
		path := filepath.Join(component.RegisteredDir(home, component.KindPDFRenderer), "manifest.json")
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		var m component.Manifest
		require.NoError(t, json.Unmarshal(raw, &m))
		loop := filepath.Join(t.TempDir(), "loop")
		require.NoError(t, os.Symlink(loop, loop))
		m.Root = loop
		raw, err = json.Marshal(m)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, raw, 0600))
		_, err = os.Stat(loop)
		require.Error(t, err, "instrument: the physical comparison genuinely cannot be decided")
		officeProtectedMount(t, t.TempDir(), home)
	})
	if runtime.GOOS == "darwin" {
		t.Run("case_and_unicode_identity", func(t *testing.T) {
			for _, names := range [][2]string{{"OfficeCASE", "officecase"}, {"Café", "Café"}} {
				home := t.TempDir()
				parent := t.TempDir()
				root, alias := filepath.Join(parent, names[0]), filepath.Join(parent, names[1])
				officepreview.TrustedOffice(t, home, root)
				a, err := os.Stat(root)
				require.NoError(t, err)
				b, err := os.Stat(alias)
				if err != nil {
					t.Fatalf("BLOCKED: T-89 requires case/Unicode-equivalent test volume: %v", err)
				}
				require.True(t, os.SameFile(a, b), "instrument: alias is the identical physical component")
				officeProtectedMount(t, alias, home)
			}
		})
	}
	t.Run("unrelated_folder_control", func(t *testing.T) {
		_, _, err := workspace.CheckMountTarget(t.TempDir(), t.TempDir())
		require.NoError(t, err)
	})
}
