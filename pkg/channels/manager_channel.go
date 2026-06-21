package channels

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/logger"
)

// Note: hiddenValues and updateKeys were removed. Previously they re-injected *Ref fields
// into hash/config maps because the old SecureString type returned "[NOT_HERE]" from
// MarshalJSON, silently erasing secret refs on JSON round-trips. Now that all secret
// fields are plain strings (*Ref), they survive JSON marshal/unmarshal without any
// special handling.

// configKeyToChannelName maps a channel instance id (= map key in Config.Channels)
// to the factory name registered via channels.RegisterFactory. For almost all
// channels the instance id IS the registered factory name; WhatsApp is the one
// exception: instance id "whatsapp" → factory "whatsapp_native".
//
// Keeping channelHashes / compareChannels / toChannelConfig all keyed by the
// REGISTERED name means StartAll and Reload agree, and m.channels[name] resolves.
var configKeyToChannelName = map[string]string{
	"whatsapp": "whatsapp_native",
}

// channelNameForConfigKey returns the registered channel name for a channel
// instance id, applying configKeyToChannelName. Keys without an entry map to
// themselves.
func channelNameForConfigKey(instanceID string) string {
	if name, ok := configKeyToChannelName[instanceID]; ok {
		return name
	}
	return instanceID
}

func toChannelHashes(cfg *config.Config) map[string]string {
	result := make(map[string]string)
	for instanceID, inst := range cfg.Channels {
		if !inst.Enabled {
			continue
		}
		valueBytes, err := json.Marshal(inst)
		if err != nil {
			logger.WarnCF("channels", "toChannelHashes: failed to marshal channel instance",
				map[string]any{"instance_id": instanceID, "error": err.Error()})
			valueBytes = []byte("{}")
		}
		hash := md5.Sum(valueBytes)
		// Key by the REGISTERED factory name so compareChannels and m.channels agree.
		result[channelNameForConfigKey(instanceID)] = hex.EncodeToString(hash[:])
	}
	return result
}

func compareChannels(old, news map[string]string) (added, removed []string) {
	for key, newHash := range news {
		if oldHash, ok := old[key]; ok {
			if newHash != oldHash {
				removed = append(removed, key)
				added = append(added, key)
			}
		} else {
			added = append(added, key)
		}
	}
	for key := range old {
		if _, ok := news[key]; !ok {
			removed = append(removed, key)
		}
	}
	return added, removed
}

// toChannelConfig returns the subset of cfg.Channels whose registered factory
// names appear in list. The returned map is passed to initChannels on hot-reload.
func toChannelConfig(cfg *config.Config, list []string) (map[string]config.ChannelInstanceConfig, error) {
	result := make(map[string]config.ChannelInstanceConfig, len(list))
	for instanceID, inst := range cfg.Channels {
		if !inst.Enabled {
			continue
		}
		registeredName := channelNameForConfigKey(instanceID)
		found := false
		for _, s := range list {
			if registeredName == s {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		result[instanceID] = inst
	}
	return result, nil
}
