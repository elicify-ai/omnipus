package doctor

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/stretchr/testify/require"
)

// T-80 / §3.1 D1.4: doctor's WARN-PREVIEW-003 row discloses an unenforced
// memory limit with the exact reason, and no enforcing host shows one. The
// host check is a package variable (preview.HostMemoryLimitUnenforced), so
// both sides are driven here on ANY host — the unenforcing case no longer
// waits for a real 4.6/Y kernel.
func TestDoctorPreviewMemoryDisclosureMatchesHost(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	previous := preview.HostMemoryLimitUnenforced
	t.Cleanup(func() { preview.HostMemoryLimitUnenforced = previous })

	// Enforcing host: zero memory disclosure rows.
	preview.HostMemoryLimitUnenforced = func() (string, string) { return "", "" }
	for _, w := range checkDocumentPreview(&config.Config{}) {
		require.NotEqual(t, "WARN-PREVIEW-003", w.code,
			"an enforcing host must produce no memory disclosure row")
	}

	// Unenforcing host: exactly one row, disclosing the exact reason.
	reason := "the Linux kernel 4.6.0-1-amd64 is older than 4.7"
	preview.HostMemoryLimitUnenforced = func() (string, string) { return reason, "" }
	rows := 0
	for _, w := range checkDocumentPreview(&config.Config{}) {
		if w.code != "WARN-PREVIEW-003" {
			continue
		}
		rows++
		require.Contains(t, w.message, "memory limit not enforced: "+reason,
			"§3.1 D1.4: the doctor row discloses the exact reason")
	}
	require.Equal(t, 1, rows, "an unenforcing host produces exactly one memory disclosure row")
}
