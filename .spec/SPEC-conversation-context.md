# Spec: conversation-context

## Objective

Slack のイベントを、返信対象のイベント本体とは独立した command output の conversation destination として扱えるようにする。ユーザーに見える成果は、スレッド内の任意のメッセージを起点としても、同じ root thread に結果を投稿できること。これにより後続の対話機能は、イベントの種類や返信メッセージの timestamp に依存せず出力先を識別できる。

受入条件:

- 通常のチャンネル投稿は `channel ID` と自分の `timestamp` を root thread として持つ。
- スレッド内の返信は `channel ID` とイベントの `thread_ts` を root thread として持つ。
- 同じスレッドのすべての入力は、同一の会話コンテキストを得る。
- 出力は明示された root thread に投稿できる。

## Tech Stack

Go 1.25、`github.com/slack-go/slack` v0.18.0、Socket Mode。既存の `cmd` パッケージがコマンド実行、`pubsub` パッケージが Slack 入出力を担う。

## Commands

```sh
go test ./...
go vet ./...
golangci-lint run
go build ./...
```

## Project Structure

```text
cmd/                  → CommandInput / CommandOutput と実行パイプライン
pubsub/slackListener.go → Slack event からの入力変換
pubsub/slackWriter.go   → CommandOutput の Slack 投稿
pubsub/*_test.go        → Slack 境界のユニットテスト
cmd/*_test.go           → 実行パイプラインのユニットテスト
.spec/                 → 承認済み仕様と計画
```

## Code Style

新しいコンテキストは Slack の生イベント型を漏らさない値オブジェクトとして定義し、入力と出力に同じ値を渡す。zero value の `ConversationContext` は context 未設定を表し、その場合は既存の `ReplyInfo` ベースの Slack 投稿挙動へフォールバックしてよい。conversation-aware code では可能な限り context を明示的に設定する。

```go
type ConversationContext struct {
	ChannelID           string
	RootThreadTimestamp string
}
```

- Go の標準フォーマットに従う。
- exported identifier は GoDoc を付ける。
- `ReplyInfo` は invocation event / reaction target に保持し、context 未設定時の後方互換 fallback に使ってよい。新しい conversation-aware output destination の唯一の情報源にしてはならない。
- 既存の公開設定名・既存メッセージ処理の既定値は変更しない。

## Testing Strategy

`pubsub/slackListener_test.go` で MessageEvent と AppMentionEvent の通常投稿・スレッド返信をテーブル駆動で検証する。`pubsub/slackWriter` のテストで、出力の root thread が Slack 投稿パラメータへ使われることを確認する。既存の `go test ./...` を回帰確認に使う。

## Boundaries

- Always: Slack event の channel と timestamp を入力境界で検証し、通常投稿とスレッド返信の両方をテストする。後方互換の既存テストを維持する。
- Ask first: 既存の TOML 設定名の変更、Slack API 権限の追加、依存関係の追加、永続ストアの導入。
- Never: `ReplyInfo` の具体型キャストを新たな会話識別ロジックに追加する、Slack token をコミットする、既存の失敗テストを削除する。

## Success Criteria

- `CommandInput` と `CommandOutput` が typed な conversation context を運べる。
- root の算出規則が通常投稿・スレッド返信・app mention で一貫する。
- thread への出力先は、context の root thread から決定される。
- `go test ./...`、`go vet ./...`、`golangci-lint run`、`go build ./...` が通る。

## Open Questions

なし。型契約と root 算出規則は、本仕様書のレビュー承認後に確定する。
