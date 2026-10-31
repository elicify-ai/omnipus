package skills

// RED tests — landing/skills (T9): the §7.4 save-check secret scan, pipeline
// legs. S-4.2 (a stored 4-byte value in a would-be file is refused
// SECRET_IN_SKILL naming the secret, the value itself never echoed), S-4.3
// (a stored 3-byte value is exempt — committed), S-4.4 (an OAuth sub-value
// is refused naming <NAME>.access_token), S-4.5 (locked/unreadable/damaged
// store → SECRET_CHECK_UNAVAILABLE with the reason and the readable names,
// fails closed), S-4.6 (no store → committed), S-4.7 (a restore whose
// version contains a now-stored value is refused).
// Traces to: skills-spec-r13.md §7.4, S-4.2–S-4.7, §17.1 rows 12/13.
//
// RED expectation: ApplyChange is a scaffold and SecretValuesProvider is
// nil — every RED assertion below fails on the baseline.

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withSecrets installs a SecretValuesProvider seam for the test and restores
// it afterwards.
func withSecrets(t *testing.T, fn func() ([]credentials.NamedValue, error)) {
	t.Helper()
	prev := SecretValuesProvider
	SecretValuesProvider = fn
	t.Cleanup(func() { SecretValuesProvider = prev })
}

// namedSecrets is a fixed provider result.
func namedSecrets(pairs ...[2]string) func() ([]credentials.NamedValue, error) {
	return func() ([]credentials.NamedValue, error) {
		out := make([]credentials.NamedValue, 0, len(pairs))
		for _, p := range pairs {
			out = append(out, credentials.NamedValue{Name: p[0], Value: p[1]})
		}
		return out, nil
	}
}

// TestSecretCheck_ValueInContent_RefusedNamedNotEchoed (S-4.2).
// BDD: a write whose would-be content contains a stored 4-byte value is
// refused SECRET_IN_SKILL naming the secret; the value itself appears
// nowhere in the refusal.
// Traces to: skills-spec-r13.md §7.4, S-4.2.
func TestSecretCheck_ValueInContent_RefusedNamedNotEchoed(t *testing.T) {
	root := t.TempDir()
	appDir := writeSkillFixture(t, root, "foo")
	withSecrets(t, namedSecrets([2]string{"API_KEY", "hunter2-secret-value"}))

	_, err := ApplyChange(root, ChangeRequest{
		Kind: KindEdit, Slug: "foo", Path: "SKILL.md",
		Content: []byte(validSkillMD("foo", "leaky", "call with API_KEY=hunter2-secret-value")),
		Expect:  Expectation{Revision: readLiveRevision(t, appDir)},
		Actor:   ChangeActor{Kind: "agent", AgentID: "a", Tool: "write_file"},
	})
	require.Error(t, err, "RED: the save check's secret scan is not implemented")
	assert.True(t, errors.Is(err, ErrSecretInSkill),
		"the refusal is SECRET_IN_SKILL, got: %v", err)
	assert.Contains(t, err.Error(), "API_KEY", "the refusal names the secret")
	assert.NotContains(t, err.Error(), "hunter2-secret-value",
		"the value itself is never echoed into the refusal")
	assert.FileExists(t, filepath.Join(appDir, "SKILL.md"), "nothing written")
}

// TestSecretCheck_ThreeByteValue_Exempt_Committed (S-4.3).
// BDD: a stored 3-byte value in the content does not refuse — the check's
// threshold is 4 bytes; the write commits.
// Traces to: skills-spec-r13.md §7.4, S-4.3.
func TestSecretCheck_ThreeByteValue_Exempt_Committed(t *testing.T) {
	root := t.TempDir()
	appDir := writeSkillFixture(t, root, "foo")
	withSecrets(t, namedSecrets([2]string{"SHORT", "abc"}))

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindEdit, Slug: "foo", Path: "SKILL.md",
		Content: []byte(validSkillMD("foo", "short", "uses abc today")),
		Expect:  Expectation{Revision: readLiveRevision(t, appDir)},
		Actor:   ChangeActor{Kind: "agent", AgentID: "a", Tool: "write_file"},
	})
	require.NoError(t, err, "a 3-byte stored value is exempt (RED: scaffold refuses everything)")
	assert.Equal(t, PersistenceComplete, out.PersistenceStatus, "the write committed")
}

// TestSecretCheck_OAuthSubValue_RefusedNamed (S-4.4).
// BDD: content containing an OAuth access_token sub-value is refused naming
// <NAME>.access_token.
// Traces to: skills-spec-r13.md §7.4, S-4.4.
func TestSecretCheck_OAuthSubValue_RefusedNamed(t *testing.T) {
	root := t.TempDir()
	appDir := writeSkillFixture(t, root, "foo")
	withSecrets(t, namedSecrets([2]string{"exa_oauth.access_token", "at-1234-value"}))

	_, err := ApplyChange(root, ChangeRequest{
		Kind: KindEdit, Slug: "foo", Path: "SKILL.md",
		Content: []byte(validSkillMD("foo", "oauth leak", "token: at-1234-value")),
		Expect:  Expectation{Revision: readLiveRevision(t, appDir)},
		Actor:   ChangeActor{Kind: "agent", AgentID: "a", Tool: "write_file"},
	})
	require.Error(t, err, "an OAuth sub-value is a secret")
	assert.True(t, errors.Is(err, ErrSecretInSkill))
	assert.Contains(t, err.Error(), "exa_oauth.access_token",
		"the refusal names the OAuth sub-value, not the provider blob")
}

// TestSecretCheck_StoreUnavailable_FailsClosed (S-4.5).
// BDD: a locked/unreadable/damaged store makes EVERY write refuse
// SECRET_CHECK_UNAVAILABLE with the reason and the readable names — nothing
// commits while the check cannot run.
// Traces to: skills-spec-r13.md §7.4, S-4.5.
func TestSecretCheck_StoreUnavailable_FailsClosed(t *testing.T) {
	root := t.TempDir()
	appDir := writeSkillFixture(t, root, "foo")

	t.Run("provider error refuses the write", func(t *testing.T) {
		withSecrets(t, func() ([]credentials.NamedValue, error) {
			return nil, errors.New("store locked")
		})
		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "foo", Path: "SKILL.md",
			Content: []byte(validSkillMD("foo", "v", "clean body")),
			Expect:  Expectation{Revision: readLiveRevision(t, appDir)},
			Actor:   ChangeActor{Kind: "agent", AgentID: "a", Tool: "write_file"},
		})
		require.Error(t, err, "the check fails closed")
		assert.True(t, errors.Is(err, ErrSecretCheckUnavailable),
			"the refusal is SECRET_CHECK_UNAVAILABLE, got: %v", err)
		assert.Contains(t, err.Error(), "store locked", "the refusal carries the reason")
		assert.FileExists(t, filepath.Join(appDir, "SKILL.md"), "nothing written")
	})

	t.Run("readable names still surface alongside the failure", func(t *testing.T) {
		withSecrets(t, func() ([]credentials.NamedValue, error) {
			return []credentials.NamedValue{
				{Name: "GOOD_REF", Value: "readable-value-1"},
			}, errors.New("one entry damaged")
		})
		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "foo", Path: "SKILL.md",
			Content: []byte(validSkillMD("foo", "v", "clean body")),
			Expect:  Expectation{Revision: readLiveRevision(t, appDir)},
			Actor:   ChangeActor{Kind: "agent", AgentID: "a", Tool: "write_file"},
		})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrSecretCheckUnavailable))
		assert.Contains(t, err.Error(), "GOOD_REF", "the readable names are listed")
	})
}

// TestSecretCheck_NoStore_Committed (S-4.6).
// BDD: with no store (nil provider or empty enumeration) the write commits.
// Traces to: skills-spec-r13.md §7.4, S-4.6.
func TestSecretCheck_NoStore_Committed(t *testing.T) {
	root := t.TempDir()
	appDir := writeSkillFixture(t, root, "foo")
	withSecrets(t, namedSecrets()) // empty enumeration: no store file

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindEdit, Slug: "foo", Path: "SKILL.md",
		Content: []byte(validSkillMD("foo", "no store", "clean body")),
		Expect:  Expectation{Revision: readLiveRevision(t, appDir)},
		Actor:   ChangeActor{Kind: "agent", AgentID: "a", Tool: "write_file"},
	})
	require.NoError(t, err, "an empty store is an allowed check (RED: scaffold refuses)")
	assert.Equal(t, PersistenceComplete, out.PersistenceStatus)
}

// TestSecretCheck_RestoreRefused_WhenVersionGainsAStoredValue (S-4.7).
// BDD: a version captured BEFORE a value was stored is refused on restore —
// the restore's content would reintroduce the secret; the live state is
// unchanged.
// Traces to: skills-spec-r13.md §7.4, S-4.7.
func TestSecretCheck_RestoreRefused_WhenVersionGainsAStoredValue(t *testing.T) {
	root := t.TempDir()
	appDir := writeSkillFixture(t, root, "foo")
	withSecrets(t, namedSecrets()) // empty at capture time

	// V1 contains a string that LATER becomes a stored secret.
	_, v1 := commitWrite(t, root, "foo", "SKILL.md",
		validSkillMD("foo", "v1", "body with hunter2-secret-value inside"),
		readLiveRevision(t, appDir))

	// The value is now stored.
	withSecrets(t, namedSecrets([2]string{"API_KEY", "hunter2-secret-value"}))

	after := readLiveRevision(t, appDir)
	_, err := ApplyChange(root, ChangeRequest{
		Kind: KindRestore, Slug: "foo", Version: v1,
		Expect: Expectation{Revision: after},
		Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
	})
	require.Error(t, err, "RED: the restore path must run the same secret scan")
	assert.True(t, errors.Is(err, ErrSecretInSkill),
		"the restore refusal is SECRET_IN_SKILL, got: %v", err)
	assert.Equal(t, after, readLiveRevision(t, appDir), "the live state is unchanged")
}
