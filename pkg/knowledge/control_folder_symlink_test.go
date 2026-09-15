// Empirical tests for the D-14 follow-up: a symlink planted in a vault's
// .omnipus-vault/ control folder must make the control-plane write paths
// refuse, never follow the link to write, move or delete outside the vault.
package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/stretchr/testify/require"
)

// setupLinkedControlDir builds a vault with a note and a healthy control
// folder, then replaces one of its subfolders (records/views/trash) with a
// symlink to an outside directory.
func setupLinkedControlDir(t *testing.T, which string) (vaultRoot, outside string) {
	t.Helper()
	vaultRoot = t.TempDir()
	outside = t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(vaultRoot, "Projects"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(vaultRoot, "Projects", "Acme.md"), []byte("# Acme\n\nContent.\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(vaultRoot, records.VaultMarkerDirName), 0o755))
	for _, d := range []string{"records", "views", "trash"} {
		dir := filepath.Join(vaultRoot, records.VaultMarkerDirName, d)
		if d == which {
			require.NoError(t, os.MkdirAll(filepath.Join(outside, d), 0o755))
			require.NoError(t, os.Symlink(filepath.Join(outside, d), dir))
			continue
		}
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}
	return vaultRoot, outside
}

func outsideIsEmpty(t *testing.T, outside string) {
	t.Helper()
	var found []string
	require.NoError(t, filepath.Walk(outside, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.Name() != ".DS_Store" {
			found = append(found, p)
		}
		return nil
	}))
	// The setup creates the empty subfolder the link points at; nothing else
	// may appear there.
	require.Empty(t, found, "nothing may be written through the escaping link: %v", found)
}

func TestControlFolderSymlink_TrashRefusesEscapingTrashDir(t *testing.T) {
	vaultRoot, outside := setupLinkedControlDir(t, "trash")
	root, err := NewCollectionRoot(OSLinkFS(), vaultRoot)
	require.NoError(t, err)

	tr := &Trasher{FS: OSLinkFS(), Root: root}
	_, err = tr.Trash(TrashRequest{Path: "Projects/Acme.md"})
	require.Error(t, err, "trash must refuse when .omnipus-vault/trash is a link out of the vault")
	require.True(t, strings.Contains(err.Error(), "symbolic link") || strings.Contains(err.Error(), "outside the collection"), "the refusal must name the cause: %v", err)
	require.FileExists(t, filepath.Join(vaultRoot, "Projects", "Acme.md"), "the note must stay in place")
	outsideIsEmpty(t, outside)
}

func TestControlFolderSymlink_RestoreRefusesEscapingTrashDir(t *testing.T) {
	vaultRoot, outside := setupLinkedControlDir(t, "trash")
	root, err := NewCollectionRoot(OSLinkFS(), vaultRoot)
	require.NoError(t, err)
	// A planted "trashed copy" outside the vault, as if trash had landed there.
	trashIDDir := filepath.Join(outside, "trash", "20260101T000000Z-test")
	require.NoError(t, os.MkdirAll(filepath.Join(trashIDDir, "Projects"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(trashIDDir, "Projects", "Acme.md"), []byte("# Acme\n"), 0o644))

	tr := &Trasher{FS: OSLinkFS(), Root: root}
	_, err = tr.Restore(RestoreRequest{Path: "Projects/Acme.md"})
	require.Error(t, err, "restore must refuse to pull a file from an escaping link")
	require.FileExists(t, filepath.Join(trashIDDir, "Projects", "Acme.md"), "the planted file must not move or be deleted")
}

func TestControlFolderSymlink_RecordIDSequenceRefusesEscapingRecordsDir(t *testing.T) {
	vaultRoot, outside := setupLinkedControlDir(t, "records")
	_, _, err := mintRecordIDs(NoteLockConfig{}, vaultRoot, &records.Schema{Type: "deal"}, 1, true)
	require.Error(t, err, "minting must refuse when .omnipus-vault/records is a link out of the vault")
	outsideIsEmpty(t, outside)
}

func TestControlFolderSymlink_ControlPlaneWriteRefusesEscapingViewsDir(t *testing.T) {
	vaultRoot, outside := setupLinkedControlDir(t, "views")
	target := mutationTarget{collection: &Collection{root: vaultRoot}}
	err := createControlPlaneFile(target, filepath.Join(vaultRoot, records.VaultMarkerDirName, "views", "v.yaml"), []byte("name: v\n"))
	require.Error(t, err, "view creation must refuse when .omnipus-vault/views is a link out of the vault")
	outsideIsEmpty(t, outside)
}

func TestControlFolderSymlink_HealthyVaultStillWrites(t *testing.T) {
	dir := t.TempDir()
	vaultRoot := filepath.Join(dir, "vault")
	outside := t.TempDir()
	for _, d := range []string{"records", "views", "trash"} {
		require.NoError(t, os.MkdirAll(filepath.Join(vaultRoot, records.VaultMarkerDirName, d), 0o755))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(vaultRoot, "Projects"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(vaultRoot, "Projects", "Acme.md"), []byte("# Acme\n"), 0o644))

	root, err := NewCollectionRoot(OSLinkFS(), vaultRoot)
	require.NoError(t, err)
	tr := &Trasher{FS: OSLinkFS(), Root: root}
	res, err := tr.Trash(TrashRequest{Path: "Projects/Acme.md"})
	require.NoError(t, err, "a healthy control folder must not be refused")
	require.NotNil(t, res)
	require.True(t, strings.HasPrefix(res.TrashPath, ".omnipus-vault/trash/"))
	outsideIsEmpty(t, outside)
}
