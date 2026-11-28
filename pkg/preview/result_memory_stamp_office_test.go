//go:build darwin || linux

package preview

import (
	"context"
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/extracttext"
	"github.com/stretchr/testify/require"
)

// The test binary hosts the extract-text program, exactly as the external
// officepreview fixture hosts it for pkg/preview_test: Service.Preview's info
// and text stages target s.cfg.Self at extracttext.ModeArg, and with no
// renderer installed (the unit service's empty resolver) that is the whole
// conversion path. With this host, a full Preview runs for real inside the
// package-internal test — no production seam, no fake result.
func init() {
	if len(os.Args) >= 2 && os.Args[1] == extracttext.ModeArg {
		os.Exit(extracttext.Main())
	}
}

// T-80 / §3.1 D1.4 — the Result stamp: every successful Preview carries the
// memory-limit disclosure the service decided at New, and Preview returns it
// on the Result that the result text, the audit row and the read_file note
// are all built from. The linux-only decision unit cannot see this line
// removed (its subtests never reach Preview's return), so this test pins the
// stamp itself, on a real conversion, on every platform this suite's
// integration coverage covers.
func TestPreviewResultCarriesMemoryUnenforcedStamp(t *testing.T) {
	svc := previewUnitService(t)
	adm, err := svc.Admit(context.Background(), false)
	require.NoError(t, err)
	defer adm.Release()

	// Baseline: whatever the host check decided at New reaches the Result
	// (empty on every enforcing host and on the non-Linux stub).
	before, err := svc.Preview(context.Background(), adm, Request{Name: "one.pdf", Ext: ".pdf", Data: officeRetryPDF(), Default: true})
	require.NoError(t, err)
	require.Equal(t, svc.memoryUnenforced, before.MemoryUnenforced,
		"the Result carries the service's own memory check verdict")

	// Injected reason: a service whose memory check returned a reason stamps
	// that reason on the Result — the mutant that deletes the stamp fails
	// here, on any host, whatever the real kernel decides.
	reason := "the kernel boot option ignore_rlimit_data is set"
	svc.memoryUnenforced = reason
	after, err := svc.Preview(context.Background(), adm, Request{Name: "one.pdf", Ext: ".pdf", Data: officeRetryPDF(), Default: true})
	require.NoError(t, err)
	require.Equal(t, reason, after.MemoryUnenforced,
		"§3.1 D1.4: every result discloses the memory-limit-not-enforced reason")
}
