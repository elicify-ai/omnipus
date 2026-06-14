package email

import (
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/channels"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
)

func init() {
	channels.RegisterFactory(
		"email",
		func(cfg *config.Config, secrets credentials.SecretBundle, b *bus.MessageBus) (channels.Channel, error) {
			inst, ok := cfg.Channels["email"]
			if !ok || !inst.Enabled {
				return nil, nil
			}
			return NewEmailChannel(config.InstanceToEmail(inst), secrets, b)
		},
	)
}
