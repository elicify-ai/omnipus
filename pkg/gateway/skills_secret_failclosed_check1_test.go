package gateway

// CHECK1 CK1, fail-closed leg (skills spec r15 §7.4): when the gateway has no
// credential store, the secret check cannot run and every skill change is
// refused SECRET_CHECK_UNAVAILABLE instead of committing unchecked.
//
// Failability: before commit 755f9cfa3 wireSkillSecretCheck did not exist and
// the provider stayed nil, which skills treats as "no store, commit"; the
// package fails to build there and, with a stub of the old behaviour, the
// change commits and require.Error fails.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecretCheckWiring_MissingCredentialStoreFailsClosed(t *testing.T) {
	prev := skills.SecretValuesProvider
	t.Cleanup(func() { skills.SecretValuesProvider = prev })
	skills.SecretValuesProvider = nil

	wireSkillSecretCheck(nil)
	require.NotNil(t, skills.SecretValuesProvider, "a missing store still installs a (refusing) check")

	root := filepath.Join(t.TempDir(), "skills")
	require.NoError(t, os.MkdirAll(root, 0o755))
	_, err := ck1Create(root, "ck1-unchecked", "a perfectly clean body")
	require.Error(t, err, "no credential store: the change must not commit unchecked")
	assert.True(t, errors.Is(err, skills.ErrSecretCheckUnavailable), "refusal is SECRET_CHECK_UNAVAILABLE, got: %v", err)
	_, statErr := os.Stat(filepath.Join(root, "ck1-unchecked"))
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "nothing was written")
}
