# Architecture: data-toolbox-mcp

> Status: Draft (Phase 0)
> Date: 2026-06-05

本書は `data-toolbox-mcp` の全体設計を、Phase 0 で確定した ADR-0001〜0005 を前提に記述する。Phase 1 着手前のレビュー対象であり、実装後に Phase 1 で確定する細部（ライブラリ選定・関数名・JSON Schema の確定形）は対象外。

## 0. Binary layout — 単一バイナリ + サブコマンド

`data-toolbox-mcp` は 1 つの Go バイナリで、サブコマンドにより機能を切り替える（メモリ `feedback_single_binary_subcommand` パターン）:

| サブコマンド | 役割 |
|------------|------|
| `serve`（または引数なし） | MCP stdio サーバー起動（本書の以下の章はすべて serve モードを対象とする） |
| `build-runtime` | `go:embed` 同梱の Dockerfile を展開し、`podman build` でランタイムコンテナイメージを build（ADR-0005） |
| `doctor` | 環境診断: Podman 存在・`podman machine` 起動状態・ランタイム image 有無・config 妥当性 |
| `version` | バージョン表示 |

これにより MCP 本体・ビルドツール・診断ツールが 1 バイナリで揃い、配布・整合性管理が単純化される。

## 1. Overview

```
┌──────────────────────┐
│ MCP Client           │  Claude Desktop / Cursor / Cline / ...
│ (LLM + UI)           │
└──────────┬───────────┘
           │ stdio (JSON-RPC over stdio)
           ▼
┌──────────────────────────────────────────────┐
│ data-toolbox-mcp server (Go)                 │
│                                              │
│  ┌─────────────┐  ┌────────────────────────┐ │
│  │ Transport   │  │ Tool Dispatcher        │ │
│  │ stdio       │─▶│  load_data             │ │
│  │ JSON-RPC    │  │  query_data            │ │
│  └─────────────┘  │  execute_code          │ │
│                   └────────┬───────────────┘ │
│                            │                 │
│  ┌─────────────────────────▼──────────────┐  │
│  │ Workspace Manager                      │  │
│  │  - in-memory map: workspace_id → ref   │  │
│  │  - disk state: <workspace_dir>/<ws>/   │  │
│  └────────┬─────────────────────┬─────────┘  │
└───────────┼─────────────────────┼────────────┘
            │ podman exec/run     │ host fs (allowed_paths)
            ▼                     ▼
┌──────────────────────┐  ┌─────────────────────┐
│ Podman Container     │  │ Host filesystem     │
│ python:3.13-slim     │  │  allowed_paths/...  │
│ + duckdb,pandas,...  │  │  workspace_dir/...  │
│                      │  │                     │
│ /work ──────────────────▶ workspace/<ws>/work/│
│ analysis.duckdb ────────▶ workspace/<ws>/analy│
└──────────────────────┘  └─────────────────────┘
```

主要なデータの流れ:

- **load_data**: MCP server がホストファイル（`allowed_paths` 内のみ）を読み、コンテナ内 Python に DuckDB ロードを依頼
- **query_data**: コンテナ内 Python に SQL を実行させ、結果を JSON 配列で取得
- **execute_code**: コンテナ内 Python で任意コードを実行、stdout/stderr/exit_code を取得

## 2. Process boundaries（信頼境界）

| エンティティ | 信頼レベル | 根拠 |
|------------|----------|------|
| MCP クライアント (LLM) | 半信頼 | ユーザーが選んだソフトだが、LLM 生成コードは予測不能 |
| MCP サーバープロセス | 信頼 | 本プロジェクトが署名・配布する |
| Podman コンテナ | 半信頼 | 中で LLM 生成コードが走る |
| ホストファイルシステム（`allowed_paths` 内） | 信頼 | ユーザーが明示的に許可した範囲 |
| ホストファイルシステム（`allowed_paths` 外） | 信頼するがアクセス禁止 | サーバーが境界で拒否 |
| `workspace_dir/<ws>/` | 信頼 | サーバーが管理する所有領域 |

ガード:

- LLM → MCP server: stdio 経由のみ、JSON-RPC の手前で粗いスキーマ検証
- MCP server → ホスト fs: `allowed_paths` のホワイトリストチェック + シンボリックリンク解決後の再検査
- MCP server → コンテナ: `podman exec` 経由のみ、入力サイズ上限あり
- コンテナ → ホスト: `/work` マウントと DuckDB ファイルマウントのみ許可、`network=none` で外部通信遮断

## 3. Data flow（正常系シーケンス）

### 3.1 load_data(workspace_id, file_path, table_name)

```
1. MCP server: workspace_id を検証 ([a-zA-Z0-9_-]{1,64})
2. MCP server: file_path を allowed_paths と照合 (シンボリックリンク解決後)
3. MCP server: Workspace Manager に「workspace_id を ensure」依頼
   - in-memory map にない → Podman に container ls し、無ければ run、ある場合は紐付けのみ
4. MCP server: ホストの file_path を <workspace_dir>/<ws>/work/_upload/<fname> にコピー
5. MCP server: podman exec で Python に "duckdb から table_name = read_csv_auto('/work/_upload/<fname>')" を実行
6. Python: DuckDB ファイルに INSERT、行数とスキーマを stdout に JSON で返す
7. MCP server: JSON をパースして {rows_loaded, schema} を MCP クライアントに返却
```

### 3.2 query_data(workspace_id, sql)

```
1. MCP server: workspace_id 検証 + ensure (load_data と同じ手順)
2. MCP server: SQL に LIMIT が無ければ自動で LIMIT 20000 を末尾に付加（外側に LIMIT 句 wrap）。default 値は `[query] default_row_limit` で変更可能
3. MCP server: podman exec で Python に SQL 実行を依頼
4. Python: DuckDB に対して SQL 実行、結果を JSON 配列で stdout
5. MCP server: 行数が LIMIT に達していたら warning を結果に添えて返却
```

### 3.3 execute_code(workspace_id, language, code)

```
1. MCP server: language == "python" を検証（それ以外は unsupported_language エラー）
2. MCP server: workspace_id 検証 + ensure
3. MCP server: code を一時ファイル <workspace_dir>/<ws>/work/_code/<uuid>.py に書く
4. MCP server: podman exec で `python /work/_code/<uuid>.py` を起動
5. Python: コードを実行、stdout/stderr/exit_code が返る
6. MCP server: timeout 内に終了したら結果を返却、超過したらコンテナに kill シグナル
7. MCP server: 一時 code ファイルは保持（デバッグ用、Phase 2 で TTL 削除）
```

## 4. State model

### 4.1 in-memory（サーバープロセス内）

```go
type WorkspaceManager struct {
    mu         sync.Mutex
    workspaces map[string]*Workspace  // key: workspace_id
}

type Workspace struct {
    ID          string
    ContainerID string  // Podman container ID
    LastUsed    time.Time
    InUse       bool    // 同一 workspace への並行アクセス防御
}
```

### 4.2 ディスク（永続）

```
<workspace_dir>/
└── <workspace_id>/
    ├── analysis.duckdb       # DuckDB データファイル（コンテナ /work/analysis.duckdb にマウント）
    └── work/                 # コンテナ /work にマウント
        ├── _upload/          # load_data でコピーしたホストファイル
        ├── _code/            # execute_code で書いた一時 .py
        └── (user artifacts)  # plot.png 等
```

### 4.3 in-memory ⇄ disk sync ポイント

メモリ `feedback_in_memory_disk_sync` に従い、状態の sync は **必ず WorkspaceManager 経由** で行う:

- `ensure(workspace_id)` を呼ぶと:
  1. in-memory map を参照
  2. なければ disk の `<workspace_dir>/<workspace_id>/` を確認
  3. ディレクトリがあれば DuckDB ファイルを reattach、ContainerID を Podman に問い合わせ
  4. ディレクトリが無ければ作成、`podman run` で新規コンテナ起動
- サーバー起動時に既存ディスク状態を eager に読み込まない（lazy: ensure 時に初めて触れる）
- サーバー停止時にコンテナを停止するか「動かしっぱなしで次回 ensure 時に再利用」するかは、設定 `[container] stop_on_exit = true|false` で切替（default true、安全寄り）

## 5. Error & lifecycle

### 5.1 MCP プロトコル原則

メモリ `feedback_mcp_proxy_always_responds` に従い、**MCP リクエストは未応答で放置しない**。コンテナ実行失敗・タイムアウト・Podman エラーすべて JSON-RPC error 応答で返す。

### 5.2 タイムアウト

- 設定 `[container.limits] timeout_seconds` でツール単位のタイムアウト
- 超過時: `podman kill <container>` でコンテナごと強制終了 → 同 workspace の次回 ensure で再起動
- メモリ `feedback_mcp_no_protocol_cancel` / `feedback_child_process_exit_status` に従い、子プロセス kill が唯一の中断手段で、終了ステータスを surface する

### 5.3 コンテナクラッシュ

- `podman exec` がコンテナ消失でエラーを返したら、in-memory map から削除し、エラーを JSON-RPC で返却
- 次回同 workspace_id 呼び出しで ensure が走り、自動復旧

### 5.4 MCP 切断

- stdio が EOF を受けたら graceful shutdown
- 設定に従いコンテナを停止する（または継続）
- 進行中のリクエストは完了を待たずに中断（タイムアウトで cleanup）

## 6. Security model

メモリ `feedback_security_first` に従い、Phase 1 から day-1 で組み込む。

### 6.1 ホストファイルアクセス

- `allowed_paths` ホワイトリスト
- パス解決アルゴリズム:
  1. 入力 `file_path` を `filepath.Abs` で絶対化
  2. `filepath.EvalSymlinks` でシンボリックリンクを解決
  3. 解決後のパスが `allowed_paths` のいずれかのプレフィックスに一致するか検査
  4. 一致しなければ `path_not_allowed` エラー
- これにより、`/Users/me/symlink-to-secret` のような迂回攻撃を防ぐ

### 6.2 workspace_id 検証

- 正規表現 `^[a-zA-Z0-9_-]{1,64}$` でディレクトリトラバーサル防御
- コンテナ名にも同じ workspace_id を埋め込むため、Podman 命名規則とも整合

### 6.3 コンテナ実行時制限

| 項目 | 既定値 | 設定キー |
|------|------|---------|
| CPU | 1.0 | `[container.limits] cpu` |
| メモリ | 2GB | `[container.limits] memory` |
| Timeout | 60s | `[container.limits] timeout_seconds` |
| Network | none | `[container.limits] network` |
| Read-only fs | false | `[container.limits] read_only` (Phase 2 検討) |
| Query 行数 | 20000 | `[query] default_row_limit` |

**Network 設定と pip install の関係 (Phase 0 Q5-4 解決)**: `network=none`（既定）ではコンテナ内から外部接続が遮断されるため、`execute_code` 内で `pip install` を呼んでも失敗する（自然な動作）。`network` を別値に切替えれば LLM 生成コードから自由に `pip install` 可能だが、その判断は config を変更するユーザーの責任となる。Phase 1 では「pip だけ通す」のような細粒度 ACL は実装しない。

### 6.4 コンテナリーク防止

- すべてのコンテナにラベル `app=data-toolbox-mcp` を付与
- サーバー起動時 / 終了時に `podman ps --filter label=app=data-toolbox-mcp` で孤児を検出
- 孤児コンテナは ensure 時に再 attach するか、強制削除して新規起動

## 7. Testability

ADR-0004 の方針と「ダミー MCP クライアントによる自動 E2E テストハーネス」（RFP §4 Phase 1）に従い、テストを 2 層構造で構築する。

### 7.1 Unit tests

- 各パッケージ（transport / workspace / tools / config）の単体テスト
- DuckDB アクセスは可能な限り pure function に切り出して mockable に
- Podman 関連は interface 化せず `exec.Command` を直接使うため、テストでは PATH を差し替えて mock binary を使う

### 7.2 Integration tests

- `_test/integration/` に Podman 実機統合テスト
- 環境変数 `DATA_TOOLBOX_TEST_PODMAN=1` でのみ実行（CI スキップ可能）
- shell-agent-v2 の `internal/sandbox/integration_test.go` パターンを参考

### 7.3 自動 E2E テストハーネス（ダミー MCP クライアント）

- `_test/e2e/` に MCP クライアント役の Go テストコード
- バイナリビルド → spawn → JSON-RPC 経由でツール呼び出し → 戻り値検証
- mcp-guardian の `internal/proxy/proxy_test.go` の shell mock 構造を流用
- カバーするシナリオ:
  - workspace ライフサイクル全パス (init → load → query → execute → cleanup)
  - エラーパス (unsupported_language, path_not_allowed, workspace_id_invalid)
  - タイムアウト → コンテナ強制停止 → 次回 ensure で復旧
  - 並行アクセス時の後着拒否

## 8. Out of scope (Phase 1)

RFP §3 / Design Decisions「明示的にスコープ外」と整合:

- HTTP / SSE transport（ADR-0004）
- LLM 連携機能一切（思想として LLM 非依存を維持）
- shell-agent-v2 の 4-facility memory / System Rules / Global Memory
- 認証・認可（個人マシン stdio 前提。必要なら mcp-guardian を前段に）
- GUI / Web UI
- 非 Python ランタイム（R / Node / Bash; ADR-0003）
- workspace TTL ベース garbage collection（Phase 2）
- パッケージ追加インストールへの細粒度 ACL（`network` 設定が `none` でなければ LLM 生成コードから `pip install` 可能、それ以上の制御は Phase 1 では行わない — §6.3 参照）
- URL fetch (`https://`, `s3://` 等を `load_data` で受ける)（Phase 2 検討）

## See also

- ADR-0001: workspace_id によるスコープとライフサイクル
- ADR-0002: Podman 採用（抽象化なし）
- ADR-0003: Python のみのランタイム
- ADR-0004: stdio トランスポートのみ
- ADR-0005: ランタイムコンテナイメージはローカルビルド配布
- `_wip/data-toolbox-mcp/docs/ja/data-toolbox-mcp-rfp.ja.md` (RFP 全体)
- `_wip/data-toolbox-mcp/docs/ja/reference/phase1-plan.ja.md` (Phase 1 開発計画)
