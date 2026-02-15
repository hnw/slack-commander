package pubsub

type Config struct {
	ReplyConfig
	SlackBotToken         string   `toml:"slack_bot_token"`
	SlackAppToken         string   `toml:"slack_app_token"`
	AllowUnsafeOpenAccess bool     `toml:"allow_unsafe_open_access"`
	AcceptReminder        bool     `toml:"accept_reminder"`
	AcceptBotMessage      bool     `toml:"accept_bot_message"`
	AcceptThreadMessage   bool     `toml:"accept_thread_message"`
	AllowedUserIDs        []string `toml:"allowed_user_ids"`
	AllowedChannelIDs     []string `toml:"allowed_channel_ids"`
}

type ReplyConfig struct {
	Username        string `toml:"username"`
	IconEmoji       string `toml:"icon_emoji"`
	IconURL         string `toml:"icon_url"`
	PostAsReply     bool   `toml:"post_as_reply"`
	AlwaysBroadcast bool   `toml:"always_broadcast"`
	Monospaced      bool
}
