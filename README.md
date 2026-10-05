# slack-commander

SlackからCLIツール、スクリプト、HTTP APIを実行できるセルフホスト型のSlack botです。

設定ファイルにキーワードと実行内容を登録すると、既存のCLIツールをSlackから呼び出せます。コマンドの出力は元の投稿のスレッドへ送られ、実行中プロセスへの標準入力や、スレッド返信を起点とした別コマンドの実行にも対応します。

SlackとはSocket Modeで接続するため、公開HTTPエンドポイントは不要です。

## 特徴

* `exec`、`compose`、`http`の3種類のrunner
* コマンド出力をSlackスレッドへ送信
* スレッド返信を実行中プロセスの標準入力へ転送
* スレッド返信に応じた別コマンドの実行
* `;`、`&&`、`||`によるコマンドの連結
* TTYを必要とするCLIへの限定的な対応
* ユーザー・チャンネル単位の実行制限

## Quick Start

### 1. Slack Appを作成する

SlackのApp管理画面から新しいAppを作成し、このリポジトリの [`manifest.yaml`](./manifest.yaml) をインポートします。

AppをWorkspaceへインストールし、Bot User OAuth Tokenを取得します。続いて、`connections:write` scopeを持つApp-Level Tokenを作成してください。

チャンネルで利用する場合は、slack-commanderのbotを対象チャンネルへ追加します。

### 2. 設定ファイルを作成する

`config.toml.example`をコピーします。

```console
git clone https://github.com/hnw/slack-commander
cd slack-commander
cp config.toml.example config.toml
```

最小構成は次のようになります。

```toml
slack_bot_token = "xoxb-..."
slack_app_token = "xapp-..."

allowed_user_ids = ["U0123456789"]

[[commands]]
keyword = "date"
command = "date"
```

Slackへ

```text
date
```

と投稿すると、`date`の出力がその投稿のスレッドへ送られます。

トップレベルの`allowed_user_ids`と`allowed_channel_ids`が両方空の場合は起動できません。制限なしで起動する場合は、`allow_unsafe_open_access = true`を明示する必要があります。

### 3. 起動する

```console
go build
./slack-commander --config-file=config.toml
```

ビルドにはGo 1.26以降が必要です。

設定だけを検証する場合は`--check-config`を使用できます。

```console
./slack-commander --check-config --config-file=config.toml
```

バージョンを確認するには`--version`を指定します。

```console
./slack-commander --version
```

Dockerで運用する場合は [Dockerでの運用](./docs/docker.md) を参照してください。

## Runner

コマンドごとに実行方法を選択できます。

| runner | 用途 |
| --- | --- |
| `exec` | slack-commanderと同じ環境で外部コマンドを実行する |
| `compose` | Docker Composeで定義したservice内でコマンドを実行する |
| `http` | HTTP APIを呼び出す |

`runner`を省略すると`exec`になります。

```toml
[[commands]]
keyword = "uname"
command = "uname -mrs"
```

Docker Composeのserviceで実行する場合は次のように指定します。

```toml
[[commands]]
keyword = "echo *"
runner = "compose"
command = "worker echo *"
```

HTTP APIもコマンドとして登録できます。

```toml
[[commands]]
keyword = "notify *"
runner = "http"
method = "POST"
url = "https://example.com/hooks/notify"
body = '{"text":"*"}'
```

`keyword`では、空白で区切られた単独の`*`を1個までワイルドカードとして使用できます。詳しいマッチング規則と展開先は [設定リファレンス](./docs/config.md) を参照してください。

## スレッドでの対話

`interaction`を指定すると、起点メッセージに続くSlackスレッドへの返信を処理できます。

| `interaction` | 動作 |
| --- | --- |
| `oneshot` | 1回だけ実行し、スレッドへの返信は処理しない |
| `stdin` | スレッドへの返信を実行中プロセスの標準入力へ渡す |
| `command` | スレッドへの返信に応じて`[[commands.replies]]`を実行する |

省略時は`oneshot`です。

### 標準入力へ送る

```toml
[[commands]]
keyword = "agent"
command = "my-agent"
interaction = "stdin"
timeout = "1h"
```

この設定で起動したプロセスへは、同じSlackスレッドから標準入力を送れます。

`interaction = "stdin"`は`;`、`&&`、`||`でつないだコマンドチェーン内でも利用できます。返信は、その時点で実行中のコマンドの許可設定に従って受け付けます。標準入力を受け付けるコマンドが実行中でなければ返信は無視されます。

起点メッセージの2行目以降も最初の標準入力として利用できます。

`interaction = "stdin"`は`exec`と`compose`で利用できます。`http`では利用できません。

### 返信に応じてコマンドを実行する

```toml
[[commands]]
keyword = "todo *"
command = "todo-wrapper *"
interaction = "command"

[[commands.replies]]
keyword = "cancel"
command = "todo-wrapper --cancel"

[[commands.replies]]
keyword = "*"
command = "todo-wrapper *"
```

この場合、`todo ...`から始まったスレッド内でだけ返信用コマンドを使用できます。

`interaction = "command"`を含むchainは使用できません。

返信設定の継承規則は [設定リファレンス](./docs/config.md#replies)、stdinの詳細な動作は [対話的な標準入力](./docs/config.md#対話的な標準入力) を参照してください。

## TTY

`exec`または`compose`では、TTYを必要とするCLIに`tty = true`を指定できます。

```toml
[[commands]]
keyword = "agent"
command = "my-agent"
interaction = "stdin"
tty = true
timeout = "1h"
```

TTY対応は完全な端末エミュレーションではありません。対応範囲と制約は [設定リファレンス](./docs/config.md#ttyの入出力) を参照してください。

## Docker

公式コンテナイメージにはslack-commander本体だけを含めています。

コンテナで運用する場合は、CLIツールをslack-commander自身へ追加するより、実際の処理を別コンテナへ分離し、`compose` runnerから実行する構成を想定しています。

`compose` runnerが接続するDocker Engineは、Docker Engine 28以降が必要です。

Docker socket、Compose projectのパス、credentialやnetworkの分離については [Dockerでの運用](./docs/docker.md) を参照してください。

## Security

slack-commanderはSlackから外部コマンドやHTTP APIを実行できるため、利用できるユーザーやチャンネルを制限して運用してください。

```toml
allowed_user_ids = ["U0123456789", "U9876543210"]
allowed_channel_ids = ["C0123456789", "C9876543210"]
```

各コマンドや返信用コマンドでは、親の許可範囲をさらに狭められます。

```toml
[[commands]]
keyword = "deploy"
command = "deploy"
allowed_user_ids = ["U9876543210"]
allowed_channel_ids = ["C9876543210"]
```

Reminderとbot投稿では許可リストの扱いが異なります。詳しくは [accept_reminder](./docs/config.md#accept_reminder-bool) と [allowed_user_ids](./docs/config.md#allowed_user_ids-string) を参照してください。

## Documentation

* [設定リファレンス](./docs/config.md) — 設定項目と動作仕様
* [Dockerでの運用](./docs/docker.md) — コンテナ構成とDocker利用時の注意
* [内部構造](./docs/internal.md) — slack-commander内部の構成

## License

MIT License
