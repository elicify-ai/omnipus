package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

// Skills & Tools C-3, S-28b: with the live trust setting wired in, install_skill
// refuses an unverified skill under Deny with the screen's and REST's text,
// stores nothing and raises no approval card; under Allow it installs.
func TestInstallSkill_TrustDenyRefusesUnverified(t *testing.T) {
	newTool := func(t *testing.T, trust config.SkillTrustLevel) (*InstallSkillTool, string) {
		t.Helper()
		dir := t.TempDir()
		mgr := skills.NewRegistryManager()
		mgr.AddRegistry(fakeSkillRegistry{}) // installs an unverified skill
		tool := NewInstallSkillTool(mgr, dir)
		tool.SetSkillTrust(func() config.SkillTrustLevel { return trust })
		return tool, dir
	}
	args := map[string]any{"slug": "notes-helper", "registry": "fake"}

	t.Run("Deny refuses the call with the C-3 text and stores nothing", func(t *testing.T) {
		tool, dir := newTool(t, config.SkillTrustDeny)
		res := tool.Execute(context.Background(), args)
		require.True(t, res.IsError, "got %+v", res)
		// The result is a JSON envelope; encoding/json writes "&" as \u0026,
		// so the sentence is read from the decoded reason.
		var envelope struct {
			Reason string `json:"reason"`
		}
		require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &envelope), res.ForLLM)
		require.Equal(t, skills.InstallTrustDenyText, envelope.Reason)
		_, err := os.Stat(filepath.Join(dir, "notes-helper"))
		require.True(t, errors.Is(err, os.ErrNotExist), "no folder for the refused skill")
		staged, err := os.ReadDir(filepath.Join(dir, stagingDirName))
		require.NoError(t, err)
		require.Empty(t, staged, "the staged copy is removed")
	})

	t.Run("Deny refuses before an approval card", func(t *testing.T) {
		tool, _ := newTool(t, config.SkillTrustDeny)
		facts, pin, refusal := tool.PrepareForApproval(context.Background(), args)
		require.Nil(t, facts)
		require.Nil(t, pin)
		require.NotNil(t, refusal)
		require.Contains(t, refusal.ForLLM, skills.InstallTrustDenyText)
	})

	t.Run("control: Allow installs the unverified skill", func(t *testing.T) {
		tool, dir := newTool(t, config.SkillTrustAllow)
		res := tool.Execute(context.Background(), args)
		require.False(t, res.IsError, "got %s", res.ForLLM)
		_, err := os.Stat(filepath.Join(dir, "notes-helper", "SKILL.md"))
		require.NoError(t, err)
	})
}
