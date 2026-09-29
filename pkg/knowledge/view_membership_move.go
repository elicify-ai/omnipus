// Omnipus — verify a pipeline-owned view at its current recorded path.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ErrViewMoveIdentityMismatch means the saved view no longer proves the
// revoked identity; its editable provenance cannot restore write authority.
var ErrViewMoveIdentityMismatch = errors.New("knowledge: revoked view identity mismatch")

// readManagedView checks BOTH pipeline membership (supplied by the caller)
// and the current file's provenance before any managed-view mutation.
func (m *ViewMembership) readManagedView(base, name, rel string) (*records.SavedView, []byte, error) {
	root, err := NewCollectionRoot(OSLinkFS(), m.Root)
	if err != nil {
		return nil, nil, err
	}
	file, err := ReadViewFile(OSLinkFS(), root, rel)
	if err != nil {
		return nil, nil, fmt.Errorf("knowledge: read managed view %q: %w", rel, err)
	}
	if file.Rejection != "" || file.Err != nil || file.Skipped {
		return nil, nil, fmt.Errorf("knowledge: managed view %q cannot be read safely: %s: %v", rel, file.Rejection, file.Err)
	}
	view, rejection := records.ParseView(file.Path, file.Bytes)
	if rejection != nil {
		return nil, nil, fmt.Errorf("%w: managed view %q: %s", ErrViewMoveIdentityMismatch, rel, rejection.Reason)
	}
	if view.Def.Name != name || view.Def.DerivedFrom == nil || *view.Def.DerivedFrom != base {
		return nil, nil, fmt.Errorf("%w: view %q no longer matches its pipeline-owned membership", ErrViewMoveIdentityMismatch, rel)
	}
	return view, file.Bytes, nil
}
