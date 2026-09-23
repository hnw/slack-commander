# KNOWLEDGE

## Slack thread context と実行直列化（2026-09-23）
- `ConversationContext` の `ChannelID` と `RootThreadTimestamp` を実行直前に環境変数へ合成することで、exec / compose の runner 実装ごとの分岐を避けつつ、Slack event の値を既存同名値より優先できる。
- Executor worker 間で `ThreadLocks` を共有し、`ThreadKey` ごとの `sync.Mutex` を実行全体に適用する。mutex待機のFIFOやworker占有は保証・回避せず、map entryのcleanupも初版では行わない。
- sync mode のreplyは listener が `ThreadInputRegistry` へ先にrouteするため、lock付きExecutor経路でも新規commandにせず実行中processへstdinとして渡されることを統合テストで確認した。

## stdin session と idle EOF（2026-09-21）
- exec の finite / interactive は private な `stdinSession` の Start / Close を共有し、EOF のアプリケーション側所有者を session に統一する。finite は入力を改変せず転送後に閉じ、interactive のみ LF 補完・追加入力を扱う。
- idle timer は interactive session が所有する。writer 接続時に開始し、TrySend の受付成功時に reset する。Busy / Closed は activity に含めず、受付と期限切れを同じロックで順序付ける。process timeout とは独立し、idle EOF 自体は kill しない。
- `stdin_idle_timeout` は秒単位の `int` で、未指定または `0` は無効、正の値のみ有効、負数は設定エラーとする。
- Executor が session の終了通知で unregister し、process 終了時は defer Close で転送処理を回収する。登録時に closed を確認し、Start 直後の EOF / write error と登録の競合でも stale endpoint を残さない。
- ADR候補: ユーザーの確定仕様により、以前の「EOF 待ちは process timeout で扱う」方針に任意の stdin idle EOF を追加する。既定値0で従来動作を維持し、EOF による正常終了と強制終了の安全弁を分離する。局所的で戻せる追加設定のため独立 ADR は見送り、この節を判断記録とする。

## exec stdin lifecycle の責務整理（2026-09-21）
- `cmd/runner.go` の共通 `run` は process lifecycle を担当し、接続は private な `execStdin` に分離する。finite Reader を `os/exec` に任せる旧方式は、上記 stdin session へ移行した。
- `cmd/execStdin.go` は Start 前だけ writer を所有する。Start 成功後は保持を解除して `InteractiveStdin` に渡し、executor の defer が unregister / endpoint.Close を担当する。`os/exec.Wait` 自体による pipe close は維持する。
- ADR候補: ユーザーの指摘により、execCmd と `InteractiveStdin` に重複していたアプリケーション側の writer close 責務を整理した。外部契約・永続化を変えない局所的で戻せる変更のため、独立 ADR の起票は見送る。

## live stdin の設計判断
- ユーザー決定: live 入力自体に opt-in を追加しない。EOF 待ちはコマンド本来の挙動とし、既存 timeout を安全弁とする。任意の idle EOF は上記の追加仕様に従う。
- ユーザー決定: reply 本文が LF で終わらなければ1つ補い、本文中の改行は保持する。
- ユーザー決定: 初期 stdin にも末尾 LF 補完を適用する。ただし初期 stdin が空の場合は空行を送らない。
- ユーザー決定: live reply は本文を保持し、command parsing 用の変換を通さない。メンション・URL・引用符等の変換は実利用で必要になった時点で再検討する。
- ユーザー決定: `ThreadInputRegistry` は `(ChannelID, RootThreadTimestamp) -> InteractiveStdin` の一時的な routing table に限定する。履歴は Slack を正とし、固定キュー容量・重複排除・thread reservation・多重起動制御は追加しない。
- ユーザー決定: interactive stdin と `stdin_idle_timeout` は exec / compose に適用し、HTTP は非対応とする。compose-exec の内部 forwarding と Docker attach は変更しない。
- ユーザー決定: Start 成功後、初期入力を先頭として順序確定してから endpoint を公開する。同じ thread の送信先は最後に登録された process とする。
- ユーザー決定: live reply channel は buffer 1 を第一候補とし、producer / consumer の瞬間的なずれを吸収する。転送中とは別に未処理 reply を1件だけ保持し、即時受付できない reply は drop してログに残す。無制限 queue / goroutine は作らない。
- ユーザー決定: chain 切り替え中も registry に対象がなければ既存 routing に進む。thread reservation / chain-level state は追加しない。
- exec 相当の実測では、OS pipe 直接接続で date / 1行 read は EOF 前に終了、awk は逐次出力、wc -l は EOF 待ちとなった。
- io.Pipe を exec.Cmd.Stdin に渡す場合の Wait ブロックは、子プロセスの stdin semantics と分離する。Go の入力コピー処理を終了待ちに残さない接続が必要。
- compose runner は `StdinPipe` を Start 前に取得し、Start 成功後だけ callback へ writer の所有権を渡す。Start 失敗時は compose-exec が pipe を閉じ、endpoint を公開しない。`Run` と `RunWithStdin` の error / exit-code mapping は compose runner 内で共通化する。
- compose-exec v0.3.9 の `Wait` は container exit 後に stdin forwarding goroutine を最大約1秒待つ。この遅延は既知の挙動として受け入れ、実測で問題化した場合に compose-exec 側の別タスクで扱う。
- ADR候補: 互換性懸念だけで opt-in を増やさず、live stdin を標準動作とし EOF 待ちには既存 timeout を使う。根拠はユーザー指示と上記実測。仕様承認時に起票を判断する。
- ADR候補: registry を process の一時参照だけに限定し、先回りしたキュー・重複・起動制御を外す。ユーザーが計画を簡素化するよう指示したため。追加策は実際の問題を観測してから検討する。
