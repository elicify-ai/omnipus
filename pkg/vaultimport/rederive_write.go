// Omnipus — record-authorized view writes for .base re-derivation.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package vaultimport

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"gopkg.in/yaml.v3"
)

// fileTranslatedBaseWithRecord requires the caller to hold the collection's
// membership lock. No field inside a view is allowed to enroll a member.
func fileTranslatedBaseWithRecord(vaultRoot, baseRelPath string, pb *ParsedBase, schemaIdx *SchemaIndex, slugs *SlugRegistry, record *knowledge.ViewMembership, mine []string) (*RederiveBaseResult, error) {
	outcome, produced := TranslateBase(pb, baseRelPath, schemaIdx, slugs)
	res := &RederiveBaseResult{BaseRelPath: baseRelPath, Status: outcome.Status,
		RefusedReason: outcome.RefusedReason, Views: outcome.Views}
	if outcome.Status == OutcomeRefused {
		res.KeptExisting = mine
		return res, nil
	}
	for _, pv := range produced {
		original := filepath.Base(pv.RelPath)
		name := strings.TrimSuffix(original, filepath.Ext(original))
		if recorded, owns := record.Bases[baseRelPath][name]; owns {
			// The record names a CURRENT path; a filename reconstructed from a
			// slug cannot supersede it, even after a Library move.
			updated, err := record.RewriteManagedView(baseRelPath, name, pv.Bytes)
			if err != nil {
				return nil, fmt.Errorf("vaultimport: rewrite managed view %q at %q: %w", name, recorded, err)
			}
			if updated {
				res.Written = append(res.Written, name)
			} else {
				res.Unchanged = append(res.Unchanged, name)
			}
			setTranslatedOutputPath(res.Views, pv.RelPath, recorded)
			continue
		}
		if err := createTranslatedView(record, baseRelPath, &pv, slugs, res); err != nil {
			return nil, err
		}
	}

	// Names that translated successfully stay enrolled. Every other OLD
	// record entry is eligible for deletion only after both record + current
	// marker have been rechecked under that view's own file lock.
	producedNames := make(map[string]bool, len(produced))
	for _, name := range append(append([]string{}, res.Written...), res.Unchanged...) {
		producedNames[name] = true
	}
	for _, name := range mine {
		if producedNames[name] {
			continue
		}
		deleted, err := record.DeleteManagedView(baseRelPath, name)
		if err != nil {
			return nil, fmt.Errorf("vaultimport: delete managed view %q: %w", name, err)
		}
		if deleted {
			res.Deleted = append(res.Deleted, name)
		}
	}
	sort.Strings(res.Written)
	sort.Strings(res.Unchanged)
	sort.Strings(res.Deleted)
	return res, nil
}

func createTranslatedView(record *knowledge.ViewMembership, base string, pv *ProducedView, slugs *SlugRegistry, res *RederiveBaseResult) error {
	original := pv.RelPath
	name := strings.TrimSuffix(filepath.Base(original), filepath.Ext(original))
	for {
		if err := record.CreateManagedView(base, name, pv.RelPath, pv.Bytes); err == nil {
			res.Written = append(res.Written, name)
			setTranslatedOutputPath(res.Views, original, pv.RelPath)
			return nil
		} else if !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("vaultimport: create managed view %q: %w", pv.RelPath, err)
		}
		// The same slug registry used for translation allocates a suffix; an
		// occupied path never becomes an overwrite merely because it parses.
		label := translatedViewLabel(res.Views, original)
		next := slugs.Slug(base, label)
		if next == name {
			return fmt.Errorf("vaultimport: unable to allocate an unused view name for %q", original)
		}
		data, err := renameGeneratedView(pv.Bytes, name, next)
		if err != nil {
			return err
		}
		pv.Bytes = data
		name = next
		pv.RelPath = filepath.ToSlash(filepath.Join(filepath.Dir(filepath.FromSlash(original)), name+".view"))
	}
}

func translatedViewLabel(views []ViewOutcome, original string) string {
	for _, v := range views {
		if v.OutputRelPath == original {
			return v.DisplayName
		}
	}
	return ""
}

func setTranslatedOutputPath(views []ViewOutcome, original, current string) {
	for i := range views {
		if views[i].OutputRelPath == original {
			views[i].OutputRelPath = current
			return
		}
	}
}

// renameGeneratedView edits only a PIPELINE-created document when its original
// filename was occupied. The rewritten view name must match its new filename.
func renameGeneratedView(data []byte, old, next string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("vaultimport: generated view is not a YAML mapping")
	}
	for i := 0; i+1 < len(doc.Content[0].Content); i += 2 {
		k, v := doc.Content[0].Content[i], doc.Content[0].Content[i+1]
		if k.Value == "name" {
			if v.Value != old || v.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("vaultimport: generated view's name changed before placement")
			}
			v.Value = next
			return yaml.Marshal(&doc)
		}
	}
	return nil, fmt.Errorf("vaultimport: generated view has no name to disambiguate")
}
