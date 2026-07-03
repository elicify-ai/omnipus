// goolm is the pure-Go OLM implementation (replaces libolm which requires CGo).
// Without it, matrix.go (and this package's real init.go) are excluded so
// CGO_ENABLED=0 builds still succeed — see matrix.go's top comment. Without a
// stub, an operator who enables the "matrix" channel on a non-goolm build hit
// the manager's initChannels loop failing with "factory not registered for
// channel", which is not in isNonFatalChannelName's allow-list and, per
// bootFatalError's deny-by-default policy, aborted the ENTIRE gateway boot —
// every other channel with it. This stub registers the "matrix" factory
// anyway so that failure is recorded as a normal (non-fatal, once
// pkg/channels/manager.go's isNonFatalChannelName is taught to recognize it)
// ChannelInitError with a clear, actionable message, and the gateway boots
// degraded instead. Mirrors the WhatsApp-lite pattern in
// pkg/channels/whatsapp_native/whatsapp_native_stub.go.
//go:build !goolm

package matrix

import (
	"fmt"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/channels"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
)

func init() {
	channels.RegisterFactory(
		"matrix",
		func(_ *config.Config, _ string, _ credentials.SecretBundle, _ *bus.MessageBus) (channels.Channel, error) {
			return nil, fmt.Errorf("matrix channel requires a goolm-tagged build (pure-Go OLM crypto); " +
				"this binary was built without -tags goolm, so the matrix channel is unavailable — " +
				"rebuild with -tags goolm,stdjson (the project's default build tags) to enable it")
		},
	)
}
