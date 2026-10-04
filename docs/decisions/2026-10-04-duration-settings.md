# 時間設定を duration として解決する

## Status

Accepted

## Context

`output_flush_interval`はGo duration文字列を使う一方、`timeout`と`stdin_idle_timeout`は整数秒を使っており、時間設定の表現が統一されていない。

`timeout`と`stdin_idle_timeout`に既存の`Duration`を使用すると、go-tomlは文字列のdurationに加えて整数も読み込み、整数はナノ秒として扱われる。この動作を変更するための独自decoderは導入せず、整数秒との互換も維持しない。

ただし、旧設定の整数秒をそのまま使用すると極端に短いtimeoutとして解釈されるため、`timeout`と`stdin_idle_timeout`にはゼロまたは1ms以上という制約を設ける。

## Decision

時間を表す3項目の設定例を Go duration 文字列に統一する。
`timeout` と `stdin_idle_timeout` は既存の `Duration` decode を再利用し、解決後は `time.Duration` を渡す。
独自の整数秒 decoder は導入せず、整数は go-toml の標準動作に従ってナノ秒として扱う。
`timeout` と `stdin_idle_timeout` はゼロまたは1ms以上に限定する。
`output_flush_interval` にはこの下限を適用しない。
未指定値の継承、明示した `"0s"` による上書き、タイムアウトの動作と併用制約は維持する。

## Consequences

- 時間設定の例をGo duration文字列に揃え、分・時間・秒未満の値も同じ構文で表せる。
- 整数秒を使う既存設定は duration 文字列または整数ナノ秒への移行が必要になる。
- 1ms未満の正の実行・無入力タイムアウトは設定エラーになる。
- 未指定とゼロ値を区別するため、decode 時のポインタは引き続き必要になる。
