# Dockerでの運用

slack-commanderは公式コンテナイメージを利用して実行できます。
公式イメージは[koの設定](../.ko.yaml)に従い、`distroless/static`をベースに[CIでビルドして公開](../.github/workflows/ci.yml)しています。
シェル、Docker CLI、Compose CLI、一般的なCLIツールは含みません。

実際の処理は別コンテナへ分離し、`runner = "compose"`で必要なときに実行する構成を想定しています。
処理に必要なCLI、ライブラリ、認証情報は、その処理を実行するコンテナに持たせます。

## Composeサービスでコマンドを実行する

`compose`を使うと、Docker Composeのサービス定義を使って一時コンテナを起動し、その中でコマンドを実行できます。
次の設定では、`worker`サービスの定義を使って一時コンテナを起動し、`echo`を実行します。

```toml
[[commands]]
keyword = "echo *"
runner = "compose"
command = "worker echo *"
```

Slackに`echo hello`と投稿すると、`worker`コンテナ内で`echo hello`を実行し、その出力をSlackへ返します。
設定項目については[設定リファレンス](config.md#runner-string)を参照してください。

## ホストのDockerデーモンを利用する

slack-commander自身をコンテナで動かし、そこから`compose`を使う場合は、ホストのDockerデーモンを利用します。
この構成は**DooD（Docker outside of Docker）**と呼ばれます。

```text
Slack
  |
  v
slack-commanderコンテナ
  |
  | Docker API
  v
ホストのDockerデーモン
  |
  +--> workerコンテナ
  +--> agentコンテナ
  +--> その他の実行先コンテナ
```

ホストのDockerソケットをslack-commanderコンテナへマウントすると、コンテナ内からDocker APIを呼び出せます。

```yaml
volumes:
  - /var/run/docker.sock:/var/run/docker.sock
```

`compose`の実行機能はslack-commanderに組み込まれているため、Docker CLIやCompose CLIを追加する必要はありません。
Dockerソケットを渡すとホストを操作する強い権限を与えることになるため、[Dockerソケットの権限](#dockerソケットの権限)も確認してください。

## 構成例

ホスト上の`/opt/stacks/slack-app`に、次のComposeファイルを配置する例です。
slack-commander本体と、実際の処理を行う`worker`サービスを同じプロジェクトに定義します。

```yaml
services:
  slack-commander:
    image: ghcr.io/hnw/slack-commander:latest
    container_name: slack-commander
    restart: unless-stopped
    init: true
    volumes:
      - ./config.toml:/etc/slack-commander/config.toml:ro
      - /var/run/docker.sock:/var/run/docker.sock
      - /opt/stacks/slack-app:/opt/stacks/slack-app
    working_dir: /opt/stacks/slack-app
    command: --config-file=/etc/slack-commander/config.toml

  worker:
    image: busybox:1.36
    profiles:
      - manual
```

同じディレクトリに`config.toml`を作成し、[READMEの手順](../README.md#はじめに)に従ってトークンと許可するIDを設定します。
コマンド定義には次の内容を追加してください。

```toml
[[commands]]
keyword = "echo *"
runner = "compose"
command = "worker echo *"
```

`worker`には`profiles`を指定しているため、通常の`docker compose up`では起動しません。
slack-commanderから必要なときだけ実行します。
実際の用途では、`busybox`の代わりに必要なCLIや依存関係を含む専用イメージを使用します。

## Composeプロジェクトはホストと同じパスに配置する

DooD構成では、slack-commanderが参照するComposeプロジェクトのディレクトリを、**ホストとslack-commanderコンテナで同じ絶対パスにしてください**。
先ほどの例では、次のマウントと作業ディレクトリの指定がこれに当たります。

```yaml
volumes:
  - /opt/stacks/slack-app:/opt/stacks/slack-app

working_dir: /opt/stacks/slack-app
```

Compose設定を読むのはslack-commanderコンテナですが、実際にバインドマウントを作成するのはホストのDockerデーモンです。
そのため、設定から解決されたパスがホスト上のパスと一致する必要があります。

```yaml
services:
  worker:
    volumes:
      - ./data:/data
```

たとえば、プロジェクトのディレクトリが`/opt/stacks/slack-app`なら、`./data`は`/opt/stacks/slack-app/data`として解決される必要があります。
コンテナ内だけでプロジェクトを`/workspace`へ配置すると、解決されたパスとホスト上の実際のパスが一致しません。

同じ絶対パスが必要なのは、slack-commanderコンテナが参照するプロジェクトのディレクトリです。
実行先のサービスでは、通常のDocker Composeと同様に、任意の`working_dir`やバインドマウント先を使用できます。

## 処理ごとに依存関係を分離する

実行先を分けると、必要なCLI、ライブラリ、認証情報を処理ごとに管理できます。
たとえば、次のような構成にできます。

```text
slack-commander
  |
  +--> backupコンテナ
  |      AWS認証情報
  |      バックアップ用CLI
  |
  +--> network-toolコンテナ
  |      LANへのアクセス
  |      機器操作用CLI
  |
  +--> agentコンテナ
         AIツール
         APIトークン
```

slack-commander本体に処理用のツールを追加せずに済み、本体のイメージを小さく保てます。
ファイルシステムやネットワークへのアクセスも、実行先ごとに調整できます。

## 実行先コンテナのネットワークを分離する

実行先のサービス同士が通信する必要がなければ、Dockerネットワークを分離できます。
次の例では、`backup`と`agent`を別々のネットワークに参加させています。
slack-commanderの設定ファイルのマウントなど、ネットワーク以外の設定は省略しています。

```yaml
services:
  slack-commander:
    image: ghcr.io/hnw/slack-commander:latest
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - /opt/stacks/slack-app:/opt/stacks/slack-app
    working_dir: /opt/stacks/slack-app

  backup:
    image: example/backup
    profiles:
      - manual
    networks:
      - backup

  agent:
    image: example/agent
    profiles:
      - manual
    networks:
      - agent

networks:
  backup:
  agent:
```

同じComposeプロジェクト内でも、`backup`と`agent`は別々のDockerネットワークに参加します。
必要なネットワーク、認証情報、ボリュームだけを各サービスへ渡すことで、ある実行先の問題が他へ広がる範囲を小さくできます。

ただし、共通のボリュームを共有している場合や強い権限を与えている場合は、ネットワーク以外の経路で他のサービスへ影響を与える可能性があります。
分離の範囲は、ネットワークだけでなくマウントや権限も含めて確認してください。

## Dockerソケットの権限

一般的なDocker環境では、`/var/run/docker.sock`へアクセスできるプロセスはDockerデーモンを操作できます。
新しいコンテナの作成、ホストのファイルシステムのマウント、強い権限を持つコンテナの起動も可能です。
そのため、Dockerソケットを持つslack-commanderは、ホストに対して強い権限を持つものとして扱ってください。

運用時は次の点を確認してください。

* 信頼できるslack-commanderのイメージを使用する
* Slackから実行できるユーザーやチャンネルを制限する
* 不要な認証情報をslack-commander本体へ渡さない
* 処理用の認証情報や依存関係は実行先コンテナへ分離する

公式イメージにシェルや汎用ツールを含めない構成も、不要な実行環境を減らすためのものです。
ただし、Dockerソケットから与えられる権限そのものが小さくなるわけではありません。

## 認証情報を渡すサービスを限定する

APIキーやログイン情報などは、その処理を実行するサービスにだけ渡す構成にします。
次の例は、`API_TOKEN`を`worker`サービスへ渡す指定です。

```yaml
services:
  worker:
    environment:
      - API_TOKEN
```

認証情報の受け渡し先を設定上で限定しても、Dockerソケットを持つslack-commanderとの完全な隔離にはなりません。
本体の権限については、前節の説明も踏まえて運用してください。
