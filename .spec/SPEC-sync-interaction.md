# Spec: sync-interaction — running-process thread interaction

状態: 動作仕様の確認済み。実装計画は tasks/plan.md でレビューする。
既存 CAPABILITY-MAP の `sync-interaction` に対応し、`conversation-context` に依存する。

## Objective
Slack thread の返信本文を、その thread で exec runner が実行中のプロセスの stdin に渡す。
初回実行と continuation による実行の両方を対象にする。
コマンド単位の opt-in、interaction mode、waiting_for_input 設定は追加しない。
プロセスが見つからない場合だけ既存 continuation 判定、その後 accept_thread_message 判定へ進む。

## 確定した方針と前提
- stdin を読まないコマンド、1行で終了するコマンドは、入力側の EOF なしで終了できること。
- live stdin の接続・コピー処理が Run/Wait の完了を妨げてはならない。
- EOF を必要とするコマンドの待機は本来の挙動として扱い、既存 timeout を安全弁とする。
- timeout の値・未設定時の意味は変更せず、新たな既定 timeout や EOF 操作を追加しない。
- live stdin 対応は exec runner のみを対象とする。compose / HTTP runner は既存動作を維持する。
- compose の live stdin 対応は tasks/compose-live-input.md の別タスクに分離する。
- 通常出力の投稿先・reply_broadcast は既存動作を維持する。

## Reply routing
1. 既存の投稿者・channel 許可判定と自己出力の除外を通った thread reply を対象にする。
2. ChannelID と RootThreadTimestamp の組で実行中プロセスを検索する。
3. 対象があれば endpoint への即時受付を試みる。受け付けられない reply は drop して理由をログに残す。履歴取得・matcher・continuation は実行しない。
4. 対象がなければ既存のイベント種別ごとの処理へ進む。
5. 対象を見つけた後の drop、終了競合や write 失敗はログに残し、continuation として再解釈しない。drop した reply の再試行・保留は行わない。
listener の責務は endpoint の受付結果までとする。即時受付不能は listener が drop としてログに残し、受付後の stdin write error は live input / executor 側でログに残す。listener への非同期エラー通知は追加しない。
accept_thread_message は通常コマンドとしての受け付け設定であり、live stdin の有効化条件にはしない。
本文に履歴・role prefix を追加しない。受け付けた入力の順序を保ち、別 thread へ転送しない。受付成功は stdin write 完了や子プロセスの読み取り完了を意味しない。
返信本文が LF で終わっていなければ末尾に LF を1つ補う。本文中と既存末尾の改行は保持する。
live stdin には Slack reply の本文をそのまま渡し、command parsing 用の正規化を通さない。メンション・URL・引用符・エスケープ等は変換しない。実利用で不都合が判明した場合に見直す。
app_mention も同じ routing table を参照する。イベントの重複排除は今回追加せず、live stdin で二重投入を実測した場合に検討する。

## Process lifecycle / stdin
- registry は worker 間で共有する一時的な routing table とし、キーは ChannelID と RootThreadTimestamp の組だけにする。ConversationContext 全体をキーにしない。
- registry の値は thread に対応する live input endpoint とし、生の stdin 参照に限定しない。保持するのは現在の running process への入力先だけであり、会話履歴の source of truth は Slack とする。履歴・永続状態は持たせない。
- exec process の Start 成功後、初期 stdin を live input の先頭として順序確定してから endpoint を registry に公開する。以後の reply は必ず初期 stdin より後に流す。公開前に初期入力の write 完了を待つ必要はない。
- command chain の排他や同一 thread の多重起動制御は追加せず、既存の起動挙動を維持する。
- command chain の process 切り替え中に registry が空になる期間も、対象なしとして既存 routing へ進む。thread reservation や chain-level state でこの race を解消しない。
- 同じ thread で複数 process が生存する場合、最後に登録された process の endpoint を送信先とする。終了時は自身の登録と一致する場合だけ削除し、置換後の endpoint を古いプロセスが消さない。過去の送信先を保持・復元しない。
- 起動成功と入力受付開始、終了・起動失敗・timeout・キャンセルと受付停止を対応させる。
- 実行待ち command queue を live reply の経路にせず、実行中でも入力を届けられる構造にする。
- 初期 stdin（初回の2行目以降、continuation の transcript）を先に渡し、以後の reply を順に渡す。
- 初期 stdin が空なら何も書き込まず、空行も補わない。空でなく末尾が LF でなければ LF を1つ補い、既存の改行は保持する。
- 初期入力を渡し終えても stdin は閉じない。有限 reader を live reader に単純置換するだけの実装にはしない。
- exec は OS pipe の直接接続など、stdin 読み取り goroutine が Wait を保持しない接続を使う。
- endpoint は listener を blocking stdin write から切り離し、受け付けた入力を順番に転送する。即時受付できない reply は drop してログに残し、入力保持のために無制限な queue や goroutine を増やさない。
- 第一候補は live reply 用 channel の buffer 1 とする。producer / consumer の瞬間的なずれを吸収する最小バッファとして、転送中の入力とは別に未処理 reply を最大1件だけ保持する。writer が空いていれば受け付け、前の入力を処理中なら次の1件を保持し、その枠も埋まっていれば即時 drop してログに残す。
- 終了・キャンセル時は reader/writer と転送処理を解放し、blocked write を残さない。
- stdin を読んでいるかどうかの検出は行わない。

## 既存仕様との差分
SPEC-thread-continuation.md の「transcript 全体を stdin に渡して閉じる」は、exec のみ「渡した後も live stdin を維持する」に変更する。compose は従来の有限入力を維持する。
continuation の eligibility、履歴の組み立て、再実行の条件は変更しない。
EOF 待ちのコマンドは、入力末尾ではなく設定済み timeout 等によって終了する場合がある。

## Tech Stack / Project Structure / Code Style
Go 1.25.0（go.mod）、標準 os/exec、slack-go/slack v0.23.1 を使用する。
cmd/ は executor と runner、pubsub/ は Slack event と出力、main.go は依存の組み立てを担当する。
テストは各 package の *_test.go、仕様は .spec/ に置く。Slack API 型を cmd/ の lifecycle 管理へ持ち込まない。
既存の名前と gofmt を使う。識別子は既存定義を再利用する。
```go
type ConversationContext struct {
    ChannelID           string
    RootThreadTimestamp string
}
```

## Commands / Testing Strategy
- build: `go build ./...`
- test: `go test ./...`
- race: `go test -race ./...`
- static checks: `go vet ./...` / `golangci-lint run`
- dev: `go run . -config-file config.toml`（Slack 接続設定を用意した手動検証時）
unit test は routing、入力順序、登録・削除、終了競合を扱い、実プロセスの integration test は EOF と終了待ちを扱う。
数値の coverage 閾値は追加せず、次の観測可能な条件を検証する。

## Success Criteria
- date と1行 read は live stdin を開いたまま正常終了し、registry から解除される。
- fflush を使う awk は2回の入力を逐次出力し、入力間も生存する。
- wc -l は EOF 前に結果を出さず、設定済み timeout で停止・解除される。
- exec で起動失敗・自然終了・timeout・キャンセル後に入力処理が残らない。
- endpoint の公開直後に受け付けた reply も、初期 stdin の後に流れる。即時受付できなければ drop し、初期入力を追い越さない。
- 受け付けた live reply は履歴取得なしで転送され、本文に履歴や role prefix が混ざらない。
- 初期 stdin が空文字列なら何も書き込まない。`alpha` なら `alpha\n`、`alpha\n` ならそのまま渡る。初期メッセージに2行目以降がなければ、最初の reply より前に空行を送らない。
- stdin 未消費で転送が詰まっても listener が長時間停止せず、後続 reply は即時受付できなければ drop される。drop 理由と write 失敗をログで確認でき、reply 数に比例して queue や goroutine が増えない。
- writer が前の入力を処理中でも次の reply 1件は保持でき、その1件が未処理の間にさらに来た reply は即時 drop される。転送側が receive 待ちでなくてもバッファが空なら受け付けられる。
- 別 channel/thread、非許可ユーザー、自己出力が入力として混入しない。
- process 不在時の continuation と accept_thread_message の既存動作を維持する。
- chain の process 切り替え中に registry が空の場合も既存 routing に進む。endpoint が存在して drop した場合は既存 routing に進まない。
- 終了直前の reply や登録の置換で panic、deadlock が起きず、古いプロセスの終了が新しい参照を削除しない。
- 同じ thread に複数の exec process が存在する場合、最後に登録された process へ reply を送る。
- command chain と同一 thread の多重起動について、新しい予約・起動拒否・排他制御を導入しない。
- 上記の build / test / race / static checks が成功する。Slack から exec への実環境検証結果は別途記録する。

## Boundaries
- Always: 既存許可判定、timeout、入力順序、終了時の資源解放を維持して検証する。
- Regression: compose / HTTP runner の既存経路を維持する。compose の live stdin 検証・Success Criteria・実環境確認は別タスクで扱う。
- Ask first: 外部依存の追加・更新、対象範囲や確定した入出力仕様の変更。
- Never: opt-in 設定追加、履歴の永続化、Block Kit、stdin 読み取り状態の推測、失敗入力の continuation 再解釈。
- 今回は追加しない: thread reservation、chain-level state、多重起動制御、大きな固定キュー、Slack event 重複排除。

## Open Questions
現在の対象は一時的な登録・検索・転送・削除と、buffer 1 による即時受付および受付不能時の drop に限定する。重複排除は実測で必要性が確認された場合に限り再検討する。
