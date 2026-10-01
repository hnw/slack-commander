package pubsub

const (
	// OutputFormatPlain sends output as plain Slack text.
	OutputFormatPlain = "plain"
	// OutputFormatMonospaced wraps output in a monospace code block.
	OutputFormatMonospaced = "monospaced"
	// OutputFormatMarkdown sends output as a Slack markdown block.
	OutputFormatMarkdown = "markdown"
)

// Config defines Slack pub/sub settings.
type Config struct {
	ReplyConfig
	SlackBotToken         string           `toml:"slack_bot_token"`
	SlackAppToken         string           `toml:"slack_app_token"`
	AllowUnsafeOpenAccess bool             `toml:"allow_unsafe_open_access"`
	AllowedUserIDs        []string         `toml:"allowed_user_ids"`
	AllowedChannelIDs     []string         `toml:"allowed_channel_ids"`
	ListenerConfigs       []ListenerConfig `toml:"-"`
}

// RawListenerConfig はTOMLで指定可能なListener設定を保持する。
type RawListenerConfig struct {
	AllowedUserIDs    []string `toml:"allowed_user_ids"`
	AllowedChannelIDs []string `toml:"allowed_channel_ids"`
	AcceptReminder    bool     `toml:"accept_reminder"`
}

// ListenerConfig は解決済みListener設定にruntime routing metadataを加える。
type ListenerConfig struct {
	CommandIndex int  `toml:"-"`
	IsReply      bool `toml:"-"`
	RawListenerConfig
}

// ReplyConfig defines reply formatting options.
type ReplyConfig struct {
	Username       string `toml:"username"`
	IconEmoji      string `toml:"icon_emoji"`
	IconURL        string `toml:"icon_url"`
	ReplyBroadcast *bool  `toml:"reply_broadcast"`
	OutputFormat   string `toml:"output_format"`
}

// NewSystemReplyConfig creates the reply settings used by system messages.
func NewSystemReplyConfig(replyBroadcast *bool) *ReplyConfig {
	return &ReplyConfig{
		Username:       "Slack commander",
		IconEmoji:      ":ghost:",
		ReplyBroadcast: replyBroadcast,
	}
}
