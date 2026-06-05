# data-toolbox-mcp

> DuckDB データ分析 + コンテナ化 Python 実行を単一バイナリの MCP サーバーとして提供。LLM クライアントは持ち込みで。

`data-toolbox-mcp` は任意の MCP クライアント（Claude Desktop, Cursor 等）が、workspace 単位の DuckDB にデータをロードし、SQL や Python を Podman サンドボックス内で実行できるようにする MCP サーバーです。公開するツールは 3 つ:

- `load_data(workspace_id, file_path, table_name)`
- `query_data(workspace_id, sql)`
- `execute_code(workspace_id, language, code)`

LLM プロバイダーには一切依存しません。stdio で素の MCP プロトコルを話すだけです。

[English README](README.md)

## なぜ存在するか

[shell-agent-v2](https://github.com/nlink-jp/util-series/tree/main/shell-agent-v2) は Wails GUI、LLM クライアント、DuckDB + Podman ツール層を 1 プロセスに同梱しています。同じツール層を **別の** LLM クライアントから使いたい場合、その同梱物に手を入れる必要がありました。`data-toolbox-mcp` はツール層だけを抽出し、再利用可能な MCP サーバーとして出荷します。プロトコルに従う任意のクライアントから使えます。

## 機能

- **3 ツール** で load → query → analyze のループをカバー
- **workspace_id スコープ**: 各 workspace がコンテナ 1 つと DuckDB ファイル 1 つを所有。サーバー再起動を跨いで永続。([ADR-0001](docs/ja/adr/0001-workspace-id-lifecycle.ja.md))
- **Podman サンドボックス**: 既定で `network=none`、CPU / memory / timeout 上限を config で調整可能。([ADR-0002](docs/ja/adr/0002-podman-engine-choice.ja.md))
- **Python ランタイム**（`duckdb`, `pandas`, `polars`, `pyarrow` 同梱）([ADR-0003](docs/ja/adr/0003-python-only-runtime.ja.md))
- **stdio トランスポートのみ** — ネットワーク非公開、認証不要。([ADR-0004](docs/ja/adr/0004-stdio-only-transport.ja.md))
- **レジストリ push なし** — ランタイム Dockerfile は `go:embed` でバイナリに同梱、初回利用時にローカル build。([ADR-0005](docs/ja/adr/0005-local-build-image-distribution.ja.md))
- **単一バイナリ・単一バージョン**: `serve` / `build-runtime` / `doctor` / `version` のサブコマンドはすべて 1 バイナリ
- **構造化ツールエラー**: すべてのツールエラーには LLM クライアントが分岐に使える安定した `code` が付く（`path_not_allowed`, `unsupported_language`, `script_failed`, ...）
- **多層パス防御**: `allowed_paths` は両側で `EvalSymlinks` を解決してから比較するため、シンボリックリンク jail-break を防ぐ

## 必要環境

- macOS または Linux
- [Podman](https://podman.io/)（rootless）。macOS では事前に `podman machine start` を実行
- ソースから build する場合 Go 1.23+

## クイックスタート

```sh
# 1. バイナリビルド（macOS で Developer ID 証明書がキーチェーンにあれば自動署名）
make build

# 2. ランタイムコンテナイメージ build（初回のみ、約 2 分）
dist/data-toolbox-mcp build-runtime

# 3. 環境診断
dist/data-toolbox-mcp doctor

# 4. MCP クライアントに登録（Claude Desktop の場合）
cat >> ~/Library/Application\ Support/Claude/claude_desktop_config.json <<'JSON'
{
  "mcpServers": {
    "data-toolbox": {
      "command": "/absolute/path/to/dist/data-toolbox-mcp",
      "args": ["serve", "--config", "/Users/you/.config/data-toolbox-mcp/config.toml"]
    }
  }
}
JSON
```

最小限の `config.toml`:

```toml
[workspace]
workspace_dir = "~/.data-toolbox"
allowed_paths = ["~/data", "~/Downloads"]

[container]
image        = "localhost/data-toolbox-runtime:latest"
stop_on_exit = true

[container.limits]
cpu             = "1.0"
memory          = "2GB"
timeout_seconds = 60
network         = "none"

[query]
default_row_limit = 20000
```

全スキーマは [`config.example.toml`](config.example.toml) を参照。Claude Desktop / Cursor の完全な設定手順 + トラブルシュートは [`docs/ja/reference/client-setup.ja.md`](docs/ja/reference/client-setup.ja.md)。

## サブコマンド

| コマンド | 役割 |
|---------|------|
| `serve`（既定） | MCP stdio サーバーを起動 |
| `build-runtime` | 同梱 Dockerfile を展開して `podman build` でランタイム image を作成 |
| `doctor` | Podman / podman machine (macOS) / ランタイム image / config を診断 |
| `version` | バージョン表示 |

## ツール

| ツール | 引数 | 戻り値 |
|------|------|------|
| `load_data` | `workspace_id`, `file_path`, `table_name` | `{rows_loaded, schema}` |
| `query_data` | `workspace_id`, `sql` | `{rows, row_count, limit_applied, limit_reached}` |
| `execute_code` | `workspace_id`, `language: "python"`, `code` | `{stdout, stderr, exit_code}` |

`load_data` は拡張子で reader を選択（`.csv` → `read_csv_auto`、`.json` / `.jsonl` → `read_json_auto`、`.parquet` → `read_parquet`）。`query_data` は SQL に `LIMIT` がない場合 `LIMIT [query] default_row_limit`（既定 20000）を自動付加。`execute_code` は `language="python"` のみ受け付け（ADR-0003）、ランタイムコンテナには `duckdb` / `pandas` / `polars` / `pyarrow` が同梱されています。

## セキュリティモデル（要点）

- `load_data` が読むファイルは `allowed_paths` で必ず制限。入力パスは絶対化 → `EvalSymlinks` 解決後、同じく解決済みの `allowed_paths` エントリと比較
- コンテナは既定で `network=none`。ネットワーク（およびコンテナ内 `pip install`）を有効にするには `[container.limits] network = "bridge"` を設定。**特定プロセスのみ許可するような細粒度 ACL は意図的に提供しません**
- コンテナは非 root ユーザー（ランタイム Dockerfile の UID 1000）で動作。rootless Podman ではホストユーザーが `--userns keep-id:uid=1000,gid=1000` でその UID にマップされる
- ツールごとの timeout は `context.WithTimeout` で強制。期限切れ時は `podman exec` の子プロセスを kill した上で MCP リクエストには応答を返す（ハングしない）
- ツールエラーは MCP content block 内の構造化 JSON として返却。LLM クライアントは `code` slug で分岐可能

詳細: [`docs/ja/reference/architecture.ja.md`](docs/ja/reference/architecture.ja.md) §6

## サンプルデータ

`samples/` に 3 つの小規模データセット（`sales.csv` 40 行、`products.json` 10 行、`logs.jsonl` 41 行）と、Stage 1-7 で順に load → SQL → JOIN → 窓関数 → 分位関数 → pandas → polars → workspace 分離 → セキュリティ境界を検証する `samples/README.md` を同梱しています。

## ドキュメント

- [`docs/ja/data-toolbox-mcp-rfp.ja.md`](docs/ja/data-toolbox-mcp-rfp.ja.md) — RFP
- [`docs/ja/reference/architecture.ja.md`](docs/ja/reference/architecture.ja.md) — アーキテクチャ
- [`docs/ja/reference/phase1-plan.ja.md`](docs/ja/reference/phase1-plan.ja.md) — Phase 1 開発計画
- [`docs/ja/reference/client-setup.ja.md`](docs/ja/reference/client-setup.ja.md) — Claude Desktop / Cursor 接続手順
- [`docs/ja/adr/`](docs/ja/adr/) — workspace_id, Podman, Python 限定, stdio, ローカル build 配布 の 5 件の ADR

## 謝辞

ツール表面と「workspace 単位の DuckDB + コンテナ」パターンは [shell-agent-v2](https://github.com/nlink-jp/util-series/tree/main/shell-agent-v2) から派生しています。`data-toolbox-mcp` はそのアイデアを抽出し、スタンドアロンの MCP サーバーとして再構成しました。

## ライセンス

[MIT](LICENSE)
