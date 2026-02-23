## 設定について

## トップレベル設定項目

### slack_bot_token `string`

Slackのボットトークンを指定します。`xoxb-`から始まります。
Slack管理画面「Settings」「OAuth & Permissions」「OAuth Tokens for Your Workspace」からコピーします。

### slack_app_token `string`

Slackのアプリレベルトークンを指定します。`xapp-`から始まります。
Slack管理画面「General」「Basic Information」「App-Level Tokens」で生成します。

### num_workers `int`

外部コマンドの最大並列数を指定します。

### accept_reminder `bool`

Reminderの発言もキーワードマッチの対象にするか（`cron`や`at`の代用になります）

### accept_bot_message `bool`

Botの発言もキーワードマッチの対象にする

### accept_thread_message `bool`

返信（スレッド内）の発言もキーワードマッチの対象にする

### allowed_user_ids `[]string`

コマンドを実行できるユーザーIDの許可リストを指定します。空の場合はユーザー制限なしです。

### allowed_channel_ids `[]string`

コマンドを実行できるチャンネルIDの許可リストを指定します。空の場合はチャンネル制限なしです。

`allowed_user_ids` と `allowed_channel_ids` の両方を空にする構成は、デフォルトでは起動時エラーになります。

### allow_unsafe_open_access `bool`

`true` にすると、`allowed_user_ids` と `allowed_channel_ids` の両方が空でも起動を許可します。

この設定は後方互換のための暫定逃げ道です。セキュリティの観点から、通常は `false` のまま使ってください。

## コマンドごとの設定項目

### keyword `string`

マッチするキーワードを指定します。キーワードにはワイルドカード `*` を含めることができます。

ワイルドカードは1つのキーワード指定について1個しか使えません。また、単体のトークンになっていないとワイルドカードと見なされません（例：`ssh*`はワイルドカード扱いにならない、`ssh *`なら大丈夫）

2つ以上のキーワードにマッチするような場合、先に定義した方が採用されます。

### runner `string`

コマンドの実行ランナーを指定します。省略時は `exec` です。

* `exec`: ホスト上で外部コマンドを実行します（従来通り）。
* `compose`: `docker-compose.yml` のサービスを実行します。`command` には `<service> <args>` を指定してください。
* `http`: HTTPリクエストを送信します。`method` と `url` を指定してください。

### command `string`

キーワードにマッチした場合に起動するコマンドを指定します。

ワイルドカード `*` が指定された場合、キーワードの `*` にマッチした内容が展開されます。

`runner = "http"` の場合、この項目は使用されません。

### method `string`

`runner = "http"` の場合に使用するHTTPメソッドを指定します。省略時は `POST` です。

### url `string`

`runner = "http"` の場合に送信先URLを指定します。必須です。

### headers `map[string]string`

`runner = "http"` の場合に付与するHTTPヘッダーを指定します。
TOMLのインラインテーブル形式で指定してください（例: `headers = { "Content-Type" = "application/json" }`）。

### body `string`

`runner = "http"` の場合に送信するリクエストボディを指定します。
キーワードの `*` にマッチした文字列があれば、`body` 内の `*` がその文字列で置換されます。
同様に `url` や `headers` の値に `*` が含まれている場合も置換されます。

### icon_emoji `string`

botがSlackにポストする時のアイコンをSlack絵文字で指定します。

### icon_url `string`

botがSlackにポストする時のアイコンをURLで指定します。

### username `string`

botがSlackにポストする時のユーザー名を指定します。

### monospaced `bool`

コマンドの出力を等幅フォントで表示します。

### post_as_reply `bool`

コマンドの出力をスレッド形式でポストします。

### always_broadcast `bool`

コマンドの出力をスレッド形式にした場合に、チャンネルにもポストします。

### timeout `int`

外部コマンドのタイムアウト時間を秒で指定します。
