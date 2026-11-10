package credentials

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCommitCard_DeletedReferenceNameIsNeverReissued is Train 2 gate r1 M1
// (CR-T2-01, architect F01, security T2-S2): removing a version must never
// let a later card reuse its reference name, otherwise a consumer that still
// names the deleted reference silently resolves the NEW value without an
// approved selection. Covers deleting the highest version and the only one;
// removing an older version is the ordinary control.
func TestCommitCard_DeletedReferenceNameIsNeverReissued(t *testing.T) {
	recipient := CardRecipient{Kind: "provider", Provider: "openai", Field: "api_key"}
	commit := func(t *testing.T, s *Store, requestID string) string {
		receipt, err := s.CommitCard(requestID, "s1", []CardValue{{BaseName: "provider_openai_abc", Recipient: recipient, Value: "value-" + requestID}}, time.Unix(1, 0))
		require.NoError(t, err)
		return receipt.References[0]
	}

	t.Run("highest version deleted", func(t *testing.T) {
		s, _ := aadTestStore(t, 0x61)
		v1 := commit(t, s, "r1")
		v2 := commit(t, s, "r2")
		require.Equal(t, "provider_openai_abc.v2", v2)
		_, err := s.RemoveReference(v2)
		require.NoError(t, err)
		v3 := commit(t, s, "r3")
		require.NotEqual(t, v2, v3, "a deleted reference name must never be issued again")
		require.Equal(t, "provider_openai_abc.v3", v3)
		_ = v1
	})

	t.Run("only version deleted", func(t *testing.T) {
		s, _ := aadTestStore(t, 0x62)
		v1 := commit(t, s, "r1")
		_, err := s.RemoveReference(v1)
		require.NoError(t, err)
		next := commit(t, s, "r2")
		require.NotEqual(t, v1, next, "the sole deleted version's name must not come back")
	})

	t.Run("older version deleted (control)", func(t *testing.T) {
		s, _ := aadTestStore(t, 0x63)
		v1 := commit(t, s, "r1")
		commit(t, s, "r2")
		_, err := s.RemoveReference(v1)
		require.NoError(t, err)
		require.Equal(t, "provider_openai_abc.v3", commit(t, s, "r3"))
	})
}
