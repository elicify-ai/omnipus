package channels

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/dapicom-ai/omnipus/pkg/logger"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// Note: hiddenValues and updateKeys were removed. Previously they re-injected *Ref fields
// into hash/config maps because the old SecureString type returned "[NOT_HERE]" from
// MarshalJSON, silently erasing secret refs on JSON round-trips. Now that all secret
// fields are plain strings (*Ref), they survive JSON marshal/unmarshal without any
// special handling.

// configKeyToChannelName maps a channels config JSON key to the channel name that
// initChannels registers in m.channels (and that StartAll iterates). For almost all
// channels the config key IS the registered name, so the map only needs the divergent
// cases. WhatsApp is the one mismatch: its config lives under the "whatsapp" key
// (config.ChannelsConfig.WhatsApp `json:"whatsapp"`), but initChannels registers the
// always-native whatsmeow implementation under "whatsapp_native" (initChannel(
// "whatsapp_native", ...)). Without this remap, a Reload that enables WhatsApp produces
// added=["whatsapp"] while m.channels only has "whatsapp_native" — so the Reload
// added-start loop dereferences a nil channel and crashes the gateway.
//
// Keeping channelHashes / compareChannels / toChannelConfig all keyed by the REGISTERED
// name means StartAll and Reload agree, and m.channels[name] always resolves.
var configKeyToChannelName = map[string]string{
	"whatsapp": "whatsapp_native",
}

// channelNameForConfigKey returns the registered channel name for a channels config
// JSON key, applying configKeyToChannelName. Keys without an entry map to themselves.
func channelNameForConfigKey(configKey string) string {
	if name, ok := configKeyToChannelName[configKey]; ok {
		return name
	}
	return configKey
}

func toChannelHashes(cfg *config.Config) map[string]string {
	result := make(map[string]string)
	ch := cfg.Channels
	marshal, err := json.Marshal(ch)
	if err != nil {
		logger.ErrorCF(
			"channels",
			"toChannelHashes: failed to marshal channel config",
			map[string]any{"error": err.Error()},
		)
		return result
	}
	var channelConfig map[string]map[string]any
	if err := json.Unmarshal(marshal, &channelConfig); err != nil {
		logger.ErrorCF(
			"channels",
			"toChannelHashes: failed to unmarshal channel config",
			map[string]any{"error": err.Error()},
		)
		return result
	}

	for key, value := range channelConfig {
		enabled, _ := value["enabled"].(bool)
		if !enabled {
			continue
		}
		valueBytes, err := json.Marshal(value)
		if err != nil {
			logger.WarnCF("channels", "toChannelHashes: failed to marshal channel config",
				map[string]any{"channel": key, "error": err.Error()})
			valueBytes = []byte("{}")
		}
		hash := md5.Sum(valueBytes)
		// Key the hash map by the REGISTERED channel name, not the raw config key,
		// so compareChannels' added/removed lists (and m.channelHashes) line up with
		// m.channels — which initChannels populates under the registered name. For all
		// channels except WhatsApp these are identical; see channelNameForConfigKey.
		result[channelNameForConfigKey(key)] = hex.EncodeToString(hash[:])
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

func toChannelConfig(cfg *config.Config, list []string) (*config.ChannelsConfig, error) {
	result := &config.ChannelsConfig{}
	ch := cfg.Channels
	marshal, err := json.Marshal(ch)
	if err != nil {
		logger.ErrorCF(
			"channels",
			"toChannelConfig: failed to marshal channel config",
			map[string]any{"error": err.Error()},
		)
		return nil, fmt.Errorf("toChannelConfig: marshal: %w", err)
	}
	var channelConfig map[string]map[string]any
	if unmarshalErr := json.Unmarshal(marshal, &channelConfig); unmarshalErr != nil {
		logger.ErrorCF(
			"channels",
			"toChannelConfig: failed to unmarshal channel config",
			map[string]any{"error": unmarshalErr.Error()},
		)
		return nil, fmt.Errorf("toChannelConfig: unmarshal: %w", unmarshalErr)
	}
	temp := make(map[string]map[string]any, 0)

	for key, value := range channelConfig {
		// `list` holds REGISTERED channel names (the output of toChannelHashes /
		// compareChannels), while `key` is the config JSON key. Translate the key to
		// its registered name before matching so a list entry like "whatsapp_native"
		// still selects the "whatsapp" config block. See channelNameForConfigKey.
		registeredName := channelNameForConfigKey(key)
		found := false
		for _, s := range list {
			if registeredName == s {
				found = true
				break
			}
		}
		chEnabled, _ := value["enabled"].(bool)
		if !found || !chEnabled {
			continue
		}
		temp[key] = value
	}

	marshal, err = json.Marshal(temp)
	if err != nil {
		logger.Errorf("marshal error: %v", err)
		return nil, err
	}
	err = json.Unmarshal(marshal, result)
	if err != nil {
		logger.Errorf("unmarshal error: %v", err)
		return nil, err
	}

	return result, nil
}
