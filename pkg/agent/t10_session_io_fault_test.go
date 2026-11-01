//go:build goolm && stdjson

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	_ "unsafe" // Test-only access to the existing session filesystem edge; see gateway's binding-fault fixture.

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

//go:linkname t10SessionAtomicWriter github.com/elicify-ai/omnipus/pkg/session.writeFileAtomicFn
var t10SessionAtomicWriter func(string, []byte, os.FileMode) error

// Real atomic writes still execute unless the injected filesystem failure
// rejects one. This never implements loop recovery or manufactures a store.
func t10InterceptLoopWrites(t *testing.T, hook func(string, session.MetaPatch) error) func() {
	t.Helper()
	original := t10SessionAtomicWriter
	require.NotNil(t, original, "existing session writer must be available")
	var mu sync.Mutex
	active := true
	t10SessionAtomicWriter = func(path string, data []byte, perm os.FileMode) error {
		mu.Lock()
		defer mu.Unlock()
		if active && filepath.Base(path) == "loop.json" {
			var v struct {
				Job  string `json:"loop_job_id"`
				Mode string `json:"loop_mode"`
			}
			if err := json.Unmarshal(data, &v); err != nil {
				return err
			}
			if err := hook(filepath.Base(filepath.Dir(path)), session.MetaPatch{LoopJobID: &v.Job, LoopMode: &v.Mode}); err != nil {
				return err
			}
		}
		return original(path, data, perm)
	}
	restore := func() { mu.Lock(); active = false; mu.Unlock() }
	t.Cleanup(func() { restore(); t10SessionAtomicWriter = original })
	return restore
}
