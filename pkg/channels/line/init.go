package line

import (
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/channels"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
)

func init() {
	channels.RegisterFactory(
		"line",
		func(cfg *config.Config, secrets credentials.SecretBundle, b *bus.MessageBus) (channels.Channel, error) {
			inst := cfg.Channels["line"]
			return NewLINEChannel(config.InstanceToLINE(inst), secrets, b)
		},
	)
}
