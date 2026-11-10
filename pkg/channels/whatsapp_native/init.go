package whatsapp

import (
	"fmt"
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
				// workspace/whatsapp/store.db. The codebase is greenfield (no
				// migration or upgrade path exists), so this is not a breaking change.
				storePath = filepath.Join(cfg.AgentHomeBasePath(), "whatsapp", instanceID)
			}
			ch, err := NewWhatsAppNativeChannel(waCfg, b, storePath)
			if err == nil {
				native, ok := ch.(*WhatsAppNativeChannel)
				if !ok {
					return nil, fmt.Errorf("whatsapp native factory: expected *WhatsAppNativeChannel, got %T", ch)
				}
				native.originConfig = cfg
			}
			return ch, err
		},
	)
}
