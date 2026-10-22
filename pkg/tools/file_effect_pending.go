package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// not-wire-format: content-free index of unresolved managed writers to one target.
type pendingFileEffects struct {
	Target      string          `json:"target"`
	Occurrences map[string]bool `json:"occurrences"`
}

func fileEffectPendingKey(w FileEffectWitness) string {
	return digestBytes([]byte(w.SessionID + "\x00" + FileEffectKey(w.Assistant, w.ToolCallID)))
}

func fileEffectPendingPath(home, target string) string {
	return filepath.Join(home, "session_lifecycle", "file_effect_targets", digestBytes([]byte(fileEffectLockKey(target)))+".json")
}

func readPendingFileEffects(home, target string) (pendingFileEffects, error) {
	data, err := os.ReadFile(fileEffectPendingPath(home, target))
	if err != nil {
		return pendingFileEffects{}, err
	}
	var state pendingFileEffects
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	if state.Occurrences == nil || fileEffectLockKey(state.Target) != fileEffectLockKey(target) {
		return state, fmt.Errorf("file recovery: corrupt unresolved-writer index")
	}
	return state, nil
}

// The caller holds the same canonical target lock as all three managed writers.
func registerPendingFileEffect(home string, w FileEffectWitness) error {
	state, err := readPendingFileEffects(home, w.Target)
	if errors.Is(err, fs.ErrNotExist) {
		state = pendingFileEffects{Target: w.Target, Occurrences: map[string]bool{}}
	} else if err != nil {
		return err
	}
	state.Occurrences[fileEffectPendingKey(w)] = true
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomicSyncDir(fileEffectPendingPath(home, w.Target), data, 0o600)
}

func otherPendingFileEffects(home string, w FileEffectWitness) (bool, error) {
	state, err := readPendingFileEffects(home, w.Target)
	if err != nil {
		return true, err
	}
	key := fileEffectPendingKey(w)
	if !state.Occurrences[key] {
		return true, fmt.Errorf("file recovery: unresolved occurrence is missing from writer index")
	}
	for other, pending := range state.Occurrences {
		if pending && other != key {
			return true, nil
		}
	}
	return false, nil
}

// SettleFileEffectWitness retires only a durably accounted occurrence: an actual
// original result or a checked present/absent observation, never Unknown. Losing
// this metadata write blocks continuation, rather than guessing another writer finished.
func SettleFileEffectWitness(ctx context.Context) error {
	identity, found := ctx.Value(fileEffectContextKey{}).(FileEffectContext)
	if !found || identity.SessionID == "" {
		return nil
	}
	w, err := ReadFileEffectWitness(identity.Home, identity.SessionID, identity.Assistant, identity.ToolCallID)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if w.Execution != identity.Execution {
		return fmt.Errorf("file recovery: archived result execution does not own the witness")
	}
	lock := task.TaskFileLock.Get(fileEffectLockKey(w.Target))
	lock.Lock()
	defer lock.Unlock()
	state, err := readPendingFileEffects(identity.Home, w.Target)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	key := fileEffectPendingKey(w)
	if !state.Occurrences[key] {
		return nil
	}
	delete(state.Occurrences, key)
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomicSyncDir(fileEffectPendingPath(identity.Home, w.Target), data, 0o600)
}
