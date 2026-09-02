package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/skills"
)

// fakeSkillRegistry is a minimal skills.SkillRegistry test double that
// installs a real SKILL.md into targetDir with no network access, so
// TestInstallSkillTool_DiscoverableByDifferentAgent can drive install_skill's
// actual Execute path end-to-end without depending on a live ClawHub/GitHub
// registry.
type fakeSkillRegistry struct{}

func (fakeSkillRegistry) Name() string { return "fake" }

func (fakeSkillRegistry) Search(_ context.Context, _ string, _ int) ([]skills.SearchResult, error) {
	return nil, nil
}

func (fakeSkillRegistry) GetSkillMeta(_ context.Context, slug string) (*skills.SkillMeta, error) {
	return &skills.SkillMeta{Slug: slug, DisplayName: slug, LatestVersion: "1.0.0"}, nil
}

func (fakeSkillRegistry) DownloadAndInstall(
	_ context.Context, slug, _ string, targetDir string,
) (*skills.InstallResult, error) {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, err
	}
	skillMD := "---\nname: " + slug + "\ndescription: a fake test skill for install_skill's global-dir test\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(targetDir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		return nil, err
	}
	return &skills.InstallResult{Version: "1.0.0"}, nil
}

func TestInstallSkillToolName(t *testing.T) {
	tool := NewInstallSkillTool(skills.NewRegistryManager(), t.TempDir())
	assert.Equal(t, "install_skill", tool.Name())
}

func TestInstallSkillToolMissingSlug(t *testing.T) {
	tool := NewInstallSkillTool(skills.NewRegistryManager(), t.TempDir())
	result := tool.Execute(context.Background(), map[string]any{})
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "identifier is required and must be a non-empty string")
}

func TestInstallSkillToolEmptySlug(t *testing.T) {
	tool := NewInstallSkillTool(skills.NewRegistryManager(), t.TempDir())
	result := tool.Execute(context.Background(), map[string]any{
		"slug": "   ",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "identifier is required and must be a non-empty string")
}

func TestInstallSkillToolUnsafeSlug(t *testing.T) {
	tool := NewInstallSkillTool(skills.NewRegistryManager(), t.TempDir())

	cases := []string{
		"../etc/passwd",
		"path/traversal",
		"path\\traversal",
	}

	for _, slug := range cases {
		result := tool.Execute(context.Background(), map[string]any{
			"slug": slug,
		})
		assert.True(t, result.IsError, "slug %q should be rejected", slug)
		assert.Contains(t, result.ForLLM, "invalid slug")
	}
}

func TestInstallSkillToolAlreadyExists(t *testing.T) {
	globalSkillsDir := t.TempDir()
	skillDir := filepath.Join(globalSkillsDir, "existing-skill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))

	tool := NewInstallSkillTool(skills.NewRegistryManager(), globalSkillsDir)
	result := tool.Execute(context.Background(), map[string]any{
		"slug":     "existing-skill",
		"registry": "clawhub",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "already installed")
}

func TestInstallSkillToolRegistryNotFound(t *testing.T) {
	globalSkillsDir := t.TempDir()
	tool := NewInstallSkillTool(skills.NewRegistryManager(), globalSkillsDir)
	result := tool.Execute(context.Background(), map[string]any{
		"slug":     "some-skill",
		"registry": "nonexistent",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "registry")
	assert.Contains(t, result.ForLLM, "not found")
}

func TestInstallSkillToolParameters(t *testing.T) {
	tool := NewInstallSkillTool(skills.NewRegistryManager(), t.TempDir())
	params := tool.Parameters()

	props, ok := params["properties"].(map[string]any)
	assert.True(t, ok)
	assert.Contains(t, props, "slug")
	assert.Contains(t, props, "version")
	assert.Contains(t, props, "registry")
	assert.Contains(t, props, "force")
	// Defect 2 fix: the ambiguous-slug error tells callers to retry with
	// ownerHandle — the schema must actually expose that parameter or the
	// error's own advice is unfollowable.
	assert.Contains(t, props, "ownerHandle")

	required, ok := params["required"].([]string)
	assert.True(t, ok)
	assert.Contains(t, required, "slug")
	assert.Contains(t, required, "registry")
}

func TestInstallSkillToolUnsafeOwnerHandle(t *testing.T) {
	tool := NewInstallSkillTool(skills.NewRegistryManager(), t.TempDir())
	result := tool.Execute(context.Background(), map[string]any{
		"slug":        "docker-compose",
		"registry":    "clawhub",
		"ownerHandle": "../etc",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "invalid ownerHandle")
}

// fakeUnscopedRegistry implements skills.SkillRegistry but NOT
// skills.OwnerScopedRegistry, to verify the tool surfaces a clear error
// instead of silently ignoring ownerHandle for a registry that can't use it.
type fakeUnscopedRegistry struct{}

func (f *fakeUnscopedRegistry) Name() string { return "fake-unscoped" }
func (f *fakeUnscopedRegistry) Search(context.Context, string, int) ([]skills.SearchResult, error) {
	return nil, nil
}

func (f *fakeUnscopedRegistry) GetSkillMeta(context.Context, string) (*skills.SkillMeta, error) {
	return &skills.SkillMeta{}, nil
}

func (f *fakeUnscopedRegistry) DownloadAndInstall(
	context.Context, string, string, string,
) (*skills.InstallResult, error) {
	return &skills.InstallResult{Version: "1.0.0"}, nil
}

func TestInstallSkillToolOwnerHandleUnsupportedByRegistry(t *testing.T) {
	mgr := skills.NewRegistryManager()
	mgr.AddRegistry(&fakeUnscopedRegistry{})

	tool := NewInstallSkillTool(mgr, t.TempDir())
	result := tool.Execute(context.Background(), map[string]any{
		"slug":        "docker-compose",
		"registry":    "fake-unscoped",
		"ownerHandle": "acme",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "does not support ownerHandle-scoped installs")
}

// fakeOwnerScopedRegistry implements both skills.SkillRegistry and
// skills.OwnerScopedRegistry to verify the tool actually dispatches through
// the owner-scoped path (rather than, say, silently calling the unscoped
// method and dropping ownerHandle on the floor).
type fakeOwnerScopedRegistry struct {
	fakeUnscopedRegistry
	gotSlug, gotOwnerHandle, gotVersion string
}

func (f *fakeOwnerScopedRegistry) Name() string { return "fake-owner-scoped" }

func (f *fakeOwnerScopedRegistry) DownloadAndInstallForOwner(
	_ context.Context, slug, ownerHandle, version, _ string,
) (*skills.InstallResult, error) {
	f.gotSlug = slug
	f.gotOwnerHandle = ownerHandle
	f.gotVersion = version
	return &skills.InstallResult{Version: "2.0.0"}, nil
}

func TestInstallSkillToolOwnerHandleDispatchesToScopedInstall(t *testing.T) {
	fake := &fakeOwnerScopedRegistry{}
	mgr := skills.NewRegistryManager()
	mgr.AddRegistry(fake)

	tool := NewInstallSkillTool(mgr, t.TempDir())
	result := tool.Execute(context.Background(), map[string]any{
		"slug":        "docker-compose",
		"registry":    "fake-owner-scoped",
		"ownerHandle": "acme",
	})
	require.False(t, result.IsError, "unexpected error: %s", result.ForLLM)
	assert.Equal(t, "docker-compose", fake.gotSlug)
	assert.Equal(t, "acme", fake.gotOwnerHandle)
	assert.Contains(t, result.ForLLM, "v2.0.0")
}

func TestInstallSkillToolMissingRegistry(t *testing.T) {
	tool := NewInstallSkillTool(skills.NewRegistryManager(), t.TempDir())
	result := tool.Execute(context.Background(), map[string]any{
		"slug": "some-skill",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "invalid registry")
}

// TestInstallSkillTool_DiscoverableByDifferentAgent proves the ADR-046
// FR-009 defect fix: install_skill must target the fixed GLOBAL skills
// directory ($OMNIPUS_HOME/skills) regardless of which agent's workspace
// installed it, so a skill installed via one agent is discoverable by a
// DIFFERENT agent's own SkillsLoader — via the global bucket
// skills.SkillRoots()/ListSkills() already searches (pkg/skills/loader.go),
// which requires no change on the discovery side.
func TestInstallSkillTool_DiscoverableByDifferentAgent(t *testing.T) {
	globalSkills := t.TempDir()

	registryMgr := skills.NewRegistryManager()
	registryMgr.AddRegistry(fakeSkillRegistry{})

	tool := NewInstallSkillTool(registryMgr, globalSkills)
	result := tool.Execute(context.Background(), map[string]any{
		"slug":     "shared-fake-skill",
		"registry": "fake",
	})
	require.False(t, result.IsError, "install must succeed, got: %s", result.ForLLM)

	// Confirm the skill actually landed under the GLOBAL directory, not some
	// agent-specific workspace path.
	_, statErr := os.Stat(filepath.Join(globalSkills, "shared-fake-skill", "SKILL.md"))
	require.NoError(t, statErr, "installed skill must be on disk under the global skills dir")

	// Two DIFFERENT agents — distinct workspace and builtin roots each — but
	// the SAME global skills dir, exactly as every agent's own
	// NewContextBuilder wires it (pkg/agent/context.go's globalSkillsDir()).
	loaderA := skills.NewSkillsLoader(t.TempDir(), globalSkills, t.TempDir())
	loaderB := skills.NewSkillsLoader(t.TempDir(), globalSkills, t.TempDir())

	for name, loader := range map[string]*skills.SkillsLoader{"agent-a": loaderA, "agent-b": loaderB} {
		found := false
		for _, info := range loader.ListSkills() {
			if info.ID == "shared-fake-skill" {
				found = true
				break
			}
		}
		assert.True(t, found, "%s's SkillsLoader must discover the globally-installed skill", name)
	}
}

// blockingSkillRegistry is a skills.SkillRegistry test double whose
// DownloadAndInstall extracts a real SKILL.md into targetDir and then blocks
// until the test signals it to proceed — used to hold install_skill open in
// the window between staging extraction and the final os.Rename, so a
// concurrent ListSkills call can observe the live global skills directory
// mid-install.
type blockingSkillRegistry struct {
	proceed chan struct{}
	staged  chan string // reports the stageDir once SKILL.md has been written
}

func (blockingSkillRegistry) Name() string { return "blocking" }

func (blockingSkillRegistry) Search(_ context.Context, _ string, _ int) ([]skills.SearchResult, error) {
	return nil, nil
}

func (blockingSkillRegistry) GetSkillMeta(_ context.Context, slug string) (*skills.SkillMeta, error) {
	return &skills.SkillMeta{Slug: slug, DisplayName: slug, LatestVersion: "1.0.0"}, nil
}

func (r blockingSkillRegistry) DownloadAndInstall(
	_ context.Context, slug, _ string, targetDir string,
) (*skills.InstallResult, error) {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, err
	}
	skillMD := "---\nname: " + slug + "\ndescription: mid-install staging probe\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(targetDir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		return nil, err
	}
	r.staged <- targetDir
	<-r.proceed
	return &skills.InstallResult{Version: "1.0.0"}, nil
}

// TestInstallSkillTool_StagingDirectoryNeverVisibleToListSkills is the FIX 1
// (HIGH) regression test: the staging directory install_skill extracts a
// download into must never be visible to pkg/skills.SkillsLoader.ListSkills,
// even WHILE the install is in flight — a concurrent list_skills call or a
// system-prompt build must never see a phantom skill with an ID shaped like
// the staging directory's name. This drives a real Execute() call in a
// goroutine, blocks it mid-download after the staged SKILL.md is written but
// before the final os.Rename, and asserts ListSkills sees only the
// already-installed control skill — never the in-flight staging entry.
func TestInstallSkillTool_StagingDirectoryNeverVisibleToListSkills(t *testing.T) {
	globalSkills := t.TempDir()

	// A control skill that is already fully installed, to prove ListSkills
	// keeps working normally throughout.
	require.NoError(t, os.MkdirAll(filepath.Join(globalSkills, "already-installed"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(globalSkills, "already-installed", "SKILL.md"),
		[]byte("---\nname: already-installed\ndescription: control skill\n---\n\nBody.\n"),
		0o644,
	))

	reg := blockingSkillRegistry{proceed: make(chan struct{}), staged: make(chan string, 1)}
	registryMgr := skills.NewRegistryManager()
	registryMgr.AddRegistry(reg)

	tool := NewInstallSkillTool(registryMgr, globalSkills)

	done := make(chan *ToolResult, 1)
	go func() {
		done <- tool.Execute(context.Background(), map[string]any{
			"slug":     "mid-install-skill",
			"registry": "blocking",
		})
	}()

	// Wait until the download has staged its SKILL.md but Execute has not
	// yet renamed it into place.
	var stageDir string
	select {
	case stageDir = <-reg.staged:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the staged install to write SKILL.md")
	}
	require.Contains(t, stageDir, ".staging"+string(filepath.Separator),
		"the staged directory must live under the dedicated, non-scanned .staging subdirectory")

	// Mid-install: ListSkills must see the control skill and NOTHING else —
	// specifically not the in-flight staging entry.
	loader := skills.NewSkillsLoader(t.TempDir(), globalSkills, t.TempDir())
	infos := loader.ListSkills()
	ids := make([]string, 0, len(infos))
	for _, info := range infos {
		ids = append(ids, info.ID)
	}
	assert.Contains(t, ids, "already-installed")
	assert.Len(t, ids, 1, "no in-flight staging entry may be visible mid-install; got: %v", ids)
	for _, id := range ids {
		assert.NotContains(t, id, "install-", "no staging-shaped ID may ever be listed, got %q", id)
	}

	// Let the install finish and confirm it completed normally.
	close(reg.proceed)
	result := <-done
	require.False(t, result.IsError, "install must succeed once unblocked, got: %s", result.ForLLM)
	_, err := os.Stat(filepath.Join(globalSkills, "mid-install-skill", "SKILL.md"))
	require.NoError(t, err, "the installed skill must land at its final location after Execute returns")

	// And now it IS visible.
	infosAfter := loader.ListSkills()
	found := false
	for _, info := range infosAfter {
		if info.ID == "mid-install-skill" {
			found = true
		}
	}
	assert.True(t, found, "the completed install must be discoverable after Execute returns")
}

// TestNewInstallSkillTool_SweepsStaleStagingLeftoverFromCrash is the second
// half of the FIX 1 (HIGH) regression coverage: if the process dies between
// staging extraction and the final os.Rename (crash, OOM-kill, forced
// restart), the deferred os.RemoveAll in Execute never runs, so a leftover
// staging directory survives on disk. NewInstallSkillTool's constructor —
// which runs once per agent at startup, see pkg/agent/loop.go's
// registerSharedTools — must sweep any such leftover out of
// skillsDir/.staging so it does not accumulate forever.
func TestNewInstallSkillTool_SweepsStaleStagingLeftoverFromCrash(t *testing.T) {
	globalSkills := t.TempDir()

	// Simulate a crash-orphaned staging directory: fully extracted, complete
	// with SKILL.md, exactly as DownloadAndInstall would leave it, but never
	// renamed into place because the process died first.
	orphan := filepath.Join(globalSkills, ".staging", "orphan-skill.install-abc123")
	require.NoError(t, os.MkdirAll(orphan, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(orphan, "SKILL.md"),
		[]byte("---\nname: orphan-skill\ndescription: crash leftover\n---\n\nBody.\n"),
		0o644,
	))

	// A real, fully-installed skill must be left completely untouched by the
	// sweep — the sweep is scoped to .staging only.
	require.NoError(t, os.MkdirAll(filepath.Join(globalSkills, "real-skill"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(globalSkills, "real-skill", "SKILL.md"),
		[]byte("---\nname: real-skill\ndescription: must survive the sweep\n---\n\nBody.\n"),
		0o644,
	))

	_ = NewInstallSkillTool(skills.NewRegistryManager(), globalSkills)

	_, err := os.Stat(orphan)
	assert.True(t, os.IsNotExist(err), "the crash-orphaned staging directory must be removed by the startup sweep")
	_, err = os.Stat(filepath.Join(globalSkills, "real-skill", "SKILL.md"))
	assert.NoError(t, err, "the sweep must not touch real, already-installed skills")
}
