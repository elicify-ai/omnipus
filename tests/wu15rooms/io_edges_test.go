package wu15rooms_test

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/rooms"
	"github.com/elicify-ai/omnipus/pkg/session"
)

type countingWriter struct {
	*session.UnifiedStore
	mu      sync.Mutex
	calls   map[string]int
	commits map[string]int
	fail    func(string, session.TranscriptEntry) bool
	failed  atomic.Int64
}

func (w *countingWriter) AppendTranscriptOnce(sid string, entry session.TranscriptEntry) (bool, error) {
	w.mu.Lock()
	if w.calls == nil {
		w.calls = map[string]int{}
		w.commits = map[string]int{}
	}
	w.calls[entry.ID]++
	cut := w.fail != nil && w.fail(sid, entry)
	w.mu.Unlock()
	if cut {
		w.failed.Add(1)
		return false, errCrashCut
	}
	written, err := w.UnifiedStore.AppendTranscriptOnce(sid, entry)
	if written && err == nil {
		w.mu.Lock()
		w.commits[entry.ID]++
		w.mu.Unlock()
	}
	return written, err
}

func (w *countingWriter) attempts(id string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls[id]
}
func (w *countingWriter) committed(id string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.commits[id]
}
func (w *countingWriter) setFault(fn func(string, session.TranscriptEntry) bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.fail = fn
}

type cutJournal struct {
	rooms.Journal
	mu      sync.Mutex
	cut     func(string, string, any) string // "before" or "after" the real fsynced append
	fired   bool
	hits    atomic.Int64
	crashed *atomic.Bool
}

func (j *cutJournal) Append(roomID, stream string, record any) error {
	j.mu.Lock()
	phase := ""
	if !j.fired && j.cut != nil {
		phase = j.cut(roomID, stream, record)
		if phase != "" {
			j.fired = true
		}
	}
	j.mu.Unlock()
	if phase == "before" {
		j.hits.Add(1)
		j.crashed.Store(true)
		return errCrashCut
	}
	if err := j.Journal.Append(roomID, stream, record); err != nil {
		return err
	}
	if phase == "after" {
		j.hits.Add(1)
		j.crashed.Store(true)
		return errCrashCut
	}
	return nil
}

func (j *cutJournal) setCut(fn func(string, string, any) string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.cut = fn
	j.fired = false
}

// Decode only public delivery-state fields used to select the fault boundary;
// never use private records to supply an expected result.
func deliveryIdentity(t *testing.T, record any) (requestID, agentID, state string) {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Errorf("instrument: encode delivery boundary: %v", err)
		return "", "", ""
	}
	var delivery gen.RoomDeliveryState
	if err := json.Unmarshal(data, &delivery); err != nil {
		t.Errorf("instrument: decode public delivery boundary fields: %v", err)
		return "", "", ""
	}
	return delivery.RequestId, delivery.AgentId, delivery.State
}
