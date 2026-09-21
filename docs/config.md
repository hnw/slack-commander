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

### reply_broadcast `bool`

コマンドの出力は常に入力に対応する root thread への reply として投稿されます。

`true`（デフォルト）にすると、同じ thread reply が Slack の “Also send to channel” 相当の reply broadcast によりチャンネルにも表示されます。
`false` にすると、出力は thread reply としてのみ投稿されます。

### continuation `string`

プロセス終了後の継続方法を指定します。省略時は継続しません。

* `thread`: root 投稿のスレッド返信で新しいプロセスを起動し、conversation を継続します。command output は常に root thread への reply として投稿されます。継続時は root 投稿の1行目を command と argv の決定にのみ使用し、stdin には含めません。root 投稿の2行目以降と後続の thread conversation を stdin に渡します。

exec / compose runner の実行中は、同じ thread への返信を実行中プロセスの stdin に渡します。
この転送には `continuation` や `accept_thread_message` の有効化は不要で、既存の投稿者・チャンネルの許可判定が適用されます。
HTTP runner は対象外です。

初期 stdin は空文字列なら何も書き込まず、空でなければ末尾に LF がない場合だけ補います。
返信は Slack の本文をそのまま渡し、末尾の LF だけ必要に応じて補います。メンション・URL・引用符などは変換しません。
初期入力と受け付けた返信は順番に転送します。転送中の入力とは別に未処理の返信を最大1件保持し、その枠も埋まっていれば新しい返信を drop してログに残します。
drop した返信は再試行せず、continuation にも回しません。受付後の stdin write error は executor 側でログに残します。

同じ thread に複数のプロセスがある場合、最後に登録されたプロセスが送信先になります。
送信先がない場合は既存の continuation / thread 処理に進みます。command chain のプロセス切り替え中も同じ扱いです。
初期入力の転送後も stdin は開いたままです。`wc -l` のように EOF を必要とするコマンドは、`stdin_idle_timeout` を設定すると無入力時に EOF を受け取り、正常終了できます。

### stdin_idle_timeout `int`

exec / compose runner の interactive stdin を、最後に入力を受け付けてから何秒後に閉じるか指定します。
省略または `0` は無効で、自動では EOF を送りません。HTTP runner には適用しません。
負数は設定エラーです。

計測はプロセスの Start 成功後、stdin writer を session に接続した時点から始まります。
初期入力が空でも計測し、空でなければ最初の入力として扱います。
返信を受け付けるたびに期限を延長します。書き込み完了は待たず、Busy で drop した返信や Closed で拒否した返信では延長しません。

期限が来ると stdin を閉じ、通常の EOF を渡します。プロセスや compose container を強制終了する設定ではありません。
入力先の登録も解除するため、その後の返信は送信先がない場合の既存 routing に進みます。
すでに入力先を取得した返信が終了と競合して Closed になった場合は、continuation に回しません。

```toml
[[commands]]
keyword = "agent"
command = "..."
stdin_idle_timeout = 300
timeout = 3600
```

この例では、300秒間入力を受け付けなければ EOF を渡します。
EOF 後も終了しないプロセスには、起動から3600秒の `timeout` が最終的な安全弁として働きます。

### timeout `int`

外部コマンドのタイムアウト時間を秒で指定します。
プロセスの実行時間を制限する設定で、stdin の無入力時間を計る `stdin_idle_timeout` とは独立しています。
