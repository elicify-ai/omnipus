package whatsapp

import (
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

func init() {
	channels.RegisterFactory(
		"whatsapp_native",
		func(cfg *config.Config, instanceID string, _ credentials.SecretBundle, b *bus.MessageBus) (channels.Channel, error) {
			inst := cfg.Channels[instanceID]
			waCfg := config.InstanceToWhatsApp(inst)
			storePath := waCfg.SessionStorePath
			if storePath == "" {
				// FR-018: per-instance store path so two WhatsApp instances
				// (e.g. "whatsapp.eu" and "whatsapp.us") never share store.db.
				// Legacy single-instance ("whatsapp") uses
				// workspace/whatsapp/whatsapp/store.db — different from the old
				// workspace/whatsapp/store.db, but v0.3 is a fresh-build so
				// no migration is required.
				storePath = filepath.Join(cfg.AgentHomeBasePath(), "whatsapp", instanceID)
			}
			return NewWhatsAppNativeChannel(waCfg, b, storePath)
		},
	)
}
