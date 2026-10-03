# KNOWLEDGE

## 現在の設計判断

- `RawCommandConfig` とその `RawExecutorConfig` は TOML 上の未指定値を保持する decode 用構造とする。root / reply の既定値、継承、制約は `resolveConfig` で一度だけ解決し、後段には resolved `cmd.CommandConfig` だけを渡す。`--check-config` は runner を生成しない。
- `CommandConfig.Replies` は resolved config tree にだけ残す。instantiate 時に `Command.config.Replies` を nil にし、runtime reply tree は `Command.replies *CommandSet` だけで表現する。
- startup で `CommandSet` とその `Command` / matcher / runner、および `Executor` を一度だけ構築する。routing と全 worker、および非同期HTTP実行は同じ runtime object を使う。matcher / runner は実行ごとの可変状態を持たず、compose runner も同時実行可能であるため worker ごとには生成しない。
- runtime `CommandConfig` の入力解析設定は `ParserConfig`、実行時設定は `ExecutorConfig` に分ける。TOMLタグを持つ `cmd.RawMatcherConfig` / `cmd.RawRunnerConfig` はruntimeの `MatcherConfig` / `RunnerConfig` に埋め込んで共有し、field定義を重複させない。raw executor設定はmainに保持する。
- `ConversationID` は transport 固有の thread 型ではなく、継続した command interaction を識別する単位とする。Slack では channel ID + root timestamp に対応する。
- conversation 単位の責務を `ConversationRouter`（root/reply routing と explicit reply route cache）、stdin reply runner（選択済みendpointへの直接配送）、`StdinStore`（active stdin endpoint・implicit reply Command・lifecycleの組）、`ConversationLocks`（command 実行の直列化）へ分ける。main で各 instance を生成し、RouterとworkerがStoreを使う。worker は Lock の unlock 関数を defer し、Executor は直列化を担当しない。
- stdin endpoint はrunnerが実際のwriterを渡した後にだけ公開し、古いendpointのcloseは後から登録されたendpointを削除しない。RouterはACL照合時に得たendpoint pointerを`CommandInput`の内部runtime fieldに保持し、direct stdin reply Cmdへ渡す。stdin reply runnerはStoreやConversationIDを知らず、runtime argumentも受け取らない。
- `ConversationRouter` のroute cacheはexplicit thread replyのresolveに使う `*Command` だけを保持する。`(*Command, bool)` の二値で未確認・reply不可（nil）・reply可能を表す。live受付とhistory復元は同じexplicit reply選択を使い、cache hitではrootを再parseしない。single commandの非stdin reply対応Commandを選び、chainとstdin rootはnegative cacheする。active stdin replyでは、Store entryのimplicit reply Commandだけで候補indexを照合する。
- `CommandDispatcher` はparse error処理後にchain可否を判定し、`ResolvedInput.DispatchTarget()` がmatch済みcommandの実行経路を集約する。HTTPだけのchainもworker外で全体を一度だけ共有Executorへ渡し、exec / composeを含むchainはqueueを使う。両経路で同じConversationLocks、outputQueue、SlackWriterを共有する。
- `CommandConfig.Dispatch` は各commandの実行経路を `DispatchMode`（Queue / Executor / Runner）で保持する。ゼロ値は通常のQueueであり、NoneやInvalidを持たない。設定解決時にexec / composeはQueue、HTTPはExecutor、stdin replyはRunnerを明示する。入力全体の実行経路も同じ `DispatchMode` 型で表す。
- `ResolveInput` はparse / matchを行い、`ResolvedInput.DispatchTarget()` はparse errorを考慮せずmatch済みcommandの `DispatchMode` を集約する。未matchは集約対象から除外し、match済みcommandがなければdispatch先なしとして返す。Queueを優先し、Runnerは既存executionへのstdin配送専用で、複数partに含まれた場合は拒否する。
- parse errorはexecution前の入力エラーとしてDispatcher入口で共有outputQueueへ送り、Executorへ渡さない。最初のpartがmatch済みの場合だけ、同commandのSystemReplyConfigと入力のConversationID / MessageIDで表示する。`Spawned` / `Finished`は付けず、エラー出力のExitCodeを2とする。DispatcherのClose / Waitはこの出力配送も管理する。Executorはparse済み・resolve済みcommand / chainの実行だけを担当する。
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

## 既知の境界上の課題

- route cache miss 時の再判定では `RootInputResolver` を通じて transport 側の root text と起点senderの候補indexを取得している。現在の Slack 実装では `pubsub.SlackRootInputResolver` が同期的に取得するため、cmd 側から transport 側へ問い合わせる形が残っている。
- `cmd.CommandConfig` は `[[commands]]` 全体を表す型ではなく、cmd 側で使う resolved 情報に加えて writer へ渡す `ReplyConfig` / `SystemReplyConfig` を opaque に保持している。名前と責務にずれがあるため、output / writer 周りの責務整理と合わせて、所属・名前・保持内容を見直す余地がある。
- `runWithLifecycleInput*` は `runMatchedCommand` が既に持つCommand実行contextを複数引数へ展開して受け取っており、interactive stdinの仕様追加に伴って引数が増えている。独立helperとして残す必要性を見直し、`runMatchedCommand`への統合またはstdin endpoint生成処理だけの切り出しを検討する。
