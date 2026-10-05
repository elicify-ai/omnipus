package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// mintGenuineBootEpochForLoop mints ONE real boot epoch and registers it on al,
// exactly as production does before any admission: the gateway constructs the
// AgentLoop, mints the epoch (pkg/gateway/boot_epoch_warn.go::mintBootEpoch,
// failure aborts boot) and registers the store before a scheduler, channel or
// human turn can admit work. Ordinary-root admission refuses an unminted epoch
// ("ordinary admission: no minted boot epoch",
// ordinary_execution_admission.go), and the boot-epoch ruling (architect,
// BOOT-EPOCH-RULING.md) classes an epoch-0 positive harness as invalid setup —
// so a fixture that drives an ordinary root through a real turn mints a real
// store here instead of hard-coding or skipping the epoch.
//
// The store lives in a dedicated subdirectory of the loop's home (the same
// layout newSteerAL uses) so fixtures that mint their own epoch directly over
// home still see a fresh counter file.
func mintGenuineBootEpochForLoop(t *testing.T, al *AgentLoop) *session.BootEpochStore {
	t.Helper()
	dir := filepath.Join(al.GetConfig().Agents.Defaults.Home, "boot_epoch")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("SETUP create boot epoch dir: %v", err)
	}
	boot := session.NewBootEpochStore(dir)
	epoch, err := boot.Mint()
	if err != nil {
		t.Fatalf("SETUP Mint genuine boot epoch: %v", err)
	}
	if epoch == 0 || boot.Current() != epoch {
		t.Fatalf("SETUP minted/current boot epoch = %d/%d, require one genuine nonzero epoch", epoch, boot.Current())
	}
	al.SetBootEpochStore(boot)
	if got := al.bootEpochFor(); got != epoch {
		t.Fatalf("SETUP: loop reads boot epoch %d after registering the store minted at %d", got, epoch)
	}
	return boot
}
