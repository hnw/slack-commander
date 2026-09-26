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

Reminderの発言もキーワードマッチの対象にするか指定します。`cron`や`at`の代用になります。

### accept_bot_message `bool`

Botの発言もキーワードマッチの対象にするか指定します。

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

ワイルドカードは1つのキーワード指定につき1個だけ使用できます。また、単体のトークンになっている必要があります。たとえば `ssh*` はワイルドカードとして扱われませんが、`ssh *` はワイルドカードとして扱われます。

複数のキーワードにマッチする場合は、先に定義したものが採用されます。

### interaction `string`

root commandがSlack thread上の追加入力をどう扱うかを指定します。省略時は`oneshot`です。

* `oneshot`: root messageの2行目以降をstdinへ渡します。thread replyは無視します。`;`、`&&`、`||`によるcommand chainを使用できます。
* `stdin`: root messageの2行目以降を初期stdinへ渡します。実行中のprocessが同じthreadのreplyを受け取ると、その本文をstdinへ渡します。送信先がないreplyは無視します。
* `command`: root messageおよびreplyの1行目だけでkeyword matchingを行います。1行目との境界となる改行を含め、2行目以降を1個のargvとして末尾へ追加します。たとえば`echo foo\nbar`は概念的に`echo "foo" "\nbar"`として実行されます。replyは`[[commands.replies]]`だけで評価します。

`stdin`と`command`ではcommand chainを使用できません。`runner = "http"`では`stdin`を使用できません。

`interaction`はroot commandの属性です。`[[commands.replies]]`には指定できません。

### replies

root messageでこのcommandにマッチしたSlack thread内の返信だけに適用するcommandを、`[[commands.replies]]`として定義します。reply commandはグローバルな`[[commands]]`には含まれません。

reply commandは通常のcommandと同じ`keyword`、`command`、`runner`、`timeout`、`tty`、`stdin_idle_timeout`、HTTP runner用設定、およびreply表示関連設定を指定できます。ただし、`interaction`と`[[commands.replies.replies]]`のような入れ子は指定できません。

thread routeのcache miss時はSlack history APIでroot messageを取得するため、古いthreadのreply routingを復元するにはbot tokenに対応する履歴取得権限が必要です。

```toml
[[commands]]
keyword = "todo *"
command = "todo-wrapper *"

[[commands.replies]]
keyword = "cancel"
command = "todo-wrapper --cancel"

[[commands.replies]]
keyword = "*"
command = "todo-wrapper *"
```

### runner `string`

コマンドの実行ランナーを指定します。省略時は `exec` です。

* `exec`: ホスト上で外部コマンドを実行します。
* `compose`: `docker-compose.yml` のサービスを実行します。`command` には `<service> <args>` を指定してください。
* `http`: HTTPリクエストを送信します。`method` と `url` を指定してください。

### Slack thread context

Slackから起動する`exec`および`compose` runnerのコマンドには、次の環境変数を設定します。

* `SLACK_CHANNEL_ID`: Slack channel ID
* `SLACK_THREAD_TS`: root messageのtimestamp

root messageから起動した場合も、そのmessage自身のtimestampを`SLACK_THREAD_TS`に使用します。そのため、同じSlack threadに属するroot messageとreplyでは同じ値になります。これらの値は実行時のSlack eventから得るため、既存の同名環境変数より優先されます。

同じSlack threadから起動したcommandは同時には実行されません。mutexの待機順は厳密なFIFOではなく、待機中のcommandも`num_workers`のworkerを1つ使用します。

### command `string`

キーワードにマッチした場合に起動するコマンドを指定します。

ワイルドカード `*` が指定された場合、キーワードの `*` にマッチした内容が展開されます。

`runner = "http"` の場合、この項目は使用されません。

### tty `bool`

`true` にすると、`exec` または `compose` runnerでTTYを確保してコマンドを実行します。省略時は `false` です。

TTYを要求するCLIのための互換機能です。TTYなしで正常に動作するCLIでは、通常どおり `tty = false` のまま利用してください。

TTYではstdoutとstderrを分離せず、1本のterminal outputとしてSlackへ投稿します。ESCで始まる7-bit形式のterminal control sequenceは除去します。raw 8-bit C1 controlは、UTF-8のcontinuation byteと値域が重なるため解釈しません。

CRLFと単独CRはLFへ正規化します。cursor positioningによる画面状態の再現、full-screen TUI、terminal resize、特殊キー入力には対応しません。

Slackスレッドから受け取ったinteractive inputは、TTY上のEnter操作に相当するCRで終端します。通常のinteractive inputはLFで終端します。

TTYでは `stdin_idle_timeout` を使用できません。コマンドが応答しない場合の安全弁には `timeout` を指定してください。

```toml
[[commands]]
keyword = "opencode *"
command = "opencode *"
tty = true
timeout = 3600
```

### method `string`

`runner = "http"` の場合に使用するHTTPメソッドを指定します。省略時は `POST` です。

### url `string`

`runner = "http"` の場合に送信先URLを指定します。必須です。

### headers `map[string]string`

`runner = "http"` の場合に付与するHTTPヘッダーを指定します。

TOMLのインラインテーブル形式で指定してください。

```toml
headers = { "Content-Type" = "application/json" }
```

### body `string`

`runner = "http"` の場合に送信するリクエストボディを指定します。

キーワードの `*` にマッチした文字列があれば、`body` 内の `*` がその文字列で置換されます。同様に、`url` や `headers` の値に `*` が含まれている場合も置換されます。`interaction = "command"`では、matcherで得たwildcard引数と2行目以降から追加された引数を連結して、`*` の展開値に使用します。

### icon_emoji `string`

botがSlackにポストする時のアイコンをSlack絵文字で指定します。

### icon_url `string`

botがSlackにポストする時のアイコンをURLで指定します。

### username `string`

botがSlackにポストする時のユーザー名を指定します。

### output_format `string`

コマンド出力の表示形式を指定します。省略時または `plain` は従来どおり通常のattachment本文として表示します。

`monospaced` を指定すると、出力全体をcode blockとして表示します。

`markdown` を指定すると、出力文字列を変換せずそのままSlackのMarkdown Blockへ渡します。slack-commanderはMarkdownの変換を行いません。

### reply_broadcast `bool`

コマンドの出力は常に入力に対応するroot threadへのreplyとして投稿されます。

`true`（デフォルト）にすると、Slackの「Also send to channel」に相当するreply broadcastにより、同じ内容がチャンネルにも表示されます。

`false` にすると、出力はthread内だけに投稿されます。

### Interactive stdin

`interaction = "stdin"` を指定した`exec`と`compose` runnerでは、コマンドの実行中に同じSlack threadへ返信すると、その本文を実行中プロセスのstdinへ渡します。HTTP runnerは対象外です。

投稿者とチャンネルには通常の許可判定が適用されます。

初期stdinが空の場合は何も書き込みません。空でない場合は、末尾にLFがなければLFを補います。

thread replyはSlackの本文をそのまま渡し、末尾にLFがなければ補います。メンション、URL、引用符などの変換は行いません。

初期入力と返信は順番に転送します。転送中の入力とは別に、未処理の返信を最大1件保持します。すでに1件保持している場合、新しい返信はdropしてログに記録します。dropした返信は再試行しません。

stdinへの書き込みでエラーが発生した場合はexecutor側でログに記録します。

同じthreadに複数のプロセスが登録されている場合は、最後に登録されたプロセスへ入力を渡します。command chainでプロセスが切り替わる途中も同様です。

stdinの送信先がない返信は、root messageが単一の`[[commands]]`にマッチした場合だけ、そのcommandの`[[commands.replies]]`で評価します。root messageがcommand chainの場合やreply ruleにマッチしない場合は無視し、グローバルな`[[commands]]`には流しません。

初期入力を送信した後もstdinは開いたままです。`wc -l` のようにEOFを待つコマンドでは、必要に応じて `stdin_idle_timeout` を設定してください。

TTYモードでは入力終端が異なり、`stdin_idle_timeout` は使用できません。詳細は [`tty`](#tty-bool) を参照してください。

### stdin_idle_timeout `int`

`exec` / `compose` runnerのinteractive stdinを、最後に入力を受け付けてから何秒後に閉じるか指定します。

省略または `0` の場合は無効で、自動的にEOFを送りません。HTTP runnerには適用されません。負数は設定エラーです。

計測はプロセスのStart成功後、stdin writerをsessionに接続した時点から始まります。初期入力が空でも計測を開始し、空でなければ最初の入力として扱います。

thread replyを受け付けるたびに期限を延長します。stdinへの書き込み完了は待ちません。Busyでdropした返信やClosedで拒否した返信では期限を延長しません。

期限が来るとstdinを閉じ、通常のEOFを渡します。プロセスやcompose containerを強制終了する設定ではありません。

入力先の登録も解除されるため、その後の返信は無視されます。すでに入力先を取得した返信が終了処理と競合してClosedになった場合は破棄されます。

```toml
[[commands]]
keyword = "agent"
command = "..."
stdin_idle_timeout = 300
timeout = 3600
```

この例では、300秒間入力を受け付けなければEOFを渡します。EOF後も終了しないプロセスには、起動から3600秒の `timeout` が最終的な安全弁として働きます。

`tty = true` とは併用できません。

### timeout `int`

外部コマンドのタイムアウト時間を秒で指定します。

プロセスの実行時間全体を制限します。stdinの無入力時間を計る `stdin_idle_timeout` とは独立しています。
