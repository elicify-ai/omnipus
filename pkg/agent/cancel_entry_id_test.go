//go:build goolm && stdjson

package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A5: each cancel in a session writes its own turn_canceled id, so an
// id-deduplicating append can never drop a later cancel.
func TestCanceledEntryID_IsUniquePerCancel(t *testing.T) {
	a := canceledEntryID("sess-1", "turn-A")
	b := canceledEntryID("sess-1", "turn-B")
	assert.NotEqual(t, a, b, "two canceled turns of one session need distinct ids")
	assert.Equal(t, a, canceledEntryID("sess-1", "turn-A"), "one turn's id is stable")
	assert.NotEqual(t, canceledEntryID("sess-1", ""), canceledEntryID("sess-1", ""),
		"a cancel with no turn id still gets its own id")
	assert.Contains(t, a, "sess-1", "the id still names the session")
}
