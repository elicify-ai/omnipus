package whatsapp

import (
	"path/filepath"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/channels"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
)

func init() {
	channels.RegisterFactory(
		"whatsapp_native",
		func(cfg *config.Config, _ credentials.SecretBundle, b *bus.MessageBus) (channels.Channel, error) {
			inst := cfg.Channels["whatsapp"]
			waCfg := config.InstanceToWhatsApp(inst)
			storePath := waCfg.SessionStorePath
			if storePath == "" {
				storePath = filepath.Join(cfg.WorkspacePath(), "whatsapp")
			}
			return NewWhatsAppNativeChannel(waCfg, b, storePath)
		},
	)
}
