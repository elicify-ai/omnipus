package gateway

// CHECK1 CK1 (skills spec r15 §7.4): the gateway's real start-up wires the
// skills save check to the credential store, so a skill change containing a
// stored secret value is refused SECRET_IN_SKILL. Real credentials/config
// start-up (rc.loadConfigAndProvider); nothing here assigns
// skills.SecretValuesProvider.
//
// Failability: before commit 755f9cfa3 nothing in production assigned
// skills.SecretValuesProvider, so the check never ran and the change below
// committed (the test fails at require.Error). Fail-closed leg: see
// skills_secret_failclosed_check1_test.go.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const ck1SecretName = "SKILL_LEAK_TOKEN"
const ck1SecretValue = "ck1-stored-secret-value-9f3a"

func ck1Create(root, slug, body string) (skills.PublishOutcome, error) {
	return skills.ApplyChange(root, skills.ChangeRequest{
		Kind: skills.KindCreate, Slug: slug,
		Content: []byte("---\nname: " + slug + "\ndescription: ck1 fixture\n---\n\n" + body + "\n"),
		Expect:  skills.Expectation{Absent: true},
		Actor:   skills.ChangeActor{Kind: "person", Tool: "rest-create"},
	})
}

func TestGatewayStartup_WiresSecretCheckToCredentialStore(t *testing.T) {
	home, _ := bootFixture(t) // sets OMNIPUS_HOME and the synthetic master key
	prev := skills.SecretValuesProvider
	t.Cleanup(func() { skills.SecretValuesProvider = prev })
	skills.SecretValuesProvider = nil // start from "never wired": only the boot may set it

	seed := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, seed.UnlockWithKey(make([]byte, 32))) // the synthetic all-zero key bootFixture exports
	require.NoError(t, seed.Set(ck1SecretName, ck1SecretValue))
	seed.Close()

	rc := &runContextWithOptions{ctx: context.Background(), homePath: home, configPath: filepath.Join(home, "config.json"), allowEmptyStartup: true}
	err, stop := rc.loadConfigAndProvider()
	if rc.credStore != nil {
		t.Cleanup(rc.credStore.Close)
	}
	require.NoError(t, err, "real credentials/config start-up")
	require.False(t, stop)
	require.NotNil(t, skills.SecretValuesProvider, "start-up must wire the secret check")

	root := filepath.Join(home, "skills")
	_, err = ck1Create(root, "ck1-leaky", "call with token "+ck1SecretValue)
	require.Error(t, err, "a change containing a stored secret value must be refused")
	assert.True(t, errors.Is(err, skills.ErrSecretInSkill), "refusal is SECRET_IN_SKILL, got: %v", err)
	assert.Contains(t, err.Error(), ck1SecretName, "the refusal names the secret")
	assert.NotContains(t, err.Error(), ck1SecretValue, "the value is never echoed")
	_, statErr := os.Stat(filepath.Join(root, "ck1-leaky"))
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "nothing was written")

	// Positive control: the same wiring lets a clean change through.
	_, err = ck1Create(root, "ck1-clean", "no secrets here")
	require.NoError(t, err, "a change without a stored value commits")
}
