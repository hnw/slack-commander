# KNOWLEDGE

## 現在の設計判断

- command出力は `Command.output CommandOutputHandler` が所有する。mainがoutput queueを作った後、resolved config treeからroot / explicit reply / implicit stdin replyを再帰構築し、`NewCommand`の必須引数としてhandlerを渡す。nil / typed nilは生成時に拒否し、後付けの`ConfigureOutput`は持たない（`config.go`、`cmd/command.go`）。
- `SystemErrorKind` でparse errorとchain内command not foundを区別する。前者はSystemReplyConfigとExitCode=2、後者はnilのsystem defaultと既存OutputWriter経路を維持する。chainのStart / Finishは先頭Command、各commandのstdout / stderrは各自のhandlerを使う。TTYとtimeout文言の経路は既存どおり。
- `pubsub.NewSlackOutputHandler`は内部の具体型を`cmd.CommandOutputHandler`として返す。表示設定・system設定・flush interval・queue配送、`CommandOutput` / writer / sixelはpubsubが所有し、Executor / Dispatcherは出力設定やqueueを参照しない。SystemReplyConfigはReplyBroadcastから生成する。
- handler境界はfakeで検証し、queue形式・表示設定はpubsubのadapterテストとmainのreply tree統合テストで確認する。Go 1.25で全テスト・race検出・lint・buildを確認した。今回の責務移動とconstructor注入は明示要件に沿った可逆な内部変更であり、独立ADRは見送る。PR #48はレビュー待ちとする。
- ADR候補: 時間設定を duration として解決し、整数秒の互換は維持しない。整数はgo-tomlによるtime.Durationのdecode結果をそのまま受け入れ、ナノ秒として扱う。旧整数秒設定の誤用を検出するため、`timeout` / `stdin_idle_timeout` は0または1ms以上に制限する。方針を [duration settings](../docs/decisions/2026-10-04-duration-settings.md) に記録した。既存の `Duration` とポインタによる指定有無の区別を再利用し、解決後は `time.Duration` とする。
- timeout は `runMatchedCommand` が context の生成と表示を管理する。`WithTimeoutCause` と専用の `errCommandTimeout` を使い、command 自身の timeout だけを `context.Cause` で識別する。`Cmd.Run()` / `RunWithStdin(started)` は timeout 値を受け取らず、渡された context に従って停止する。親 context の deadline/cancel・通常の非0終了・143だけでは timeout 表示しない。親 deadline の回帰テストは timeout 設定が0の場合も通常出力・TTYの両方で確認する（`cmd/executor_timeout_test.go`）。TTY は従来の terminal 出力経路を維持する。runner 単体では timeout 文言を出さず、exec / compose / HTTP の deadline 終了で143を返す（`cmd/runner_timeout_test.go`）。依頼に明記された責務移動と判定修正で可逆な変更のため、独立 ADR は見送る。
- `RawCommandConfig` とその `RawExecutorConfig` は TOML 上の未指定値を保持する decode 用構造とする。root / reply の既定値、継承、制約は `resolveConfig` で一度だけ解決し、mainの`resolvedCommandConfig`にcmdの設定・型付きpubsub.ReplyConfig・flush interval・reply treeを保持する。`--check-config` は runner を生成しない。
- `cmd.CommandConfig`にはRepliesや出力設定を持たせず、runtime reply treeは`Command.replies *CommandSet`だけで表現する。mainのresolved reply treeから表示設定とrunnerを組み合わせて生成する。
- startup で `CommandSet` とその `Command` / matcher / runner、および `Executor` を一度だけ構築する。routing と全 worker、および非同期HTTP実行は同じ runtime object を使う。matcher / runner は実行ごとの可変状態を持たず、compose runner も同時実行可能であるため worker ごとには生成しない。
- runtime `CommandConfig` の入力解析設定は `ParserConfig`、実行時設定は `ExecutorConfig` に分ける。TOMLタグを持つ `cmd.RawMatcherConfig` / `cmd.RawRunnerConfig` はruntimeの `MatcherConfig` / `RunnerConfig` に埋め込んで共有し、field定義を重複させない。raw executor設定はmainに保持する。
- `ConversationID` は transport 固有の thread 型ではなく、継続した command interaction を識別する単位とする。Slack では channel ID + root timestamp に対応する。
- conversation 単位の責務を `ConversationRouter`（root/reply routing と explicit reply route cache）、stdin reply runner（選択済みendpointへの直接配送）、`StdinStore`（active stdin endpoint・implicit reply Command・lifecycleの組）、`ConversationLocks`（command 実行の直列化）へ分ける。main で各 instance を生成し、RouterとworkerがStoreを使う。worker は Lock の unlock 関数を defer し、Executor は直列化を担当しない。
- stdin endpoint はrunnerが実際のwriterを渡した後にだけ公開し、古いendpointのcloseは後から登録されたendpointを削除しない。RouterはACL照合時に得たendpoint pointerを`CommandInput`の内部runtime fieldに保持し、direct stdin reply Cmdへ渡す。stdin reply runnerはStoreやConversationIDを知らず、runtime argumentも受け取らない。
- `ConversationRouter` のroute cacheはexplicit thread replyのresolveに使う `*Command` だけを保持する。`(*Command, bool)` の二値で未確認・reply不可（nil）・reply可能を表す。live受付とhistory復元は同じexplicit reply選択を使い、cache hitではrootを再parseしない。single commandの非stdin reply対応Commandを選び、chainとstdin rootはnegative cacheする。active stdin replyでは、Store entryのimplicit reply Commandだけで候補indexを照合する。
- `CommandDispatcher` はparse error処理後にchain可否を判定し、`ResolvedInput.DispatchTarget()` がmatch済みcommandの実行経路を集約する。HTTPだけのchainもworker外で全体を一度だけ共有Executorへ渡し、exec / composeを含むchainはqueueを使う。両経路で同じConversationLocks、outputQueue、SlackWriterを共有する。
- `CommandConfig.Dispatch` は各commandの実行経路を `DispatchMode`（Queue / Executor / Runner）で保持する。ゼロ値は通常のQueueであり、NoneやInvalidを持たない。設定解決時にexec / composeはQueue、HTTPはExecutor、stdin replyはRunnerを明示する。入力全体の実行経路も同じ `DispatchMode` 型で表す。
- `ResolveInput` はparse / matchを行い、`ResolvedInput.DispatchTarget()` はparse errorを考慮せずmatch済みcommandの `DispatchMode` を集約する。未matchは集約対象から除外し、match済みcommandがなければdispatch先なしとして返す。Queueを優先し、Runnerは既存executionへのstdin配送専用で、複数partに含まれた場合は拒否する。
- parse errorはexecution前の入力エラーとしてDispatcher入口で先頭Commandのhandlerへ渡し、Executorへ渡さない。最初のpartがmatch済みの場合だけ受理し、handler・ConversationID / MessageID・文言をその時点で確定する。pubsubのhandlerはSystemReplyConfigを使い、`Spawned` / `Finished`なし・ExitCode=2で配送する。DispatcherのClose / Waitはこの出力配送も管理する。
- ADR候補: ユーザーの追加要件により、Runnerのparse errorをqueueへ送る前回判断を撤回し、入力エラーをdispatch前に出力する。永続化やTOML契約を変えない可逆な内部変更のため独立ADRは見送る。SlackWriterはlifecycle flagなしでも本文を投稿できるので、入力エラーにexecution lifecycleは発行しない。
- DispatcherのCloseと非同期WaitGroupへのAddは同じmutexで保護する。listener終了後にCloseし、workerと非同期実行の完了を待ってからoutputQueueを閉じる。WaitはClose後にだけ呼ぶ。
- ADR検討: Dispatcherへの責務抽出とHTTPの実行goroutine変更は今回の明示要件であり、永続化や設定契約を変えない可逆な内部変更のため、独立ADRは見送る。HTTP同時実行数の上限とconversation内FIFOは導入しない。
- `stdin_idle_timeout` は process timeout と独立した EOF の安全弁である。`0` は無効、負値は設定エラーとする。interactive stdin は exec と compose だけに適用し、HTTP には適用しない。
- Slack 固有の入力表現は `pubsub` で正規化してから `cmd` に渡す。mention 除去や Slack による quote / entity / URL 表現の復元は transport boundary の責務とし、command body や stdin reply も例外扱いしない。reply の末尾には LF を一つ補うが、本文中の改行は保持する。
- Slack ACLはconfig resolve時にglobal command index付きのflat `ListenerConfig`へ展開する。トップレベルallowlistを最大範囲とし、下位の非空リストは親の部分集合、空リストは継承として解決する。open access判定は従来どおりトップレベルの両allowlistだけを見る。詳しくは [command ACL ADR](../docs/decisions/2026-10-01-command-acl.md)。
- Listener候補は入力種別に対応するglobal command indexだけをExecutorへ渡す。Executorは候補外commandを無視して定義順に照合するため、同一keywordやspecific commandから後続wildcardへ通常どおりfallthroughする。nil候補・明示empty候補はいずれもdeny。
- `accept_reminder`はcommand/reply専用で既定false、継承しない。Reminderはuser allowlistを免除するがchannel allowlistは適用する。bot投稿はBot IDを通常のsender IDとして評価し、自身のuser IDまたはBot IDからの投稿は常に無視する。
- stdin返信は独立したglobal command indexを持つ`*` replyとして照合し、root commandのresolved ACLを複製する。Listenerはroot候補とreply候補を分け、stdin runnerが返信本文全体をRouterの選択endpointへ渡す。explicit replyの`accept_reminder`は引き続き継承しない。
- ADR候補: raw返信照合を `InputBodyRawStdin` で表す。今回の明示指示に基づく。可逆な内部構造変更のため独立ADRは見送る。
- Dispatcherの副作用なし判定をlive受付とhistory復元のroot資格確認にも使い、liveで拒否されるchainがhistory復元経由で返信を受理することを防ぐ。parse error出力を受理したrootはlive cacheへ保存しない。historyの未一致・不正rootはnilのreply Commandとしてnegative cacheし、繰り返し問い合わせを避ける。
- stdin interactionをchain内で許可する。Executorは実行中の各Commandが生成したendpointとimplicit reply Commandを一括登録し、Routerはactive entryのreply ACLで照合する。Routerにoperatorや実行状態を持たせない。既存設定のchain制約を緩め、active stdinのACLを明確にする可逆な変更として独立ADRを作成した（[active stdin reply ACL](../docs/decisions/2026-10-03-active-stdin-reply-acl.md)）。


## Slack output pipeline の内部化（2026-10-05）

- `SlackOutput`がprivateな`slackOutputEvent` channelを所有する。mainは生成・command出力factory注入・Run・Closeだけを担当する。
- cmdの出力抽象を`CommandOutput`へ改名し、pubsubのbuffer / timer streamをprivate化した。text / sixel分離用rawWriterは責務が異なるため維持する。
- producerと最終Flushの完了後にCloseする契約を維持する。context終了後の複数producer drainと非同期HTTP drainをテストで確認する。
- ADR検討: queue所有者のpubsubへの移動は依頼された可逆な内部変更であり、設定・永続化・外部API契約を変えないため独立ADRは見送る。

## 既知の境界上の課題

- route cache miss 時の再判定では `RootInputResolver` を通じて transport 側の root text と起点senderの候補indexを取得している。現在の Slack 実装では `pubsub.SlackRootInputResolver` が同期的に取得するため、cmd 側から transport 側へ問い合わせる形が残っている。
- `runWithLifecycleInput*` は `runMatchedCommand` が既に持つCommand実行contextを複数引数へ展開して受け取っており、interactive stdinの仕様追加に伴って引数が増えている。独立helperとして残す必要性を見直し、`runMatchedCommand`への統合またはstdin endpoint生成処理だけの切り出しを検討する。
