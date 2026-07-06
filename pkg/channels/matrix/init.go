// goolm is the pure-Go OLM implementation (replaces libolm which requires CGo).
// Build with: -tags goolm
//go:build goolm

package matrix

import (
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

func init() {
	channels.RegisterFactory(
		"matrix",
		func(cfg *config.Config, instanceID string, secrets credentials.SecretBundle, b *bus.MessageBus) (channels.Channel, error) {
			inst := cfg.Channels[instanceID]
			matrixCfg := config.InstanceToMatrix(inst)
			cryptoDatabasePath := matrixCfg.CryptoDatabasePath
			if cryptoDatabasePath == "" {
				cryptoDatabasePath = filepath.Join(cfg.WorkspacePath(), "matrix")
			}
			return NewMatrixChannel(matrixCfg, secrets, b, cryptoDatabasePath)
		},
	)
}
