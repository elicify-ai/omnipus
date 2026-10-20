package credentials

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSelectPrivateVersion_UnreadableVersionKeepsCurrentSelection is Train 2
// gate r1 SFH T2-SF-05: a rollback to a version whose stored value cannot be
// decrypted must be refused with a named entry error and must leave the
// current (readable) selection in place — metadata presence is not proof the
// value is usable. A readable older version is the control.
func TestSelectPrivateVersion_UnreadableVersionKeepsCurrentSelection(t *testing.T) {
	store, path := aadTestStore(t, 0x33)
	recipient := CardRecipient{Kind: KindAgentPrivate, WorkspaceID: "sales", AgentID: "mia", Field: "WEATHER_TOKEN"}
	commit := func(requestID, value string) string {
		receipt, err := store.CommitCard(requestID, "s1", []CardValue{{BaseName: "private_weather_token_abc", Recipient: recipient, Value: value}}, time.Unix(1, 0))
		require.NoError(t, err)
		require.Len(t, receipt.References, 1)
		return receipt.References[0]
	}
	v1 := commit("req-1", "first-readable-value")
	v2 := commit("req-2", "second-readable-value")
	v3 := commit("req-3", "third-readable-value")
	key := privateKey(recipient)
	require.Equal(t, v3, aadTestReadFile(t, path).PrivateSelected[key], "instrument: the newest version is selected on submit")

	// Damage v1 only.
	sf := aadTestReadFile(t, path)
	entry := sf.Credentials[v1]
	raw, err := base64.StdEncoding.DecodeString(entry.Ciphertext)
	require.NoError(t, err)
	raw[0] ^= 0x01
	entry.Ciphertext = base64.StdEncoding.EncodeToString(raw)
	sf.Credentials[v1] = entry
	aadTestWriteFile(t, path, sf)

	_, err = store.SelectPrivateVersion(v1)
	require.Error(t, err, "rolling back to an unreadable version must be refused")
	var authErr *EntryAuthError
	require.True(t, errors.As(err, &authErr), "the refusal names the damaged entry: %v", err)
	require.Equal(t, v3, aadTestReadFile(t, path).PrivateSelected[key], "the current selection must be kept")

	// Control: a readable older version is selected.
	_, err = store.SelectPrivateVersion(v2)
	require.NoError(t, err)
	require.Equal(t, v2, aadTestReadFile(t, path).PrivateSelected[key])
}
