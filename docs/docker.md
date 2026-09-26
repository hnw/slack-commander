# Dockerでの運用

slack-commanderは公式コンテナイメージを利用して実行できます。

公式イメージはslack-commander本体を動かすための最小構成になっており、shell、Docker CLI、Compose CLI、一般的なCLIツールは含みません。

そのため、公式コンテナ内へさまざまなツールを追加するよりも、実際の処理を別コンテナへ分離し、`compose` runnerから必要なときだけ実行する構成を想定しています。

## 公式コンテナを最小構成にしている理由

公式コンテナが最小構成なのは、イメージサイズを小さくするためだけではありません。

slack-commander本体にshellや多数の汎用ツールを含めないことで、不要な実行環境を持ち込まず、攻撃面を小さくできます。

実際に必要なCLI、ライブラリ、credentialは、それらを利用する処理側のコンテナにだけ持たせることができます。

たとえば、

* AWS CLIを使う処理
* GitHub APIへアクセスする処理
* 家庭内ネットワーク上の機器を操作する処理
* AIエージェントを実行する処理

をそれぞれ別コンテナに分ければ、それぞれに必要な依存関係やcredentialだけを持たせることができます。

slack-commander本体は、Slackからの入力を受け取り、必要な処理を起動し、結果をSlackへ返す役割に限定できます。

## `compose` runner

`compose` runnerを使うと、Docker Composeで定義したserviceをslack-commanderから実行できます。

たとえば、次の設定では`worker` service内で`echo`を実行します。

```toml
[[commands]]
keyword = "echo *"
runner = "compose"
command = "worker echo *"
```

Slackに

```text
echo hello
```

と投稿すると、`worker`コンテナ内で`echo hello`を実行し、その出力をSlackへ返します。

`compose` runnerの設定項目については [設定リファレンス](config.md) を参照してください。

## DooD

slack-commander自身をコンテナで実行し、その中から`compose` runnerを使う場合は、ホスト側のDocker daemonを利用します。

このように、コンテナ内で別のDocker daemonを動かすのではなく、ホスト側のDocker daemonを使って別コンテナを起動する構成は、DooD（Docker outside of Docker）と呼ばれることがあります。

```text
Slack
  |
  v
slack-commander container
  |
  | Docker API
  v
host Docker daemon
  |
  +--> worker container
  +--> agent container
  +--> other service container
```

典型的には、ホストのDocker socketをslack-commanderコンテナへマウントします。

```yaml
volumes:
  - /var/run/docker.sock:/var/run/docker.sock
```

slack-commanderの公式コンテナにはDocker CLIやCompose CLIは含まれていませんが、`compose` runnerはslack-commander自身に組み込まれているため、Docker CLIを追加する必要はありません。

## 構成例

たとえば、ホスト上のCompose projectが次のディレクトリにあるとします。

```text
/opt/stacks/slack-app
```

その場合、次のようにslack-commander本体と、実際の処理を行うserviceを同じCompose projectに定義できます。

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

対応する`config.toml`は、たとえば次のようになります。

```toml
[[commands]]
keyword = "echo *"
runner = "compose"
command = "worker echo *"
```

`worker`には`profiles`を指定しているため、通常の`docker compose up`では起動せず、slack-commanderから必要なときだけ実行するserviceとして扱えます。

実際の用途では、`busybox`の代わりに必要なCLIや依存関係を含む専用イメージを使用します。

## Compose projectはホストと同じパスに配置する

DooD構成で`compose` runnerを利用する場合、slack-commanderが参照するCompose projectのディレクトリは、**ホストとslack-commanderコンテナで同じ絶対パスにしてください**。

先ほどの例では、

```yaml
volumes:
  - /opt/stacks/slack-app:/opt/stacks/slack-app

working_dir: /opt/stacks/slack-app
```

としています。

これは、Compose設定を読むのはslack-commanderコンテナ内であっても、実際にbind mountを作成するのはホスト側のDocker daemonだからです。

たとえば、Compose設定に次のような指定があるとします。

```yaml
services:
  worker:
    volumes:
      - ./data:/data
```

ホスト上のproject directoryが

```text
/opt/stacks/slack-app
```

であれば、`./data`は最終的に

```text
/opt/stacks/slack-app/data
```

として扱われる必要があります。

一方、slack-commanderコンテナ内だけでCompose projectを`/workspace`のような別パスに配置すると、Compose側で解決されたパスと、ホスト側の実際のパスが一致しません。

そのため、DooDで`compose` runnerを利用する場合は、Compose projectをホストと同じ絶対パスへマウントしてください。

この制約は、**slack-commanderコンテナ自身にだけ適用されます**。

`compose` runnerから起動されるserviceについては、通常のDocker Composeと同様に、任意の`working_dir`やbind mount先を使用できます。

## 実処理を別コンテナへ分離する

DooDを使う利点の一つは、slack-commander本体と実際の処理を分離できることです。

たとえば、次のような構成にできます。

```text
slack-commander
  |
  +--> backup container
  |      AWS credential
  |      backup CLI
  |
  +--> network-tool container
  |      LAN access
  |      device CLI
  |
  +--> agent container
         AI tool
         API token
```

slack-commander本体には、これらのcredentialや依存関係を持たせる必要がありません。

これにより、

* 本体コンテナを小さく保てる
* 処理ごとに依存関係を分離できる
* credentialを必要なコンテナにだけ渡せる
* filesystemやnetwork accessを処理ごとに調整しやすい

といった利点があります。

## 実行先コンテナをネットワークで分離する

複数のserviceを`compose` runnerから実行する場合、それぞれが互いに通信する必要がなければ、Docker networkを分離できます。

たとえば、次の構成では`backup`と`agent`を別々のnetworkに参加させています。

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

この場合、`backup`と`agent`は同じCompose projectに含まれていても、互いに同じDocker networkへ参加しません。

実行する処理ごとに、

* 必要なnetworkだけへ参加させる
* 不要なservice同士を同じnetworkへ参加させない
* credentialやvolumeも必要なserviceだけへ渡す

ようにすると、ある実行先コンテナに問題が起きた場合でも、他の実行先コンテナへ影響が広がる範囲を小さくできます。

networkの分離だけで完全な隔離になるわけではありません。共通のvolumeを共有している場合や、強い権限を与えている場合などは、別の経路で他のserviceへ影響を与えられる可能性があります。

## Docker socketの権限

DooD構成では、Docker socketへのアクセス権に注意が必要です。

一般的なDocker環境では、`/var/run/docker.sock`へアクセスできるプロセスはDocker daemonを操作できます。

これは単に「既存のserviceを起動できる」というだけの権限ではありません。

Docker daemonを通じて新しいコンテナを作成したり、ホストのfilesystemをマウントしたり、強い権限を持つコンテナを起動したりできるため、Docker socketへアクセスできるプロセスは、ホストに対して非常に強い権限を持つものとして扱う必要があります。

そのため、

* slack-commander自身を信頼できるイメージとして扱う
* Slackから実行できるユーザーやチャンネルを制限する
* 不要なcredentialをslack-commander本体へ渡さない
* 実際のcredentialや依存関係は実行先コンテナへ分離する

といった構成を推奨します。

公式コンテナを最小構成にしていることも、この信頼境界をできるだけ小さく保つための一つの手段です。

## credentialの扱い

API keyやログイン情報など、実処理だけが必要とするcredentialは、できるだけslack-commander本体へ渡さず、実行先コンテナにだけ渡すことを推奨します。

たとえば、

```yaml
services:
  worker:
    environment:
      - API_TOKEN
```

のように、必要なserviceだけへcredentialを渡します。

slack-commander本体は「どの処理を起動するか」を担当し、実際のcredentialは処理側に持たせることで、役割と信頼境界を分離しやすくなります。
