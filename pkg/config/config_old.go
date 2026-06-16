// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
)

// CredentialStore is a minimal interface satisfied by credentials.Store.
// Using an interface here avoids a circular import (config → credentials).
// The caller (gateway, CLI commands) supplies the real store at migration time.
type CredentialStore interface {
	// Set stores a named credential value.
	Set(name, value string) error
}

type agentDefaultsV0 struct {
	Workspace                 string         `json:"workspace"                       env:"OMNIPUS_AGENTS_DEFAULTS_WORKSPACE"`
	RestrictToWorkspace       bool           `json:"restrict_to_workspace"           env:"OMNIPUS_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE"`
	AllowReadOutsideWorkspace bool           `json:"allow_read_outside_workspace"    env:"OMNIPUS_AGENTS_DEFAULTS_ALLOW_READ_OUTSIDE_WORKSPACE"`
	Provider                  string         `json:"provider"                        env:"OMNIPUS_AGENTS_DEFAULTS_PROVIDER"`
	ModelName                 string         `json:"model_name,omitempty"            env:"OMNIPUS_AGENTS_DEFAULTS_MODEL_NAME"`
	Model                     string         `json:"model"                           env:"OMNIPUS_AGENTS_DEFAULTS_MODEL"` // Deprecated: use model_name instead
	ModelFallbacks            []string       `json:"model_fallbacks,omitempty"`
	ImageModel                string         `json:"image_model,omitempty"           env:"OMNIPUS_AGENTS_DEFAULTS_IMAGE_MODEL"`
	ImageModelFallbacks       []string       `json:"image_model_fallbacks,omitempty"`
	MaxTokens                 int            `json:"max_tokens"                      env:"OMNIPUS_AGENTS_DEFAULTS_MAX_TOKENS"`
	Temperature               *float64       `json:"temperature,omitempty"           env:"OMNIPUS_AGENTS_DEFAULTS_TEMPERATURE"`
	MaxToolIterations         int            `json:"max_tool_iterations"             env:"OMNIPUS_AGENTS_DEFAULTS_MAX_TOOL_ITERATIONS"`
	SummarizeMessageThreshold int            `json:"summarize_message_threshold"     env:"OMNIPUS_AGENTS_DEFAULTS_SUMMARIZE_MESSAGE_THRESHOLD"`
	SummarizeTokenPercent     int            `json:"summarize_token_percent"         env:"OMNIPUS_AGENTS_DEFAULTS_SUMMARIZE_TOKEN_PERCENT"`
	MaxMediaSize              int            `json:"max_media_size,omitempty"        env:"OMNIPUS_AGENTS_DEFAULTS_MAX_MEDIA_SIZE"`
	Routing                   *RoutingConfig `json:"routing,omitempty"`
}

// GetModelName returns the effective model name for the agent defaults.
// It prefers the new "model_name" field but falls back to "model" for backward compatibility.
func (d *agentDefaultsV0) GetModelName() string {
	if d.ModelName != "" {
		return d.ModelName
	}
	return d.Model
}

type agentsConfigV0 struct {
	Defaults agentDefaultsV0 `json:"defaults"`
	List     []AgentConfig   `json:"list,omitempty"`
}

// configV0 represents the config structure before versioning was introduced.
// This struct is used for loading legacy config files (version 0).
// It is unexported since it's only used internally for migration.
type configV0 struct {
	Agents    agentsConfigV0    `json:"agents"`
	Bindings  []AgentBinding    `json:"bindings,omitempty"`
	Session   SessionConfig     `json:"session,omitempty"`
	Channels  channelsConfigV0  `json:"channels"`
	Providers providersConfigV0 `json:"providers,omitempty"`
	ModelList []modelConfigV0   `json:"model_list"`
	Gateway   GatewayConfig     `json:"gateway"`
	Tools     toolsConfigV0     `json:"tools"`
	Heartbeat HeartbeatConfig   `json:"heartbeat"`
	Devices   DevicesConfig     `json:"devices"`
}

type toolsConfigV0 struct {
	AllowReadPaths  []string            `json:"allow_read_paths"  env:"OMNIPUS_TOOLS_ALLOW_READ_PATHS"`
	AllowWritePaths []string            `json:"allow_write_paths" env:"OMNIPUS_TOOLS_ALLOW_WRITE_PATHS"`
	Web             webToolsConfigV0    `json:"web"`
	Cron            CronToolsConfig     `json:"cron"`
	Exec            ExecConfig          `json:"exec"`
	Skills          skillsToolsConfigV0 `json:"skills"`
	MediaCleanup    MediaCleanupConfig  `json:"media_cleanup"`
	MCP             MCPConfig           `json:"mcp"`
	AppendFile      ToolConfig          `json:"append_file"                                             envPrefix:"OMNIPUS_TOOLS_APPEND_FILE_"`
	EditFile        ToolConfig          `json:"edit_file"                                               envPrefix:"OMNIPUS_TOOLS_EDIT_FILE_"`
	FindSkills      ToolConfig          `json:"find_skills"                                             envPrefix:"OMNIPUS_TOOLS_FIND_SKILLS_"`
	InstallSkill    ToolConfig          `json:"install_skill"                                           envPrefix:"OMNIPUS_TOOLS_INSTALL_SKILL_"`
	ListDir         ToolConfig          `json:"list_dir"                                                envPrefix:"OMNIPUS_TOOLS_LIST_DIR_"`
	Message         ToolConfig          `json:"message"                                                 envPrefix:"OMNIPUS_TOOLS_MESSAGE_"`
	ReadFile        ReadFileToolConfig  `json:"read_file"                                               envPrefix:"OMNIPUS_TOOLS_READ_FILE_"`
	SendFile        ToolConfig          `json:"send_file"                                               envPrefix:"OMNIPUS_TOOLS_SEND_FILE_"`
	Spawn           ToolConfig          `json:"spawn"                                                   envPrefix:"OMNIPUS_TOOLS_SPAWN_"`
	SpawnStatus     ToolConfig          `json:"spawn_status"                                            envPrefix:"OMNIPUS_TOOLS_SPAWN_STATUS_"`
	Subagent        ToolConfig          `json:"subagent"                                                envPrefix:"OMNIPUS_TOOLS_SUBAGENT_"`
	WebFetch        ToolConfig          `json:"web_fetch"                                               envPrefix:"OMNIPUS_TOOLS_WEB_FETCH_"`
	WriteFile       ToolConfig          `json:"write_file"                                              envPrefix:"OMNIPUS_TOOLS_WRITE_FILE_"`
}

type channelsConfigV0 struct {
	WhatsApp WhatsAppConfig   `json:"whatsapp"`
	Telegram telegramConfigV0 `json:"telegram"`
	Feishu   feishuConfigV0   `json:"feishu"`
	Discord  discordConfigV0  `json:"discord"`

	Weixin   weixinConfigV0   `json:"weixin"`
	QQ       qqConfigV0       `json:"qq"`
	DingTalk dingtalkConfigV0 `json:"dingtalk"`
	Slack    slackConfigV0    `json:"slack"`
	Matrix   matrixConfigV0   `json:"matrix"`
	LINE     lineConfigV0     `json:"line"`
	WeCom    wecomConfigV0    `json:"wecom"    envPrefix:"OMNIPUS_CHANNELS_WECOM_"`
	IRC      ircConfigV0      `json:"irc"`
}

// ToChannelsConfig converts the v0 typed channel config into the new
// map[string]ChannelInstanceConfig format. Each channel type is seeded as an
// instance keyed by its type name (cap-1 invariant holds since v0 had one
// instance per type by definition).
func (v *channelsConfigV0) ToChannelsConfig() map[string]ChannelInstanceConfig {
	m := make(map[string]ChannelInstanceConfig)

	wa := v.WhatsApp
	m["whatsapp"] = ChannelInstanceConfig{
		Type:             "whatsapp",
		Enabled:          wa.Enabled,
		SessionStorePath: wa.SessionStorePath,
		AllowFrom:        wa.AllowFrom,
	}

	tg := v.Telegram.ToTelegramConfig()
	m["telegram"] = ChannelInstanceConfig{
		Type:               "telegram",
		Enabled:            tg.Enabled,
		TokenRef:           tg.TokenRef,
		BaseURL:            tg.BaseURL,
		Proxy:              tg.Proxy,
		AllowFrom:          tg.AllowFrom,
		GroupTrigger:       tg.GroupTrigger,
		Typing:             tg.Typing,
		Placeholder:        tg.Placeholder,
		Streaming:          tg.Streaming,
		ReasoningChannelID: tg.ReasoningChannelID,
		UseMarkdownV2:      tg.UseMarkdownV2,
	}

	fs := v.Feishu.ToFeishuConfig()
	m["feishu"] = ChannelInstanceConfig{
		Type:                 "feishu",
		Enabled:              fs.Enabled,
		AppID:                fs.AppID,
		AppSecretRef:         fs.AppSecretRef,
		EncryptKeyRef:        fs.EncryptKeyRef,
		VerificationTokenRef: fs.VerificationTokenRef,
		AllowFrom:            fs.AllowFrom,
		GroupTrigger:         fs.GroupTrigger,
		Placeholder:          fs.Placeholder,
		ReasoningChannelID:   fs.ReasoningChannelID,
		RandomReactionEmoji:  fs.RandomReactionEmoji,
		IsLark:               fs.IsLark,
	}

	dc := v.Discord.ToDiscordConfig()
	m["discord"] = ChannelInstanceConfig{
		Type:               "discord",
		Enabled:            dc.Enabled,
		TokenRef:           dc.TokenRef,
		Proxy:              dc.Proxy,
		AllowFrom:          dc.AllowFrom,
		MentionOnly:        dc.MentionOnly,
		GroupTrigger:       dc.GroupTrigger,
		Typing:             dc.Typing,
		Placeholder:        dc.Placeholder,
		ReasoningChannelID: dc.ReasoningChannelID,
	}

	qq := v.QQ.ToQQConfig()
	m["qq"] = ChannelInstanceConfig{
		Type:                 "qq",
		Enabled:              qq.Enabled,
		AppID:                qq.AppID,
		AppSecretRef:         qq.AppSecretRef,
		AllowFrom:            qq.AllowFrom,
		GroupTrigger:         qq.GroupTrigger,
		MaxMessageLength:     qq.MaxMessageLength,
		MaxBase64FileSizeMiB: qq.MaxBase64FileSizeMiB,
		SendMarkdown:         qq.SendMarkdown,
		ReasoningChannelID:   qq.ReasoningChannelID,
	}

	wx := v.Weixin.ToWeiXinConfig()
	m["weixin"] = ChannelInstanceConfig{
		Type:               "weixin",
		Enabled:            wx.Enabled,
		TokenRef:           wx.TokenRef,
		AccountID:          wx.AccountID,
		BaseURL:            wx.BaseURL,
		CDNBaseURL:         wx.CDNBaseURL,
		Proxy:              wx.Proxy,
		AllowFrom:          wx.AllowFrom,
		ReasoningChannelID: wx.ReasoningChannelID,
	}

	dt := v.DingTalk.ToDingTalkConfig()
	m["dingtalk"] = ChannelInstanceConfig{
		Type:               "dingtalk",
		Enabled:            dt.Enabled,
		ClientID:           dt.ClientID,
		ClientSecretRef:    dt.ClientSecretRef,
		AllowFrom:          dt.AllowFrom,
		GroupTrigger:       dt.GroupTrigger,
		ReasoningChannelID: dt.ReasoningChannelID,
	}

	sl := v.Slack.ToSlackConfig()
	m["slack"] = ChannelInstanceConfig{
		Type:               "slack",
		Enabled:            sl.Enabled,
		BotTokenRef:        sl.BotTokenRef,
		AppTokenRef:        sl.AppTokenRef,
		AllowFrom:          sl.AllowFrom,
		GroupTrigger:       sl.GroupTrigger,
		Typing:             sl.Typing,
		Placeholder:        sl.Placeholder,
		ReasoningChannelID: sl.ReasoningChannelID,
	}

	mx := v.Matrix.ToMatrixConfig()
	m["matrix"] = ChannelInstanceConfig{
		Type:                "matrix",
		Enabled:             mx.Enabled,
		Homeserver:          mx.Homeserver,
		UserID:              mx.UserID,
		AccessTokenRef:      mx.AccessTokenRef,
		DeviceID:            mx.DeviceID,
		JoinOnInvite:        mx.JoinOnInvite,
		MessageFormat:       mx.MessageFormat,
		AllowFrom:           mx.AllowFrom,
		GroupTrigger:        mx.GroupTrigger,
		Placeholder:         mx.Placeholder,
		ReasoningChannelID:  mx.ReasoningChannelID,
		CryptoDatabasePath:  mx.CryptoDatabasePath,
		CryptoPassphraseRef: mx.CryptoPassphraseRef,
	}

	ln := v.LINE.ToLINEConfig()
	m["line"] = ChannelInstanceConfig{
		Type:                  "line",
		Enabled:               ln.Enabled,
		ChannelSecretRef:      ln.ChannelSecretRef,
		ChannelAccessTokenRef: ln.ChannelAccessTokenRef,
		WebhookHost:           ln.WebhookHost,
		WebhookPort:           ln.WebhookPort,
		WebhookPath:           ln.WebhookPath,
		AllowFrom:             ln.AllowFrom,
		GroupTrigger:          ln.GroupTrigger,
		Typing:                ln.Typing,
		Placeholder:           ln.Placeholder,
		ReasoningChannelID:    ln.ReasoningChannelID,
	}

	wc := v.WeCom.ToWeComConfig()
	m["wecom"] = ChannelInstanceConfig{
		Type:                "wecom",
		Enabled:             wc.Enabled,
		BotID:               wc.BotID,
		SecretRef:           wc.SecretRef,
		WebSocketURL:        wc.WebSocketURL,
		SendThinkingMessage: wc.SendThinkingMessage,
		AllowFrom:           wc.AllowFrom,
		ReasoningChannelID:  wc.ReasoningChannelID,
	}

	irc := v.IRC.ToIRCConfig()
	m["irc"] = ChannelInstanceConfig{
		Type:                "irc",
		Enabled:             irc.Enabled,
		Server:              irc.Server,
		TLS:                 irc.TLS,
		Nick:                irc.Nick,
		IRCUser:             irc.User,
		RealName:            irc.RealName,
		PasswordRef:         irc.PasswordRef,
		NickServPasswordRef: irc.NickServPasswordRef,
		SASLUser:            irc.SASLUser,
		SASLPasswordRef:     irc.SASLPasswordRef,
		IRCChannels:         irc.Channels,
		RequestCaps:         irc.RequestCaps,
		AllowFrom:           irc.AllowFrom,
		GroupTrigger:        irc.GroupTrigger,
		Typing:              irc.Typing,
		ReasoningChannelID:  irc.ReasoningChannelID,
	}

	return m
}

type qqConfigV0 struct {
	Enabled              bool                `json:"enabled"                  env:"OMNIPUS_CHANNELS_QQ_ENABLED"`
	AppID                string              `json:"app_id"                   env:"OMNIPUS_CHANNELS_QQ_APP_ID"`
	AppSecret            string              `json:"app_secret"               env:"OMNIPUS_CHANNELS_QQ_APP_SECRET"`
	AllowFrom            FlexibleStringSlice `json:"allow_from"               env:"OMNIPUS_CHANNELS_QQ_ALLOW_FROM"`
	GroupTrigger         GroupTriggerConfig  `json:"group_trigger,omitempty"`
	MaxMessageLength     int                 `json:"max_message_length"       env:"OMNIPUS_CHANNELS_QQ_MAX_MESSAGE_LENGTH"`
	MaxBase64FileSizeMiB int64               `json:"max_base64_file_size_mib" env:"OMNIPUS_CHANNELS_QQ_MAX_BASE64_FILE_SIZE_MIB"`
	SendMarkdown         bool                `json:"send_markdown"            env:"OMNIPUS_CHANNELS_QQ_SEND_MARKDOWN"`
	ReasoningChannelID   string              `json:"reasoning_channel_id"     env:"OMNIPUS_CHANNELS_QQ_REASONING_CHANNEL_ID"`
}

func (v *qqConfigV0) ToQQConfig() QQConfig {
	return QQConfig{
		Enabled:              v.Enabled,
		AppID:                v.AppID,
		AllowFrom:            v.AllowFrom,
		GroupTrigger:         v.GroupTrigger,
		MaxMessageLength:     v.MaxMessageLength,
		MaxBase64FileSizeMiB: v.MaxBase64FileSizeMiB,
		SendMarkdown:         v.SendMarkdown,
		ReasoningChannelID:   v.ReasoningChannelID,
	}
}

type telegramConfigV0 struct {
	Enabled            bool                `json:"enabled"                 env:"OMNIPUS_CHANNELS_TELEGRAM_ENABLED"`
	Token              string              `json:"token"                   env:"OMNIPUS_CHANNELS_TELEGRAM_TOKEN"`
	BaseURL            string              `json:"base_url"                env:"OMNIPUS_CHANNELS_TELEGRAM_BASE_URL"`
	Proxy              string              `json:"proxy"                   env:"OMNIPUS_CHANNELS_TELEGRAM_PROXY"`
	AllowFrom          FlexibleStringSlice `json:"allow_from"              env:"OMNIPUS_CHANNELS_TELEGRAM_ALLOW_FROM"`
	GroupTrigger       GroupTriggerConfig  `json:"group_trigger,omitempty"`
	Typing             TypingConfig        `json:"typing,omitempty"`
	Placeholder        PlaceholderConfig   `json:"placeholder,omitempty"`
	ReasoningChannelID string              `json:"reasoning_channel_id"    env:"OMNIPUS_CHANNELS_TELEGRAM_REASONING_CHANNEL_ID"`
	UseMarkdownV2      bool                `json:"use_markdown_v2"         env:"OMNIPUS_CHANNELS_TELEGRAM_USE_MARKDOWN_V2"`
}

func (v *telegramConfigV0) ToTelegramConfig() TelegramConfig {
	return TelegramConfig{
		Enabled:            v.Enabled,
		BaseURL:            v.BaseURL,
		Proxy:              v.Proxy,
		AllowFrom:          v.AllowFrom,
		GroupTrigger:       v.GroupTrigger,
		Typing:             v.Typing,
		Placeholder:        v.Placeholder,
		ReasoningChannelID: v.ReasoningChannelID,
		UseMarkdownV2:      v.UseMarkdownV2,
	}
}

type feishuConfigV0 struct {
	Enabled             bool                `json:"enabled"                 env:"OMNIPUS_CHANNELS_FEISHU_ENABLED"`
	AppID               string              `json:"app_id"                  env:"OMNIPUS_CHANNELS_FEISHU_APP_ID"`
	AppSecret           string              `json:"app_secret"              env:"OMNIPUS_CHANNELS_FEISHU_APP_SECRET"`
	EncryptKey          string              `json:"encrypt_key"             env:"OMNIPUS_CHANNELS_FEISHU_ENCRYPT_KEY"`
	VerificationToken   string              `json:"verification_token"      env:"OMNIPUS_CHANNELS_FEISHU_VERIFICATION_TOKEN"`
	AllowFrom           FlexibleStringSlice `json:"allow_from"              env:"OMNIPUS_CHANNELS_FEISHU_ALLOW_FROM"`
	GroupTrigger        GroupTriggerConfig  `json:"group_trigger,omitempty"`
	Placeholder         PlaceholderConfig   `json:"placeholder,omitempty"`
	ReasoningChannelID  string              `json:"reasoning_channel_id"    env:"OMNIPUS_CHANNELS_FEISHU_REASONING_CHANNEL_ID"`
	RandomReactionEmoji FlexibleStringSlice `json:"random_reaction_emoji"   env:"OMNIPUS_CHANNELS_FEISHU_RANDOM_REACTION_EMOJI"`
	IsLark              bool                `json:"is_lark"                 env:"OMNIPUS_CHANNELS_FEISHU_IS_LARK"`
}

func (v *feishuConfigV0) ToFeishuConfig() FeishuConfig {
	return FeishuConfig{
		Enabled:            v.Enabled,
		AppID:              v.AppID,
		AllowFrom:          v.AllowFrom,
		GroupTrigger:       v.GroupTrigger,
		Placeholder:        v.Placeholder,
		ReasoningChannelID: v.ReasoningChannelID,
	}
}

type discordConfigV0 struct {
	Enabled            bool                `json:"enabled"                 env:"OMNIPUS_CHANNELS_DISCORD_ENABLED"`
	Token              string              `json:"token"                   env:"OMNIPUS_CHANNELS_DISCORD_TOKEN"`
	Proxy              string              `json:"proxy"                   env:"OMNIPUS_CHANNELS_DISCORD_PROXY"`
	AllowFrom          FlexibleStringSlice `json:"allow_from"              env:"OMNIPUS_CHANNELS_DISCORD_ALLOW_FROM"`
	MentionOnly        bool                `json:"mention_only"            env:"OMNIPUS_CHANNELS_DISCORD_MENTION_ONLY"`
	GroupTrigger       GroupTriggerConfig  `json:"group_trigger,omitempty"`
	Typing             TypingConfig        `json:"typing,omitempty"`
	Placeholder        PlaceholderConfig   `json:"placeholder,omitempty"`
	ReasoningChannelID string              `json:"reasoning_channel_id"    env:"OMNIPUS_CHANNELS_DISCORD_REASONING_CHANNEL_ID"`
}

func (v *discordConfigV0) ToDiscordConfig() DiscordConfig {
	return DiscordConfig{
		Enabled:            v.Enabled,
		Proxy:              v.Proxy,
		AllowFrom:          v.AllowFrom,
		MentionOnly:        v.MentionOnly,
		GroupTrigger:       v.GroupTrigger,
		Typing:             v.Typing,
		Placeholder:        v.Placeholder,
		ReasoningChannelID: v.ReasoningChannelID,
	}
}

type dingtalkConfigV0 struct {
	Enabled            bool                `json:"enabled"                 env:"OMNIPUS_CHANNELS_DINGTALK_ENABLED"`
	ClientID           string              `json:"client_id"               env:"OMNIPUS_CHANNELS_DINGTALK_CLIENT_ID"`
	ClientSecret       string              `json:"client_secret"           env:"OMNIPUS_CHANNELS_DINGTALK_CLIENT_SECRET"`
	AllowFrom          FlexibleStringSlice `json:"allow_from"              env:"OMNIPUS_CHANNELS_DINGTALK_ALLOW_FROM"`
	GroupTrigger       GroupTriggerConfig  `json:"group_trigger,omitempty"`
	ReasoningChannelID string              `json:"reasoning_channel_id"    env:"OMNIPUS_CHANNELS_DINGTALK_REASONING_CHANNEL_ID"`
}

func (v *dingtalkConfigV0) ToDingTalkConfig() DingTalkConfig {
	return DingTalkConfig{
		Enabled:            v.Enabled,
		ClientID:           v.ClientID,
		AllowFrom:          v.AllowFrom,
		GroupTrigger:       v.GroupTrigger,
		ReasoningChannelID: v.ReasoningChannelID,
	}
}

type slackConfigV0 struct {
	Enabled            bool                `json:"enabled"                 env:"OMNIPUS_CHANNELS_SLACK_ENABLED"`
	BotToken           string              `json:"bot_token"               env:"OMNIPUS_CHANNELS_SLACK_BOT_TOKEN"`
	AppToken           string              `json:"app_token"               env:"OMNIPUS_CHANNELS_SLACK_APP_TOKEN"`
	AllowFrom          FlexibleStringSlice `json:"allow_from"              env:"OMNIPUS_CHANNELS_SLACK_ALLOW_FROM"`
	GroupTrigger       GroupTriggerConfig  `json:"group_trigger,omitempty"`
	Typing             TypingConfig        `json:"typing,omitempty"`
	Placeholder        PlaceholderConfig   `json:"placeholder,omitempty"`
	ReasoningChannelID string              `json:"reasoning_channel_id"    env:"OMNIPUS_CHANNELS_SLACK_REASONING_CHANNEL_ID"`
}

func (v *slackConfigV0) ToSlackConfig() SlackConfig {
	return SlackConfig{
		Enabled:            v.Enabled,
		AllowFrom:          v.AllowFrom,
		GroupTrigger:       v.GroupTrigger,
		Typing:             v.Typing,
		Placeholder:        v.Placeholder,
		ReasoningChannelID: v.ReasoningChannelID,
	}
}

type matrixConfigV0 struct {
	Enabled            bool                `json:"enabled"                  env:"OMNIPUS_CHANNELS_MATRIX_ENABLED"`
	Homeserver         string              `json:"homeserver"               env:"OMNIPUS_CHANNELS_MATRIX_HOMESERVER"`
	UserID             string              `json:"user_id"                  env:"OMNIPUS_CHANNELS_MATRIX_USER_ID"`
	AccessToken        string              `json:"access_token"             env:"OMNIPUS_CHANNELS_MATRIX_ACCESS_TOKEN"`
	DeviceID           string              `json:"device_id,omitempty"      env:"OMNIPUS_CHANNELS_MATRIX_DEVICE_ID"`
	JoinOnInvite       bool                `json:"join_on_invite"           env:"OMNIPUS_CHANNELS_MATRIX_JOIN_ON_INVITE"`
	MessageFormat      string              `json:"message_format,omitempty" env:"OMNIPUS_CHANNELS_MATRIX_MESSAGE_FORMAT"`
	AllowFrom          FlexibleStringSlice `json:"allow_from"               env:"OMNIPUS_CHANNELS_MATRIX_ALLOW_FROM"`
	GroupTrigger       GroupTriggerConfig  `json:"group_trigger,omitempty"`
	Placeholder        PlaceholderConfig   `json:"placeholder,omitempty"`
	ReasoningChannelID string              `json:"reasoning_channel_id"     env:"OMNIPUS_CHANNELS_MATRIX_REASONING_CHANNEL_ID"`
}

func (v *matrixConfigV0) ToMatrixConfig() MatrixConfig {
	return MatrixConfig{
		Enabled:            v.Enabled,
		Homeserver:         v.Homeserver,
		UserID:             v.UserID,
		DeviceID:           v.DeviceID,
		JoinOnInvite:       v.JoinOnInvite,
		MessageFormat:      v.MessageFormat,
		AllowFrom:          v.AllowFrom,
		GroupTrigger:       v.GroupTrigger,
		Placeholder:        v.Placeholder,
		ReasoningChannelID: v.ReasoningChannelID,
	}
}

type lineConfigV0 struct {
	Enabled            bool                `json:"enabled"                 env:"OMNIPUS_CHANNELS_LINE_ENABLED"`
	ChannelSecret      string              `json:"channel_secret"          env:"OMNIPUS_CHANNELS_LINE_CHANNEL_SECRET"`
	ChannelAccessToken string              `json:"channel_access_token"    env:"OMNIPUS_CHANNELS_LINE_CHANNEL_ACCESS_TOKEN"`
	WebhookHost        string              `json:"webhook_host"            env:"OMNIPUS_CHANNELS_LINE_WEBHOOK_HOST"`
	WebhookPort        int                 `json:"webhook_port"            env:"OMNIPUS_CHANNELS_LINE_WEBHOOK_PORT"`
	WebhookPath        string              `json:"webhook_path"            env:"OMNIPUS_CHANNELS_LINE_WEBHOOK_PATH"`
	AllowFrom          FlexibleStringSlice `json:"allow_from"              env:"OMNIPUS_CHANNELS_LINE_ALLOW_FROM"`
	GroupTrigger       GroupTriggerConfig  `json:"group_trigger,omitempty"`
	Typing             TypingConfig        `json:"typing,omitempty"`
	Placeholder        PlaceholderConfig   `json:"placeholder,omitempty"`
	ReasoningChannelID string              `json:"reasoning_channel_id"    env:"OMNIPUS_CHANNELS_LINE_REASONING_CHANNEL_ID"`
}

func (v *lineConfigV0) ToLINEConfig() LINEConfig {
	return LINEConfig{
		Enabled:            v.Enabled,
		WebhookHost:        v.WebhookHost,
		WebhookPort:        v.WebhookPort,
		WebhookPath:        v.WebhookPath,
		AllowFrom:          v.AllowFrom,
		GroupTrigger:       v.GroupTrigger,
		Typing:             v.Typing,
		Placeholder:        v.Placeholder,
		ReasoningChannelID: v.ReasoningChannelID,
	}
}

type wecomConfigV0 struct {
	Enabled             bool                        `json:"enabled"                    env:"ENABLED"`
	BotID               string                      `json:"bot_id"                     env:"BOT_ID"`
	Secret              string                      `json:"secret"                     env:"SECRET"`
	WebSocketURL        string                      `json:"websocket_url,omitempty"    env:"WEBSOCKET_URL"`
	SendThinkingMessage bool                        `json:"send_thinking_message"      env:"SEND_THINKING_MESSAGE"`
	DMPolicy            string                      `json:"dm_policy,omitempty"        env:"DM_POLICY"`
	AllowFrom           FlexibleStringSlice         `json:"allow_from"                 env:"ALLOW_FROM"`
	GroupPolicy         string                      `json:"group_policy,omitempty"     env:"GROUP_POLICY"`
	GroupAllowFrom      FlexibleStringSlice         `json:"group_allow_from,omitempty" env:"GROUP_ALLOW_FROM"`
	Groups              map[string]WeComGroupConfig `json:"groups,omitempty"`
	ReasoningChannelID  string                      `json:"reasoning_channel_id"       env:"REASONING_CHANNEL_ID"`
}

func (v *wecomConfigV0) ToWeComConfig() WeComConfig {
	return WeComConfig{
		Enabled:             v.Enabled,
		BotID:               v.BotID,
		WebSocketURL:        v.WebSocketURL,
		SendThinkingMessage: v.SendThinkingMessage,
		AllowFrom:           v.AllowFrom,
		ReasoningChannelID:  v.ReasoningChannelID,
	}
}

type weixinConfigV0 struct {
	Enabled            bool                `json:"enabled"              env:"OMNIPUS_CHANNELS_WEIXIN_ENABLED"`
	Token              string              `json:"token"                env:"OMNIPUS_CHANNELS_WEIXIN_TOKEN"`
	BaseURL            string              `json:"base_url"             env:"OMNIPUS_CHANNELS_WEIXIN_BASE_URL"`
	CDNBaseURL         string              `json:"cdn_base_url"         env:"OMNIPUS_CHANNELS_WEIXIN_CDN_BASE_URL"`
	Proxy              string              `json:"proxy"                env:"OMNIPUS_CHANNELS_WEIXIN_PROXY"`
	AllowFrom          FlexibleStringSlice `json:"allow_from"           env:"OMNIPUS_CHANNELS_WEIXIN_ALLOW_FROM"`
	ReasoningChannelID string              `json:"reasoning_channel_id" env:"OMNIPUS_CHANNELS_WEIXIN_REASONING_CHANNEL_ID"`
}

func (v *weixinConfigV0) ToWeiXinConfig() WeixinConfig {
	return WeixinConfig{
		Enabled:            v.Enabled,
		BaseURL:            v.BaseURL,
		CDNBaseURL:         v.CDNBaseURL,
		Proxy:              v.Proxy,
		AllowFrom:          v.AllowFrom,
		ReasoningChannelID: v.ReasoningChannelID,
	}
}

type ircConfigV0 struct {
	Enabled            bool                `json:"enabled"                 env:"OMNIPUS_CHANNELS_IRC_ENABLED"`
	Server             string              `json:"server"                  env:"OMNIPUS_CHANNELS_IRC_SERVER"`
	TLS                bool                `json:"tls"                     env:"OMNIPUS_CHANNELS_IRC_TLS"`
	Nick               string              `json:"nick"                    env:"OMNIPUS_CHANNELS_IRC_NICK"`
	User               string              `json:"user,omitempty"          env:"OMNIPUS_CHANNELS_IRC_USER"`
	RealName           string              `json:"real_name,omitempty"     env:"OMNIPUS_CHANNELS_IRC_REAL_NAME"`
	Password           string              `json:"password"                env:"OMNIPUS_CHANNELS_IRC_PASSWORD"`
	NickServPassword   string              `json:"nickserv_password"       env:"OMNIPUS_CHANNELS_IRC_NICKSERV_PASSWORD"`
	SASLUser           string              `json:"sasl_user"               env:"OMNIPUS_CHANNELS_IRC_SASL_USER"`
	SASLPassword       string              `json:"sasl_password"           env:"OMNIPUS_CHANNELS_IRC_SASL_PASSWORD"`
	Channels           FlexibleStringSlice `json:"channels"                env:"OMNIPUS_CHANNELS_IRC_CHANNELS"`
	RequestCaps        FlexibleStringSlice `json:"request_caps,omitempty"  env:"OMNIPUS_CHANNELS_IRC_REQUEST_CAPS"`
	AllowFrom          FlexibleStringSlice `json:"allow_from"              env:"OMNIPUS_CHANNELS_IRC_ALLOW_FROM"`
	GroupTrigger       GroupTriggerConfig  `json:"group_trigger,omitempty"`
	Typing             TypingConfig        `json:"typing,omitempty"`
	ReasoningChannelID string              `json:"reasoning_channel_id"    env:"OMNIPUS_CHANNELS_IRC_REASONING_CHANNEL_ID"`
}

func (v *ircConfigV0) ToIRCConfig() IRCConfig {
	return IRCConfig{
		Enabled:            v.Enabled,
		Server:             v.Server,
		TLS:                v.TLS,
		Nick:               v.Nick,
		User:               v.User,
		RealName:           v.RealName,
		SASLUser:           v.SASLUser,
		Channels:           v.Channels,
		RequestCaps:        v.RequestCaps,
		AllowFrom:          v.AllowFrom,
		GroupTrigger:       v.GroupTrigger,
		Typing:             v.Typing,
		ReasoningChannelID: v.ReasoningChannelID,
	}
}

type providersConfigV0 struct {
	Anthropic     providerConfigV0       `json:"anthropic"`
	OpenAI        openAIProviderConfigV0 `json:"openai"`
	LiteLLM       providerConfigV0       `json:"litellm"`
	OpenRouter    providerConfigV0       `json:"openrouter"`
	Groq          providerConfigV0       `json:"groq"`
	Zhipu         providerConfigV0       `json:"zhipu"`
	VLLM          providerConfigV0       `json:"vllm"`
	Gemini        providerConfigV0       `json:"gemini"`
	Nvidia        providerConfigV0       `json:"nvidia"`
	Ollama        providerConfigV0       `json:"ollama"`
	Moonshot      providerConfigV0       `json:"moonshot"`
	ShengSuanYun  providerConfigV0       `json:"shengsuanyun"`
	DeepSeek      providerConfigV0       `json:"deepseek"`
	Cerebras      providerConfigV0       `json:"cerebras"`
	Vivgrid       providerConfigV0       `json:"vivgrid"`
	VolcEngine    providerConfigV0       `json:"volcengine"`
	GitHubCopilot providerConfigV0       `json:"github_copilot"`
	Antigravity   providerConfigV0       `json:"antigravity"`
	Qwen          providerConfigV0       `json:"qwen"`
	Mistral       providerConfigV0       `json:"mistral"`
	Avian         providerConfigV0       `json:"avian"`
	Minimax       providerConfigV0       `json:"minimax"`
	LongCat       providerConfigV0       `json:"longcat"`
	ModelScope    providerConfigV0       `json:"modelscope"`
	Novita        providerConfigV0       `json:"novita"`
}

// IsEmpty checks if all provider configs are empty (no API keys or API bases set)
// Note: WebSearch is an optimization option and doesn't count as "non-empty"
func (p providersConfigV0) IsEmpty() bool {
	return p.Anthropic.APIKey == "" && p.Anthropic.APIBase == "" &&
		p.OpenAI.APIKey == "" && p.OpenAI.APIBase == "" &&
		p.LiteLLM.APIKey == "" && p.LiteLLM.APIBase == "" &&
		p.OpenRouter.APIKey == "" && p.OpenRouter.APIBase == "" &&
		p.Groq.APIKey == "" && p.Groq.APIBase == "" &&
		p.Zhipu.APIKey == "" && p.Zhipu.APIBase == "" &&
		p.VLLM.APIKey == "" && p.VLLM.APIBase == "" &&
		p.Gemini.APIKey == "" && p.Gemini.APIBase == "" &&
		p.Nvidia.APIKey == "" && p.Nvidia.APIBase == "" &&
		p.Ollama.APIKey == "" && p.Ollama.APIBase == "" &&
		p.Moonshot.APIKey == "" && p.Moonshot.APIBase == "" &&
		p.ShengSuanYun.APIKey == "" && p.ShengSuanYun.APIBase == "" &&
		p.DeepSeek.APIKey == "" && p.DeepSeek.APIBase == "" &&
		p.Cerebras.APIKey == "" && p.Cerebras.APIBase == "" &&
		p.Vivgrid.APIKey == "" && p.Vivgrid.APIBase == "" &&
		p.VolcEngine.APIKey == "" && p.VolcEngine.APIBase == "" &&
		p.GitHubCopilot.APIKey == "" && p.GitHubCopilot.APIBase == "" &&
		p.Antigravity.APIKey == "" && p.Antigravity.APIBase == "" &&
		p.Qwen.APIKey == "" && p.Qwen.APIBase == "" &&
		p.Mistral.APIKey == "" && p.Mistral.APIBase == "" &&
		p.Avian.APIKey == "" && p.Avian.APIBase == "" &&
		p.Minimax.APIKey == "" && p.Minimax.APIBase == "" &&
		p.LongCat.APIKey == "" && p.LongCat.APIBase == "" &&
		p.ModelScope.APIKey == "" && p.ModelScope.APIBase == "" &&
		p.Novita.APIKey == "" && p.Novita.APIBase == ""
}

type providerConfigV0 struct {
	APIKey         string `json:"api_key"                   env:"OMNIPUS_PROVIDERS_{{.Name}}_API_KEY"`
	APIBase        string `json:"api_base"                  env:"OMNIPUS_PROVIDERS_{{.Name}}_API_BASE"`
	Proxy          string `json:"proxy,omitempty"           env:"OMNIPUS_PROVIDERS_{{.Name}}_PROXY"`
	RequestTimeout int    `json:"request_timeout,omitempty" env:"OMNIPUS_PROVIDERS_{{.Name}}_REQUEST_TIMEOUT"`
	AuthMethod     string `json:"auth_method,omitempty"     env:"OMNIPUS_PROVIDERS_{{.Name}}_AUTH_METHOD"`
	ConnectMode    string `json:"connect_mode,omitempty"    env:"OMNIPUS_PROVIDERS_{{.Name}}_CONNECT_MODE"` // only for Github Copilot, `stdio` or `grpc`
}

// MarshalJSON implements custom JSON marshaling for providersConfig
// to omit the entire section when empty
func (p providersConfigV0) MarshalJSON() ([]byte, error) {
	if p.IsEmpty() {
		return []byte("null"), nil
	}
	type Alias providersConfigV0
	return json.Marshal((*Alias)(&p))
}

type openAIProviderConfigV0 struct {
	providerConfigV0
	WebSearch bool `json:"web_search" env:"OMNIPUS_PROVIDERS_OPENAI_WEB_SEARCH"`
}

type modelConfigV0 struct {
	// Required fields
	ModelName string `json:"model_name"` // User-facing alias for the model
	Model     string `json:"model"`      // Protocol/model-identifier (e.g., "openai/gpt-4o", "anthropic/claude-sonnet-4.6")

	// HTTP-based providers
	APIBase   string   `json:"api_base,omitempty"`  // API endpoint URL
	APIKey    string   `json:"api_key"`             // API authentication key (single key)
	APIKeys   []string `json:"api_keys,omitempty"`  // API authentication keys (multiple keys for failover)
	Proxy     string   `json:"proxy,omitempty"`     // HTTP proxy URL
	Fallbacks []string `json:"fallbacks,omitempty"` // Fallback model names for failover

	// Special providers (CLI-based, OAuth, etc.)
	AuthMethod  string `json:"auth_method,omitempty"`  // Authentication method: oauth, token
	ConnectMode string `json:"connect_mode,omitempty"` // Connection mode: stdio, grpc
	Workspace   string `json:"workspace,omitempty"`    // Workspace path for CLI-based providers

	// Optional optimizations
	RPM            int    `json:"rpm,omitempty"`              // Requests per minute limit
	MaxTokensField string `json:"max_tokens_field,omitempty"` // Field name for max tokens (e.g., "max_completion_tokens")
	RequestTimeout int    `json:"request_timeout,omitempty"`
	ThinkingLevel  string `json:"thinking_level,omitempty"` // Extended thinking: off|low|medium|high|xhigh|adaptive
}

func (c *configV0) migrateChannelConfigs() {
	// Discord: mention_only -> group_trigger.mention_only
	if c.Channels.Discord.MentionOnly && !c.Channels.Discord.GroupTrigger.MentionOnly {
		c.Channels.Discord.GroupTrigger.MentionOnly = true
	}
}

// secretFieldPatterns is the set of JSON tag base-names that identify a field
// as a legacy plaintext secret in configV0. The reflection walker in
// hasLegacySecretsReflect matches the base name of the json struct tag (the
// part before the first comma) against this set.
//
// Drift guard: config_old_test.go contains TestHasLegacySecrets_CoversAllSecretFields
// which walks configV0 via reflection and fails if a string field whose json tag
// suffix matches one of these patterns is not detected by hasLegacySecrets.
// If a new v0 secret field is added, its json tag must match one of these patterns
// OR this list must be updated — otherwise the drift guard test will catch the gap.
var secretFieldPatterns = []string{
	"token", "secret", "password", "api_key", "app_secret",
	"access_token", "bot_token", "app_token", "channel_secret",
	"channel_access_token", "client_secret", "verification_token",
	"nickserv_password", "sasl_password", "encrypt_key",
	"crypto_passphrase", "auth_token", "elevenlabs_api_key",
}

// hasLegacySecrets returns true if the v0 config contains any non-empty
// plaintext secret fields that would be silently lost during a plain Migrate.
// It uses reflection to walk configV0 so new secret fields are detected
// automatically as long as their json tag matches one of secretFieldPatterns.
func (c *configV0) hasLegacySecrets() bool {
	return hasLegacySecretsReflect(reflect.ValueOf(c))
}

// hasLegacySecretsReflect is the recursive reflection walker for hasLegacySecrets.
func hasLegacySecretsReflect(v reflect.Value) bool {
	// Dereference pointer/interface.
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return false
		}
		v = v.Elem()
	}
	t := v.Type()
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			fv := v.Field(i)
			if fv.Kind() == reflect.String && fv.String() != "" {
				tag := strings.Split(field.Tag.Get("json"), ",")[0]
				for _, pat := range secretFieldPatterns {
					if tag == pat {
						return true
					}
				}
			}
			if hasLegacySecretsReflect(fv) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if hasLegacySecretsReflect(v.Index(i)) {
				return true
			}
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			if hasLegacySecretsReflect(iter.Value()) {
				return true
			}
		}
	}
	return false
}

// MigrateWithStore migrates the v0 config to the current schema and, for each
// non-empty legacy plaintext secret field, writes the value into store and sets
// the corresponding *Ref field in the output config. If store is nil and
// plaintext secrets are present, MigrateWithStore returns an error directing the
// operator to set OMNIPUS_MASTER_KEY before migrating.
func (c *configV0) MigrateWithStore(store CredentialStore) (*Config, error) {
	cfg, err := c.Migrate()
	if err != nil {
		return nil, err
	}

	// migrateSecret is a helper that writes value into store under refName, sets
	// *Ref in cfg (via the setter callback), and logs the operation.
	migrateSecret := func(refName, value string, setter func(ref string)) error {
		if value == "" {
			return nil
		}
		if store == nil {
			return fmt.Errorf(
				"config migration: legacy plaintext secret %q requires OMNIPUS_MASTER_KEY to be set before migration",
				refName,
			)
		}
		if err := store.Set(refName, value); err != nil {
			return fmt.Errorf("migrate %s: %w", refName, err)
		}
		setter(refName)
		slog.Warn("config migration: moved legacy plaintext secret to credential store", "ref", refName)
		return nil
	}

	// Channel secrets — each setter mutates a copy and re-assigns via the map key
	// (map values are not addressable in Go, so we must use copy+assign).
	setChannelRef := func(key, field string, setter func(inst *ChannelInstanceConfig, ref string)) func(ref string) {
		return func(ref string) {
			if inst, ok := cfg.Channels[key]; ok {
				setter(&inst, ref)
				cfg.Channels[key] = inst
			}
		}
	}

	if err := migrateSecret("TELEGRAM_TOKEN", c.Channels.Telegram.Token,
		setChannelRef("telegram", "token_ref", func(inst *ChannelInstanceConfig, ref string) { inst.TokenRef = ref })); err != nil {
		return nil, err
	}
	if err := migrateSecret("DISCORD_TOKEN", c.Channels.Discord.Token,
		setChannelRef("discord", "token_ref", func(inst *ChannelInstanceConfig, ref string) { inst.TokenRef = ref })); err != nil {
		return nil, err
	}
	if err := migrateSecret("WECOM_SECRET", c.Channels.WeCom.Secret,
		setChannelRef("wecom", "secret_ref", func(inst *ChannelInstanceConfig, ref string) { inst.SecretRef = ref })); err != nil {
		return nil, err
	}
	if err := migrateSecret("SLACK_BOT_TOKEN", c.Channels.Slack.BotToken,
		setChannelRef("slack", "bot_token_ref", func(inst *ChannelInstanceConfig, ref string) { inst.BotTokenRef = ref })); err != nil {
		return nil, err
	}
	if err := migrateSecret("SLACK_APP_TOKEN", c.Channels.Slack.AppToken,
		setChannelRef("slack", "app_token_ref", func(inst *ChannelInstanceConfig, ref string) { inst.AppTokenRef = ref })); err != nil {
		return nil, err
	}
	if err := migrateSecret("FEISHU_APP_SECRET", c.Channels.Feishu.AppSecret,
		setChannelRef("feishu", "app_secret_ref", func(inst *ChannelInstanceConfig, ref string) { inst.AppSecretRef = ref })); err != nil {
		return nil, err
	}
	if err := migrateSecret("FEISHU_ENCRYPT_KEY", c.Channels.Feishu.EncryptKey,
		setChannelRef("feishu", "encrypt_key_ref", func(inst *ChannelInstanceConfig, ref string) { inst.EncryptKeyRef = ref })); err != nil {
		return nil, err
	}
	if err := migrateSecret("FEISHU_VERIFICATION_TOKEN", c.Channels.Feishu.VerificationToken,
		setChannelRef("feishu", "verification_token_ref", func(inst *ChannelInstanceConfig, ref string) { inst.VerificationTokenRef = ref })); err != nil {
		return nil, err
	}
	if err := migrateSecret("QQ_APP_SECRET", c.Channels.QQ.AppSecret,
		setChannelRef("qq", "app_secret_ref", func(inst *ChannelInstanceConfig, ref string) { inst.AppSecretRef = ref })); err != nil {
		return nil, err
	}
	if err := migrateSecret("DINGTALK_CLIENT_SECRET", c.Channels.DingTalk.ClientSecret,
		setChannelRef("dingtalk", "client_secret_ref", func(inst *ChannelInstanceConfig, ref string) { inst.ClientSecretRef = ref })); err != nil {
		return nil, err
	}
	if err := migrateSecret("MATRIX_ACCESS_TOKEN", c.Channels.Matrix.AccessToken,
		setChannelRef("matrix", "access_token_ref", func(inst *ChannelInstanceConfig, ref string) { inst.AccessTokenRef = ref })); err != nil {
		return nil, err
	}
	if err := migrateSecret("LINE_CHANNEL_SECRET", c.Channels.LINE.ChannelSecret,
		setChannelRef("line", "channel_secret_ref", func(inst *ChannelInstanceConfig, ref string) { inst.ChannelSecretRef = ref })); err != nil {
		return nil, err
	}
	if err := migrateSecret("LINE_CHANNEL_ACCESS_TOKEN", c.Channels.LINE.ChannelAccessToken,
		setChannelRef("line", "channel_access_token_ref", func(inst *ChannelInstanceConfig, ref string) { inst.ChannelAccessTokenRef = ref })); err != nil {
		return nil, err
	}
	if err := migrateSecret("WEIXIN_TOKEN", c.Channels.Weixin.Token,
		setChannelRef("weixin", "token_ref", func(inst *ChannelInstanceConfig, ref string) { inst.TokenRef = ref })); err != nil {
		return nil, err
	}
	if err := migrateSecret("IRC_PASSWORD", c.Channels.IRC.Password,
		setChannelRef("irc", "password_ref", func(inst *ChannelInstanceConfig, ref string) { inst.PasswordRef = ref })); err != nil {
		return nil, err
	}
	if err := migrateSecret("IRC_NICKSERV_PASSWORD", c.Channels.IRC.NickServPassword,
		setChannelRef("irc", "nickserv_password_ref", func(inst *ChannelInstanceConfig, ref string) { inst.NickServPasswordRef = ref })); err != nil {
		return nil, err
	}
	if err := migrateSecret("IRC_SASL_PASSWORD", c.Channels.IRC.SASLPassword,
		setChannelRef("irc", "sasl_password_ref", func(inst *ChannelInstanceConfig, ref string) { inst.SASLPasswordRef = ref })); err != nil {
		return nil, err
	}

	// Provider secrets from model_list
	for i, m := range c.ModelList {
		if m.APIKey == "" || i >= len(cfg.Providers) {
			continue
		}
		// Only migrate if the Ref field is not already set
		if cfg.Providers[i].APIKeyRef != "" {
			continue
		}
		refName := fmt.Sprintf("%s_API_KEY", sanitizeRefName(m.ModelName))
		if err := migrateSecret(refName, m.APIKey,
			func(ref string) { cfg.Providers[i].APIKeyRef = ref }); err != nil {
			return nil, err
		}
	}

	return cfg, nil
}

// sanitizeRefName converts a model name to an env-var-safe uppercase ref name.
func sanitizeRefName(name string) string {
	result := make([]byte, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			result[i] = c &^ 0x20 // uppercase
		} else {
			result[i] = '_'
		}
	}
	return string(result)
}

func (c *configV0) Migrate() (*Config, error) {
	// Migrate legacy channel config fields to new unified structures
	cfg := DefaultConfig()

	// Always copy user's Agents config to preserve settings like Provider, Model, MaxTokens
	cfg.Agents.List = c.Agents.List
	cfg.Agents.Defaults.Workspace = c.Agents.Defaults.Workspace
	cfg.Agents.Defaults.RestrictToWorkspace = c.Agents.Defaults.RestrictToWorkspace
	cfg.Agents.Defaults.AllowReadOutsideWorkspace = c.Agents.Defaults.AllowReadOutsideWorkspace
	cfg.Agents.Defaults.Provider = c.Agents.Defaults.Provider
	cfg.Agents.Defaults.ModelName = c.Agents.Defaults.GetModelName()
	cfg.Agents.Defaults.ModelFallbacks = c.Agents.Defaults.ModelFallbacks
	cfg.Agents.Defaults.ImageModel = c.Agents.Defaults.ImageModel
	cfg.Agents.Defaults.ImageModelFallbacks = c.Agents.Defaults.ImageModelFallbacks
	cfg.Agents.Defaults.MaxTokens = c.Agents.Defaults.MaxTokens
	cfg.Agents.Defaults.Temperature = c.Agents.Defaults.Temperature
	cfg.Agents.Defaults.MaxToolIterations = c.Agents.Defaults.MaxToolIterations
	cfg.Agents.Defaults.SummarizeMessageThreshold = c.Agents.Defaults.SummarizeMessageThreshold
	cfg.Agents.Defaults.SummarizeTokenPercent = c.Agents.Defaults.SummarizeTokenPercent
	cfg.Agents.Defaults.MaxMediaSize = c.Agents.Defaults.MaxMediaSize
	cfg.Agents.Defaults.Routing = c.Agents.Defaults.Routing

	// Copy other top-level fields
	cfg.Bindings = c.Bindings
	cfg.Session = c.Session
	cfg.Channels = c.Channels.ToChannelsConfig()
	cfg.Gateway = c.Gateway
	cfg.Tools.Web = c.Tools.Web.ToWebToolsConfig()
	cfg.Tools.Cron = c.Tools.Cron
	cfg.Tools.Exec = c.Tools.Exec
	cfg.Tools.Skills = c.Tools.Skills.ToSkillsToolsConfig()
	cfg.Tools.MediaCleanup = c.Tools.MediaCleanup
	cfg.Tools.MCP = c.Tools.MCP
	cfg.Tools.AppendFile = c.Tools.AppendFile
	cfg.Tools.EditFile = c.Tools.EditFile
	cfg.Tools.FindSkills = c.Tools.FindSkills
	cfg.Tools.InstallSkill = c.Tools.InstallSkill
	cfg.Tools.ListDir = c.Tools.ListDir
	cfg.Tools.Message = c.Tools.Message
	cfg.Tools.ReadFile = c.Tools.ReadFile
	cfg.Tools.SendFile = c.Tools.SendFile
	cfg.Tools.Spawn = c.Tools.Spawn
	cfg.Tools.SpawnStatus = c.Tools.SpawnStatus
	cfg.Tools.Subagent = c.Tools.Subagent
	cfg.Tools.WebFetch = c.Tools.WebFetch
	cfg.Tools.AllowReadPaths = c.Tools.AllowReadPaths
	cfg.Tools.AllowWritePaths = c.Tools.AllowWritePaths
	cfg.Heartbeat = c.Heartbeat
	cfg.Devices = c.Devices

	if len(c.ModelList) > 0 {
		// Convert []modelConfigV0 to []*ModelConfig (Providers)
		cfg.Providers = make([]*ModelConfig, len(c.ModelList))
		for i, m := range c.ModelList {
			cfg.Providers[i] = &ModelConfig{
				ModelName:      m.ModelName,
				Model:          m.Model,
				APIBase:        m.APIBase,
				Proxy:          m.Proxy,
				Fallbacks:      m.Fallbacks,
				AuthMethod:     m.AuthMethod,
				ConnectMode:    m.ConnectMode,
				Workspace:      m.Workspace,
				RPM:            m.RPM,
				MaxTokensField: m.MaxTokensField,
				RequestTimeout: m.RequestTimeout,
				ThinkingLevel:  m.ThinkingLevel,
			}
		}
	}

	cfg.Version = CurrentVersion
	return cfg, nil
}

type webToolsConfigV0 struct {
	ToolConfig           `                    envPrefix:"OMNIPUS_TOOLS_WEB_"`
	Brave                braveConfigV0       `                               json:"brave"`
	Tavily               tavilyConfigV0      `                               json:"tavily"`
	DuckDuckGo           DuckDuckGoConfig    `                               json:"duckduckgo"`
	Perplexity           perplexityConfigV0  `                               json:"perplexity"`
	SearXNG              SearXNGConfig       `                               json:"searxng"`
	GLMSearch            glmSearchConfigV0   `                               json:"glm_search"`
	BaiduSearch          baiduSearchConfigV0 `                               json:"baidu_search"`
	PreferNative         bool                `                               json:"prefer_native"                    env:"OMNIPUS_TOOLS_WEB_PREFER_NATIVE"`
	Proxy                string              `                               json:"proxy,omitempty"                  env:"OMNIPUS_TOOLS_WEB_PROXY"`
	FetchLimitBytes      int64               `                               json:"fetch_limit_bytes,omitempty"      env:"OMNIPUS_TOOLS_WEB_FETCH_LIMIT_BYTES"`
	Format               string              `                               json:"format,omitempty"                 env:"OMNIPUS_TOOLS_WEB_FORMAT"`
	PrivateHostWhitelist FlexibleStringSlice `                               json:"private_host_whitelist,omitempty" env:"OMNIPUS_TOOLS_WEB_PRIVATE_HOST_WHITELIST"`
}

type braveConfigV0 struct {
	Enabled    bool     `json:"enabled"     env:"OMNIPUS_TOOLS_WEB_BRAVE_ENABLED"`
	APIKey     string   `json:"api_key"     env:"OMNIPUS_TOOLS_WEB_BRAVE_API_KEY"`
	APIKeys    []string `json:"api_keys"    env:"OMNIPUS_TOOLS_WEB_BRAVE_API_KEYS"`
	MaxResults int      `json:"max_results" env:"OMNIPUS_TOOLS_WEB_BRAVE_MAX_RESULTS"`
}

func (v *braveConfigV0) ToBraveConfig() BraveConfig {
	return BraveConfig{
		Enabled:    v.Enabled,
		MaxResults: v.MaxResults,
	}
}

type tavilyConfigV0 struct {
	Enabled    bool     `json:"enabled"     env:"OMNIPUS_TOOLS_WEB_TAVILY_ENABLED"`
	APIKey     string   `json:"api_key"     env:"OMNIPUS_TOOLS_WEB_TAVILY_API_KEY"`
	APIKeys    []string `json:"api_keys"    env:"OMNIPUS_TOOLS_WEB_TAVILY_API_KEYS"`
	BaseURL    string   `json:"base_url"    env:"OMNIPUS_TOOLS_WEB_TAVILY_BASE_URL"`
	MaxResults int      `json:"max_results" env:"OMNIPUS_TOOLS_WEB_TAVILY_MAX_RESULTS"`
}

func (v *tavilyConfigV0) ToTavilyConfig() TavilyConfig {
	return TavilyConfig{
		Enabled:    v.Enabled,
		BaseURL:    v.BaseURL,
		MaxResults: v.MaxResults,
	}
}

type perplexityConfigV0 struct {
	Enabled    bool     `json:"enabled"     env:"OMNIPUS_TOOLS_WEB_PERPLEXITY_ENABLED"`
	APIKey     string   `json:"api_key"     env:"OMNIPUS_TOOLS_WEB_PERPLEXITY_API_KEY"`
	APIKeys    []string `json:"api_keys"    env:"OMNIPUS_TOOLS_WEB_PERPLEXITY_API_KEYS"`
	MaxResults int      `json:"max_results" env:"OMNIPUS_TOOLS_WEB_PERPLEXITY_MAX_RESULTS"`
}

func (v *perplexityConfigV0) ToPerplexityConfig() PerplexityConfig {
	return PerplexityConfig{
		Enabled:    v.Enabled,
		MaxResults: v.MaxResults,
	}
}

type glmSearchConfigV0 struct {
	Enabled      bool   `json:"enabled"       env:"OMNIPUS_TOOLS_WEB_GLM_ENABLED"`
	APIKey       string `json:"api_key"       env:"OMNIPUS_TOOLS_WEB_GLM_API_KEY"`
	BaseURL      string `json:"base_url"      env:"OMNIPUS_TOOLS_WEB_GLM_BASE_URL"`
	SearchEngine string `json:"search_engine" env:"OMNIPUS_TOOLS_WEB_GLM_SEARCH_ENGINE"`
}

func (v *glmSearchConfigV0) ToGLMSearchConfig() GLMSearchConfig {
	return GLMSearchConfig{
		Enabled:      v.Enabled,
		BaseURL:      v.BaseURL,
		SearchEngine: v.SearchEngine,
	}
}

type baiduSearchConfigV0 struct {
	Enabled    bool   `json:"enabled"     env:"OMNIPUS_TOOLS_WEB_BAIDU_ENABLED"`
	APIKey     string `json:"api_key"     env:"OMNIPUS_TOOLS_WEB_BAIDU_API_KEY"`
	BaseURL    string `json:"base_url"    env:"OMNIPUS_TOOLS_WEB_BAIDU_BASE_URL"`
	MaxResults int    `json:"max_results" env:"OMNIPUS_TOOLS_WEB_BAIDU_MAX_RESULTS"`
}

func (v *baiduSearchConfigV0) ToBaiduSearchConfig() BaiduSearchConfig {
	return BaiduSearchConfig{
		Enabled:    v.Enabled,
		BaseURL:    v.BaseURL,
		MaxResults: v.MaxResults,
	}
}

func (v *webToolsConfigV0) ToWebToolsConfig() WebToolsConfig {
	brave := v.Brave.ToBraveConfig()
	tavily := v.Tavily.ToTavilyConfig()
	perplexity := v.Perplexity.ToPerplexityConfig()
	glmSearch := v.GLMSearch.ToGLMSearchConfig()
	baiduSearch := v.BaiduSearch.ToBaiduSearchConfig()

	return WebToolsConfig{
		ToolConfig:           v.ToolConfig,
		Brave:                brave,
		Tavily:               tavily,
		DuckDuckGo:           v.DuckDuckGo,
		Perplexity:           perplexity,
		SearXNG:              v.SearXNG,
		GLMSearch:            glmSearch,
		PreferNative:         v.PreferNative,
		Proxy:                v.Proxy,
		FetchLimitBytes:      v.FetchLimitBytes,
		Format:               v.Format,
		PrivateHostWhitelist: v.PrivateHostWhitelist,
		BaiduSearch:          baiduSearch,
	}
}

type skillsToolsConfigV0 struct {
	ToolConfig            `                         envPrefix:"OMNIPUS_TOOLS_SKILLS_"`
	Registries            skillsRegistriesConfigV0 `                                  json:"registries"`
	Github                skillsGithubConfigV0     `                                  json:"github"`
	MaxConcurrentSearches int                      `                                  json:"max_concurrent_searches" env:"OMNIPUS_TOOLS_SKILLS_MAX_CONCURRENT_SEARCHES"`
	SearchCache           SearchCacheConfig        `                                  json:"search_cache"`
}

type skillsRegistriesConfigV0 struct {
	ClawHub clawHubRegistryConfigV0 `json:"clawhub"`
}

type clawHubRegistryConfigV0 struct {
	Enabled    bool   `json:"enabled"     env:"OMNIPUS_SKILLS_REGISTRIES_CLAWHUB_ENABLED"`
	BaseURL    string `json:"base_url"    env:"OMNIPUS_SKILLS_REGISTRIES_CLAWHUB_BASE_URL"`
	AuthToken  string `json:"auth_token"  env:"OMNIPUS_SKILLS_REGISTRIES_CLAWHUB_AUTH_TOKEN"`
	SearchPath string `json:"search_path" env:"OMNIPUS_SKILLS_REGISTRIES_CLAWHUB_SEARCH_PATH"`
	SkillsPath string `json:"skills_path" env:"OMNIPUS_SKILLS_REGISTRIES_CLAWHUB_SKILLS_PATH"`
}

func (v *clawHubRegistryConfigV0) ToClawHubRegistryConfig() ClawHubRegistryConfig {
	return ClawHubRegistryConfig{
		Enabled:    v.Enabled,
		BaseURL:    v.BaseURL,
		SearchPath: v.SearchPath,
		SkillsPath: v.SkillsPath,
	}
}

type skillsGithubConfigV0 struct {
	Token string `json:"token"           env:"OMNIPUS_TOOLS_SKILLS_GITHUB_TOKEN"`
	Proxy string `json:"proxy,omitempty" env:"OMNIPUS_TOOLS_SKILLS_GITHUB_PROXY"`
}

func (v *skillsGithubConfigV0) ToSkillsGithubConfig() SkillsGithubConfig {
	return SkillsGithubConfig{
		Proxy: v.Proxy,
	}
}

func (v *skillsRegistriesConfigV0) ToSkillsRegistriesConfig() SkillsRegistriesConfig {
	clawHub := v.ClawHub.ToClawHubRegistryConfig()

	return SkillsRegistriesConfig{
		ClawHub: clawHub,
	}
}

func (v *skillsToolsConfigV0) ToSkillsToolsConfig() SkillsToolsConfig {
	registries := v.Registries.ToSkillsRegistriesConfig()
	github := v.Github.ToSkillsGithubConfig()
	return SkillsToolsConfig{
		ToolConfig:            v.ToolConfig,
		Registries:            registries,
		Github:                github,
		MaxConcurrentSearches: v.MaxConcurrentSearches,
		SearchCache:           v.SearchCache,
	}
}
