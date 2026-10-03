# stdin返信の認可をactiveなCommandに結び付ける

## Status

Accepted

## Context

stdinを含むchainで、最初のstdin Commandを返信の照合に使うと、後続のstdin Commandが実行中でも最初のCommandのACLが適用される。
各stdin Commandのimplicit `*` replyには独立したglobal command indexと、そのCommandから継承したresolved ACLがある。
Listenerは返信者とchannelに対して許可されたreply indexを渡しており、この候補生成方式は維持する。

## Decision

active stdinを生成したCommandのimplicit reply Commandを、endpointと同じStdinStore entryに登録する。
Routerは返信ごとにactive entryを一度lookupし、そのimplicit replyだけで返信を照合する。
候補に含まれない場合は返信を無視し、explicit replyへはフォールバックしない。
返信がdispatch可能なら、Routerは同じentryのendpointを`CommandInput`の内部runtime fieldに保持し、direct stdin reply Cmdへ渡す。RunnerはStoreを再lookupしない。
選択endpointがすでにclosedなら返信をdropし、切替後のendpointへ転送しない。
active entryがない場合は、route cacheのexplicit reply routeを使う。
explicit reply routeがないことを表すnegative cacheは維持する。

Executorが実際にCommandを開始した際にreply情報を登録し、Routerにはchainの現在位置やoperatorの実行状態を持たせない。
active stdinがない期間の返信は待機・再送しない。

## Consequences

- chainでstdin Commandが切り替わると、返信の認可もそのCommandのACLへ切り替わる。
- stdin返信はStdinStore、explicit replyはroute cacheという責務に分かれる。
- StdinLifecycleはendpointに加えてimplicit reply Commandを通知し、Routerがactive entryを一度lookupする。
- RouterはACL照合に使ったentryのendpointを`CommandInput`の内部runtime fieldへ保持し、direct stdin reply Cmdへ渡す。RunnerはStdinStoreもConversationIDも参照しないため、Storeの切替後に別endpointへ再routingしない。
- 選択endpointがすでにclosedなら返信をdropし、後から登録されたendpointへ転送しない。

## Notes

- 根拠: 2026-10-03に本チャットでユーザーが提示したactive stdinのACL仕様。
- [Slack入力制限の判断](2026-10-01-command-acl.md)
- [内部構造](../internal.md)
