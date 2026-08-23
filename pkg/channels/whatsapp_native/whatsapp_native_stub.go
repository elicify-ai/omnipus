//go:build mipsle || netbsd || (freebsd && arm)

package whatsapp

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// NativeAvailable reports whether the native whatsmeow-based WhatsApp transport
// was compiled into this binary. False in this stub (architectures where
// modernc.org/sqlite is unavailable). REST callers use this
// to know native WhatsApp is unavailable on this build/arch and must NOT offer
// the QR-pairing flow. (The legacy bridge transport has been removed, so on this
// build WhatsApp is simply unavailable.)
const NativeAvailable = false

// NewWhatsAppNativeChannel returns an error when native WhatsApp is not compiled in.
// This stub is built for targets where modernc.org/sqlite cannot build (mipsle,
// netbsd, freebsd/arm), matching the arch triple of the Matrix gate
// (pkg/gateway/channel_matrix.go). Unlike Matrix this gate has no cgo dimension —
// the native build is CGO_ENABLED=0 (modernc is pure Go). (Before ADR-067 §10
// step 14 retired the `lite` build variant, `-tags lite` also selected this stub.)
//
// Native WhatsApp ships in every build on all supported targets: go build ./cmd/...
func NewWhatsAppNativeChannel(
	cfg config.WhatsAppConfig,
	bus *bus.MessageBus,
	storePath string,
) (channels.Channel, error) {
	return nil, fmt.Errorf(
		"whatsapp native is not compiled into this build (this architecture has no modernc.org/sqlite support — mipsle, netbsd, freebsd/arm); WhatsApp requires the native build (the legacy bridge has been removed) — use a supported target",
	)
}
