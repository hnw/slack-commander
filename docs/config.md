# 設定リファレンス

## 設定項目一覧

### トップレベル設定

slack-commander全体に適用する設定です。

| 項目 | 型 | 既定値 | 概要 |
| --- | --- | --- | --- |
| [`slack_bot_token`](#slack_bot_token-string) | `string` | — | SlackのBot User OAuth Token |
| [`slack_app_token`](#slack_app_token-string) | `string` | — | SlackのApp-Level Token |
| [`num_workers`](#num_workers-int) | `int` | `1` | `exec`と`compose`を実行するワーカー数 |
| [`output_flush_interval`](#output_flush_interval-duration) | `duration` | `1s` | コマンド出力を途中送信するまでの最大待ち時間 |
| [`allowed_user_ids`](#allowed_user_ids-string) | `[]string` | 空 | 実行を許可するユーザーID |
| [`allowed_channel_ids`](#allowed_channel_ids-string) | `[]string` | 空 | 実行を許可するチャンネルID |
| [`allow_unsafe_open_access`](#allow_unsafe_open_access-bool) | `bool` | `false` | ユーザー・チャンネル制限なしでの起動を許可する |

### コマンド設定

`[[commands]]`で使用する設定です。`[[commands.replies]]`でも多くの項目を使用できます。

#### 基本設定

| 項目 | 型 | 既定値 | 概要 |
| --- | --- | --- | --- |
| [`keyword`](#keyword-string) | `string` | — | Slackへの入力と照合するキーワード |
| [`runner`](#runner-string) | `string` | `exec` | コマンドの実行方法 |
| [`command`](#command-string) | `string` | — | `exec`または`compose`で実行する内容 |
| [`timeout`](#timeout-duration) | `duration` | `0s` | 実行全体のタイムアウト |
| [`output_flush_interval`](#output_flush_interval-duration) | `duration` | 継承 | コマンド出力を途中送信するまでの最大待ち時間 |

#### 実行許可とスレッド

| 項目 | 型 | 既定値 | 概要 |
| --- | --- | --- | --- |
| [`allowed_user_ids`](#allowed_user_ids-string) | `[]string` | 継承 | このコマンドを実行できるユーザーID |
| [`allowed_channel_ids`](#allowed_channel_ids-string) | `[]string` | 継承 | このコマンドを実行できるチャンネルID |
| [`accept_reminder`](#accept_reminder-bool) | `bool` | `false` | Slack Reminderからの入力を受け付ける |
| [`interaction`](#interaction-string) | `string` | `oneshot` | スレッドへの返信の扱い |
| [`replies`](#replies) | `[[commands.replies]]` | — | `interaction = "command"`で使用する返信用コマンド |
| [`stdin_idle_timeout`](#stdin_idle_timeout-duration) | `duration` | `0s` | 標準入力を自動的に閉じるまでの無入力時間 |
| [`tty`](#tty-bool) | `bool` | `false` | TTYを使用して実行する |

#### HTTP runner

`runner = "http"`で使用する設定です。

| 項目 | 型 | 既定値 | 概要 |
| --- | --- | --- | --- |
| [`method`](#method-string) | `string` | `POST` | HTTPメソッド |
| [`url`](#url-string) | `string` | — | 送信先URL |
| [`headers`](#headers-mapstringstring) | `map[string]string` | 空 | HTTPヘッダー |
| [`body`](#body-string) | `string` | 空 | リクエストボディ |

#### Slackへの出力

コマンドの実行結果をSlackへ投稿するときの設定です。

| 項目 | 型 | 既定値 | 概要 |
| --- | --- | --- | --- |
| [`username`](#username-string) | `string` | — | 投稿時のユーザー名 |
| [`icon_emoji`](#icon_emoji-string) | `string` | — | 投稿時に使うSlack絵文字 |
| [`icon_url`](#icon_url-string) | `string` | — | 投稿時に使うアイコンのURL |
| [`output_format`](#output_format-string) | `string` | `plain` | 実行結果の表示形式 |
| [`reply_broadcast`](#reply_broadcast-bool) | `bool` | `true` | スレッドへの返信をチャンネルにも表示する |

### 返信用コマンド

`interaction = "command"`を指定したコマンドでは、`[[commands.replies]]`にスレッド返信用のコマンドを定義できます。

`[[commands.replies]]`では、コマンド設定の多くを使用できます。省略した項目の一部は親の`[[commands]]`から継承します。

`interaction`は指定できず、`[[commands.replies.replies]]`のように返信用コマンドをさらに入れ子にすることもできません。

利用できる項目と継承規則は[`replies`](#replies)を参照してください。

## トップレベル設定

### slack_bot_token `string`

SlackのBot User OAuth Tokenを指定します。`xoxb-`から始まる文字列です。

取得方法はREADMEのQuick Startを参照してください。

### slack_app_token `string`

SlackのApp-Level Tokenを指定します。`xapp-`から始まる文字列です。

Socket Modeで使用するため、`connections:write` scopeを持つトークンが必要です。取得方法はREADMEのQuick Startを参照してください。

### num_workers `int`

`exec`または`compose`を同時に実行するワーカー数を指定します。省略時は`1`です。`1`以上を指定してください。

単一のHTTPコマンドと、HTTPだけで構成されたコマンドチェーンはワーカーを使用しないため、`num_workers`の制限を受けません。

`exec`または`compose`を1つでも含むコマンドチェーンはワーカーで実行します。

同じSlackスレッドから起動した処理は、runnerにかかわらず同時には実行しません。詳しくは[同一スレッドの実行順序](#同一スレッドの実行順序)を参照してください。

### output_flush_interval `duration`

コマンドの出力をSlackへ途中送信するまでの最大待ち時間を指定します。省略時は`1s`です。

Goのduration構文を使用し、`500ms`、`1s`、`2m`のように指定します。

`0s`を指定すると待ち時間を設けずに送信します。2KBを超える出力は、この時間を待たずに送信される場合があります。

負の値は指定できません。

`[[commands]]`と`[[commands.replies]]`にも指定でき、トップレベル、command、replyの順に継承します。

### allowed_user_ids `[]string`

コマンドの実行を許可するユーザーIDを指定します。

トップレベルで空の場合、ユーザーIDによる制限は行いません。

`[[commands]]`ではトップレベル、`[[commands.replies]]`では親コマンドの許可リストを継承します。

子では親の許可範囲を狭められますが、親で許可されていないIDを追加することはできません。

たとえば、

```toml
allowed_user_ids = ["U0123456789", "U9876543210"]

[[commands]]
keyword = "status"
command = "status"
allowed_user_ids = ["U0123456789"]
```

は有効です。

一方、親にないIDを指定すると設定エラーになります。

空配列を指定しても制限解除にはならず、親の値を継承します。親に制限がない場合は、子で新たに制限を追加できます。

botからの投稿を許可する場合は、`allowed_user_ids`にBot IDを指定します。slack-commander自身の投稿は常に無視します。

Slack Reminderからの投稿に使われる`USLACKBOT`は`allowed_user_ids`には指定できません。Reminderを受け付ける場合は、対象のコマンドまたは返信用コマンドに`accept_reminder = true`を指定してください。

botを許可するときは、そのbotへ誰がどんな内容を投稿させられるかにも注意してください。任意のユーザーが自由な本文を投稿させられるbotを許可すると、そのbotを経由してslack-commanderのコマンドを実行できるため、`allowed_user_ids`による制限を迂回される可能性があります。

### allowed_channel_ids `[]string`

コマンドの実行を許可するチャンネルIDを指定します。

トップレベルで空の場合、チャンネルIDによる制限は行いません。

継承規則は`allowed_user_ids`と同じです。子では親の許可範囲を狭められますが、広げることはできません。

トップレベルの`allowed_user_ids`と`allowed_channel_ids`が両方とも空の場合、`allow_unsafe_open_access = true`を明示しない限り起動できません。

各コマンドに許可リストを指定していても、この条件は変わりません。

### allow_unsafe_open_access `bool`

トップレベルの`allowed_user_ids`と`allowed_channel_ids`をどちらも指定せずに起動する場合は`true`にします。

省略時は`false`です。

制限なしでの起動を明示的に許可するための設定なので、通常はトップレベルでユーザーまたはチャンネルを制限したまま使用してください。

旧設定のトップレベル`accept_reminder`と、すべての階層の`accept_bot_message`は使用できません。設定ファイルに残っている場合はエラーになります。

## コマンド設定

### accept_reminder `bool`

Slack Reminderによる投稿を`keyword`との照合対象にする場合は`true`を指定します。省略時は`false`です。

`[[commands]]`と`[[commands.replies]]`に指定でき、親からは継承しません。

Reminderからの投稿では`allowed_user_ids`を適用しません。`allowed_channel_ids`は通常どおり適用します。

Slack Reminderと組み合わせることで、コマンドの定期実行や時刻指定実行に利用できます。

Reminderによる投稿は、Reminderを設定したユーザー本人ではなくSlackbotからの投稿として届きます。

Reminderを受け付ける場合は、対象のコマンドまたは返信用コマンドに`accept_reminder = true`を指定してください。Reminderからの投稿には`allowed_user_ids`を適用せず、`allowed_channel_ids`だけを適用します。

`accept_reminder = true`を指定すると、そのチャンネルの通常メンバーはReminderを経由してコマンドを実行できます。`allowed_user_ids`で実行者を限定している場合でも、この制限を迂回できることに注意してください。Reminderは必要なコマンドにだけ有効にしてください。

### keyword `string`

Slackへの入力と照合するキーワードを指定します。

`keyword`は空白で区切った語列として扱います。引用符やバックスラッシュに特別な意味はありません。

```toml
keyword = "echo foo"
```

この例は、`echo`と`foo`の2語からなる入力にマッチします。

単独の`*`を1個だけワイルドカードとして使用できます。

```toml
keyword = "echo *"
```

`*`は0個以上の語にマッチします。

`foo*`のように他の文字とつながった`*`はワイルドカードとして扱いません。

単独の`*`を2個以上指定すると設定エラーになります。

```toml
# 設定エラー
keyword = "foo * bar *"
```

複数のコマンドが同じ入力にマッチする場合は、設定ファイルで先に定義したコマンドを使用します。

ただし、投稿者やチャンネルの許可設定によって実行できないコマンドは照合対象から除外します。その場合は、後に定義されたコマンドへの照合を続けます。

ワイルドカードにマッチした値の使い方は、[ワイルドカードの展開](#ワイルドカードの展開)を参照してください。

### interaction `string`

起点となるSlackメッセージを実行した後、そのスレッドへの返信をどのように扱うか指定します。

省略時は`oneshot`です。

| 値 | 起点メッセージの2行目以降 | スレッドへの返信 | コマンドチェーン |
| --- | --- | --- | --- |
| `oneshot` | 標準入力へ渡す | 無視する | 使用可能 |
| `stdin` | 標準入力へ渡す | 実行中のコマンドの標準入力へ渡す | 使用可能 |
| `command` | 条件に応じて引数へ追加する | `[[commands.replies]]`として処理する | 使用不可 |

`oneshot`と`stdin`では、起点メッセージの2行目以降を最初の標準入力として渡します。

`interaction = "stdin"`では、その後に同じスレッドへ届いた返信も標準入力へ渡します。コマンドチェーン内で使用した場合は、その時点で実行中のコマンドが返信を受け取ります。

返信できるユーザーも、その時点で実行中のコマンドの`allowed_user_ids`に従います。

`interaction = "command"`では、スレッドへの返信を`[[commands.replies]]`で定義した別のコマンドとして処理します。

`interaction = "command"`を指定したコマンドは、`;`、`&&`、`||`でつなぐコマンドチェーンには含められません。

`interaction = "command"`で`keyword`の末尾が`*`の場合は、起点メッセージとスレッドへの返信のどちらでも、2行目以降をワイルドカードにマッチした値へ追加します。

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

と入力すると、実行時の引数は次のようになります。

```text
["echo", "foo", "\nbar"]
```

1行目と2行目の間の改行も保持します。

`keyword`の末尾が`*`でない場合、2行目以降は使用しません。

`runner = "http"`では`oneshot`と`command`を使用できます。`stdin`は使用できません。

`interaction`を指定できるのは`[[commands]]`だけです。`[[commands.replies]]`には指定できません。

`interaction = "stdin"`の詳しい動作は、[対話的な標準入力](#対話的な標準入力)を参照してください。

### replies

`interaction = "command"`でスレッドへの返信を処理する場合は、`[[commands.replies]]`に返信用コマンドを定義します。

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

この例では、`todo ...`を起点とするスレッド内でのみ`cancel`やその他の返信を処理します。

`[[commands.replies]]`は、それを定義した親コマンドのスレッド内でだけ有効です。通常の`[[commands]]`としては使用されません。

次の項目は、返信側で省略すると親コマンドの値を継承します。

* `runner`
* `timeout`
* `stdin_idle_timeout`
* `tty`
* `output_flush_interval`
* `username`
* `icon_emoji`
* `icon_url`
* `reply_broadcast`
* `output_format`
* `allowed_user_ids`
* `allowed_channel_ids`

次の項目は継承しません。

* `keyword`
* `command`
* `method`
* `url`
* `headers`
* `body`

継承する項目も返信側で上書きできます。`timeout = "0s"`、`stdin_idle_timeout = "0s"`、`tty = false`、`output_flush_interval = "0s"`、`reply_broadcast = false`も明示的な上書きとして扱います。

`accept_reminder`は親から継承しません。

`allowed_user_ids = []`と`allowed_channel_ids = []`は親の許可リストを継承します。親で許可されていないIDを追加すると設定エラーになります。

`interaction`は`[[commands.replies]]`には指定できません。また、返信用コマンドをさらに入れ子にすることもできません。

### runner `string`

コマンドの実行方法を指定します。省略時は`exec`です。

使用できる値は次の3つです。

* `exec`: slack-commanderと同じ環境で外部コマンドを実行する
* `compose`: Docker Composeで定義したservice内でコマンドを実行する
* `http`: HTTPリクエストを送信する

`compose`では、`command`に`<service> <args>`の形式でservice名と引数を指定します。

`exec`と`compose`でSlackスレッドの情報を外部コマンドから参照する方法は、[Slackスレッドのコンテキスト](#slackスレッドのコンテキスト)を参照してください。

### command `string`

`runner = "exec"`または`runner = "compose"`で実行する内容を指定します。

`exec`と`compose`では必須です。

`keyword`にワイルドカードがある場合は、`command`内の`*`へマッチした値を展開できます。

`command`を`*`から始めることはできません。

`runner = "http"`では使用しません。

### tty `bool`

`exec`または`compose`をTTY付きで実行する場合に`true`を指定します。省略時は`false`です。

TTYを必要とするCLI向けの機能であり、完全な端末エミュレーションではありません。TTYなしで正常に動作するCLIでは`false`のまま使用してください。

`runner = "http"`では使用できません。

`stdin_idle_timeout`とも併用できません。実行時間そのものを制限する場合は`timeout`を使用します。

```toml
[[commands]]
keyword = "opencode *"
command = "opencode *"
tty = true
timeout = "1h"
```

TTY使用時の制約は、[TTYの入出力](#ttyの入出力)を参照してください。

### method `string`

`runner = "http"`で使用するHTTPメソッドを指定します。省略時は`POST`です。

### url `string`

`runner = "http"`の送信先URLを指定します。HTTP runnerでは必須です。

`keyword`にワイルドカードがある場合は、URL内の`*`を展開できます。

### headers `map[string]string`

`runner = "http"`で送信するHTTPヘッダーを指定します。

TOMLのインラインテーブル形式を使用します。

```toml
headers = { "Content-Type" = "application/json" }
```

`keyword`にワイルドカードがある場合、各ヘッダー値に含まれる`*`も展開対象になります。

### body `string`

`runner = "http"`で送信するリクエストボディを指定します。

`keyword`にワイルドカードがある場合、本文中の`*`も展開対象になります。

HTTP runnerを含むワイルドカードの規則は、[ワイルドカードの展開](#ワイルドカードの展開)を参照してください。

### icon_emoji `string`

Slackへ結果を投稿するときのアイコンをSlack絵文字で指定します。

### icon_url `string`

Slackへ結果を投稿するときのアイコンをURLで指定します。

### username `string`

Slackへ結果を投稿するときのユーザー名を指定します。

### output_format `string`

実行結果の表示形式を指定します。

使用できる値は次の3つです。

* `plain`: 通常のattachment本文として表示する。省略時はこの形式
* `monospaced`: 出力全体をコードブロックとして表示する
* `markdown`: 出力文字列をそのままSlackのMarkdown Blockとして表示する

それ以外の値を指定すると設定エラーになります。

### reply_broadcast `bool`

実行結果は、起点となったSlackメッセージのスレッドへ返信として投稿します。

省略時は`true`で、返信をチャンネルにも表示します。

`false`にするとスレッド内だけに表示します。

### stdin_idle_timeout `duration`

`interaction = "stdin"`を指定した`exec`または`compose`で、最後に入力を受け付けてから標準入力を閉じるまでの時間をGoのduration構文で指定します。たとえば`30s`、`5m`を使用できます。

整数を指定した場合はナノ秒として扱われます。`0`または`1ms`以上を指定してください。

省略するか`"0s"`を指定した場合は無効となり、入力がなくても自動では標準入力を閉じません。数値の`0`も無効です。負の値は指定できません。

HTTP runnerでは使用できず、`tty = true`とも併用できません。

標準入力を閉じるとプロセスにはEOFが渡されます。プロセス自体を終了する設定ではありません。

### timeout `duration`

外部コマンドまたはHTTPリクエストの実行時間をGoのduration構文で制限します。たとえば`30s`、`5m`を使用できます。

整数を指定した場合はナノ秒として扱われます。`0`または`1ms`以上を指定してください。

省略するか`"0s"`を指定すると、実行時間に上限を設けません。数値の`0`も同様です。負の値は指定できません。

`stdin_idle_timeout`は無入力時に標準入力を閉じる設定ですが、`timeout`は処理全体の実行時間を制限します。

## 関連する動作仕様

### ワイルドカードの展開

`keyword`に単独の`*`がある場合、その`*`にマッチした値を`command`、`url`、`body`、`headers`内の`*`へ展開します。

たとえば、

```toml
keyword = "copy *"
command = "copy * /backup/*"
```

に対して、

```text
copy foo
```

と入力すると、`command`内の2個の`*`はいずれも`foo`になります。

HTTP runnerでも同じ規則を使用します。

```toml
keyword = "notify *"
runner = "http"
url = "https://example.com/users/*/messages/*"
body = '{"text":"*","raw":"*"}'
headers = { "X-Value" = "*:*" }
```

この設定に

```text
notify hello
```

と入力すると、`url`、`body`、`headers`内のすべての`*`が`hello`に置き換わります。

`keyword`にワイルドカードがない場合、これらの項目に含まれる`*`は置き換えません。

`interaction = "command"`で`keyword`の末尾が`*`の場合は、2行目以降もワイルドカードにマッチした値の一部として扱います。

```toml
keyword = "notify *"
runner = "http"
url = "https://example.com/"
body = '{"text":"*"}'
interaction = "command"
```

この設定に

```text
notify hello
world
```

と入力すると、`*`には次の文字列が入ります。

```text
hello
world
```

1行目と2行目の間の改行も保持します。

展開対象となる項目では、その項目内のすべての`*`を置換します。文字として`*`を残すためのエスケープ構文はありません。

### Slackスレッドのコンテキスト

`exec`または`compose`で外部コマンドを実行するとき、次の環境変数を設定します。

* `SLACK_CHANNEL_ID`: SlackのチャンネルID
* `SLACK_THREAD_TS`: スレッドの起点メッセージのタイムスタンプ

起点メッセージから実行した場合も、そのメッセージ自身のタイムスタンプを`SLACK_THREAD_TS`に設定します。

同じスレッドから実行したコマンドには、同じチャンネルIDとスレッドの起点タイムスタンプが設定されます。

### 同一スレッドの実行順序

同じSlackスレッドから起動した処理は同時には実行しません。

先に実行中の処理がある場合、後から起動した処理はその終了を待ちます。待機順は厳密なFIFOではありません。

`exec`または`compose`は、同じスレッドの処理を待っている間もワーカーを1つ使用します。そのため、待機中の処理があると、別のスレッドで同時に実行できる数が減ることがあります。

HTTPだけで構成された処理はワーカーを使用しません。

### 対話的な標準入力

`interaction = "stdin"`を指定した`exec`または`compose`では、コマンドの実行中に同じSlackスレッドへ返信すると、その本文を標準入力へ渡します。

HTTP runnerでは使用できません。

起点メッセージの2行目以降は、最初の標準入力として使用します。

その後のスレッドへの返信も、受け付けた順に標準入力へ渡します。

コマンドチェーン内に`interaction = "stdin"`を指定したコマンドが複数ある場合、返信はその時点で実行中のコマンドへ渡します。標準入力を受け付けるコマンドが実行中でなければ、その返信は無視します。

返信できるユーザーは、その時点で実行中のコマンドの`allowed_user_ids`に従います。

初期入力を書き込んだ後も標準入力は開いたままです。`wc -l`のようにEOFを待つコマンドでは、必要に応じて`stdin_idle_timeout`を指定してください。

`stdin_idle_timeout`に達すると標準入力を閉じてEOFを渡します。プロセス自体は終了しません。

```toml
[[commands]]
keyword = "agent"
command = "..."
interaction = "stdin"
stdin_idle_timeout = "5m"
timeout = "1h"
```

この例では、300秒間入力がなければEOFを渡します。その後もプロセスが終了しない場合は、起動から3600秒後に`timeout`で実行を終了します。

### TTYの入出力

`tty = true`では、標準出力と標準エラー出力を分離せず、1本の端末出力としてSlackへ投稿します。

一般的な端末制御シーケンスの一部を除去し、CRLFと単独のCRはLFへ変換します。

完全な端末エミュレーションではないため、カーソル移動を使った画面更新、フルスクリーンTUI、端末サイズの変更、特殊キー入力には対応していません。

`interaction = "stdin"`でSlackスレッドから送った入力は、TTY使用時にはEnterキーを押した場合に相当する形でプロセスへ渡します。

TTYでは`stdin_idle_timeout`を使用できません。実行時間を制限する場合は`timeout`を指定してください。
