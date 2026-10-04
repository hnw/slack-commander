# slack-commander

SlackからCLIツール、スクリプト、HTTP APIを実行し、その出力や失敗をSlackで確認できるセルフホスト型のSlack botです。

設定ファイルにキーワードと実行内容を登録するだけで、既存のCLIツールやスクリプトをSlackから利用できます。ツール側にSlack固有の処理を追加する必要はありません。

コマンドの出力は元の投稿に対応するSlackスレッドへ送られます。実行中のプロセスへスレッドから標準入力を送ることもできるため、人間の入力が必要な処理や対話的なCLIもSlackから扱えます。

SlackとはSocket Modeで接続するため、slack-commanderを動かすマシンに公開HTTPエンドポイントを用意する必要はありません。

## 特徴

* 既存のCLIツールやスクリプトをそのままSlackから実行できます
* コマンドごとにわかりやすい`keyword`を定義できます
* コマンドの出力を元の投稿のSlackスレッドへ送信します
* Slackスレッドへの返信を、実行中プロセスの標準入力へ渡せます
* スレッドへの返信に応じて別のコマンドを実行できます
* `exec`、`compose`、`http`の3種類のrunnerを利用できます
* TTYを必要とするCLIも限定的に利用できます
* Slackのリマインダーを`cron`や`at`の代わりとして利用できます
* Socket Modeを利用するため、公開Webサーバーは不要です
* 実行を許可するユーザーやチャンネルを制限できます

## Quick Start

### 1. Slack Appを作成する

SlackのApp管理画面から新しいAppを作成し、このリポジトリの [`manifest.yaml`](./manifest.yaml) をインポートします。

AppをWorkspaceへインストールし、`xoxb-`から始まるBot User OAuth Tokenを取得してください。

続いて、App-Level Tokenを`connections:write` scope付きで作成し、`xapp-`から始まるトークンを取得します。

チャンネルで利用する場合は、slack-commanderのbotを対象チャンネルへ追加してください。

### 2. 設定ファイルを作成する

`config.toml.example`をコピーします。

```console
git clone https://github.com/hnw/slack-commander
cd slack-commander
cp config.toml.example config.toml
```

まずは最小限、Slackのtoken、利用を許可するユーザー、コマンドを設定します。

```toml
slack_bot_token = "xoxb-..."
slack_app_token = "xapp-..."

allowed_user_ids = ["U0123456789"]

[[commands]]
keyword = "date"
command = "date"
```

この状態でSlackに

```text
date
```

と投稿すると、`date`コマンドの出力がSlackへ返ります。

トップレベルの`allowed_user_ids`と`allowed_channel_ids`が両方空の場合、デフォルトでは起動できません。コマンド側で制限を追加しても、`allow_unsafe_open_access = true`が必要です。

### 3. 起動する

ソースから起動する場合:

```console
go build
./slack-commander --config-file=config.toml
```

ビルドにはGo 1.25以降が必要です。

起動前に設定ファイルだけを検証するには、`--check-config`を指定します。Slackへ接続せず、成功時は何も出力せず終了します。

```console
./slack-commander --check-config --config-file=config.toml
```

Dockerで運用する場合は [Dockerでの運用](./docs/docker.md) を参照してください。

## Runner

slack-commanderはSlackへの投稿を`keyword`と照合し、マッチした設定に従って処理を実行します。

実行方法はコマンドごとに3種類のrunnerから選べます。

| runner    | 用途                                    |
| --------- | ------------------------------------- |
| `exec`    | slack-commanderと同じ環境で外部コマンドを実行する      |
| `compose` | Docker Composeで定義したservice内でコマンドを実行する |
| `http`    | HTTP APIを呼び出す                         |

### exec

`runner`を省略すると`exec`になります。

```toml
[[commands]]
keyword = "uname"
command = "uname -mrs"
```

Slackに

```text
uname
```

と投稿すると、slack-commanderが動作している環境で`uname -mrs`を実行します。

### compose

Docker Composeで定義したserviceを実行できます。

```toml
[[commands]]
keyword = "echo *"
runner = "compose"
command = "worker echo *"
```

Slackに

```text
echo hello
```

と投稿すると、`worker` service内で`echo hello`を実行します。

slack-commander自身をコンテナで実行する場合の構成や、DooD（Docker outside of Docker）については [Dockerでの運用](./docs/docker.md) を参照してください。

### http

HTTP APIをコマンドとして利用することもできます。

```toml
[[commands]]
keyword = "notify *"
runner = "http"
method = "POST"
url = "https://example.com/hooks/notify"
headers = { "Content-Type" = "application/json" }
body = '{"text":"*"}'
```

Slackに

```text
notify hello
```

と投稿すると、`*`に`hello`を展開してHTTPリクエストを送信します。

## ワイルドカード

`keyword`では、空白で区切られた単独の`*`を1つだけワイルドカードとして使用できます。

```toml
[[commands]]
keyword = "echo *"
command = "echo *"
```

この設定に対して

```text
echo hello world
```

と投稿すると、`*`には`hello world`がマッチします。

マッチした内容は、`command`内のすべての`*`へ展開されます。`http` runnerでは`url`、`body`、`headers`内の`*`にも展開できます。

詳細なマッチング規則は [設定リファレンス](./docs/config.md) を参照してください。

## スレッドでの対話

コマンドごとに`interaction`を指定すると、Slackスレッドへの返信を処理できます。

| `interaction` | 動作                                                |
| ------------- | ------------------------------------------------- |
| `oneshot`     | 1回だけ実行します。スレッドへの返信は処理しません                         |
| `stdin`       | スレッドへの返信を実行中プロセスの標準入力へ渡します                        |
| `command`     | スレッドへの返信を`[[commands.replies]]`に従って別のコマンドとして処理します |

省略時は`oneshot`です。

### 標準入力へ送る

`interaction = "stdin"`を指定すると、同じSlackスレッドへの返信を実行中プロセスの標準入力へ渡します。stdin commandは`;`、`&&`、`||`でつなぐchain内でも使用できます。返信は、その時点で実行中のstdin commandの許可リストと本文設定に従って照合します。実行中のstdin commandがなければ返信は無視します。

```toml
[[commands]]
keyword = "agent"
command = "my-agent"
interaction = "stdin"
timeout = "1h"
```

たとえばCLIが、

```text
Continue? [y/N]
```

と出力した場合、その出力はSlackスレッドへ送られます。

同じスレッドへ

```text
y
```

と返信すると、その内容が実行中の`my-agent`の標準入力へ渡されます。

起点メッセージの2行目以降も、最初の標準入力として利用できます。

`interaction = "stdin"`は`exec`と`compose`で利用できます。`http`では利用できません。`interaction = "command"`を含むchainは使用できません。

### 返信に応じてコマンドを実行する

`interaction = "command"`では、スレッドへの返信ごとに実行するコマンドを定義できます。

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

この設定では、`todo ...`から始まったスレッド内だけで`cancel`やその他の返信を処理します。

`interaction`ごとの細かい動作や、`[[commands.replies]]`の継承規則については [設定リファレンス](./docs/config.md) を参照してください。

## TTY

TTYを必要とするCLIでは、`exec`または`compose`に`tty = true`を指定できます。

```toml
[[commands]]
keyword = "agent"
command = "my-agent"
interaction = "stdin"
tty = true
timeout = "1h"
```

TTY使用時は標準出力と標準エラー出力を1本の端末出力として扱い、一般的な端末制御シーケンスの一部を除去してSlackへ送ります。

完全な端末エミュレーションを行うわけではありません。フルスクリーンTUI、カーソル位置を利用した画面更新、端末サイズの変更、特殊キー入力などには対応していません。

TTYがなくても正常に動作するCLIでは、通常どおり`tty = false`のまま利用してください。

詳細は [設定リファレンス](./docs/config.md) を参照してください。

## コマンドの出力

コマンドの出力は、起点となったSlackメッセージのスレッドへ投稿されます。

`output_format`では、出力の表示方法を選択できます。

```toml
output_format = "plain"
```

```toml
output_format = "monospaced"
```

```toml
output_format = "markdown"
```

`reply_broadcast`はデフォルトで`true`です。`false`を指定すると、結果をチャンネルへbroadcastせずスレッド内だけに投稿します。

```toml
reply_broadcast = false
```

投稿時のユーザー名やアイコンもコマンドごとに変更できます。

```toml
username = "Clock"
icon_emoji = ":alarm_clock:"
```

## Slack Reminderから実行する

コマンドに`accept_reminder = true`を指定すると、Slack Reminderによる投稿も`keyword`との照合対象になります。既定値は`false`で、親コマンドから継承しません。

```toml
[[commands]]
keyword = "date"
command = "date"
accept_reminder = true
```

Reminderではユーザー許可リストを適用せず、チャンネル許可リストは適用します。トップレベルには設定できません。Slack側のReminderと組み合わせることで、簡単な定期実行に利用できます。

## Docker

公式コンテナイメージは、slack-commander本体だけを実行する最小構成です。

shell、Docker CLI、Compose CLI、一般的なCLIツールを含めないことで、不要な実行環境を持ち込まず、攻撃面を小さくしています。

コンテナで運用する場合は、slack-commander本体へさまざまなツールを追加するよりも、実際の処理をsibling containerへ分離し、`compose` runnerから実行する構成を推奨します。

DooDの仕組み、Compose projectのパスに関する注意、credentialやnetworkの分離、Docker socketの権限については [Dockerでの運用](./docs/docker.md) を参照してください。

## Security

slack-commanderはSlackから外部コマンドやHTTP APIを実行できるため、実行元を制限して利用することを推奨します。

トップレベルの`allowed_user_ids`または`allowed_channel_ids`で最大の利用範囲を設定できます。

```toml
allowed_user_ids = ["U0123456789"]
allowed_channel_ids = ["C0123456789"]
```

コマンドと返信はトップレベルまたは親コマンドの許可リストを継承し、さらに範囲を狭められます。親にないIDを追加すると設定エラーになります。空配列も親の値を継承します。

```toml
allowed_user_ids = ["U0123456789", "U-admin"]
allowed_channel_ids = ["C0123456789", "C-ops"]

[[commands]]
keyword = "status"
command = "status"
allowed_user_ids = ["U0123456789"]
allowed_channel_ids = ["C0123456789"]

[[commands]]
keyword = "deploy"
command = "deploy"
allowed_user_ids = ["U-admin"]
allowed_channel_ids = ["C-ops"]
```

Reminderではユーザー許可リストを適用せず、チャンネル許可リストは適用します。自身のボットの投稿は常に無視し、それ以外のボット投稿はBot IDを通常のsender IDとして許可リストで判定します。

旧トップレベル`accept_reminder`と`accept_bot_message`は使用できず、設定エラーになります。Reminderを有効にする場合は各commandまたはreplyに`accept_reminder = true`を設定してください。

トップレベルのユーザーとチャンネルの制限が両方ない構成は、コマンド側で制限してもデフォルトでは起動できません。

制限なしでの起動が必要な場合は、

```toml
allow_unsafe_open_access = true
```

を明示する必要があります。

Dockerで運用する場合の権限構成については [Dockerでの運用](./docs/docker.md) も参照してください。

## Documentation

* [設定リファレンス](./docs/config.md) — 設定項目と詳細な動作仕様
* [Dockerでの運用](./docs/docker.md) — 公式コンテナ、DooD、実行先コンテナの分離
* [内部構造](./docs/internal.md) — slack-commander内部の構成

## License

MIT License
