# KNOWLEDGE

## 現在の設計判断

- `RawCommandConfig` とその `RawExecutorConfig` は TOML 上の未指定値を保持する decode 用構造とする。root / reply の既定値、継承、制約は `resolveConfig` で一度だけ解決し、後段には resolved `cmd.CommandConfig` だけを渡す。`--check-config` は runner を生成しない。
- `CommandConfig.Replies` は resolved config tree にだけ残す。instantiate 時に `Command.config.Replies` を nil にし、runtime reply tree は `Command.replies *CommandSet` だけで表現する。
- startup で `CommandSet` とその `Command` / matcher / runner を一度だけ構築し、routing と全 worker の Executor で共有する。matcher / runner は実行ごとの可変状態を持たず、compose runner も同時実行可能であるため worker ごとには生成しない。route cache が保持する command と実行される command は同じ runtime object とする。
- `ConversationID` は transport 固有の thread 型ではなく、継続した command interaction を識別する単位とする。Slack では channel ID + root timestamp に対応する。
- conversation 単位の runtime state として、root command の route、active stdin endpoint、実行 lock を管理する。これらは worker 個別の状態にはしない。stdin endpoint は runner が実際の writer を渡した後にだけ公開し、古い endpoint の close は後から登録された endpoint を削除しない。
- `stdin_idle_timeout` は process timeout と独立した EOF の安全弁である。`0` は無効、負値は設定エラーとする。interactive stdin は exec と compose だけに適用し、HTTP には適用しない。
- Slack 固有の入力表現は `pubsub` で正規化してから `cmd` に渡す。mention 除去や Slack による quote / entity / URL 表現の復元は transport boundary の責務とし、command body や stdin reply も例外扱いしない。reply の末尾には LF を一つ補うが、本文中の改行は保持する。

## 既知の境界上の課題

- route cache miss 時の再判定では `RootTextResolver` を通じて transport 側の root text を取得している。現在の Slack 実装では `pubsub.SlackRootTextResolver` が同期的に取得するため、cmd 側から transport 側へ問い合わせる形が残っている。
- `cmd.CommandConfig` は `[[commands]]` 全体を表す型ではなく、cmd 側で使う resolved 情報に加えて writer へ渡す `ReplyConfig` / `SystemReplyConfig` を opaque に保持している。名前と責務にずれがあるため、output / writer 周りの責務整理と合わせて、所属・名前・保持内容を見直す余地がある。
