package doctor

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// AD-7 / FR-026: an install missing the optional components must say so;
// unrelated passing checks cannot silently stand in for preview diagnostics.
func TestOfficePreview_DoctorNamesMissingComponents(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	cfg := &config.Config{}
	warnings := checkConfig(cfg)
	require.NotEmpty(t, warnings, "positive control: real doctor checks executed")
	for _, component := range []string{"LibreOffice", "PDF renderer"} {
		t.Run(component, func(t *testing.T) {
			var matched []string
			for _, warning := range warnings {
				if strings.Contains(strings.ToLower(warning.message), strings.ToLower(component)) {
					matched = append(matched, warning.message)
				}
			}
			if len(matched) == 0 {
				t.Fatalf("BLOCKED: doctor diagnostic for %s not implemented — required by office-preview spec AD-4, AD-7 / FR-026", component)
			}
			require.Len(t, matched, 1, "one component diagnostic, not duplicate warnings")
			require.Contains(t, strings.ToLower(matched[0]), "not installed")
		})
	}
}
