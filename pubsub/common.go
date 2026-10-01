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

// ListenerConfig はraw設定と解決済みACLで同じSlack入力設定を共有するための型。
type ListenerConfig struct {
	CommandIndex      int      `toml:"-"`
	AllowedUserIDs    []string `toml:"allowed_user_ids"`
	AllowedChannelIDs []string `toml:"allowed_channel_ids"`
	AcceptReminder    bool     `toml:"accept_reminder"`
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
