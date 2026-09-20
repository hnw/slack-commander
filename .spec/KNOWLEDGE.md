# KNOWLEDGE

## stdin session と idle EOF（2026-09-21）
- exec の finite / interactive は private な `stdinSession` の Start / Close を共有し、EOF のアプリケーション側所有者を session に統一する。finite は入力を改変せず転送後に閉じ、interactive のみ LF 補完・追加入力を扱う。
- idle timer は interactive session が所有する。writer 接続時に開始し、TrySend の受付成功時に reset する。Busy / Closed は activity に含めず、受付と期限切れを同じロックで順序付ける。process timeout とは独立し、idle EOF 自体は kill しない。
- `stdin_idle_timeout` は秒単位の `int` で、未指定または `0` は無効、正の値のみ有効、負数は設定エラーとする。
- Executor が session の終了通知で unregister し、process 終了時は defer Close で転送処理を回収する。登録時に closed を確認し、Start 直後の EOF / write error と登録の競合でも stale endpoint を残さない。
- ADR候補: ユーザーの確定仕様により、以前の「EOF 待ちは process timeout で扱う」方針に任意の stdin idle EOF を追加する。既定値0で従来動作を維持し、EOF による正常終了と強制終了の安全弁を分離する。局所的で戻せる追加設定のため独立 ADR は見送り、この節を判断記録とする。

## exec stdin lifecycle の責務整理（2026-09-21）
- `cmd/runner.go` の共通 `run` は process lifecycle を担当し、接続は private な `execStdin` に分離する。finite Reader を `os/exec` に任せる旧方式は、上記 stdin session へ移行した。
- `cmd/execStdin.go` は Start 前だけ writer を所有する。Start 成功後は保持を解除して LiveInput に渡し、executor の defer が unregister / endpoint.Close を担当する。`os/exec.Wait` 自体による pipe close は維持する。
- ADR候補: ユーザーの指摘により、execCmd と LiveInput に重複していたアプリケーション側の writer close 責務を整理した。外部契約・永続化を変えない局所的で戻せる変更のため、独立 ADR の起票は見送る。

## live stdin の設計判断
- ユーザー決定: live 入力自体に opt-in を追加しない。EOF 待ちはコマンド本来の挙動とし、既存 timeout を安全弁とする。任意の idle EOF は上記の追加仕様に従う。
- ユーザー決定: reply 本文が LF で終わらなければ1つ補い、本文中の改行は保持する。
- ユーザー決定: 初期 stdin にも末尾 LF 補完を適用する。ただし初期 stdin が空の場合は空行を送らない。
- ユーザー決定: live reply は本文を保持し、command parsing 用の変換を通さない。メンション・URL・引用符等の変換は実利用で必要になった時点で再検討する。
- ユーザー決定: registry は `(ChannelID, RootThreadTimestamp) -> live input endpoint` の一時的な routing table に限定する。履歴は Slack を正とし、固定キュー容量・重複排除・thread reservation・多重起動制御は追加しない。
- ユーザー決定: 今回は exec のみ対応する。compose は既存動作を維持し、live stdin 対応と検証は tasks/compose-live-input.md の別タスクにする。
- ユーザー決定: Start 成功後、初期入力を先頭として順序確定してから endpoint を公開する。同じ thread の送信先は最後に登録された process とする。
- ユーザー決定: live reply channel は buffer 1 を第一候補とし、producer / consumer の瞬間的なずれを吸収する。転送中とは別に未処理 reply を1件だけ保持し、即時受付できない reply は drop してログに残す。無制限 queue / goroutine は作らない。
- ユーザー決定: chain 切り替え中も registry に対象がなければ既存 routing に進む。thread reservation / chain-level state は追加しない。
- exec 相当の実測では、OS pipe 直接接続で date / 1行 read は EOF 前に終了、awk は逐次出力、wc -l は EOF 待ちとなった。
- io.Pipe を exec.Cmd.Stdin に渡す場合の Wait ブロックは、子プロセスの stdin semantics と分離する。Go の入力コピー処理を終了待ちに残さない接続が必要。
- 実験コードとログ: /private/tmp/slack-stdin-check.J2oHRy/{main.go,results.jsonl}。compose の実環境検証は未実施。
- ADR候補: 互換性懸念だけで opt-in を増やさず、live stdin を標準動作とし EOF 待ちには既存 timeout を使う。根拠はユーザー指示と上記実測。仕様承認時に起票を判断する。
- ADR候補: registry を process の一時参照だけに限定し、先回りしたキュー・重複・起動制御を外す。ユーザーが計画を簡素化するよう指示したため。追加策は実際の問題を観測してから検討する。
