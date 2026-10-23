package session

import (
	"errors"
	"fmt"
)

// ErrWorkspaceImmutable is a visible refusal, never permission to continue in
// the old workspace while presenting the request as a successful move.
var ErrWorkspaceImmutable = errors.New("this conversation cannot change workspace; open the conversation in the workspace you want")

func (us *UnifiedStore) validateWorkspaceBindingLocked(meta *UnifiedMeta, target string) error {
	if target == meta.WorkspaceID {
		return nil
	}
	if meta.WorkspaceID != "" {
		return fmt.Errorf("%w (session %q)", ErrWorkspaceImmutable, meta.ID)
	}
	// A fresh, unfiled session may receive its initial creation-time binding.
	// Chat-only saved inputs count as use too, not just model placements.
	archive, err := us.archiveStore(meta.ID)
	if err != nil {
		return fmt.Errorf("check initial workspace binding: %w", err)
	}
	used := false
	if scanErr := archive.ScanAll(func(_ ArchiveAddress, _ ArchiveRecord) bool { used = true; return false }); scanErr != nil {
		return fmt.Errorf("check initial workspace binding: %w", scanErr)
	}
	if used {
		return fmt.Errorf("%w (session %q)", ErrWorkspaceImmutable, meta.ID)
	}
	return nil
}

// ValidateWorkspaceReuse checks a request against an existing session without
// changing it. An omitted workspace retains the stored binding.
func ValidateWorkspaceReuse(meta *UnifiedMeta, requested string) error {
	if meta == nil {
		return errors.New("session metadata unavailable")
	}
	if requested != "" && requested != meta.WorkspaceID {
		return fmt.Errorf("%w (session %q)", ErrWorkspaceImmutable, meta.ID)
	}
	return nil
}
