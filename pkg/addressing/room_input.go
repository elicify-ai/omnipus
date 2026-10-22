package addressing

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func (l *Ledger) ListCaptures(sessionID string) ([]Capture, error) {
	path, err := l.path(sessionID)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return readAllLatest(path)
}

// BindRoomInput records the exact successful model placement before delivery.
func (l *Ledger) BindRoomInput(sessionID, requestID string, input session.ArchiveAddress, execution session.ExecutionIdentity) error {
	if input.EntryID == "" || execution.RunID == "" || execution.BootSeq == 0 {
		return fmt.Errorf("room input: exact admitted occurrence is missing")
	}
	path, err := l.path(sessionID)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	c, found, err := readLatest(path, requestID)
	if err != nil {
		return err
	}
	if !found || c.Source.Kind != SourceRoom || c.Discarded {
		return fmt.Errorf("room input: request no longer admits model placement")
	}
	if c.ModelInput != nil {
		if *c.ModelInput == input && c.ModelExecution != nil && *c.ModelExecution == execution {
			return nil
		}
		return fmt.Errorf("room input: request already belongs to another model placement")
	}
	c.ModelInput = &input
	c.ModelExecution = &execution
	if err := repairTornTail(path); err != nil {
		return err
	}
	return fileutil.AppendJSONLSync(path, c)
}
