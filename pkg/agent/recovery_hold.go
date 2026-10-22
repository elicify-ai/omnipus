package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// not-wire-format: private durable continuation fence.
type recoveryHold struct {
	Occurrences map[string]string `json:"occurrences"`
	Reported    bool              `json:"reported"`
}

func recoveryHoldPath(id string) (string, error) {
	if id == "" || id == "." || id == ".." || filepath.Base(id) != id {
		return "", fmt.Errorf("recovery: invalid session identity")
	}
	return filepath.Join(omnipusHome(), "sessions", id, "recovery_hold.json"), nil
}

func loadRecoveryHold(id string) (recoveryHold, error) {
	p, err := recoveryHoldPath(id)
	if err != nil {
		return recoveryHold{}, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return recoveryHold{}, nil
	}
	if err != nil {
		return recoveryHold{}, err
	}
	var held recoveryHold
	if err := json.Unmarshal(data, &held); err != nil {
		return held, err
	}
	if held.Occurrences == nil {
		return held, fmt.Errorf("recovery: invalid durable hold")
	}
	return held, nil
}

func recordRecoveryHold(id, key, reason string) error {
	p, err := recoveryHoldPath(id)
	if err != nil {
		return err
	}
	lock := task.TaskFileLock.Get(p)
	lock.Lock()
	defer lock.Unlock()
	held, err := loadRecoveryHold(id)
	if err != nil {
		return err
	}
	if held.Occurrences == nil {
		held.Occurrences = map[string]string{}
	}
	if _, found := held.Occurrences[key]; !found {
		held.Reported = false
	}
	held.Occurrences[key] = reason
	data, err := json.Marshal(held)
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomicSyncDir(p, data, 0o600)
}

func markRecoveryReported(id string) error {
	p, err := recoveryHoldPath(id)
	if err != nil {
		return err
	}
	lock := task.TaskFileLock.Get(p)
	lock.Lock()
	defer lock.Unlock()
	held, err := loadRecoveryHold(id)
	if err != nil || len(held.Occurrences) == 0 {
		return err
	}
	held.Reported = true
	data, err := json.Marshal(held)
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomicSyncDir(p, data, 0o600)
}

func recoveryConsentKeys(id string) ([]string, error) {
	held, err := loadRecoveryHold(id)
	if err != nil {
		return nil, err
	}
	if len(held.Occurrences) > 0 && !held.Reported {
		return nil, fmt.Errorf("reconciliation_required: the unresolved operation report has not been saved")
	}
	keys := make([]string, 0, len(held.Occurrences))
	for key := range held.Occurrences {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

func recoveryConsentAllows(rec *session.LifecycleRecord, claim executionClaim, held recoveryHold) bool {
	consent := rec.RecoveryConsent
	if consent == nil || consent.Generation != claim.Generation || consent.ExecutionID == nil ||
		consent.ExecutionID.RunID != claim.RunID || consent.ExecutionID.BootSeq != claim.BootSeq || len(consent.OccurrenceKeys) != len(held.Occurrences) {
		return false
	}
	for _, key := range consent.OccurrenceKeys {
		if _, ok := held.Occurrences[key]; !ok {
			return false
		}
	}
	return true
}
