# Sample data for end-to-end verification

3 つの形式（CSV / JSON / JSONL）を 1 セットで提供し、`load_data` の各 reader と `query_data` / `execute_code` の組み合わせを一通り試せるようにしている。架空のデータで PII は含まない。

| File | Rows | Schema |
|------|------|--------|
| `sales.csv` | 40 | order_id, order_date, region, product_id, qty, unit_price |
| `products.json` | 10 | product_id, name, category, cost |
| `logs.jsonl` | 40 | timestamp, level, service, request_id, duration_ms, path |

## このディレクトリを allowed_paths に追加する

`~/.config/data-toolbox-mcp/config.toml` の `[workspace] allowed_paths` に絶対パスを足してください:

```toml
allowed_paths = [
    "/Users/magi/works/nlink-jp/_wip/data-toolbox-mcp/samples",
    "~/Downloads",
]
```

Claude Desktop を再起動して設定を反映。

## 試したいプロンプト例

### Stage 1: load + 基本クエリ

> data-toolbox を使って `/Users/.../samples/sales.csv` を `sales` テーブルに、`products.json` を `products` テーブルにロードして、各テーブルの行数とスキーマを教えて。workspace_id は `samples` でいい。

期待動作: `load_data` を 2 回呼び、`{rows_loaded: 40, schema: [...]}` と `{rows_loaded: 10, ...}` が返る。

### Stage 2: JOIN + 集計

> 地域別の売上合計（qty × unit_price の総和）を多い順に教えて。

期待動作: `query_data` で `SELECT region, SUM(qty*unit_price) FROM sales GROUP BY region ORDER BY 2 DESC`。

> カテゴリ別の利益合計を出して。利益は (unit_price - cost) × qty。products と sales を JOIN する必要あり。

期待動作: `query_data` で JOIN + GROUP BY。

### Stage 3: 時系列・ウィンドウ関数

> 月別の売上合計を出して、前月比成長率も付けて。

期待動作: `query_data` で `DATE_TRUNC` + `LAG` ウィンドウ関数。

### Stage 4: JSONL ロード + ログ分析

> `logs.jsonl` を `logs` テーブルにロードして、サービスごと・レベルごとのリクエスト数と平均 duration_ms を出して。

期待動作: `load_data` で JSONL ロード → `query_data` で集計。

> エラー応答 (level=ERROR) の path 別件数を、レイテンシ p95 と共に出して。

期待動作: `query_data` で `PERCENTILE_CONT` または `QUANTILE`。

### Stage 5: execute_code で pandas/polars 分析

> sales テーブルを pandas DataFrame として読み込んで、product_id ごとの売上を計算し、上位 3 つの product 名を products テーブルから join して取得して。

期待動作: `execute_code` で Python（duckdb → pandas → df.merge → df.nlargest）。

> sales を polars で読み込んで、order_date を週単位に丸めて週次売上を出して。

期待動作: `execute_code` で polars (group_by_dynamic 等)。

### Stage 6: workspace 分離の確認

> workspace_id を `samples` から `analysis-2` に変えて、`SELECT * FROM sales` を実行して。

期待動作: 新 workspace では table が存在せず、構造化エラー (code: script_failed, message: catalog "sales" does not exist) が返る。

### Stage 7: エラーハンドリングの確認

> `/etc/passwd` を読み込もうとして。

期待動作: `path_not_allowed` 構造化エラー、`{"code":"path_not_allowed", ...}`。

> 言語を bash にして `echo hi` を実行して。

期待動作: `unsupported_language` 構造化エラー、`{"code":"unsupported_language", ...}`。

## サンプルデータの解析でわかること

- region は Tokyo / Osaka / Nagoya / Sapporo / Fukuoka の 5 か所
- カテゴリは Electronics / Books / Apparel / Accessories の 4 種
- 期間は 2026-03 から 2026-06 まで約 3 ヶ月
- logs は約 90 秒間、INFO 多数 + WARN/ERROR が混在、`/orders/checkout` の ERROR が遅延傾向
