package cron

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type t10LogBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *t10LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}
func (b *t10LogBuffer) snapshot() string { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }

// Parse structured identities, not substring matches (job '-2' is legal).
// Wording is intentionally not specified; the operator line must name memory.
func t10MemoryWaits(t *testing.T, b *t10LogBuffer) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(b.snapshot()), "\n") {
		if line == "" {
			continue
		}
		var v map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &v))
		message, _ := v["msg"].(string)
		if message == "" {
			message, _ = v["message"].(string)
		}
		if !strings.Contains(strings.ToLower(message), "memory") {
			continue
		}
		id, _ := v["job_id"].(string)
		if id != "" {
			out[id]++
		}
	}
	return out
}
