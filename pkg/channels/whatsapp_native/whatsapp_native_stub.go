//go:build lite || mipsle || netbsd || (freebsd && arm)

package whatsapp

import (
	"fmt"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/channels"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// NativeAvailable reports whether the native whatsmeow-based WhatsApp transport
// was compiled into this binary. False in this stub (lite variant or
// architectures where modernc.org/sqlite is unavailable). REST callers use this
// to know native WhatsApp is unavailable on this build/arch and must NOT offer
// the QR-pairing flow. (The legacy bridge transport has been removed, so on this
// build WhatsApp is simply unavailable.)
const NativeAvailable = false

// NewWhatsAppNativeChannel returns an error when native WhatsApp is not compiled in.
// This stub is built in two cases:
//   - the lite variant (-tags lite), which deliberately excludes the whatsmeow + SQLite
//     stack to keep the binary small; and
//   - targets where modernc.org/sqlite cannot build (mipsle, netbsd, freebsd/arm),
//     matching the arch triple of the Matrix gate (pkg/gateway/channel_matrix.go).
//     Unlike Matrix this gate has no cgo dimension — the native build is CGO_ENABLED=0
//     (modernc is pure Go).
//
// Native WhatsApp ships in the DEFAULT build on all supported targets. To get it,
// build without the lite tag (the default) on a supported target: go build ./cmd/...
func NewWhatsAppNativeChannel(
	cfg config.WhatsAppConfig,
	bus *bus.MessageBus,
	storePath string,
) (channels.Channel, error) {
	return nil, fmt.Errorf("whatsapp native is not compiled into this build (lite variant, or an architecture where modernc.org/sqlite is unavailable such as mipsle/netbsd); WhatsApp requires the native build (the legacy bridge has been removed) — use the default build on a supported target")
}
