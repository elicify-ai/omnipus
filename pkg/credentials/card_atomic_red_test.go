package credentials

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// FR-063/087 and BDD-6.9/6.24: values, bindings and receipt are one
// credentials.json publication. Only the filesystem edge is faulted.
// No production changes or test-time changes to the transaction are allowed.
func newCardAtomicStore(t *testing.T) (*Store, []byte) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	key := bytes.Repeat([]byte{0x5a}, 32)
	store := NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, store.UnlockWithKey(key))
	t.Cleanup(store.Close)
	return store, key
}

func cardAtomicValues() []CardValue {
	// Synthetic test values, never credentials from a real account.
	return []CardValue{
		{BaseName: "slack_slack-1_bot_token", Recipient: CardRecipient{Kind: "channel_credential", ChannelType: "slack", Instance: "slack-1", Field: "bot_token"}, Value: "FAKE-ATOMIC-BOT"},
		{BaseName: "slack_slack-1_app_token", Recipient: CardRecipient{Kind: "channel_credential", ChannelType: "slack", Instance: "slack-1", Field: "app_token"}, Value: "FAKE-ATOMIC-APP"},
	}
}

func requireCardPublication(t *testing.T, data []byte, key []byte, values []CardValue, id, sid string) {
	t.Helper()
	var sf storeFile
	require.NoError(t, json.Unmarshal(data, &sf))
	require.Len(t, sf.Credentials, len(values), "every published image must contain all values, never a prefix")
	require.Len(t, sf.CardSecrets, len(values))
	require.Len(t, sf.CardReceipts, 1, "values must never be published before their receipt")
	receipt, exists := sf.CardReceipts[id]
	require.True(t, exists, "same publication must carry the request's commit fact")
	require.Equal(t, id, receipt.RequestID)
	require.Equal(t, sid, receipt.SessionID)
	refs := make([]string, 0, len(values))
	for _, value := range values {
		ref := value.BaseName + ".v1"
		refs = append(refs, ref)
		require.NotContains(t, string(data), value.Value, "published bytes must remain encrypted")
		entry, exists := sf.Credentials[ref]
		require.True(t, exists)
		plain, err := decrypt(key, ref, entry)
		require.NoError(t, err)
		require.Equal(t, value.Value, plain)
		require.Equal(t, CardSecret{Recipient: value.Recipient, Version: 1}, sf.CardSecrets[ref])
	}
	require.Equal(t, refs, receipt.References)
}

func TestCardAtomic_ValuesAndReceiptPublishOnceTogether(t *testing.T) {
	store, key := newCardAtomicStore(t)
	values := cardAtomicValues()
	original := writeFileAtomicFn
	t.Cleanup(func() { writeFileAtomicFn = original })
	writes := 0
	writeFileAtomicFn = func(path string, data []byte, mode os.FileMode) error {
		writes++
		require.Equal(t, store.Path(), path)
		require.Equal(t, os.FileMode(0o600), mode)
		requireCardPublication(t, data, key, values, "request-atomic", "session-atomic")
		return original(path, data, mode)
	}
	at := time.Date(2026, 10, 16, 12, 30, 0, 0, time.UTC)
	receipt, err := store.CommitCard("request-atomic", "session-atomic", values, at)
	require.NoError(t, err)
	require.Equal(t, 1, writes, "FR-087: one atomic store publication, not values then receipt")
	require.Equal(t, CardReceipt{RequestID: "request-atomic", SessionID: "session-atomic", References: []string{"slack_slack-1_bot_token.v1", "slack_slack-1_app_token.v1"}, SubmittedAt: at}, receipt)
	before, err := os.ReadFile(store.Path())
	require.NoError(t, err)
	values[0].Value = "FAKE-REPLAY-CANNOT-REPLACE"
	replay, err := store.CommitCard("request-atomic", "session-atomic", values, at.Add(time.Hour))
	require.ErrorIs(t, err, ErrCardAlreadySubmitted)
	require.Equal(t, receipt, replay)
	require.Equal(t, 1, writes, "replay must issue zero filesystem writes")
	after, err := os.ReadFile(store.Path())
	require.NoError(t, err)
	require.Equal(t, before, after, "duplicate submit must leave ciphertext and receipt unchanged")
}

// Fault every attempted publication before or immediately after the atomic
// publisher. The hook checks every intermediate proposed image: a regression
// that writes values then receipt is caught even if the first write fails.
func TestCardAtomic_CrashCutsNeverHalfApplyValuesOrReceipt(t *testing.T) {
	for _, cut := range []string{"before_publication", "after_publication"} {
		t.Run(cut, func(t *testing.T) {
			store, key := newCardAtomicStore(t)
			values := cardAtomicValues()
			original := writeFileAtomicFn
			t.Cleanup(func() { writeFileAtomicFn = original })
			fault := errors.New("synthetic credentials publication interruption")
			writes := 0
			writeFileAtomicFn = func(path string, data []byte, mode os.FileMode) error {
				writes++
				requireCardPublication(t, data, key, values, "request-cut", "session-cut")
				if cut == "after_publication" {
					if err := original(path, data, mode); err != nil {
						return err
					}
				}
				return fault
			}
			_, err := store.CommitCard("request-cut", "session-cut", values, time.Now())
			require.ErrorIs(t, err, fault)
			require.Equal(t, 1, writes)
			// A reopened store is an independent disk observer, not an in-memory
			// success that a failed write might have left behind.
			reopened := NewStore(store.Path())
			require.NoError(t, reopened.UnlockWithKey(key))
			t.Cleanup(reopened.Close)
			names, err := reopened.List()
			require.NoError(t, err)
			receipt, exists, err := reopened.CardReceipt("request-cut")
			require.NoError(t, err)
			if cut == "before_publication" {
				require.Empty(t, names)
				require.False(t, exists, "neither receipt nor values may be half-applied")
				metadata, err := reopened.CardSecretMetadata()
				require.NoError(t, err)
				require.Empty(t, metadata)
			} else {
				require.True(t, exists, "committed receipt is durable even if caller did not observe success")
				require.ElementsMatch(t, receipt.References, names)
				require.Len(t, receipt.References, 2)
				for i, ref := range receipt.References {
					got, err := reopened.Get(ref)
					require.NoError(t, err)
					require.Equal(t, values[i].Value, got)
				}
			}
		})
	}
}

// FR-071/D2: exercise 0/1/2 and 7/8/9 fields at the delivered atomic store
// boundary; 4a has no caller-supplied arbitrary field list on Create.
func TestCardAtomic_FieldCountAndInvalidSecondValueAreAllOrNone(t *testing.T) {
	for _, count := range []int{0, 1, 2, 7, 8, 9} {
		t.Run(fmt.Sprintf("fields_%d", count), func(t *testing.T) {
			store, _ := newCardAtomicStore(t)
			values := make([]CardValue, count)
			for i := range count {
				values[i] = CardValue{BaseName: fmt.Sprintf("field_%d", i), Recipient: CardRecipient{Kind: "mcp_env", Server: "test", Field: fmt.Sprintf("VAR_%d", i)}, Value: fmt.Sprintf("FAKE-FIELD-%d", i)}
			}
			receipt, err := store.CommitCard("request-count", "session-count", values, time.Now())
			names, listErr := store.List()
			require.NoError(t, listErr)
			if count < 1 || count > 8 {
				require.EqualError(t, err, "invalid secret submission")
				require.Empty(t, names)
			} else {
				require.NoError(t, err)
				sort.Strings(receipt.References)
				require.Equal(t, receipt.References, names)
			}
		})
	}
	t.Run("second_value_invalid", func(t *testing.T) {
		store, _ := newCardAtomicStore(t)
		values := cardAtomicValues()
		values[1].Value = "abc" // min-1, must not leave the valid first field behind
		_, err := store.CommitCard("request-invalid", "session-invalid", values, time.Now())
		require.EqualError(t, err, "invalid secret value")
		names, err := store.List()
		require.NoError(t, err)
		require.Empty(t, names)
		_, exists, err := store.CardReceipt("request-invalid")
		require.NoError(t, err)
		require.False(t, exists)
	})
}
