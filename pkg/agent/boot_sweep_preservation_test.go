package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// bootSweepRecordSnapshot captures the pre-sweep fixture, not a post-sweep
// oracle. Frozen ADR-20260928 D8.3 assigns steered records exclusively to
// SteerBootRecovery: the plan sweep must change neither a field nor the journal.
type bootSweepRecordSnapshot struct {
	record  []byte
	journal []byte
}

func snapshotBootSweepRecord(t *testing.T, ls *session.LifecycleStore, id string) bootSweepRecordSnapshot {
	t.Helper()
	rec, err := session.NewLifecycleStore(ls.Dir()).Load(id)
	if err != nil {
		t.Fatalf("snapshot lifecycle %q: %v", id, err)
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("snapshot record %q: %v", id, err)
	}
	journal, err := os.ReadFile(filepath.Join(ls.Dir(), id+".jsonl"))
	if err != nil {
		t.Fatalf("snapshot durable journal %q: %v", id, err)
	}
	return bootSweepRecordSnapshot{record: raw, journal: journal}
}

func assertBootSweepRecordUntouched(t *testing.T, ls *session.LifecycleStore, id string, before bootSweepRecordSnapshot) {
	t.Helper()
	// Reopen the real store so an in-memory copy cannot hide a durable write.
	after := snapshotBootSweepRecord(t, ls, id)
	if !bytes.Equal(after.record, before.record) {
		t.Errorf("D8.3: plan sweep changed protected record %q; want the complete pre-sweep fixture unchanged\nbefore: %s\nafter:  %s", id, before.record, after.record)
	}
	if !bytes.Equal(after.journal, before.journal) {
		t.Errorf("D8.3: plan sweep wrote protected journal %q; before %d bytes, after %d bytes — even an identical appended record is forbidden", id, len(before.journal), len(after.journal))
	}
}
