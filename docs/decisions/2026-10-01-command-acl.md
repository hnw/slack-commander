# Slack 入力制限を階層的に絞り込む

## Status

Accepted

## Context

コマンド単位の Slack 入力制限では、設定の継承によって許可範囲が広がらないことと、keyword 照合の定義順を維持することが要件である。
ユーザーが提示した仕様では、Slack 固有の認可を Listener に置き、Executor には利用可能な command 候補だけを渡す。
高度な認可 framework は導入せず、root command とその返信に同じ階層モデルを適用する。

## Decision

トップレベルの user／channel allowlist を最大許可範囲とし、root command、reply command の順に継承または縮小する。
親で許可されていない ID の追加は設定エラーにし、intersection による補正はしない。
親の空 allowlist はその軸を無制限とする。
command／reply の未指定値と空配列は親を継承するという暫定規約を採用し、空配列による制限解除は認めない。

Listener は resolved な flat `ListenerConfig` から global command index の候補一覧を作る。
Executor は候補外の command を除き、残った定義を順に keyword と照合する。
候補外の specific command から許可された wildcard への fall-through も認める。
deny の記録や追加の keyword 優先度判定は設けない。

stdin reply には root command の resolved ACL を適用する。
route cache miss では指定された thread の起点投稿を取得し、その投稿者の情報から root の候補を再現する。
返信投稿者や最新投稿から root を推測しない。

`accept_reminder` は command／reply のみに置き、既定値を false とし、継承しない。
Reminder を明示許可した command では user allowlist を免除し、channel allowlist は適用する。
`accept_bot_message` は廃止し、bot は通常の sender として認可する。
自身の bot 投稿は候補生成前に除外する。
`allow_unsafe_open_access` は従来どおり、トップレベルの両 allowlist が空の場合だけ要求する。

## Consequences

- 設定を読む際に、下位の ACL が上位の許可範囲を越えないという前提を使える。Reminder の user allowlist 免除は明示された例外となる。
- 同じ keyword の別定義や wildcard を、候補内の通常の定義順で選択できる。
- stdin返信にもcommand/replyと同じACLモデルを適用できる。
- 旧トップレベルの `accept_reminder` は各 command へ移し、`accept_bot_message` は削除する必要がある。bot の実行を許可する場合は sender ID を allowlist に追加する。

## Notes

- 根拠: 2026-10-01 に本チャットでユーザーが提示した認可モデルの仕様。
- [設定リファレンス](../config.md)
