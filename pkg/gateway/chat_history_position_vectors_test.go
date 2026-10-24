package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// Instrument check, not a product test: independent Python hashlib/struct
// derivation froze these before any product position encoder existed. This
// catches mistakes in the test-side D2 oracle used by the real handler test.
func pagingVerifyPositionVectors(t *testing.T) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "tests", "chat-history-paging-be", "position-vectors.json"))
	require.NoError(t, err)
	var vectors []map[string]string
	require.NoError(t, json.Unmarshal(b, &vectors))
	require.Len(t, vectors, 6)
	for _, v := range vectors {
		offset, err := strconv.ParseInt(v["byte_offset"], 10, 64)
		require.NoError(t, err)
		got := pagingPositionOracle(v["session_id"], session.ArchiveAddress{PartitionKey: v["partition_key"], ByteOffset: offset, EntryID: v["entry_id"]})
		require.Equal(t, v["expected"], got, "independent frozen D2 vector: %s", v["name"])
	}
}
