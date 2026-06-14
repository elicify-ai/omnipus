package irc

import (
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/channels"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
)

func init() {
	channels.RegisterFactory(
		"irc",
		func(cfg *config.Config, secrets credentials.SecretBundle, b *bus.MessageBus) (channels.Channel, error) {
			inst := cfg.Channels["irc"]
			if !inst.Enabled {
				return nil, nil
			}
			return NewIRCChannel(config.InstanceToIRC(inst), secrets, b)
		},
	)
}
