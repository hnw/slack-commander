# 内部実装について

## goroutine と channel の関係図

```mermaid
flowchart TD
    listener[SlackListener] --> router[ConversationRouter]
    router --> resolve[ResolveInput: parse and match]
    resolve --> dispatcher[CommandDispatcher]
    dispatcher -->|ParseErr| parseOutput[parse error output]
    parseOutput --> output[outputQueue]
    dispatcher -->|DispatchNone| ignored[ignore]
    dispatcher -->|DispatchQueue: exec / compose / mixed chain| queue[commandQueue]
    queue --> workers[worker pool]
    workers --> locks[共有 ConversationLocks]
    dispatcher -->|DispatchExecutor: HTTP-only input / chain| async[非同期 goroutine]
    async --> locks
    locks --> executor[共有 Executor]
    dispatcher -->|DispatchRunner: stdin reply| direct[stdin reply runner の同期実行]
    executor --> output[outputQueue]
    output --> writer[SlackWriter]
```

Resolverは入力をparseしてpartごとにcommandを照合します。parse errorがある場合はDispatcherが実行経路を選ぶ前にerrorをoutputQueueへ送り、先頭partがunmatchedなら従来どおり無出力で終了します。
parse errorは`SystemReplyConfig`付きの`CommandOutput`として`ExitCode=2`で送られます。実行前の入力エラーなので`Spawned`と`Finished`は出しません。
各commandの`CommandConfig.Dispatch`は`CommandDispatch`（`CommandDispatchQueue`、`CommandDispatchExecutor`、`CommandDispatchRunner`）で実行方法を示し、`CommandDispatchQueue`はゼロ値です。`ResolvedInput.DispatchTarget()`がmatched commandの値を`DispatchTarget`（`DispatchNone`、`DispatchQueue`、`DispatchExecutor`、`DispatchRunner`）へ集約し、Dispatcherはその結果だけを見て経路を選びます。`DispatchNone`はcommandの未設定値ではなく、入力全体にmatched commandがない状態を表します。
Routerはcommandの照合とroot ownershipのキャッシュを担当し、Dispatcherが解決済み入力のpolicyから実行経路を選びます。
通常のcommand executionは必ずExecutorを通します。DispatcherはExecutorへ渡す方法として、worker queueを経由する`DispatchQueue`と、queueを使わない`DispatchExecutor`を選びます。
exec / composeを含む入力は`DispatchQueue`でworker poolへ送ります。HTTPだけで構成された入力は、単一commandでもchainでも全体を1回のExecutor呼び出しとして非同期実行し、通常のcommandQueueが満杯でも受理します。
stdin replyは既存executionへの入力配送でcommand executionではないため、`DispatchRunner`としてExecutorを介さずrunnerへ直接渡します。
Executorはresolve済みcommand / chainの実行だけを担当し、parse errorを出力しません。

workerと`DispatchExecutor`のgoroutineは同じConversationLocksとExecutorを使います。
異なるconversationのHTTPはworker数に関係なく並列実行できますが、同じconversationのcommand実行は直列です。
待機順は厳密なFIFOではありません。
stdin replyは実行中commandへの入力なので、conversation lockを待たずに同期実行します。

終了時はlistenerの停止を待ってからDispatcherを閉じ、新しいdispatchを拒否します。
commandQueueを閉じてworkerの終了を待ち、次に非同期HTTPの終了を待ってからoutputQueueを閉じます。
SlackWriterは残りの出力を処理して終了します。
DispatcherのCloseと非同期WaitGroupへのAddは同じmutexで保護し、Close後にWaitを呼びます。Waitは非同期Executor実行とparse error出力の両方を待ちます。
