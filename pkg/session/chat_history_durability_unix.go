//go:build linux || darwin

package session

import (
	"errors"
	"os"
)

// Directory-entry durability is checked before any root can reference a new
// generation. Neither an open nor a sync failure is a warn-only outcome.
func historySyncDirectory(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}
