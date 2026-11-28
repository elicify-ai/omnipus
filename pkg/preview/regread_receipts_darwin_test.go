//go:build darwin

package preview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// regReadReceipts reads the fake's per-start receipts from the work folder.
// Its T-105 callers are macOS-only; the fake converter remains cross-platform.
func regReadReceipts(t *testing.T, c *call) []regReadReceipt {
	t.Helper()
	raws, err := filepath.Glob(filepath.Join(c.dir, "regread-start-*.json"))
	require.NoError(t, err, "instrument: the fake wrote its per-start receipts")
	receipts := make([]regReadReceipt, 0, len(raws))
	for _, raw := range raws {
		data, readErr := os.ReadFile(raw)
		require.NoError(t, readErr, "instrument: receipt %s is readable", raw)
		var receipt regReadReceipt
		require.NoError(t, json.Unmarshal(data, &receipt), "instrument: receipt %s decodes", raw)
		receipts = append(receipts, receipt)
	}
	sort.Slice(receipts, func(i, j int) bool { return receipts[i].Start < receipts[j].Start })
	return receipts
}
