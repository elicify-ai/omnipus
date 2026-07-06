package qq

import (
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

func init() {
	channels.RegisterFactory(
		"qq",
		func(cfg *config.Config, instanceID string, secrets credentials.SecretBundle, b *bus.MessageBus) (channels.Channel, error) {
			inst := cfg.Channels[instanceID]
			return NewQQChannel(config.InstanceToQQ(inst), secrets, b)
		},
	)
}
