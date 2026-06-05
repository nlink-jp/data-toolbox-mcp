# ADR-0009: load_from_work — サンドボックス内 /work のデータを直接 DuckDB にロード

- Status: Accepted
- Date: 2026-06-06
- Driver: magi
- Generalises to: なし

---

## Context

Claude Desktop 実機検証 (2026-06-06) で明らかになった摩擦:

- `load_data` の `file_path` は **ホスト側絶対パス** で、`allowed_paths` ホワイトリスト内のみ許可される (ADR-0001 / architecture §6.1 のセキュリティモデル)
- ところが workspace の作業領域である `<workspace_dir>/<id>/work/` は **既定では `allowed_paths` に入っていない**
- そのため LLM が `execute_code` で生成したファイル (例: `polars.DataFrame.write_csv("/work/derived.csv")`) を、続けて `load_data` で別の table として読み込む、という典型ワークフローが成立しない
- 回避策として LLM は `execute_code` 内で `duckdb.read_csv_auto("/work/derived.csv")` を直接呼ぶことになるが、本来 `load_data` が担う「テーブル化」を Python コード経由で再実装することになり一貫性を欠く

つまり「データのロード」という同じ操作なのに、**ソースがホスト由来かサンドボックス由来か** で経路が分断されていた。

shell-agent-v2 のサンドボックス層 (`internal/sandbox/cli.go` 周辺) では同等のシナリオを「コンテナ内ファイルを直接 DuckDB にロードする」機能でカバーしており、その発想を継承する。

## Decision

新ツール **`load_from_work`** を追加する。

### `load_from_work`

- **引数**: `{workspace_id: string, file_path: string, table_name: string}`
  - `file_path` は **コンテナ内絶対パス**。`/work/` で始まる必要あり (例: `"/work/derived.csv"`, `"/work/subdir/data.parquet"`)。
  - `table_name` は `load_data` と同じく SQL 識別子パターン `^[a-zA-Z_][a-zA-Z0-9_]*$`
- **戻り値**: `load_data` と同形式 — `{rows_loaded: int, schema: [{name, type}, ...]}`
- **動作**:
  1. `workspace.ValidateID` で `workspace_id` 構文検証
  2. `file_path` が `/work/` で始まることを検証 (それ以外は `invalid_arguments` 構造化エラー)
  3. `file_path` から先頭の `/work` を剥がして `<host_work_dir>` への subpath として解決
  4. `filepath.Clean` + prefix check で `<host_work_dir>` の subtree 内であることを再検証 (path traversal 二重防御)
  5. workspace を `Ensure`
  6. 拡張子から reader を選択 (load_data と同じテーブル):
     - `.csv` / `.tsv` → `read_csv_auto`
     - `.json` / `.jsonl` / `.ndjson` → `read_json_auto`
     - `.parquet` → `read_parquet`
     - その他 → `read_csv_auto` をデフォルト試行
  7. Python script を `execScript` で実行: `CREATE OR REPLACE TABLE "<table>" AS SELECT * FROM read_xxx_auto('<container_path>')` + `SELECT COUNT(*)` + `DESCRIBE`
  8. JSON 出力をパースして戻す

### `load_data` との関係

- **責務分離**:
  - `load_data` — host fs → workspace: ホスト側ファイルをサンドボックスに取り込んで table 化 (allowed_paths 検査あり)
  - `load_from_work` — workspace 内 → table: 既にサンドボックスにあるファイルを table 化 (allowed_paths 不要、`/work` 配下のみ)
- API シグネチャをほぼ同形にすることで、LLM が「ソースがどこかで使い分ける」だけになり認知負荷が低い
- `file_path` のセマンティクスだけが違う (ホスト絶対パス vs `/work/...`)

### セキュリティ含意

- `load_from_work` は `allowed_paths` を経由しない、なぜなら対象が **サンドボックスの中** だから
- ただし `<host_work_dir>` の外に脱出する path traversal は二重防御で防ぐ:
  - `/work/` prefix 必須化
  - 解決後パスの prefix check
- `/work/_code/<uuid>.py` のような内部生成ファイルも読めるが、これは「ユーザーが書いたコードを読む」だけなので問題なし

## Consequences

**Positive:**

- LLM が「`execute_code` で `polars.write_csv` → `load_from_work` で table 化」という自然な分業を組める
- `load_data` (host source) と `load_from_work` (sandbox source) の対称性で、データ起源を意識した API として読みやすい
- `allowed_paths` の設定が不要なため、新規 workspace で「とりあえず生成して table 化」が動く (ユーザーの設定作業ゼロ)
- 既存 `load_data` の挙動・セキュリティモデルは無変更 (後方互換)

**Negative:**

- ツール表面が 7 → 8 に増加 (ADR-0008 と合わせて 6 → 8)
- 内部の Python script 生成ロジックが `load_data` と `load_from_work` で重複 (DRY 違反)。実装では reader 選択と script 組み立てを共通関数に切り出して対処
- 「`/work` から読む」と「ホストから読む」の使い分けを LLM が誤ると、`allowed_paths` 設定ミスを `load_from_work` で迂回されるリスク。ただし `load_from_work` の対象は必ず workspace 内ファイルなので、外部リーク経路にはならない

## Alternatives Considered

### A1: `load_data` に `source: "host" | "work"` 引数を追加

- 実装案: 既存ツールを拡張
- Pros: ツール数 6 のまま、API 表面が小さい
- Cons: 1 ツールに 2 つの責務 (許可ロジック + パス解釈) が同居、inputSchema が条件分岐 (`source=host` なら allowed_paths 検査 / `source=work` なら `/work` prefix 検査) で読みづらい、LLM がデフォルト挙動を間違えやすい
- 却下理由: 責務分離のほうが ADR-driven の整合性が高い

### A2: `load_data` の `file_path` が `/work/...` なら自動でサンドボックス解釈

- Pros: API シグネチャ無変更
- Cons: ADR-0003 の「Host absolute path」契約と表記が衝突、ホスト側 `/work` ディレクトリ (絶対パス) との混同リスク、ドキュメント・スキーマで「`/work` だけ特別」を説明する必要
- 却下理由: 明示的な分割のほうが運用・テスト・教示が単純

### A3: `<workspace_dir>/*/work` を起動時に自動で `allowed_paths` に追加

- Pros: 既存 `load_data` がそのまま使える
- Cons: `allowed_paths` の意味が「ユーザー明示宣言」から「動的に拡張される」に変わり ADR-0001 のセキュリティモデルが揺らぐ、テストの drift 検出も難しくなる
- 却下理由: セキュリティ前提の変更は ADR レベルの議論を要する、現時点では別経路 (新ツール) のほうが安全

### A4: 既存ツール群で対応せず、execute_code 内で `duckdb.read_csv_auto` するワークフローを継続

- Pros: 何もしない (実装ゼロ)
- Cons: 一貫性の悪さは残る、LLM が「テーブル化」と「DataFrame 化」を都度混同する
- 却下理由: 体感効率 (LLM 自身の評価で優先度高) を犠牲にする

## See also

- ADR-0001: workspace_id によるスコープとライフサイクル (`<workspace_dir>/<id>/work/` 構造)
- ADR-0003: load_data の host-path 契約
- ADR-0006 (v0.2.1 amendment): `host_work_dir` と artifact exchange convention
- ADR-0008: 対をなす「サンドボックス内 artifact を MCP コンテンツとして返す」(`attach_files`)
