//go:build darwin

package preview

import (
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/stretchr/testify/require"
)

// T-11 / §3.4 D8c: the macOS sandbox profile a real converter start used is
// exactly the converter's own read list plus the three socket rules —
// compared against the profile the REAL START used, never a profile the test
// builds. After one real confined LibreOffice conversion, the rendered
// profile must contain, and contain only:
//
//   - read+execute on the component root (LibreOffice.app) and the launcher
//     folder;
//   - read+write on this call's <id>/work;
//   - the three socket rules for this call's socket folder (read+write,
//     socket bind, socket connect — macOS only);
//   - read on the platform's system libraries preamble;
//   - read-only on Component.Registry as load resolved it (§3.4 D8c R1:
//     <home>/system/components/office/services for an operator-registered
//     LibreOffice);
//   - read-only on the platform's font folders (/Library/Fonts on macOS);
//   - NO rule granted from the turn's policy (the turn's read rights feed
//     nothing: Request.ReadModel is gone), and no network rule — the profile
//     denies network;
//   - Mach lookups exactly 2 (the macOS preamble's two).
//
// HELD on the pre-D8c code: the profile text cannot be read. It is rendered
// into an unexported field and handed to sandbox-exec inline
// (pkg/sandbox/backend_darwin_seatbelt.go, profile passed as `sandbox-exec -p`
// argument); no accessor exists. The exact seam needed — NOT invented: an
// exported profile accessor on *SeatbeltBackend, `RenderedProfile() string`,
// returning the profile text the backend last rendered, so the test can read
// the profile the real start used from the backend the conversion ran under.
// Once the seam and D8c exist, this test runs the real confined conversion
// (the registration and resolution below are its proven instrument), reads
// that profile through the seam, and diffs it against the list above —
// extra rules, missing rules and turn-policy rules all fail.
//
// Not a pass if: the profile compared is one the test assembled; the
// conversion was not confined; the component resolved is not the registered
// real LibreOffice.
func TestPreviewConverterProfileMatchesOwnReadList(t *testing.T) {
	primeKernelSandbox(t)
	officeRoot := os.Getenv("OMNIPUS_TEST_OFFICE_ROOT")
	if officeRoot == "" {
		t.Fatal("BLOCKED: real LibreOffice fixture unavailable; set OMNIPUS_TEST_OFFICE_ROOT — required by T-11 (the profile compared is the one a real LibreOffice start used)")
	}
	home := t.TempDir()
	opts := component.RegisterOptions{Home: home, Operator: "office-preview-test", Folder: officeRoot}
	_, err := component.RegisterOffice(opts)
	require.NoError(t, err, "instrument: the real LibreOffice fixture registers")
	self, err := os.Executable()
	require.NoError(t, err)
	comp, problem := (&component.Resolver{Home: home, Executable: self}).Resolve(component.KindOffice)
	require.Nil(t, problem, "instrument: the registered real LibreOffice resolves through the real load and verifies")
	require.NotEqual(t, "", comp.Registry, "instrument: the macOS office component has a filtered registry folder for the read-only rule to cover")

	t.Fatal("BLOCKED: the profile the real start used cannot be read on this code — the profile is rendered into an unexported field and handed to sandbox-exec inline (pkg/sandbox/backend_darwin_seatbelt.go), and the seam this row needs does not exist: an exported accessor on *SeatbeltBackend, RenderedProfile() string, returning the profile text the backend last rendered. Required by T-11 (§3.4 D8c): the diff of the real start's profile against the converter's own read list plus the three socket rules, with no turn-policy rules, network denied and exactly 2 Mach lookups; the held oracle is this test's doc comment")
}
