package credentials

// I3/FR048: ordinary writers cannot first-create or overwrite a reserved name.
import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProgramCredential_ReservedNameRegression(t *testing.T) {
	t.Run("F2_first_connect_reserved_name_refused", func(t *testing.T) {
		store, path, _ := newUnlockedGuardStore(t)
		t.Cleanup(store.Close)
		require.NoError(t, store.Set("ordinary-control", "ordinary_value_123"), "instrument: ordinary writer really works")
		before, err := os.ReadFile(path)
		require.NoError(t, err)
		err = store.Set("program.github", "unpublished_value_456")
		require.ErrorIs(t, err, ErrProgramOwned, "I3: program namespace is not an ordinary credential, including before first connect")
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, before, after, "refused first-create changes no encrypted value or metadata")
	})
	t.Run("F2_owned_reserved_name_refused", func(t *testing.T) {
		store, path, _ := newUnlockedGuardStore(t)
		t.Cleanup(store.Close)
		generation, err := store.CommitProgramCredential("program.github", "tok_valid_12345", nil)
		require.NoError(t, err)
		require.Equal(t, int64(1), generation)
		before, err := os.ReadFile(path)
		require.NoError(t, err)
		require.ErrorIs(t, store.Set("program.github", "unapproved_replacement"), ErrProgramOwned)
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, before, after)
		value, err := store.Get("program.github")
		require.NoError(t, err)
		require.True(t, value == "tok_valid_12345", "published token remains unchanged; value not printed")
	})
}
