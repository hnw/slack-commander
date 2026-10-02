# KNOWLEDGE

## 現在の設計判断

- `RawCommandConfig` とその `RawExecutorConfig` は TOML 上の未指定値を保持する decode 用構造とする。root / reply の既定値、継承、制約は `resolveConfig` で一度だけ解決し、後段には resolved `cmd.CommandConfig` だけを渡す。`--check-config` は runner を生成しない。
- `CommandConfig.Replies` は resolved config tree にだけ残す。instantiate 時に `Command.config.Replies` を nil にし、runtime reply tree は `Command.replies *CommandSet` だけで表現する。
- startup で `CommandSet` とその `Command` / matcher / runner を一度だけ構築し、routing と全 worker の Executor で共有する。matcher / runner は実行ごとの可変状態を持たず、compose runner も同時実行可能であるため worker ごとには生成しない。route cache が保持する command と実行される command は同じ runtime object とする。
- runtime `CommandConfig` の入力解析設定は `ParserConfig`、実行時設定は `ExecutorConfig` に分ける。TOMLタグを持つ `cmd.RawMatcherConfig` / `cmd.RawRunnerConfig` はruntimeの `MatcherConfig` / `RunnerConfig` に埋め込んで共有し、field定義を重複させない。raw executor設定はmainに保持する。
- `ConversationID` は transport 固有の thread 型ではなく、継続した command interaction を識別する単位とする。Slack では channel ID + root timestamp に対応する。
- conversation 単位の責務を `ConversationRouter`（root/reply routing と route cache）、stdin reply runner（active endpoint への直接配送）、`StdinStore`（active stdin endpoint と lifecycle）、`ConversationLocks`（command 実行の直列化）へ分ける。main で各 instance を生成し、全 worker が同じ Store と Locks を使う。worker は Lock の unlock 関数を defer し、Executor は直列化を担当しない。
- stdin endpoint は runner が実際の writer を渡した後にだけ公開し、古い endpoint の close は後から登録された endpoint を削除しない。stdin返信は他のCommandと同じ `CommandRunner` / `Cmd` を通し、`DispatchPolicy` はRouterがqueue経由かrunner直接実行かを選ぶ。ConversationIDはstdin runnerがendpointを特定するruntime引数として渡す。
- `stdin_idle_timeout` は process timeout と独立した EOF の安全弁である。`0` は無効、負値は設定エラーとする。interactive stdin は exec と compose だけに適用し、HTTP には適用しない。
- Slack 固有の入力表現は `pubsub` で正規化してから `cmd` に渡す。mention 除去や Slack による quote / entity / URL 表現の復元は transport boundary の責務とし、command body や stdin reply も例外扱いしない。reply の末尾には LF を一つ補うが、本文中の改行は保持する。
- Slack ACLはconfig resolve時にglobal command index付きのflat `ListenerConfig`へ展開する。トップレベルallowlistを最大範囲とし、下位の非空リストは親の部分集合、空リストは継承として解決する。open access判定は従来どおりトップレベルの両allowlistだけを見る。詳しくは [command ACL ADR](../docs/decisions/2026-10-01-command-acl.md)。
- Listener候補は入力種別に対応するglobal command indexだけをExecutorへ渡す。Executorは候補外commandを無視して定義順に照合するため、同一keywordやspecific commandから後続wildcardへ通常どおりfallthroughする。nil候補・明示empty候補はいずれもdeny。
- `accept_reminder`はcommand/reply専用で既定false、継承しない。Reminderはuser allowlistを免除するがchannel allowlistは適用する。bot投稿はBot IDを通常のsender IDとして評価し、自身のuser IDまたはBot IDからの投稿は常に無視する。
- stdin返信は独立したglobal command indexを持つ`*` replyとして照合し、root commandのresolved ACLを複製する。Listenerはroot候補とreply候補を分け、stdin runnerが返信本文全体を既存endpointへ渡す。explicit replyの`accept_reminder`は引き続き継承しない。
- ADR候補: raw返信照合を `InputBodyRawStdin` で表す。今回の明示指示に基づく。可逆な内部構造変更のため独立ADRは見送る。

## 既知の境界上の課題

- route cache miss 時の再判定では `RootInputResolver` を通じて transport 側の root text と起点senderの候補indexを取得している。現在の Slack 実装では `pubsub.SlackRootInputResolver` が同期的に取得するため、cmd 側から transport 側へ問い合わせる形が残っている。
- `cmd.CommandConfig` は `[[commands]]` 全体を表す型ではなく、cmd 側で使う resolved 情報に加えて writer へ渡す `ReplyConfig` / `SystemReplyConfig` を opaque に保持している。名前と責務にずれがあるため、output / writer 周りの責務整理と合わせて、所属・名前・保持内容を見直す余地がある。
