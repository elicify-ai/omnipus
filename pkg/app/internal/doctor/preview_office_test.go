package doctor

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

// Explicit oracle change, not adaptation to observed code: architect ruling
// 2026-11-01 Part 3 supersedes the former missing-component warning expectation.
// Optional absence is a supported install choice; damage/unsafe access is not.
func TestOfficePreview_DoctorMissingOptionalComponentsIsSilent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	resolver, err := component.NewResolver(home)
	require.NoError(t, err)
	for _, kind := range []component.Kind{component.KindOffice, component.KindPDFRenderer} {
		_, problem := resolver.Resolve(kind)
		require.NotNil(t, problem, "fixture must not accidentally see an installed component")
		require.Equal(t, component.StatusMissing, problem.Status)
	}
	warnings := checkConfig(&config.Config{})
	require.Empty(t, officeDoctorWarnings(warnings, "WARN-PREVIEW-001"), "no renderer is an install choice, not damage")
	require.Empty(t, officeDoctorWarnings(warnings, "WARN-PREVIEW-002"), "no Office is an install choice, not damage")
	for _, w := range warnings {
		if strings.HasPrefix(w.code, "WARN-PREVIEW-") {
			require.NotContains(t, strings.ToLower(w.message), "not installed")
		}
	}
}

func TestOfficePreview_DoctorDamagedComponentsStillWarn(t *testing.T) {
	for _, tc := range []struct {
		kind component.Kind
		code string
		name string
	}{{component.KindPDFRenderer, "WARN-PREVIEW-001", "PDF renderer"}, {component.KindOffice, "WARN-PREVIEW-002", "LibreOffice"}} {
		t.Run(string(tc.kind), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("OMNIPUS_HOME", home)
			_, dependency := officeDoctorComponent(t, home, tc.kind)
			require.NoError(t, os.WriteFile(dependency, []byte("tampered bytes"), 0600))
			resolver, err := component.NewResolver(home)
			require.NoError(t, err)
			_, problem := resolver.Resolve(tc.kind)
			require.NotNil(t, problem)
			require.Equal(t, component.StatusDamaged, problem.Status, "instrument: actual fingerprint failure")
			warnings := checkConfig(&config.Config{})
			officeDoctorRequireProblem(t, warnings, tc.code, tc.name, "damaged")
			matched := officeDoctorWarnings(warnings, tc.code)
			require.Contains(t, strings.ToLower(matched[0]), "reinstall", "damage needs an actionable remedy")
		})
	}
}

func TestOfficePreview_DoctorAgentWritableComponentsStillWarn(t *testing.T) {
	for _, tc := range []struct {
		kind component.Kind
		code string
		name string
	}{{component.KindPDFRenderer, "WARN-PREVIEW-001", "PDF renderer"}, {component.KindOffice, "WARN-PREVIEW-002", "LibreOffice"}} {
		t.Run(string(tc.kind), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("OMNIPUS_HOME", home)
			root, _ := officeDoctorComponent(t, home, tc.kind)
			pattern := "^" + regexp.QuoteMeta(root+string(filepath.Separator))
			cfg := &config.Config{}
			cfg.Tools.AllowWritePaths = []string{pattern}
			work := t.TempDir()
			probe := filepath.Join(root, "actual-agent-write.txt")
			args := map[string]any{"path": probe, "content": "actual authority", "overwrite": true}
			denied := tools.NewWriteFileTool(work, true).Execute(context.Background(), args)
			require.True(t, denied.IsError, "control: unrelated workspace alone cannot write the component")
			allowed := tools.NewWriteFileTool(work, true, []*regexp.Regexp{regexp.MustCompile(pattern)}).Execute(context.Background(), args)
			require.False(t, allowed.IsError, "actual writer control: %s", allowed.ForLLM)
			got, err := os.ReadFile(probe)
			require.NoError(t, err)
			require.Equal(t, "actual authority", string(got))
			warnings := checkConfig(cfg)
			officeDoctorRequireProblem(t, warnings, tc.code, tc.name, "is in a folder an agent can write")
			matched := officeDoctorWarnings(warnings, tc.code)
			require.Contains(t, matched[0], "until that access is removed", "unsafe access needs an actionable remedy")
		})
	}
}
