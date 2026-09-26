# 設定リファレンス

## 設定項目一覧

### トップレベル設定

| 項目                                                           | 型          | 既定値     | 概要                      |
| ------------------------------------------------------------ | ---------- | ------- | ----------------------- |
| [`slack_bot_token`](#slack_bot_token-string)                 | `string`   | —       | Slackのボットトークン           |
| [`slack_app_token`](#slack_app_token-string)                 | `string`   | —       | Slackのアプリレベルトークン        |
| [`num_workers`](#num_workers-int)                            | `int`      | `1`     | 外部コマンドの最大同時実行数          |
| [`accept_reminder`](#accept_reminder-bool)                   | `bool`     | `false` | Slackリマインダーの投稿を受け付ける    |
| [`accept_bot_message`](#accept_bot_message-bool)             | `bool`     | `false` | ボットによる投稿を受け付ける          |
| [`allowed_user_ids`](#allowed_user_ids-string)               | `[]string` | 空       | 実行を許可するユーザーID           |
| [`allowed_channel_ids`](#allowed_channel_ids-string)         | `[]string` | 空       | 実行を許可するチャンネルID          |
| [`allow_unsafe_open_access`](#allow_unsafe_open_access-bool) | `bool`     | `false` | ユーザー・チャンネル制限なしでの起動を許可する |

### `[[commands]]`

| 項目                                              | 型                      | 既定値       | 概要                                  |
| ----------------------------------------------- | ---------------------- | --------- | ----------------------------------- |
| [`keyword`](#keyword-string)                    | `string`               | —         | Slackへの入力と照合するキーワード                 |
| [`interaction`](#interaction-string)            | `string`               | `oneshot` | スレッド内の追加入力の扱い                       |
| [`replies`](#replies)                           | `[[commands.replies]]` | —         | `interaction = "command"`で使用する返信用設定 |
| [`runner`](#runner-string)                      | `string`               | `exec`    | 実行方法                                |
| [`command`](#command-string)                    | `string`               | —         | `exec`または`compose`で実行する内容           |
| [`tty`](#tty-bool)                              | `bool`                 | `false`   | TTYを確保して実行する                        |
| [`stdin_idle_timeout`](#stdin_idle_timeout-int) | `int`                  | `0`       | 標準入力を自動的に閉じるまでの時間                   |
| [`timeout`](#timeout-int)                       | `int`                  | `0`       | 実行全体のタイムアウト                         |

### HTTP runner

| 項目                                    | 型                   | 既定値    | 概要       |
| ------------------------------------- | ------------------- | ------ | -------- |
| [`method`](#method-string)            | `string`            | `POST` | HTTPメソッド |
| [`url`](#url-string)                  | `string`            | —      | 送信先URL   |
| [`headers`](#headers-mapstringstring) | `map[string]string` | 空      | HTTPヘッダー |
| [`body`](#body-string)                | `string`            | 空      | リクエストボディ |

### Slackへの出力

| 項目                                         | 型        | 既定値     | 概要                   |
| ------------------------------------------ | -------- | ------- | -------------------- |
| [`username`](#username-string)             | `string` | —       | 投稿時のユーザー名            |
| [`icon_emoji`](#icon_emoji-string)         | `string` | —       | 投稿時のアイコンをSlack絵文字で指定 |
| [`icon_url`](#icon_url-string)             | `string` | —       | 投稿時のアイコンをURLで指定      |
| [`output_format`](#output_format-string)   | `string` | `plain` | 実行結果の表示形式            |
| [`reply_broadcast`](#reply_broadcast-bool) | `bool`   | `true`  | スレッドへの返信をチャンネルにも表示する |

## トップレベル設定項目

### slack_bot_token `string`

Slackのボットトークンを指定します。`xoxb-`から始まる文字列です。

Slackの管理画面で「Settings」→「OAuth & Permissions」→「OAuth Tokens for Your Workspace」と進み、トークンをコピーしてください。

### slack_app_token `string`

Slackのアプリレベルトークンを指定します。`xapp-`から始まる文字列です。

Slackの管理画面で「General」→「Basic Information」→「App-Level Tokens」と進み、トークンを生成してください。

### num_workers `int`

外部コマンドを同時に実行できる最大数を指定します。省略時は`1`です。

`1`以上を指定してください。

### accept_reminder `bool`

Slackのリマインダーによる投稿もキーワードの照合対象にするか指定します。`cron`や`at`の代わりとして利用できます。

### accept_bot_message `bool`

ボットによる投稿もキーワードの照合対象にするか指定します。

### allowed_user_ids `[]string`

コマンドを実行できるユーザーIDの許可リストを指定します。空の場合はユーザーによる制限を行いません。

### allowed_channel_ids `[]string`

コマンドを実行できるチャンネルIDの許可リストを指定します。空の場合はチャンネルによる制限を行いません。

`allowed_user_ids`と`allowed_channel_ids`の両方を空にすると、デフォルトでは起動時にエラーになります。

### allow_unsafe_open_access `bool`

`true`にすると、`allowed_user_ids`と`allowed_channel_ids`の両方が空でも起動を許可します。

後方互換のための設定です。セキュリティ上の理由から、通常は`false`のまま使用してください。

## コマンドごとの設定項目

### keyword `string`

Slackへの入力と照合するキーワードを指定します。

`keyword`は空白で区切った語列として扱います。引用符やバックスラッシュに特別な意味はありません。

たとえば、

```toml
keyword = "echo foo"
```

は`echo`、`foo`という2つの語からなる入力にマッチします。

`keyword`では、単独の`*`をワイルドカードとして1個だけ使用できます。

```toml
keyword = "echo *"
```

`*`は0個以上の語にマッチします。

`foo*`のように他の文字とつながった`*`はワイルドカードとして扱いません。

```toml
keyword = "foo*"
```

単独の`*`を2個以上指定すると設定エラーになります。

```toml
# 設定エラー
keyword = "foo * bar *"
```

複数の`[[commands]]`が同じ入力にマッチする場合は、先に定義したものを使用します。

ワイルドカードにマッチした内容の使われ方については、[ワイルドカードの展開](#ワイルドカードの展開)を参照してください。

### interaction `string`

スレッドの起点メッセージにマッチした`[[commands]]`が、その後の入力をどのように扱うかを指定します。省略時は`oneshot`です。

* `oneshot`: 起点メッセージの2行目以降を標準入力へ渡します。スレッドへの返信は無視します。`;`、`&&`、`||`によるコマンドの連結を使用できます。
* `stdin`: 起点メッセージの2行目以降と、その後のスレッドへの返信を実行中プロセスの標準入力へ渡します。
* `command`: スレッドへの返信を`[[commands.replies]]`に従って処理します。

`interaction = "command"`では、起点メッセージとスレッドへの返信のどちらについても、`keyword`の末尾が`*`の場合だけ2行目以降を追加のargvとして渡します。1行目と2行目の間の改行も保持します。

たとえば、

```toml
keyword = "echo *"
command = "echo *"
interaction = "command"
```

に対して、

```text
echo foo
bar
```

と入力した場合、実行時のargvは次のようになります。

```text
["echo", "foo", "\nbar"]
```

`keyword`の末尾が`*`でない場合、2行目以降は使用しません。

```toml
keyword = "echo * done"
interaction = "command"
```

この場合、`*`自体は通常どおり1行目の入力にマッチしますが、2行目以降は追加されません。

`stdin`と`command`では、`;`、`&&`、`||`によるコマンドの連結を使用できません。

`runner = "http"`では`oneshot`と`command`を使用できます。`stdin`は使用できません。

`interaction`は`[[commands]]`にだけ指定できます。`[[commands.replies]]`には指定できません。

`interaction = "stdin"`の入出力については、[対話的な標準入力](#対話的な標準入力)を参照してください。

### replies

`interaction = "command"`でスレッドへの返信を処理する設定を、`[[commands.replies]]`で定義します。

`[[commands.replies]]`は、それを含む`[[commands]]`にマッチした起点メッセージのスレッド内でだけ使用されます。トップレベルの`[[commands]]`としては扱われません。

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

`[[commands.replies]]`で次の項目を省略した場合は、それを含む`[[commands]]`の設定を継承します。

* `runner`
* `timeout`
* `stdin_idle_timeout`
* `tty`
* `username`
* `icon_emoji`
* `icon_url`
* `reply_broadcast`
* `output_format`

次の項目は継承しません。

* `keyword`
* `command`
* `method`
* `url`
* `headers`
* `body`

継承される項目も、`[[commands.replies]]`側で指定すれば上書きできます。`timeout = 0`、`stdin_idle_timeout = 0`、`tty = false`、`reply_broadcast = false`のような値も明示的な上書きとして扱われます。

`interaction`は`[[commands.replies]]`には指定できません。

また、`[[commands.replies.replies]]`のように返信用の設定を入れ子にすることもできません。

### runner `string`

コマンドの実行方法を指定します。省略時は`exec`です。

* `exec`: ホスト上で外部コマンドを実行します。
* `compose`: `docker-compose.yml`に定義されたサービスを実行します。`command`には`<service> <args>`を指定してください。
* `http`: HTTPリクエストを送信します。`url`の指定が必要です。

`exec`と`compose`でSlackスレッドに関する情報を参照する方法については、[Slackスレッドのコンテキスト](#slackスレッドのコンテキスト)を参照してください。

### command `string`

`runner = "exec"`または`runner = "compose"`で実行する内容を指定します。

`keyword`にワイルドカードがある場合は、`command`内の`*`を展開できます。詳しくは[ワイルドカードの展開](#ワイルドカードの展開)を参照してください。

`exec`と`compose`では、`command`を`*`から始めることはできません。

`runner = "http"`の場合、この項目は使用しません。

### tty `bool`

`true`にすると、`exec`または`compose`でTTYを確保して外部コマンドを実行します。省略時は`false`です。

TTYを必要とするCLIのための互換機能です。TTYがなくても正常に動作するCLIでは、通常どおり`tty = false`のまま使用してください。

`stdin_idle_timeout`とは併用できません。コマンドが応答しない場合の安全弁には`timeout`を指定してください。

```toml
[[commands]]
keyword = "opencode *"
command = "opencode *"
tty = true
timeout = 3600
```

TTY使用時の入出力については、[TTYの入出力](#ttyの入出力)を参照してください。

### method `string`

`runner = "http"`の場合に使用するHTTPメソッドを指定します。省略時は`POST`です。

### url `string`

`runner = "http"`の場合に送信先URLを指定します。必須です。

`keyword`にワイルドカードがある場合は、URL内の`*`を展開できます。詳しくは[ワイルドカードの展開](#ワイルドカードの展開)を参照してください。

### headers `map[string]string`

`runner = "http"`の場合に付与するHTTPヘッダーを指定します。

TOMLのインラインテーブル形式で指定してください。

```toml
headers = { "Content-Type" = "application/json" }
```

各ヘッダーの値に含まれる`*`もワイルドカード展開の対象になります。詳しくは[ワイルドカードの展開](#ワイルドカードの展開)を参照してください。

### body `string`

`runner = "http"`の場合に送信するリクエストボディを指定します。

`keyword`にワイルドカードがある場合は、本文内の`*`を展開できます。詳しくは[ワイルドカードの展開](#ワイルドカードの展開)を参照してください。

### icon_emoji `string`

ボットがSlackへ投稿するときのアイコンをSlack絵文字で指定します。

### icon_url `string`

ボットがSlackへ投稿するときのアイコンをURLで指定します。

### username `string`

ボットがSlackへ投稿するときのユーザー名を指定します。

### output_format `string`

実行結果の表示形式を指定します。

使用できる値は次の3つです。

* `plain`: 通常のattachment本文として表示します。省略時もこの形式です。
* `monospaced`: 出力全体をコードブロックとして表示します。
* `markdown`: 出力文字列を変換せず、そのままSlackのMarkdown Blockへ渡します。

それ以外の値を指定すると設定エラーになります。

### reply_broadcast `bool`

実行結果は、入力が行われたSlackスレッドへの返信として投稿します。

`true`（デフォルト）にすると、同じ内容をチャンネルにも表示します。

`false`にすると、スレッド内だけに投稿します。

### stdin_idle_timeout `int`

`exec`または`compose`で`interaction = "stdin"`を使用する場合に、最後の入力から何秒後に標準入力を閉じるかを指定します。

省略するか`0`を指定した場合は無効となり、自動的にはEOFを送りません。負の値を指定すると設定エラーになります。

HTTPには適用されません。`tty = true`とも併用できません。

標準入力を閉じるタイミングやEOF後の動作については、[対話的な標準入力](#対話的な標準入力)を参照してください。

### timeout `int`

外部コマンドまたはHTTPリクエストのタイムアウト時間を秒単位で指定します。

実行時間全体を制限します。標準入力がない時間を計測する`stdin_idle_timeout`とは独立しています。

## 関連する動作仕様

### ワイルドカードの展開

`keyword`に単独の`*`がある場合、その`*`にマッチした内容を`command`、`url`、`body`、各`headers`の値に含まれるすべての`*`へ展開します。

たとえば、

```toml
keyword = "copy *"
command = "copy * /backup/*"
```

に対して、

```text
copy foo
```

と入力すると、`command`内の2個の`*`にはどちらも`foo`が展開されます。

HTTP runnerでも同じ規則を使用します。

```toml
keyword = "notify *"
runner = "http"
url = "https://example.com/users/*/messages/*"
body = '{"text":"*","raw":"*"}'
headers = { "X-Value" = "*:*" }
```

に対して、

```text
notify hello
```

と入力すると、`url`、`body`、`headers`に含まれるすべての`*`が`hello`に置換されます。

`keyword`にワイルドカードがない場合、`command`、`url`、`body`、`headers`内の`*`は置換しません。

`interaction = "command"`で`keyword`の末尾が`*`の場合は、2行目以降もワイルドカードの値に含めます。

たとえば、

```toml
keyword = "notify *"
runner = "http"
url = "https://example.com/"
body = '{"text":"*"}'
interaction = "command"
```

に対して、

```text
notify hello
world
```

と入力した場合、`*`には次の文字列が展開されます。

```text
hello
world
```

1行目と2行目の間の改行も保持されます。

ワイルドカードが有効な設定では、展開対象となる項目内のすべての`*`を展開位置として扱います。文字そのものとしての`*`と使い分けるためのエスケープ構文はありません。

### Slackスレッドのコンテキスト

Slackから`exec`または`compose`で外部コマンドを実行する際、次の環境変数を設定します。

* `SLACK_CHANNEL_ID`: SlackのチャンネルID
* `SLACK_THREAD_TS`: スレッドの起点メッセージのタイムスタンプ

起点メッセージから実行した場合も、そのメッセージ自身のタイムスタンプを`SLACK_THREAD_TS`に使用します。そのため、同じスレッドの起点メッセージと返信では同じ値になります。

これらの値はSlackイベントから取得するため、同名の環境変数がすでに設定されている場合は上書きします。

`interaction = "command"`でスレッドへの返信を処理するとき、ルーティング情報がキャッシュにない場合はSlackの履歴APIから起点メッセージを取得します。そのため、古いスレッドへの返信を処理するには、ボットトークンに対応する履歴取得権限が必要です。

### 同一スレッドの実行順序

同じSlackスレッドから起動した処理は同時には実行されません。

先に実行中の処理がある場合、後から起動した処理はその終了を待ちます。待機順は厳密なFIFOではありません。

実行待ちの処理も`num_workers`のワーカーを1つ使用します。そのため、同じスレッドの処理が待機している間に、別のスレッドで利用できるワーカー数が減ることがあります。

### 対話的な標準入力

`interaction = "stdin"`を指定した`exec`または`compose`では、外部コマンドの実行中に同じSlackスレッドへ返信すると、その本文を実行中プロセスの標準入力へ渡します。

HTTPでは使用できません。

投稿者とチャンネルには、通常の実行許可の判定が適用されます。

起点メッセージの2行目以降が空の場合は、初期標準入力には何も書き込みません。空でない場合は、末尾にLFがなければ補います。

スレッドへの返信はSlackから受け取った本文をそのまま渡し、末尾にLFがなければ補います。メンション、URL、引用符などの変換は行いません。

初期入力とその後の返信は順番に転送します。

転送中の入力とは別に、未処理の返信を最大1件まで保持します。すでに1件保持している状態で新しい返信が届いた場合は、その返信を破棄してログに記録します。破棄した返信は再試行しません。

標準入力への書き込みでエラーが発生した場合はログに記録します。

同じスレッドに複数のプロセスが登録されている場合は、最後に登録されたプロセスへ入力を渡します。

標準入力の送信先となるプロセスがない場合、その返信は無視します。

初期入力を送信した後も標準入力は開いたままです。`wc -l`のようにEOFを待つコマンドでは、必要に応じて`stdin_idle_timeout`を設定してください。

`stdin_idle_timeout`の計測は、プロセスの起動に成功し、標準入力を受け付けられる状態になった時点から始まります。初期入力が空の場合も計測を開始し、空でない場合は最初の入力として扱います。

スレッドへの返信を受け付けるたびに期限を延長します。標準入力への書き込みが完了するまで待ってから延長するわけではありません。

未処理の返信がすでに1件あるため破棄した場合や、標準入力が閉じていて受け付けられなかった場合は期限を延長しません。

期限に達すると標準入力を閉じ、通常のEOFを渡します。プロセスやComposeコンテナを強制終了する設定ではありません。

同時に入力先としての登録も解除します。その後に届いた返信は無視します。

```toml
[[commands]]
keyword = "agent"
command = "..."
interaction = "stdin"
stdin_idle_timeout = 300
timeout = 3600
```

この例では、300秒間入力がなければEOFを渡します。EOFを渡した後もプロセスが終了しない場合は、起動から3600秒後に`timeout`が最終的な安全弁として働きます。

### TTYの入出力

`tty = true`では、標準出力と標準エラー出力を分離せず、1本の端末出力としてSlackへ投稿します。

ESCで始まる7-bit形式の端末制御シーケンスは除去します。8-bit形式のC1制御文字はUTF-8の継続バイトと値の範囲が重なるため、解釈しません。

CRLFと単独のCRはLFへ変換します。

カーソル移動による画面状態の再現、フルスクリーンTUI、端末サイズの変更、特殊キーの入力には対応していません。

`interaction = "stdin"`でSlackスレッドから受け取った入力は、TTY上でEnterキーを押した場合に相当するCRで終端します。TTYを使用しない場合はLFで終端します。

TTYでは`stdin_idle_timeout`を使用できません。コマンドが応答しない場合の安全弁には`timeout`を指定してください。
