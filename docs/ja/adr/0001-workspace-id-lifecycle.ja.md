# ADR-0001: workspace_id によるスコープとライフサイクル

- Status: Accepted
- Date: 2026-06-05
- Driver: magi
- Generalises to: なし

---

## Context

母体である shell-agent-v2 は、UI セッション単位で DuckDB エンジン（`app/internal/analysis/engine.go` の Lazy-open Engine）と Podman コンテナ（`app/internal/sandbox/cli.go` の `EnsureContainer(ctx, sessionID)`）をスコープしている。`sessionID` は Wails の UI レイヤーが生成し、ホスト側の `base-dir/sessionID/{analysis.duckdb, work/, messages.jsonl}` という flat 構造に状態を永続化する。

これを MCP サーバーとして切り出す際、stdio MCP の「セッション」概念は脆弱である:

- stdio 接続 1 本がほぼセッションに対応するが、Claude Desktop の再起動・Cursor の MCP 再接続で切断される
- 接続が切れるたびに DuckDB ファイルとコンテナを破棄すると、データ前処理を毎回やり直すことになり実用に耐えない
- 逆に「サーバーが生きている間ずっと同じ 1 つのコンテナ」だと、複数の独立した分析タスクが state を混ぜ合わせてしまう

LLM/MCP クライアントから見て **「データのまとまり = 状態のスコープ」** を明示的に指定できる仕組みが必要である。

## Decision

`load_data` / `query_data` / `execute_code` の 3 ツールすべての第 1 引数を **`workspace_id: string`** とし、LLM/クライアントが明示的に渡す文字列キーで以下をスコープする:

- Podman コンテナ 1 つ
- DuckDB ファイル 1 つ
- ホスト側 `/work` ディレクトリ 1 つ

ディスク上の配置は shell-agent-v2 の flat 構造を踏襲:

```
<workspace_dir>/
└── <workspace_id>/
    ├── analysis.duckdb
    └── work/        # コンテナ /work へマウント
```

同じ `workspace_id` を渡す限り、コンテナと DuckDB ファイルは再利用される。サーバープロセスが再起動した後でも、ディスクから state を再構築できる（DuckDB ファイルとディレクトリが残っているため）。in-memory のコンテナ参照は揮発するが、`workspace_id` を渡された時点で「コンテナが既に動いているか」を Podman に問い合わせ、無ければ立ち上げ直す（idempotent `ensure_workspace`）。

`workspace_id` の構文制約:

- `^[a-zA-Z0-9_-]{1,64}$`（パストラバーサル防御、コンテナ名の安全性）
- LLM が読み書きする文字列のため `analysis-2026Q2` のような人間可読な値を推奨

## Consequences

**Positive:**

- LLM/クライアントが状態の所在を明示するため、複数の独立分析タスクが安全に共存できる
- サーバー再起動を跨いでデータが永続するため、長時間の分析セッションが扱える
- workspace_id の名前空間が文字列なので、別 MCP クライアントから同じ名前で参照すれば共有可能
- 「未使用 workspace の掃除」を `workspace_id` ごとに明示できる（人間が気軽に消せる）

**Negative:**

- LLM 側に「状態キーを覚えておく」責務が発生する。デフォルトの workspace_id を `"default"` で受け入れるなど UX 上の補助が必要
- workspace 数が無制限に増える運用では garbage collection が必要 → Phase 2 で TTL ベースの cleanup を別 ADR にて検討
- 同一 workspace_id への並行アクセス（複数クライアント同時操作）は DuckDB の単一プロセス書き込み制約から事実上シリアル化する必要 → Phase 1 では「同一 workspace に並行アクセスしたら後着 request はエラー」とする最小実装

## Alternatives Considered

### A1: 1 MCP サーバー = 1 コンテナ = 1 DuckDB（生存中固定）

- Pros: API が単純化、引数から `workspace_id` を排除可能
- Cons: 単一 LLM クライアントから複数の独立分析を扱えない、データ汚染が容易
- 却下理由: 個人マシン上でも「複数プロジェクトの分析を同時並行で進める」ニーズが普通にあり、データ混在は致命的

### A2: MCP セッション単位で生成・破棄（shell-agent-v2 路線）

- Pros: state 管理が宣言的、garbage collection 不要
- Cons: stdio 接続が切れるたびに DuckDB 再構築。Claude Desktop の再起動でデータ消失
- 却下理由: 実用上の摩擦が大きすぎる

### A3: ホスト側 DuckDB 永続 + コンテナエフェメラル

- Pros: コンテナの再起動コストが軽い
- Cons: DuckDB は単一プロセス書き込みのため、コンテナ内 Python から書こうとすると競合が起きる。設計が複雑化
- 却下理由: A1 と同じく state スコープの問題が残り、利点が薄い

これらの代替案は RFP の Discussion Log §「ライフサイクルモデル」でも記録済み。

## See also

- `_wip/data-toolbox-mcp/docs/ja/data-toolbox-mcp-rfp.ja.md` §2 / §3
- shell-agent-v2 ADR-0001 (Session import-export bundle format)
- shell-agent-v2 ADR-0003 (Session delete UX)
- メモリ: `feedback_in_memory_disk_sync`（in-memory と disk の sync は agent 層経由）
