package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	_ "unsafe" // Test-only alias to the existing session filesystem seam (same technique as rest_task_session_binding_fault_test.go).

	"github.com/stretchr/testify/require"
)

//go:linkname t10GatewaySessionAtomicWriter github.com/elicify-ai/omnipus/pkg/session.writeFileAtomicFn
var t10GatewaySessionAtomicWriter func(string, []byte, os.FileMode) error

// t10FailSessionStopWrites makes every write of the loop state file of one
// session that saves an EMPTY loop mode (the stopped state) fail in-process,
// with root and non-root users alike; all other writes reach the real atomic
// writer. It returns the number of failed attempts (via the pointer) and a
// restore func. Serial tests only.
func t10FailSessionStopWrites(t *testing.T, sessionID string, failures *int) func() {
	t.Helper()
	original := t10GatewaySessionAtomicWriter
	require.NotNil(t, original, "existing session writer seam must be available")
	var mu sync.Mutex
	active := true
	t10GatewaySessionAtomicWriter = func(path string, data []byte, perm os.FileMode) error {
		mu.Lock()
		defer mu.Unlock()
		if active && filepath.Base(path) == "loop.json" && filepath.Base(filepath.Dir(path)) == sessionID {
			var v struct {
				Mode string `json:"loop_mode"`
			}
			if err := json.Unmarshal(data, &v); err != nil {
				return err
			}
			if v.Mode == "" {
				*failures++
				return os.ErrPermission
			}
		}
		return original(path, data, perm)
	}
	restore := func() { mu.Lock(); active = false; mu.Unlock() }
	t.Cleanup(func() { restore(); t10GatewaySessionAtomicWriter = original })
	return restore
}
