//go:build linux

package preview

import (
	"errors"
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/stretchr/testify/require"
)

// T-80 / §3.1 D1.4 (unit): the enforcement decision is driven through its
// injected parameters — the kernel release string and the ignore_rlimit_data
// parameter bytes — never through this host's real state, so every case runs
// on any Linux CI runner (the T-91 measurement job runs exactly this test
// name and requires zero skips). The downstream disclosure surfaces that
// carry the reason — the preview_document result text and the
// preview_document.run audit field — are pinned in pkg/tools; the doctor row
// is pinned in pkg/app/internal/doctor. The final subtest proves New stores
// exactly what this check decided, so on a 4.6/Y host every result, audit
// record and doctor row disclose, and on a 4.7/N host none do.
//
// RED history: this test stood as four t.Fatal("BLOCKED: …") cases until the
// developer supplied dataLimitUnenforced and its wiring; the BLOCKED run log
// is the saved missing-implementation red for this row.
func TestPreviewMemoryEnforcementDisclosure(t *testing.T) {
	t.Run("kernel_4_6_discloses_in_result_audit_doctor", func(t *testing.T) {
		reason, note := dataLimitUnenforced("4.6.0-1-amd64", nil, []byte("N\n"), nil)
		require.NotEmpty(t, reason, "§3.1 D1.4: the data limit governs mmap only from kernel 4.7, so 4.6 must disclose")
		require.Contains(t, reason, "4.7", "the reason names the boundary")
		require.Contains(t, reason, "4.6.0-1-amd64", "the reason names the actual kernel release")
		require.Empty(t, note, "both inputs were readable — nothing to note")
	})
	t.Run("ignore_rlimit_data_Y_discloses_in_result_audit_doctor", func(t *testing.T) {
		reason, note := dataLimitUnenforced("6.12.114-fly", nil, []byte("Y\n"), nil)
		require.NotEmpty(t, reason, "§3.1 D1.4: ignore_rlimit_data=Y makes the data limit warn-only")
		require.Contains(t, reason, "ignore_rlimit_data", "the reason names the boot option")
		require.Empty(t, note, "both inputs were readable — nothing to note")
	})
	t.Run("kernel_4_7_and_N_has_no_disclosure", func(t *testing.T) {
		for _, release := range []string{"4.7.0-1-amd64", "6.12.114-fly"} {
			reason, note := dataLimitUnenforced(release, nil, []byte("N\n"), nil)
			require.Empty(t, reason, "%s with the boot option unset enforces the data limit", release)
			require.Empty(t, note, "%s: readable inputs leave nothing to note", release)
		}
	})
	t.Run("unreadable_parameter_assumes_enforcing_and_logs_once", func(t *testing.T) {
		reason, note := dataLimitUnenforced("6.12.114-fly", nil, nil, errors.New("permission denied"))
		require.Empty(t, reason, "§3.1 D1.4: an unreadable parameter file is treated as enforcing (the default)")
		require.NotEmpty(t, note, "the enforcing assumption is noted once — New logs exactly this note")
	})
	t.Run("new_stores_the_host_check_result", func(t *testing.T) {
		hostReason, _ := HostMemoryLimitUnenforced()
		home := t.TempDir()
		self, err := os.Executable()
		require.NoError(t, err)
		svc, err := New(Config{Home: home, Self: self, Resolver: &component.Resolver{Home: home, Executable: self}})
		require.NoError(t, err)
		require.Equal(t, hostReason, svc.memoryUnenforced,
			"New stores exactly what the §3.1 D1.4 check decided; Preview stamps it on every Result")
	})
}
