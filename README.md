# slack-commander

Slackチャンネル内で発言されたキーワードに応じて外部コマンドを実行し、コマンドの出力をSlackにポストするSlack botです。

コマンドラインツールなら何でもSlackから扱えるようになるので、好きなプログラミング言語でSlackの機能拡張を行うことができます。

また、このbot1つで任意個のコマンドを起動できるので、Slackのbotユーザー数を消費せずに複数の機能を実現できます。

## 実行例

![実行例](./docs/colored-post.png)

## 特徴

 * Slackチャンネル内の発言中のキーワードに応じて外部コマンドを起動し、コマンドの出力をSlackにポストします
 * 実行するコマンドごとに日本語のわかりやすいキーワードを定義できます
 * 間欠的に出力するようなコマンドの場合、コマンドの終了を待たずにコマンドの出力をSlackにポストできます
 * 外部コマンドの最大並列数やタイムアウト時間を指定できます
 * Slackのリマインダーからコマンドを起動できます（cronの代わりになります）
 * コマンドの実行結果をスレッド化してポストすることができます
 * コマンドごとにアイコンやユーザー名を変えることができます
 * Unixシェルライクな`&&`, `||`, `;`を実装しており、1行で複数コマンドの指定ができます

## インストール&実行

```
$ git clone https://github.com/hnw/slack-commander
$ cd slack-commander
$ cp config.toml.example config.toml
$ vi config.toml
$ go build
$ ./slack-commander
```

ビルドにはGo 1.13以降が必要です（Go Modulesを利用しているため）

## 設定例

``` ini
slack_token = 'xoxb-********'

[[commands]]
keyword = '残高照会 *'
command = 'node /opt/money-transfer-cli/bin/cli.js 残高照会 * -v'

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
