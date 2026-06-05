# MCP クライアント接続手順

> Phase 2 で追加。Claude Desktop / Cursor 等の主要 MCP クライアントから
> `data-toolbox-mcp` を呼び出す手順。

## 前提条件

以下を済ませてから設定してください:

1. `make build` でバイナリ作成 → `dist/data-toolbox-mcp`
2. `dist/data-toolbox-mcp build-runtime` でランタイムイメージ作成 → `localhost/data-toolbox-runtime:latest`
3. `dist/data-toolbox-mcp doctor` ですべて `[OK]` 表示
4. macOS の場合: `podman machine start` 済み

`doctor` が `[FAIL]` を返す場合、表示されたヒントに従って解消してください。

## 設定方法（共通）

MCP クライアントに以下の 3 つを伝える必要があります:

- バイナリへの**絶対パス**（`PATH` 解決に依存しないため）
- サブコマンド `serve`（省略可。引数なしでも同じ動作）
- 必要に応じて `--config /path/to/config.toml`

config.toml の例は `config.example.toml` を参照してください。配置場所は次のいずれかが標準:

- `~/.config/data-toolbox-mcp/config.toml`（推奨）
- カレントディレクトリの `./config.toml`
- 明示指定: `--config /path/to/config.toml`

設定ファイル無しでも起動できますが、`allowed_paths` が空のため `load_data` がすべて拒否されます。データ分析シナリオを使うには必ず設定してください。

## Claude Desktop

### 設定ファイル

macOS:

```text
~/Library/Application Support/Claude/claude_desktop_config.json
```

### 設定例

```json
{
  "mcpServers": {
    "data-toolbox": {
      "command": "/Users/you/path/to/data-toolbox-mcp/dist/data-toolbox-mcp",
      "args": ["serve"]
    }
  }
}
```

`config.toml` を明示指定したい場合:

```json
{
  "mcpServers": {
    "data-toolbox": {
      "command": "/Users/you/path/to/data-toolbox-mcp/dist/data-toolbox-mcp",
      "args": ["serve", "--config", "/Users/you/.config/data-toolbox-mcp/config.toml"]
    }
  }
}
```

設定後、Claude Desktop を再起動して MCP サーバーを認識させます。

### 動作確認

Claude に「`data-toolbox` の使えるツールを教えて」と聞くと、`load_data` / `query_data` / `execute_code` の 3 つが提示されます。

## Cursor

### 設定ファイル

`~/.cursor/mcp.json`（プロジェクト固有なら `<project>/.cursor/mcp.json`）。

### 設定例

```json
{
  "mcpServers": {
    "data-toolbox": {
      "command": "/Users/you/path/to/data-toolbox-mcp/dist/data-toolbox-mcp",
      "args": ["serve"]
    }
  }
}
```

Cursor を再起動 → MCP サーバーが認識されます。

## トラブルシュート

### `doctor` で podman が見つからない

PATH 解決の問題か Podman 未インストール。`which podman` を確認。`brew install podman` でインストール後、`podman machine init && podman machine start`。

### macOS で `podman machine: no running machine`

```sh
podman machine start
```

`podman-machine-default* CURRENT_UP` の表示を確認したら `doctor` を再実行。

### `runtime image: NOT present`

```sh
dist/data-toolbox-mcp build-runtime
```

初回は 1–2 分。`pip install` でネットワークが必要。完了後 `doctor` で確認。

### Claude Desktop が MCP サーバーを認識しない

- `command` フィールドが**絶対パス**になっているか確認（相対パスや `~` は使えない）
- `claude_desktop_config.json` の JSON が valid か（末尾カンマ等）
- Claude Desktop の `~/Library/Logs/Claude/mcp-server-data-toolbox.log` を見るとサーバー起動時の stderr が読めます

### `load_data` がすべて `path_not_allowed` で失敗する

`config.toml` の `[workspace] allowed_paths` が空、もしくは `--config` が渡されていない。`dist/data-toolbox-mcp doctor` で実際に使われる設定パスを確認。

### `execute_code` で `pip install` したい

`config.toml` の `[container.limits] network` を `none` から `bridge` に変更すると、コンテナから外部接続できるようになります（Phase 0 Q5-4 解決）。LLM が生成するすべてのコードが外部接続できる点に注意。

### コンテナがリークしている

```sh
podman ps -a --filter label=app=data-toolbox-mcp
```

で確認、`podman rm -f <container>` で削除。`config.toml` の `[container] stop_on_exit = true`（既定）なら正常終了時に自動掃除されますが、SIGKILL ではリークします。

## 関連ドキュメント

- [config.example.toml](../../../config.example.toml) — 設定ファイルのテンプレート
- [architecture.ja.md](architecture.ja.md) — 全体設計
- [ADR-0001](../adr/0001-workspace-id-lifecycle.ja.md) — workspace_id モデル
- [ADR-0005](../adr/0005-local-build-image-distribution.ja.md) — ローカルビルド配布
