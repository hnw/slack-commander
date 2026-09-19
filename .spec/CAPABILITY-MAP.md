# Capability Map: Slack thread interaction

| Module ID | Responsibility | Depends on |
|---|---|---|
| `conversation-context` | Slack の channel ID と root thread timestamp を、入力・出力パイプラインで明示的に運ぶ | — |
| `thread-continuation` | `continuation = "thread"` のコマンドで、Slack thread の返信を契機に再実行し、スレッド履歴を stdin に渡す | `conversation-context` |
| `sync-interaction` | 長寿命プロセスの stdin/stdout を Slack thread と中継する | `conversation-context` |
| `structured-blocks` | コマンドの平文出力内の予約構造を検出し、Block Kit と回答入力に変換する | `conversation-context` |

実装順: `conversation-context` → `thread-continuation` → `sync-interaction` → `structured-blocks`

今回の対象は `conversation-context` と `thread-continuation` である。後続モジュールの要件とプロトコルは、この2モジュールの実装後に別途仕様化する。
