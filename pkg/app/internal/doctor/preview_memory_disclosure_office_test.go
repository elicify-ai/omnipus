package doctor

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/stretchr/testify/require"
)

// T-80 / §3.1 D1.4: doctor's WARN-PREVIEW-003 row mirrors the host memory
// check. The unenforced side is host-conditioned — a real 4.6/Y Linux host
// must show the row with the disclosure text (the T-91 measurement job is
// where a disclosure-path run is recorded), while every enforcing host,
// including this one, must show none. The row's presence on an unenforcing
// host cannot be injected at the doctor level (checkDocumentPreview calls
// the host check directly; no seam exists) — that half is covered by the
// decision unit test in pkg/preview plus this mirror assertion.
func TestDoctorPreviewMemoryDisclosureMatchesHost(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	reason, _ := preview.HostMemoryLimitUnenforced()
	warnings := checkDocumentPreview(&config.Config{})
	rows := 0
	for _, w := range warnings {
		if w.code != "WARN-PREVIEW-003" {
			continue
		}
		rows++
		require.NotEmpty(t, reason, "WARN-PREVIEW-003 must not appear on an enforcing host")
		require.Contains(t, w.message, "memory limit not enforced: "+reason,
			"§3.1 D1.4: the doctor row discloses the reason")
	}
	if reason == "" {
		require.Equal(t, 0, rows, "an enforcing host produces no memory disclosure row")
	} else {
		require.Equal(t, 1, rows, "an unenforcing host produces exactly one memory disclosure row")
	}
}
