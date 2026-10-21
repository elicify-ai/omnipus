package gateway

import (
	"fmt"
	"os"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/agent"
)

// This obstructs a real filesystem append, not the transcript implementation.
// The synchronous EventBus tap restores the path at the existing save-error
// event, before deferred finalization can try another write. The gateway's real
// hub tap is still called exactly once for every event by the test wrapper.
type iterationNoticeFault struct {
	mu                   sync.Mutex
	path, backup, scope  string
	armed, restored      bool
	probeErr, setupErr   error
	saveErrors           int
	errorTurn, errorText string
}

func (f *iterationNoticeFault) observe(event agent.Event) {
	if event.Meta.SessionKey != f.scope {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if event.Kind == agent.EventKindToolExecEnd && event.Meta.Iteration == 2 && !f.armed {
		if err := os.Rename(f.path, f.backup); err != nil {
			f.setupErr = err
			return
		}
		if err := os.Mkdir(f.path, 0o700); err != nil {
			f.setupErr = err
			return
		}
		f.armed = true
		file, err := os.OpenFile(f.path, os.O_WRONLY|os.O_APPEND, 0o600)
		if file != nil {
			if closeErr := file.Close(); closeErr != nil {
				f.setupErr = closeErr
			}
		}
		f.probeErr = err
		if err == nil {
			f.setupErr = fmt.Errorf("filesystem append obstruction did not fail")
		}
	}
	payload, isSaveError := event.Payload.(agent.ErrorPayload)
	if event.Kind == agent.EventKindError && isSaveError && payload.Stage == "session_save" && f.armed {
		f.saveErrors++
		f.errorTurn, f.errorText = event.Meta.TurnID, payload.Message
		if !f.restored {
			f.restoreLocked()
		}
	}
}

func (f *iterationNoticeFault) restoreLocked() {
	if !f.armed || f.restored {
		return
	}
	if err := os.Remove(f.path); err != nil {
		f.setupErr = err
		return
	}
	if err := os.Rename(f.backup, f.path); err != nil {
		f.setupErr = err
		return
	}
	f.restored = true
}

func (f *iterationNoticeFault) restore() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restoreLocked()
	return f.setupErr
}

func (f *iterationNoticeFault) snapshot() (bool, bool, error, error, int, string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.armed, f.restored, f.probeErr, f.setupErr, f.saveErrors, f.errorTurn, f.errorText
}
