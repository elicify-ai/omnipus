// Omnipus — release and rename pipeline-owned view provenance with a .base.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/elicify-ai/omnipus/pkg/records"
	"gopkg.in/yaml.v3"
)

// VerifyBase refuses a lifecycle change if a recorded path no longer has the
// file and marker it claims. Call before moving/trashing the .base itself.
func (m *ViewMembership) VerifyBase(base string) error {
	if !validMembershipPath(base, ".base") {
		return fmt.Errorf("knowledge: invalid managed base %q", base)
	}
	for _, name := range sortedManagedNames(m.Bases[base]) {
		if _, _, err := m.readManagedView(base, name, m.Bases[base][name]); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("knowledge: verify managed view %q: %w", name, err)
		}
	}
	return nil
}

// ReleaseBase revokes all of a .base's owned paths in ONE atomic membership
// write before touching a view marker. The caller holds WithViewMembership and
// calls this before trashing the .base: no failed post-trash save can leave an
// old path authorized to overwrite a new file. A failed marker rewrite after
// revocation is visible and leaves that view unowned, never re-authorized by
// its editable derived_from field.
func (m *ViewMembership) ReleaseBase(base string) error {
	if !validMembershipPath(base, ".base") {
		return fmt.Errorf("knowledge: invalid base release %q", base)
	}
	members := m.Bases[base]
	if len(members) == 0 {
		return nil
	}
	names := sortedManagedNames(members)
	for _, name := range names {
		rel := members[name]
		err := m.withManagedViewLock(rel, func() error {
			_, _, readErr := m.readManagedView(base, name, rel)
			return readErr
		})
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("knowledge: verify base release view %q: %w", rel, err)
		}
	}
	delete(m.Bases, base)
	if err := SaveViewMembership(m); err != nil {
		m.Bases[base] = members
		return fmt.Errorf("knowledge: cannot revoke base views; trash not started: %w", err)
	}
	for _, name := range names {
		rel := members[name]
		err := m.withManagedViewLock(rel, func() error {
			_, body, readErr := m.readManagedView(base, name, rel)
			if errors.Is(readErr, os.ErrNotExist) {
				return nil
			}
			if readErr != nil {
				return readErr
			}
			released, stripErr := records.StripCopiedViewProvenance(body)
			if stripErr != nil {
				return stripErr
			}
			return m.writeManagedViewBytes(rel, released)
		})
		if err != nil {
			return fmt.Errorf("knowledge: base release incomplete; membership revoked but view %q could not be released: %w", rel, err)
		}
	}
	return nil
}

func (m *ViewMembership) withManagedViewLock(rel string, fn func() error) error {
	lockDir, err := LockDirFor(m.home, m.Root)
	if err != nil {
		return err
	}
	cfg := NoteLockConfig{CollectionRoot: m.Root, LockDir: lockDir}
	return WithNoteWriteLock(cfg, rel, fn)
}

func sortedManagedNames(names map[string]string) []string {
	keys := make([]string, 0, len(names))
	for name := range names {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	return keys
}

// rewriteDerivedBase changes only the two provenance/grouping values in a
// pipeline-generated view. YAML nodes preserve key order and comments.
func rewriteDerivedBase(data []byte, oldBase, newBase string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("knowledge: managed view is not a YAML mapping")
	}
	mapping := doc.Content[0]
	var source, derived *yaml.Node
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		switch mapping.Content[i].Value {
		case "source":
			source = mapping.Content[i+1]
		case "derived_from":
			derived = mapping.Content[i+1]
		}
	}
	if derived == nil || derived.Kind != yaml.ScalarNode || derived.Value != oldBase {
		return nil, fmt.Errorf("knowledge: managed view provenance changed before rename")
	}
	derived.Value = newBase
	if source == nil {
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "source"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: newBase})
	} else {
		if source.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("knowledge: managed view source is not a scalar")
		}
		source.Value = newBase
	}
	return yaml.Marshal(&doc)
}
