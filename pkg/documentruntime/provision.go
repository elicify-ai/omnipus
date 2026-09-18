package documentruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

var provisionMu sync.Mutex

// ProvisionFirstParty materializes the compiled probe, knowledge and helper
// files before external dependencies exist. It publishes an intentionally
// probeable manifest whose fixed paths are the contract Admin must satisfy.
// A worker invoking the probe before setup receives a structured non-zero
// missing-component result rather than falling through to host tooling.
func ProvisionFirstParty(layout Layout) (Manifest, error) {
	provisionMu.Lock()
	defer provisionMu.Unlock()
	if err := os.MkdirAll(layout.Cache, 0o755); err != nil {
		return Manifest{}, fmt.Errorf("create document worker cache: %w", err)
	}
	if data, err := os.ReadFile(layout.Manifest); err == nil {
		var existing Manifest
		if err := json.Unmarshal(data, &existing); err != nil {
			return Manifest{}, err
		}
		if existing.Revision != ManifestRevision {
			return Manifest{}, fmt.Errorf("document manifest revision mismatch: %s", existing.Revision)
		}
		// A matching revision without checksummed assets is incomplete cache
		// state, not completed first-party provisioning. Rebuild it below.
		if len(existing.Assets) > 0 {
			if err := VerifyAssets(layout.Prefix, existing.Assets); err != nil {
				return Manifest{}, err
			}
			return existing, nil
		}
	} else if !os.IsNotExist(err) {
		return Manifest{}, err
	}
	manifest := Manifest{
		Revision:  ManifestRevision,
		Python:    filepath.Join(layout.Bin, "python"),
		Node:      filepath.Join(layout.Bin, "node"),
		Converter: filepath.Join(layout.Bin, "soffice"),
		Platforms: []string{"darwin", "linux", "windows"},
		PythonRequirements: []Requirement{
			{Import: "docx", Distribution: "python-docx", Version: "1.2.0"},
			{Import: "openpyxl", Version: "3.1.5"},
			{Import: "pptx", Distribution: "python-pptx", Version: "1.0.2"},
			{Import: "reportlab", Version: "5.0.1"},
			{Import: "pypdf", Version: "6.19.0"},
		},
	}
	if err := os.MkdirAll(layout.Skills, 0o755); err != nil {
		return Manifest{}, err
	}
	if err := InstallEmbeddedSkills(layout); err != nil {
		return Manifest{}, err
	}
	assets, err := inventoryTree(layout.Prefix, layout.Skills)
	if err != nil {
		return Manifest{}, err
	}
	manifest.Assets = assets
	if err := InstallProbe(layout, manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func inventoryTree(prefix, root string) ([]Asset, error) {
	var assets []Asset
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		rel, err := filepath.Rel(prefix, path)
		if err != nil {
			return err
		}
		assets = append(assets, Asset{Path: filepath.ToSlash(rel), SHA256: hex.EncodeToString(sum[:])})
		return nil
	})
	sort.Slice(assets, func(i, j int) bool { return assets[i].Path < assets[j].Path })
	return assets, err
}
