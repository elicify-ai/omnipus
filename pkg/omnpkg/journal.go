package omnpkg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// JournalState is the journal's on-disk state (E1; Rollback contract,
// revision 7): started → committed → done, or rolled_back / rollback_incomplete.
// done and rolled_back are terminal; the rest are unfinished.
type JournalState string

const (
	JournalStarted            JournalState = "started"
	JournalCommitted          JournalState = "committed"
	JournalDone               JournalState = "done"
	JournalRolledBack         JournalState = "rolled_back"
	JournalRollbackIncomplete JournalState = "rollback_incomplete"
)

// Terminal reports whether the import behind the journal has finished either
// way (done or rolled_back). Any other value, unknown ones included, is
// unfinished and is kept for start-up recovery.
func (s JournalState) Terminal() bool {
	return s == JournalDone || s == JournalRolledBack
}

// JournalName is the journal file inside a staging folder.
const JournalName = "journal.json"

// JournalFile is the journal.json shape inside a staging folder
// ($OMNIPUS_HOME/backups/.staging/<staging_id>/journal.json). The agent ID is
// stored as new_agent_id, the name the manual recovery steps tell the owner
// to look for.
type JournalFile struct {
	State       JournalState `json:"state"`
	AgentID     string       `json:"new_agent_id"`
	StagingID   string       `json:"staging_id,omitempty"`
	WorkspaceID string       `json:"workspace_id,omitempty"`
}

// WriteJournal writes dir/journal.json atomically and synced (temp file,
// fsync, rename, directory sync), so a journal state is durable before the
// next import step writes anything (E1).
func WriteJournal(dir string, j JournalFile) error {
	raw, err := json.Marshal(j)
	if err != nil {
		return fmt.Errorf("omnpkg: encode journal: %w", err)
	}
	if err := fileutil.WriteFileAtomicSyncDir(filepath.Join(dir, JournalName), raw, 0o600); err != nil {
		return fmt.Errorf("omnpkg: write journal: %w", err)
	}
	return nil
}

// ReadJournal reads dir/journal.json. A missing journal returns an error that
// satisfies errors.Is(err, fs.ErrNotExist).
func ReadJournal(dir string) (JournalFile, error) {
	var j JournalFile
	raw, err := os.ReadFile(filepath.Join(dir, JournalName))
	if err != nil {
		return j, fmt.Errorf("omnpkg: read journal: %w", err)
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return j, fmt.Errorf("omnpkg: decode journal: %w", err)
	}
	return j, nil
}
