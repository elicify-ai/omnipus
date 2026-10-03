// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"fmt"
	"log/slog"
	"runtime"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// bootEpochDurabilityWarnOnce keeps the Windows boot diagnostic to one line
// per process. It is not an error path and it does not replace a failed
// write: mint still returns that failure and boot stops.
var bootEpochDurabilityWarnOnce sync.Once

// warnWindowsBootEpochDurability logs the platform residual once, on Windows
// only. Unix directory-sync errors fail the mint. Windows has no
// unprivileged directory flush; the durable step is the write-through rename
// inside fileutil.WriteFileAtomicSyncDir, and a power loss can still drop
// that rename's directory entry.
func warnWindowsBootEpochDurability() {
	if runtime.GOOS != "windows" {
		return
	}
	bootEpochDurabilityWarnOnce.Do(func() {
		slog.Warn("boot-epoch durability: write-through rename; no separate directory flush on this platform; a power loss can still drop the renamed file's directory entry")
	})
}

// mintBootEpoch persists this process's boot counter and keeps the store for
// later recovery. It is the only production Mint call.
func (stg *setupAndStartServicesState) mintBootEpoch() error {
	warnWindowsBootEpochDurability()
	stg.bootEpoch = session.NewBootEpochStore(stg.homePath)
	if _, err := stg.bootEpoch.Mint(); err != nil {
		return fmt.Errorf("gateway: boot epoch mint failed: %w", err)
	}
	return nil
}
