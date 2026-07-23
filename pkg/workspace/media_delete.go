package workspace

import (
	"fmt"
	"os"
	"path/filepath"
)

func WorkspaceDeleteHook(home, workspaceID string) error {
	workspaceDir, err := SafeWorkspaceDir(home, workspaceID)
	if err != nil {
		return err
	}
	mediaDir := filepath.Join(workspaceDir, "media")
	if err := os.RemoveAll(mediaDir); err != nil {
		return fmt.Errorf("workspace: cascade-delete media %q: %w", workspaceID, err)
	}
	return nil
}
