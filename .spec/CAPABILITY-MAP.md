# Capability Map: Slack thread interaction

| Module ID              | Responsibility                                                  | Depends on             |
| ---------------------- | --------------------------------------------------------------- | ---------------------- |
| `conversation-context` | Slack の channel ID と root thread timestamp を、入力・出力パイプラインで明示的に運ぶ | —                      |
| `sync-interaction`     | 長寿命プロセスの stdin/stdout を Slack thread と中継する                      | `conversation-context` |
| `structured-blocks`    | コマンドの平文出力内の予約構造を検出し、Block Kit と回答入力に変換する                        | `conversation-context` |

実装順: `conversation-context` → `sync-interaction` → `structured-blocks`

今回の対象は `sync-interaction` であり、仕様は SPEC-sync-interaction.md に記載する。`structured-blocks` は対象外とする。
