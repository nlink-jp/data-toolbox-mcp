# ADR-0004: Phase 1 では stdio トランスポートのみをサポートする

- Status: Accepted
- Date: 2026-06-05
- Driver: magi
- Generalises to: なし

---

## Context

MCP プロトコルは複数のトランスポートをサポートする:

- **stdio**: クライアントが MCP サーバープロセスを spawn し、stdin/stdout で JSON-RPC を交換
- **SSE / HTTP**: クライアントが HTTP エンドポイントに接続。複数クライアント同時接続・リモートホスティングが可能

母体である lab-series の mcp-skeleton は両方をサポートしている（`server/app.py` と `server/sse.py`）。一方、`data-toolbox-mcp` の RFP §1 では「Claude Desktop / Cursor 等の MCP クライアントから stdio で使う、個人マシン中心」を主要利用シーンと確定している。

HTTP/SSE をサポートすると以下が必要になる:

- 認証 / 認可（誰がアクセスできるか）
- TLS 構成（証明書管理、HTTPS リスナー）
- SSRF / リクエストスマグリング対策
- マルチクライアント時の workspace 競合制御の精緻化
- リバースプロキシ / ロードバランサーとの相互運用

これらは Phase 1 のスコープと体力を超える。

## Decision

Phase 1 は **stdio トランスポートのみ** をサポートする:

- エントリポイントは `data-toolbox-mcp serve`（または引数なしで起動 → stdio モード）
- HTTP / SSE モードのフラグは Phase 1 では実装しない
- mcp-guardian の `internal/transport/process.go` パターン（`bufio.Scanner` を 1MB バッファ + 1 行ずつ Scan）を流用する

将来 HTTP/SSE 対応を追加する際は、別 ADR で以下を検討する:

- 認証方式（API キー / OAuth / mTLS）
- workspace の排他制御戦略
- mcp-guardian を前段に置く構成と本サーバーが直接 HTTP 待ち受けする構成の選択

## Consequences

**Positive:**

- 認証・TLS・SSRF などのセキュリティ課題を Phase 1 でスキップできる
- mcp-guardian の transport コードを参考にできるため、実装の見通しが立ちやすい
- テストハーネスも shell mock server + exec.Command でシンプルに書ける（mcp-guardian の `proxy_test.go` パターン流用）
- 個人マシン上で Claude Desktop / Cursor から動かす主要シナリオに集中できる

**Negative:**

- チーム共有 / リモートホスティングは未対応。チーム需要がある場合は Phase 2 以降を待つ必要
- 単一プロセス stdio 接続のため複数 LLM クライアントの同時利用は不可（同一クライアントが workspace_id を切り替える形でしかマルチ workspace は使えない）
- 「同じマシン上で動く別プロセスが MCP 経由でアクセス」というユースケースには複数プロセスが各々サーバーを spawn する形になる（DuckDB の単一プロセス書き込み制約と干渉する可能性 → ADR-0001 のシリアル化方針で対処）

## Alternatives Considered

### A1: stdio + HTTP/SSE 両対応（mcp-skeleton 路線）

- Pros: 用途の幅が広い、将来の柔軟性が高い
- Cons: 認証・TLS・マルチクライアント制御を Phase 1 で実装する必要。スコープを大きく超える
- 却下理由: Phase 1 の主要シナリオが個人マシン + stdio で決まっており、追加実装の ROI が低い

### A2: HTTP/SSE 専用

- Pros: チームでの集中利用・モニタリング・監査ログが構築しやすい
- Cons: Claude Desktop / Cursor の主要 MCP クライアントが stdio 中心。主要ユーザーをカバーできない
- 却下理由: 主要シナリオに合わない

## See also

- `_wip/data-toolbox-mcp/docs/ja/data-toolbox-mcp-rfp.ja.md` §1 / §3 / §7-A
- メモリ: `feedback_mcp_no_protocol_cancel`（MCP 仕様にキャンセル通知なし）
- メモリ: `feedback_child_process_exit_status`（子プロセス終了ステータスの取り扱い）
- メモリ: `feedback_mcp_proxy_always_responds`（中継時は必ず応答を返す）
- 参考実装: `util-series/mcp-guardian/internal/transport/process.go`
