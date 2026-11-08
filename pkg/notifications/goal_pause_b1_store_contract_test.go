package notifications

// SCRATCH DRAFT ONLY. Intended primary: pkg/notifications/goal_pause_b1_store_contract_test.go.
// Existing generic store control; NOT proof boot installs the B1 producer.
import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoalPauseB1StoreContract(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notifications")
	store := NewStore(dir)
	input := Notification{Recipient: "b1-owner", Type: "goal_paused", Title: "Goal paused in fixture chat", Body: "blocked", Severity: "warning", CoalesceKey: "goal_paused:b1-goal", SessionID: "b1-session", AgentID: "b1-agent"}
	first, err := store.Create(input)
	require.NoError(t, err)
	require.NotEmpty(t, first.ID)
	repeated, err := store.Create(input)
	require.NoError(t, err)
	assert.Equal(t, first.ID, repeated.ID)
	rows, err := NewStore(dir).ListForUser(input.Recipient)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, input.CoalesceKey, rows[0].CoalesceKey)
	assert.Equal(t, input.SessionID, rows[0].SessionID)
	assert.Equal(t, "goal_paused", rows[0].Type)
	unread, err := store.UnreadCount(input.Recipient)
	require.NoError(t, err)
	assert.Equal(t, 1, unread)
	other := input
	other.Recipient = "b1-other"
	otherStored, err := store.Create(other)
	require.NoError(t, err)
	assert.NotEqual(t, first.ID, otherStored.ID)
	require.NoError(t, store.MarkRead(input.Recipient, first.ID))
	later, err := store.Create(input)
	require.NoError(t, err)
	assert.NotEqual(t, first.ID, later.ID, "a later pause after read may create a new item")
	rows, err = NewStore(dir).ListForUser(input.Recipient)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	byID := map[string]Notification{}
	for _, n := range rows {
		byID[n.ID] = n
	}
	assert.True(t, byID[first.ID].Read)
	assert.False(t, byID[later.ID].Read)
	otherRows, err := store.ListForUser(other.Recipient)
	require.NoError(t, err)
	require.Len(t, otherRows, 1)
	assert.False(t, otherRows[0].Read)
}
