# slack-commander

Slackチャンネル内で発言されたキーワードに応じて外部コマンドを実行し、コマンドの出力をSlackにポストするSlack botです。

コマンドラインツールなら何でもSlackから扱えるようになるので、好きなプログラミング言語でSlackの機能拡張を行うことができます。

また、このbot1つで任意個のコマンドを起動できるので、Slackのbotユーザー数を消費せずに複数の機能を実現できます。

## 実行例

![実行例](./docs/colored-post.png)

## 特徴

 * Slackチャンネル内の発言中のキーワードに応じて外部コマンドを起動し、コマンドの出力をSlackにポストします
 * 実行するコマンドごとに日本語のわかりやすいキーワードを定義できます
 * Slackのリマインダーからコマンドを起動できます（cronの代わりになります）
 * Unixシェルライクな`&&`, `||`, `;`を実装しており、1行で複数コマンドの指定ができます
 * Socket Modeを利用しているので、Webサーバを用意する必要がありません
   - 社内や家庭内にbotを設置したい場合に便利です

## インストール&実行

```
$ git clone https://github.com/hnw/slack-commander
$ cd slack-commander
$ cp config.toml.example config.toml
$ vi config.toml
$ go build
$ ./slack-commander
```

ビルドにはGo 1.24以降が必要です。

## 設定例

たとえば下記の設定を使えば `月末？ && 振込 foo銀行 1000` のようにして月末だけ実行するコマンドを指定することができます。

``` ini
slack_bot_token = 'xoxb-********'
slack_app_token = 'xapp-********'

[[commands]]
keyword = '月末？'
command = '/bin/sh -c "test $( date -d tomorrow +%d ) -eq 1"' # GNU dateの前提

[[commands]]
keyword = '振込 *'
command = 'node /opt/money-transfer-cli/bin/cli.js 振込 * -v'
```

設定項目の詳細は [docs/config.md](./docs/config.md) を参照してください。

## 参考：systemdで管理する例

botとして半永久的に動かしたい場合、デーモン管理ツールで管理するのがオススメです。

ここではsystemdで管理する例を示します。まず、`/etc/systemd/system/slack-commander.service`を作成しましょう。

``` ini
[Unit]
Description = slack-commander
After=network-online.target
Wants=network-online.target

[Service]
User = pi
Group = pi
WorkingDirectory = /opt/slack-commander/
ExecStart = /opt/slack-commander/slack-commander
ExecStop = /bin/kill ${MAINPID}
Restart = always
Type = simple

[Install]
WantedBy = multi-user.target
```

下記コマンドでbotとして動作しはじめます。

```
# systemctl start slack-commander.service
```

しばらく動かしてみて問題なさそうなら自動起動させるようにしましょう。

```
# systemctl enable slack-commander.service
```
