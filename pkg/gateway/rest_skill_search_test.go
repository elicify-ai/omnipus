// Tests for GET /api/v1/skills/search (ClawHub marketplace search) and the
// install-by-slug validation on POST /api/v1/skills/install. The registry is
// stubbed via a fake SkillRegistry — these tests never reach the real
// clawhub.ai network endpoint.

package gateway

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/skills"
)

// fakeSkillRegistry is an in-memory SkillRegistry stub for handler tests.
type fakeSkillRegistry struct {
	results    []skills.SearchResult
	searchErr  error
	installErr error
	installRes *skills.InstallResult
	// lastQuery / lastLimit capture the most recent Search call args.
	lastQuery string
	lastLimit int
}

func (f *fakeSkillRegistry) Name() string { return "fake" }

func (f *fakeSkillRegistry) Search(_ context.Context, query string, limit int) ([]skills.SearchResult, error) {
	f.lastQuery = query
	f.lastLimit = limit
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.results, nil
}

// GetSkillMeta is unused by this file's scenarios (they exercise Search and
// DownloadAndInstall only); return a distinct error rather than (nil, nil)
// so a caller cannot mistake "no metadata, no error" for a real lookup miss.
var errFakeSkillRegistryGetSkillMetaUnused = errors.New("fakeSkillRegistry: GetSkillMeta not stubbed for this test")

func (f *fakeSkillRegistry) GetSkillMeta(_ context.Context, _ string) (*skills.SkillMeta, error) {
	return nil, errFakeSkillRegistryGetSkillMetaUnused
}

func (f *fakeSkillRegistry) DownloadAndInstall(_ context.Context, slug, _, targetDir string) (*skills.InstallResult, error) {
	if f.installErr != nil {
		return nil, f.installErr
	}
	if f.installRes != nil {
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			return nil, err
		}
		content := "---\nname: " + slug + "\ndescription: Use this installed test skill when requested.\n---\n\nBody.\n"
		if err := os.WriteFile(filepath.Join(targetDir, "SKILL.md"), []byte(content), 0o644); err != nil {
			return nil, err
		}
		return f.installRes, nil
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, err
	}
	content := "---\nname: " + slug + "\ndescription: Use this installed test skill when requested.\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(targetDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		return nil, err
	}
	return &skills.InstallResult{}, nil
}

// assertError is a tiny error helper to avoid importing errors in every test.
type assertError string

func (e assertError) Error() string { return string(e) }
