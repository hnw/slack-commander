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

コマンドを実行できるユーザーIDの許可リストを指定します。空の場合は全ユーザー許可です。

### allowed_channel_ids `[]string`

コマンドを実行できるチャンネルIDの許可リストを指定します。空の場合は全チャンネル許可です。

## コマンドごとの設定項目

### keyword `string`

マッチするキーワードを指定します。キーワードにはワイルドカード `*` を含めることができます。

ワイルドカードは1つのキーワード指定について1個しか使えません。また、単体のトークンになっていないとワイルドカードと見なされません（例：`ssh*`はワイルドカード扱いにならない、`ssh *`なら大丈夫）

2つ以上のキーワードにマッチするような場合、先に定義した方が採用されます。

### command `string`

キーワードにマッチした場合に起動するコマンドを指定します。

ワイルドカード `*` が指定された場合、キーワードの `*` にマッチした内容が展開されます。

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
