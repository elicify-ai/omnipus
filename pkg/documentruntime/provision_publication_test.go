package documentruntime

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// A failed asset inventory must not publish a manifest that a later call can
// mistake for completed first-party provisioning. Use a real unreadable asset
// (a dangling link), leaving export, inventory and publication code real.
func TestProvisionFirstPartyInventoryFailureDoesNotPublishManifest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("dangling-link fixture requires symlink privilege on Windows")
	}
	layout, err := ResolveLayout(t.TempDir(), ManifestRevision, "mia")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.Skills, 0o755); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(layout.Skills, "unreadable-asset")
	if err := os.Symlink(filepath.Join(layout.Prefix, "missing-asset"), broken); err != nil {
		t.Fatal(err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		_, err := ProvisionFirstParty(layout)
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("attempt %d: want missing asset inventory error, got %v", attempt, err)
		}
		if _, err := os.Stat(layout.Manifest); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("attempt %d: failed inventory must leave manifest absent, stat error=%v", attempt, err)
		}
	}

	// Once the asset problem is removed, retry must actually inventory the
	// exported packages rather than accept an earlier empty-assets manifest.
	if err := os.Remove(broken); err != nil {
		t.Fatal(err)
	}
	manifest, err := ProvisionFirstParty(layout)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Assets) == 0 {
		t.Fatal("recovered provisioning must inventory packaged assets")
	}
	data, err := os.ReadFile(layout.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	var persisted Manifest
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(manifest, persisted) {
		t.Fatal("published manifest differs from completed provisioning result")
	}
	if _, err := os.Stat(filepath.Join(layout.Lib, "python", "omnipus_document_probe", "__main__.py")); err != nil {
		t.Fatalf("completed provisioning must install its probe: %v", err)
	}
}

// A matching revision is not a readiness signal without an asset inventory.
// Both an explicit empty list and an omitted/null list must be rebuilt.
func TestProvisionFirstPartyRebuildsEmptyInventoryManifest(t *testing.T) {
	for _, tc := range []struct {
		name   string
		assets []Asset
	}{
		{name: "empty", assets: []Asset{}},
		{name: "null", assets: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			layout, err := ResolveLayout(t.TempDir(), ManifestRevision, "mia")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(layout.Prefix, 0o755); err != nil {
				t.Fatal(err)
			}
			incomplete := Manifest{Revision: ManifestRevision, Assets: tc.assets}
			data, err := json.Marshal(incomplete)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(layout.Manifest, data, 0o644); err != nil {
				t.Fatal(err)
			}

			got, err := ProvisionFirstParty(layout)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Assets) == 0 {
				t.Fatal("matching revision with empty asset inventory must be rebuilt")
			}
			// Pin the four required bundled packages independently of DocumentSkillIDs.
			for _, id := range []string{"elicify-docx", "elicify-xlsx", "elicify-pptx", "elicify-pdf"} {
				rel := "skills/" + id + "/SKILL.md"
				found := false
				for _, asset := range got.Assets {
					if asset.Path == rel {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("rebuilt inventory is missing %s", rel)
				}
			}
			data, err = os.ReadFile(layout.Manifest)
			if err != nil {
				t.Fatal(err)
			}
			var persisted Manifest
			if err := json.Unmarshal(data, &persisted); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, persisted) {
				t.Fatal("rebuilt manifest must be persisted")
			}
			if _, err := os.Stat(filepath.Join(layout.Lib, "python", "omnipus_document_probe", "__main__.py")); err != nil {
				t.Fatalf("rebuilding incomplete cache must install probe: %v", err)
			}
			again, err := ProvisionFirstParty(layout)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, again) {
				t.Fatal("completed rebuilt manifest must remain stable on repeated provisioning")
			}
		})
	}
}
