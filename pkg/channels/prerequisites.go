package channels

import "github.com/dapicom-ai/omnipus/pkg/config"

// This file centralizes the per-channel-type prerequisite checks that guard
// initChannels: each named checker verifies that an enabled channel instance
// has the config fields its factory needs, returning the missing-fields string
// when the instance must be skipped. The checkers are registered in a map at
// init time (data-driven) so initChannels performs a single map lookup instead
// of a per-type switch — adding a new channel's prerequisite is a matter of
// registering a checker here (or in the channel subpackage's init alongside
// RegisterFactory), not editing the initChannels loop.
//
// A channel type with NO registered checker activates without prerequisite
// gating; factory-side validation handles misconfiguration for those.

// prereqTokenRef gates channels whose only hard requirement is a token ref
// (the secret-store pointer resolved to a plaintext token at boot).
func prereqTokenRef(inst config.ChannelInstanceConfig) (string, bool) {
	if inst.TokenRef == "" {
		return "token", false
	}
	return "", true
}

func prereqDiscord(inst config.ChannelInstanceConfig) (string, bool) {
	return prereqTokenRef(inst)
}

func prereqWeixin(inst config.ChannelInstanceConfig) (string, bool) {
	return prereqTokenRef(inst)
}

func prereqTelegram(inst config.ChannelInstanceConfig) (string, bool) {
	return prereqTokenRef(inst)
}

func prereqDingTalk(inst config.ChannelInstanceConfig) (string, bool) {
	if inst.ClientID == "" {
		return "client_id", false
	}
	return "", true
}

func prereqSlack(inst config.ChannelInstanceConfig) (string, bool) {
	if inst.BotTokenRef == "" {
		return "bot_token", false
	}
	return "", true
}

func prereqMatrix(inst config.ChannelInstanceConfig) (string, bool) {
	// Validate THIS instance's matrix config (the instance map key may differ
	// from the "matrix" type key — a hardcoded m.config.Channels["matrix"]
	// lookup would validate the wrong/absent instance and mis-gate a
	// correctly-named one).
	if inst.Homeserver == "" || inst.UserID == "" || inst.AccessTokenRef == "" {
		return "homeserver, user_id, access_token", false
	}
	return "", true
}

func prereqLINE(inst config.ChannelInstanceConfig) (string, bool) {
	if inst.ChannelAccessTokenRef == "" {
		return "channel_access_token", false
	}
	return "", true
}

func prereqWeCom(inst config.ChannelInstanceConfig) (string, bool) {
	if inst.BotID == "" || inst.SecretRef == "" {
		return "bot_id, secret", false
	}
	return "", true
}

func prereqIRC(inst config.ChannelInstanceConfig) (string, bool) {
	if inst.Server == "" {
		return "server", false
	}
	return "", true
}

func prereqGoogleChat(inst config.ChannelInstanceConfig) (string, bool) {
	gc := inst
	if gc.WebhookURLRef == "" &&
		gc.ServiceAccountJSONRef == "" &&
		gc.WebhookURL.String() == "" &&
		gc.ServiceAccountJSON.String() == "" &&
		gc.ServiceAccountFile == "" {
		return "webhook_url, service_account_json, or service_account_file", false
	}
	return "", true
}

func init() {
	// Register per-type prerequisite checkers (data-driven replacement for the
	// former per-type switch in initChannels). A type not listed here activates
	// without prerequisite gating.
	for channelType, checker := range map[string]PrerequisiteChecker{
		"telegram":    prereqTelegram,
		"discord":     prereqDiscord,
		"dingtalk":    prereqDingTalk,
		"slack":       prereqSlack,
		"matrix":      prereqMatrix,
		"line":        prereqLINE,
		"wecom":       prereqWeCom,
		"weixin":      prereqWeixin,
		"irc":         prereqIRC,
		"google-chat": prereqGoogleChat,
	} {
		RegisterPrerequisite(channelType, checker)
	}
}
