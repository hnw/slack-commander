# Spec: thread-continuation

## Objective

`continuation = "thread"` は、Slack thread の返信を受け、同じ command definition で新しいプロセスを起動して会話を継続する設定である。これは非同期実行モードではない。

プロセスが生存中の返信は、将来そのプロセスの stdin へ渡す通常の interaction とする。この仕様では実装しない。process lifecycle と continuation は別の概念として扱う。

## Configuration

```toml
[[commands]]
keyword = "agent *"
command = "agent *"
continuation = "thread"
```

`continuation` の未指定時は、プロセス終了後の thread reply による再実行を行わない。現在許可する値は `thread` のみとし、他の値は設定エラーとする。`continuation = "thread"` は `post_as_reply` を true に正規化する。

## Behavior

1. 初回実行は通常の command 実行と同じ process を起動し、出力を root thread へ投稿する。
2. thread reply を受信すると、current configuration から root message の1行目を再評価する。
3. 現在も `continuation = "thread"` に一致するときだけ、thread history を取得して transcript を構築する。
4. 元の command と同じ設定で新しい process を起動し、transcript 全体を stdin に渡して閉じる。
5. stdout / stderr / 終了状態は通常実行と同じ方法で処理し、同じ root thread に出力する。

サーバーは command ID や conversation history を永続化しない。再起動後も Slack の root message と thread replies、現在の設定から再構成する。

## Transcript

root の command line は stdin に含めない。root の2行目以降、root 後の許可済みメッセージ、および slack-commander の出力を時系列で次の形式に直列化する。

```text
user: initial request

assistant: first response

user: follow-up question
```

slack-commander 自身の出力だけを `assistant:` とし、許可済みの外部投稿は actor type に関係なく `user:` とする。root App mention は初回実行と同じ target-specific normalization を適用する。

## Boundaries

- 実行中 process への stdin 転送、waiting status protocol、`thread` 以外の continuation policy、transcript format 選択、`post_as_reply` の廃止、reply broadcast 出力モデルは今回の範囲外とする。
- thread reply は root command の matcher 入力にしない。eligibility は最初の parsed command が current configuration で `continuation = "thread"` に一致するかだけで判定する。
- Slack API error、空の history、未知の continuation は実行しない。

## Verification

- continuation 未指定では process 終了後の reply が再実行されない。
- `continuation = "thread"` では reply により新しい process が起動し、conversation 全体が stdin に渡る。
- root App mention 正規化、role 順序、複数 continuation、command output の再利用を自動テストする。
- `go test ./...`、`go vet ./...`、`golangci-lint run`、`go build ./...` を通す。
