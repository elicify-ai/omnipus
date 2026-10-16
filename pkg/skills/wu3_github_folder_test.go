package skills_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type wu3GitHubTransport struct {
	calls   int
	content string
}

// Hermetic GitHub Contents/raw edge. No downloaded script is executed.
func (edge *wu3GitHubTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	edge.calls++
	body := edge.content
	if r.URL.Host == "api.github.com" {
		body = `[{"name":"SKILL.md","path":"SKILL.md","type":"file","download_url":"https://raw.githubusercontent.com/fixture/repo/main/SKILL.md"}]`
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

func wu3Installer(t *testing.T) (*skills.SkillInstaller, *wu3GitHubTransport, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OMNIPUS_HOME", home)
	installer, err := skills.NewSkillInstaller(home, "", "")
	require.NoError(t, err)
	edge := &wu3GitHubTransport{content: "---\nname: safe-skill\ndescription: A safe inert GitHub skill fixture.\n---\n\n# Safe\n"}
	installer.SetHTTPClient(&http.Client{Transport: edge})
	return installer, edge, home
}

// #1276; chief's explicit 2026-10-14 exception to Execute/REST-only: exercise
// the existing PUBLIC InstallFromGitHub. A normal download is a real control.
func TestWU3GitHubNormalFolderInstallsWithinSkillRoot(t *testing.T) {
	installer, edge, home := wu3Installer(t)
	require.NoError(t, installer.InstallFromGitHub(context.Background(), "https://github.com/fixture/safe-skill/tree/main"))
	assert.Equal(t, 2, edge.calls, "control actually lists and downloads, not an empty/silent pass")
	data, err := os.ReadFile(filepath.Join(home, "skills", "safe-skill", "SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, edge.content, string(data))
}

func TestWU3GitHubDerivedHostileFolderRefusedBeforeNetworkOrFilesystem(t *testing.T) {
	for _, row := range []struct{ label, link string }{
		{"dot_repo", "https://github.com/fixture/."},
		{"parent_repo", "https://github.com/fixture/.."},
		{"parent_subpath", "https://github.com/fixture/repo/tree/main/.."},
		{"windows_separator", "https://github.com/fixture/repo/tree/main/evil%5Cfolder"},
		{"decoded_separator", "https://github.com/fixture/evil%2Ffolder"},
		{"double_encoded_separator", "https://github.com/fixture/evil%252Ffolder"},
		{"double_encoded_windows_separator", "https://github.com/fixture/repo/tree/main/evil%255Cfolder"},
		{"reserved_con", "https://github.com/fixture/CON"},
		{"reserved_nul", "https://github.com/fixture/nul"},
		{"reserved_com1", "https://github.com/fixture/COM1"},
		{"reserved_lpt1", "https://github.com/fixture/lpt1"},
		{"over_64", "https://github.com/fixture/" + strings.Repeat("a", 65)},
		{"oversized_component", "https://github.com/fixture/" + strings.Repeat("a", 256)},
	} {
		t.Run(row.label, func(t *testing.T) {
			installer, edge, home := wu3Installer(t)
			sentinel := filepath.Join(home, "keep.txt")
			require.NoError(t, os.WriteFile(sentinel, []byte("outside-skill-root-control"), 0o600))
			err := installer.InstallFromGitHub(context.Background(), row.link)
			assert.Error(t, err, "#1276 hostile link must be rejected explicitly")
			assert.Contains(t, strings.ToLower(fmt.Sprint(err)), "name", "visible refusal names invalid derived folder, not incidental network/stat failure")
			assert.Zero(t, edge.calls, "validation must precede every network request")
			entries, readErr := os.ReadDir(home)
			require.NoError(t, readErr)
			names := make([]string, 0, len(entries))
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
			assert.Equal(t, []string{"keep.txt"}, names, "refusal leaves zero new files/directories")
			data, readErr := os.ReadFile(sentinel)
			require.NoError(t, readErr)
			assert.Equal(t, "outside-skill-root-control", string(data))
		})
	}
}

// Agent Skills standard from pkg/skills/CLAUDE.md: 64-char maximum. The
// max-minus-one/max controls prevent a deny-everything validation from passing.
func TestWU3GitHubFolderLengthBoundaryAllowsValidSlugs(t *testing.T) {
	for _, length := range []int{1, 2, 63, 64} {
		t.Run(fmt.Sprintf("length_%d", length), func(t *testing.T) {
			installer, edge, home := wu3Installer(t)
			name := strings.Repeat("a", length)
			edge.content = "---\nname: " + name + "\ndescription: An inert boundary fixture from GitHub.\n---\n\nBody.\n"
			require.NoError(t, installer.InstallFromGitHub(context.Background(), "fixture/"+name))
			assert.Equal(t, 2, edge.calls)
			data, err := os.ReadFile(filepath.Join(home, "skills", name, "SKILL.md"))
			require.NoError(t, err)
			assert.Equal(t, edge.content, string(data))
		})
	}
}
