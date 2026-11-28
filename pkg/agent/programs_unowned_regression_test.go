package agent

// I3/FR048/S17: a decryptable ordinary lookalike is not a connected program.
// Filesystem-edge malformed-state fixture preserves real encryption/read/runtime.
import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProgramCredential_UnownedLookalikeRegression(t *testing.T) {
	t.Run("F2_unowned_persisted_lookalike_never_attaches", func(t *testing.T) {
		f := newProgramsTurnFixture(t)
		require.Equal(t, "16", programsChildCount(t, f, "GH_TOKEN", "f2-owned-control"), "instrument: actual published connection works first")
		path := filepath.Join(f.home, "credentials.json")
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		var doc map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &doc))
		// Keep the actual encrypted entry byte-for-byte, remove all publication,
		// binding and receipt metadata. This is persisted unowned state, not a
		// mocked Get or a fake RuntimeSource that hands the token to Resolve.
		encrypted := append([]byte(nil), doc["credentials"]...)
		delete(doc, "program_entries")
		delete(doc, "card_secrets")
		delete(doc, "card_receipts")
		raw, err = json.Marshal(doc)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, raw, 0600))
		require.JSONEq(t, string(encrypted), string(doc["credentials"]))
		value, err := f.al.sysagentDeps.CredStore.Get("program.github")
		require.NoError(t, err)
		require.True(t, value == "tok_valid_12345", "instrument: ordinary lookalike remains genuinely decryptable; value deliberately not printed")
		_, owned := f.al.sysagentDeps.CredStore.ProgramEntryGeneration("program.github")
		require.False(t, owned, "Given: no publication/generation metadata remains")
		require.Equal(t, "0", programsChildCount(t, f, "GH_TOKEN", "f2-unowned-child"), "I3: actual child must not consume an unowned name, even though ordinary Get can read it")
	})
}
