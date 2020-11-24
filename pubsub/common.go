package pubsub

type Config struct {
	ReplyConfig
	SlackToken          string `toml:"slack_token"`
	AcceptReminder      bool   `toml:"accept_reminder"`
	AcceptBotMessage    bool   `toml:"accept_bot_message"`
	AcceptThreadMessage bool   `toml:"accept_thread_message"`
}

type ReplyConfig struct {
	Username        string `toml:"username"`
	IconEmoji       string `toml:"icon_emoji"`
	IconURL         string `toml:"icon_url"`
	PostAsReply     bool   `toml:"post_as_reply"`
	AlwaysBroadcast bool   `toml:"always_broadcast"`
	Monospaced      bool
	Timeout         int
}

// Input はPubSubからの情報をExecutorに引き渡す構造体
type Input struct {
	ReplyInfo interface{} // PubSubの返信に必要な構造体（PubSubごとにキャストして利用する）
	Text      string      // 起動コマンド平文
}

// CommandOutput はExecutorからの実行結果を引き渡してPubSubに書き出すための構造体
// cmdパッケージを作成したらそっちに移動させた方がよさそう
type CommandOutput struct {
	ReplyConfig
	ReplyInfo interface{}
	Text      string
	IsError   bool
}
