//go:build linux

package sandbox

import (
	"log/slog"
	"net"
)

// reserveEgressListener binds 127.0.0.1:0 and parks the listener for the
// next NewEgressProxy. The Linux backend calls it on the gateway's boot
// thread immediately before landlock_restrict_self, while binds are still
// unrestricted; no bind rule is added, so sandboxed code (and children,
// which inherit the domain) stay unable to bind port 0 or any other
// unlisted port. A no-op when a listener is already parked.
func reserveEgressListener() error {
	reservedEgressListenerMu.Lock()
	defer reservedEgressListenerMu.Unlock()
	if reservedEgressListener != nil {
		return nil
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	reservedEgressListener = l
	return nil
}

// releaseReservedEgressListener closes and drops a parked listener that
// will not be used (restrict_self failed after the reservation).
func releaseReservedEgressListener() {
	if l := takeReservedEgressListener(); l != nil {
		if err := l.Close(); err != nil {
			slog.Debug("egress_proxy: closing unused reserved listener failed", "error", err)
		}
	}
}
