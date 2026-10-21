package workspace

import "errors"

// RefuseDefaultDeletion is the shared default-workspace deletion protection
// used by the HTTP handler and the agent tool. Call it on the authoritative
// state, under the workspace lock and before any deletion or cascade.
func RefuseDefaultDeletion(isDefault bool) error {
	if isDefault {
		return errors.New("cannot delete the default workspace")
	}
	return nil
}
