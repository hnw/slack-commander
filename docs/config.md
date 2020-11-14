## 設定について

## 設定例

[config.toml.example](../config.toml.example)

## トップレベル設定項目

### slack_token `string`

SlackのOAuthトークンを指定します。

### num_workers `int`

外部コマンドの最大並列数を指定します。

### accept_reminder `bool`

Reminderの発言もキーワードマッチの対象にするか（`cron`や`at`の代用になります）

### accept_bot_message `bool`

Botの発言もキーワードマッチの対象にする

### accept_thread_message `bool`

返信（スレッド内）の発言もキーワードマッチの対象にする

## コマンドごとの設定項目

### keyword `string`

マッチするキーワードを指定します。キーワードにはワイルドカード `*` を含めることができます。

指定したキーワードのうち2つ以上にマッチする場合、先に定義した方が採用されます。

### aliases `[]string`

keywordのエイリアスを設定します。

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
