// Omnipus — vault inventory for the importer.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
)

// Inventory is every note and every `.base` file a vault holds, as found by
// pkg/knowledge's own collection scanner (knowledge.Scan) — reused rather
// than reimplemented so this importer skips exactly the directories the
// running product skips (.obsidian, .omnipus-vault, .git, .trash) and
// resolves the root the same way (knowledge.ResolveCollectionRoot).
type Inventory struct {
	// Root is the resolved real path of the vault.
	Root string
	// Notes is every note file, absolute path.
	Notes []string
	// NoteRel maps an absolute note path to its vault-relative, slash
	// separated path — used only for reporting.
	NoteRel map[string]string
	// Bases is every `.base` file, absolute path.
	Bases []string
	// BaseRel is Bases' vault-relative paths, for the `source:` field a
	// translated view records (ViewDef.yaml's `source`) and for the report.
	BaseRel map[string]string
}

// ScanVault inventories a vault at root.
func ScanVault(root string) (*Inventory, error) {
	res, err := knowledge.Scan(root)
	if err != nil {
		return nil, fmt.Errorf("vaultimport: scanning %q: %w", root, err)
	}
	inv := &Inventory{
		Root:    res.Root,
		NoteRel: map[string]string{},
		BaseRel: map[string]string{},
	}
	for _, e := range res.Entries {
		abs := filepath.Join(res.Root, filepath.FromSlash(e.RelPath))
		switch {
		case e.Kind == knowledge.ScanKindNote:
			inv.Notes = append(inv.Notes, abs)
			inv.NoteRel[abs] = e.RelPath
		case strings.EqualFold(filepath.Ext(e.RelPath), ".base"):
			inv.Bases = append(inv.Bases, abs)
			inv.BaseRel[abs] = e.RelPath
		}
	}
	return inv, nil
}
