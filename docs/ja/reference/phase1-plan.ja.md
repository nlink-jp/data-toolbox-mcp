# Phase 1 開発計画: data-toolbox-mcp

> Status: Draft (Phase 0)
> Date: 2026-06-05

Phase 0 で確定した ADR-0001〜0005 とアーキテクチャ全体設計 (`architecture.ja.md`) を前提に、Phase 1 (Core MVP) の作業をトラック単位で分解する。各トラックは可能な限り並列着手可能に設計している。

## 1. Goals (Phase 1 完成条件)

RFP §4 Phase 1 から転記。完成と判断するには以下すべて満たすこと:

- 3 ツール (`load_data` / `query_data` / `execute_code`) が動作
- Podman 環境で workspace_id ベースの状態管理が機能する
- `allowed_paths` パストラバーサル防御 + リソース制限 + コンテナ強制停止フォールバックが組み込まれている
- ダミー MCP クライアントによる自動 E2E テストハーネスが主要シナリオをカバー
- ユニットテスト + Podman 統合テスト（`DATA_TOOLBOX_TEST_PODMAN=1` で skip 可能）が pass
- Phase 0 ADR の Status を Accepted に更新済み

## 2. Work breakdown (Track 別)

### Track A: Repository scaffold + サブコマンド骨格

- nlink-jp CONVENTIONS.md 準拠のディレクトリ構成（ADR-0005 に従い単一バイナリ + サブコマンド）
  ```
  _wip/data-toolbox-mcp/
  ├── main.go
  ├── cmd/
  │   ├── root.go          # cobra root
  │   ├── serve.go         # MCP stdio サーバー（引数なしのデフォルト）
  │   ├── build_runtime.go # ランタイム image の build
  │   ├── doctor.go        # 環境診断
  │   └── version.go       # バージョン表示
  ├── runtime/             # go:embed 用 Dockerfile (+ 補助ファイル)
  │   └── Dockerfile
  ├── internal/...
  ├── Makefile             # make build → dist/、make runtime-image は build-runtime のラッパー
  ├── go.mod
  ├── config.example.toml
  ├── README.md
  ├── README.ja.md
  ├── CHANGELOG.md
  ├── AGENTS.md
  └── docs/                # Phase 0 で既に作成済
  ```
- Go module 初期化 (`go mod init github.com/nlink-jp/data-toolbox-mcp`)
- Makefile に `build / build-all / test / clean / runtime-image` ターゲット
- `.gitignore` （`dist/` のみ、バイナリ名パターンは禁止 — メモリ `feedback_gitignore_binary_pattern`）
- 4 サブコマンドはこの段階では「スタブ + bare wire-up」のみ（実装は他トラック）

**DoD**: `make build` が `dist/data-toolbox-mcp` を作る、`data-toolbox-mcp version` が動く、`data-toolbox-mcp --help` が 4 サブコマンドを表示、README.md と README.ja.md の雛形あり。

### Track B: MCP stdio framework

- `internal/transport/stdio.go` — `mcp-guardian/internal/transport/process.go` の `bufio.Scanner(1MB)` パターンを流用
- `internal/jsonrpc/` — JSON-RPC 2.0 型定義（`mcp-guardian/internal/jsonrpc/` 参考）
- `internal/mcpserver/` — MCP プロトコルレベルのハンドリング:
  - `initialize` / `initialized`
  - `tools/list`
  - `tools/call`
- メモリ `feedback_mcp_proxy_always_responds` に従い、すべてのリクエストに JSON-RPC レスポンスを返す

**DoD**: 空のツールセットで `initialize` → `tools/list` → `tools/call` をダミークライアントから呼んでエコーが返る。

### Track C: workspace + Podman lifecycle manager

- `internal/workspace/manager.go` — `WorkspaceManager` 構造体、`ensure(workspace_id)`, `release(workspace_id)`
- `internal/workspace/podman.go` — `podman run / exec / stop / rm / ps` の薄い wrapper（shell-agent-v2 `internal/sandbox/cli.go` 構造参考）
- workspace_id 検証 (`^[a-zA-Z0-9_-]{1,64}$`)
- disk state レイアウト (`<workspace_dir>/<ws>/{analysis.duckdb, work/}`) の作成・検出
- 孤児コンテナ検出（label `app=data-toolbox-mcp` での filter）
- リソース制限フラグの組み立て (`--cpus`, `--memory`, `--network=none`, `--label`)

**DoD**: `ensure("foo")` が冪等、disk と Podman 状態が in-memory と sync、Integration テスト (`_test/integration/workspace_test.go`) で round-trip が pass。

### Track D: 3 ツール実装

B + C 完了後に着手。

- `internal/tools/load_data.go`
  - `allowed_paths` ホワイトリスト + `filepath.EvalSymlinks` チェック
  - ホスト → `<workspace_dir>/<ws>/work/_upload/` コピー
  - `podman exec` で Python に DuckDB ロードを依頼
  - 戻り値: `{rows_loaded, schema}`
- `internal/tools/query_data.go`
  - SQL に LIMIT 自動付加 (default 1000)
  - `podman exec` で Python に SQL 実行を依頼
  - 戻り値: JSON 配列 + LIMIT 到達警告
- `internal/tools/execute_code.go`
  - `language == "python"` 検証
  - code を `<workspace_dir>/<ws>/work/_code/<uuid>.py` に書き、`podman exec python` で実行
  - timeout 超過時のコンテナ強制停止
  - 戻り値: `{stdout, stderr, exit_code}`
- ツール JSON Schema を `internal/mcpserver/tool_definitions.go` に集約

**DoD**: 3 ツール各々に unit test + integration test。タイムアウト時にコンテナが残らない検証あり。

### Track E: Runtime container image + build-runtime サブコマンド

ADR-0005 に従い、Dockerfile は `go:embed` でバイナリに同梱、build は `data-toolbox-mcp build-runtime` サブコマンド経由とする。

- `runtime/Dockerfile`（バージョン pin 含む、ADR-0003 / Q5-5 解決を反映）:
  ```dockerfile
  FROM python:3.13-slim
  RUN pip install --no-cache-dir \
        duckdb~=1.1 \
        pandas~=2.2 \
        polars~=1.8 \
        pyarrow~=18.0
  RUN useradd -m -u 1000 toolbox
  USER 1000:1000
  WORKDIR /work
  CMD ["sleep", "infinity"]
  ```
  バージョン pin は Phase 1 着手時点での安定マイナーで再確認・更新する。
- `runtime/` ディレクトリは `go:embed` の埋め込み元。Dockerfile 以外に必要な補助ファイル（例: pip 設定）があればここに置く
- `cmd/build_runtime.go` 実装:
  1. embed の Dockerfile を一時ディレクトリに展開
  2. `podman build -t localhost/data-toolbox-runtime:vX.Y.Z -t localhost/data-toolbox-runtime:latest <tempdir>` を `exec.Command` で実行
  3. 進捗を stdout に流す（podman の標準出力をそのまま）
  4. 完了後、tempdir を削除
- イメージタグ: `latest` + `vX.Y.Z`（バイナリのバージョンと一致させる）
- `make runtime-image` は `dist/data-toolbox-mcp build-runtime` を呼ぶラッパー（開発者向け）
- レジストリ push は実装しない（ADR-0005）

**DoD**:
- `dist/data-toolbox-mcp build-runtime` でローカルに image が作れる
- イメージサイズが 700MB 以下
- `podman run --rm localhost/data-toolbox-runtime:latest python -c "import duckdb; print(duckdb.__version__)"` が動く
- `data-toolbox-mcp doctor` がランタイム image の有無を判定できる（Track A + E 完了で組み合わせ）

### Track F: ダミー MCP クライアントテストハーネス

D 完了後に着手。

- `_test/e2e/harness.go` — Go テストから MCP サーバーバイナリを spawn し、JSON-RPC で対話する driver
  - mcp-guardian の `internal/proxy/proxy_test.go` shell mock 構造を参考に、より高度な対話を扱う
- `_test/e2e/scenarios/` — シナリオ単位のテストファイル
  - `lifecycle_test.go`: init → load → query → execute → cleanup
  - `errors_test.go`: unsupported_language, path_not_allowed, workspace_id_invalid
  - `timeout_test.go`: タイムアウト → コンテナ強制停止 → 復旧
  - `concurrency_test.go`: 並行アクセス時の後着拒否

**DoD**: 4 シナリオすべて pass。`go test ./_test/e2e/...` で `DATA_TOOLBOX_TEST_PODMAN=1` 時に動作。

## 3. Dependencies between tracks

```
A (scaffold) ──┬──▶ B (mcp framework) ──┐
               ├──▶ C (workspace mgr) ──┼──▶ D (3 tools) ──▶ F (e2e harness)
               └──▶ E (runtime image) ──┘
```

- A は最初の 1 ステップ。他すべての前提
- B / C / E は並列着手可能（A 完了後）
- D は B + C + E が「動作可能なレベル」まで揃ってから着手
- F は D が一通り動いてから着手

## 4. Definition of Done per track

各トラックの完成条件（既述）に加え、共通で:

- 日本語版 + 英語版ドキュメント更新（README, 関連 ADR, architecture）
- メモリ `feedback_commit_discipline` に従い、トラック完成時点でコミット（1 トラック = 1 PR 単位）
- メモリ `feedback_make_build` に従い、`go build` 直接禁止、`make build` のみ

## 5. Open questions — 解決済（2026-06-05）

Phase 0 文書化時点で残っていた未解決事項は以下のとおり方針確定:

### Q5-1. Podman macOS gvproxy 問題の影響範囲 — Resolved

**方針**: `network=none` をデフォルトとし、gvproxy を経由する経路を回避する。実機検証は Track C / F の実装中に行い、もし非 `network=none` 設定でも問題が発生するなら別途 ADR を起こす。

### Q5-2. large query 結果のストリーミング — Resolved

**方針**: default LIMIT を 1000 → **20000** に引き上げ、設定 `[query] default_row_limit` で変更可能とする。`query_data` は「言われた範囲を可能な限り素直に返す」のがベースライン姿勢で、巨大結果のストリーミングやファイル経由受け渡しはクライアント側のエージェント実装に委ねる。Phase 1 ではストリーミング API は実装しない。

### Q5-3. コンテナ image ホスティング先 — Resolved

**方針**: Registry push を行わない。`data-toolbox-mcp build-runtime` サブコマンド + `go:embed` Dockerfile によるローカル build 配布とする（**ADR-0005** を新規制定）。

### Q5-4. 追加パッケージインストール（`pip install`）の可否 — Resolved

**方針**: `network` 設定で自然に分岐させる。`network=none`（既定）ではコンテナ内 pip install は接続失敗で動かない。`network` を別値（例: `bridge`）にすれば LLM 生成コードから `pip install` 可能になるが、判断は config を変更するユーザーの責任。「pip だけ通す」のような細粒度 ACL は Phase 1 では実装しない。

### Q5-5. DuckDB バージョン pinning — Resolved

**方針**: Phase 1 開始時の安定マイナーで pin（`duckdb~=1.1` 形式）。`pandas`, `polars`, `pyarrow` も同様に pin（Track E の Dockerfile 参照）。pin 値は Phase 1 着手時の最新安定マイナーで再確認する。

## 6. Reference reuse map

既存 nlink-jp コードからの転用箇所:

| 用途 | 参照元 | 再利用方針 |
|------|-------|----------|
| DuckDB Python アクセスの設計参考 | `util-series/shell-agent-v2/app/internal/analysis/engine.go` | パターン参考（Python 内で同等処理を書く） |
| Podman CLI ラッパー設計 | `util-series/shell-agent-v2/app/internal/sandbox/cli.go` | 構造参考、`exec.Command` 直接呼びは流用 |
| /work マウント + per-session ディレクトリ構造 | shell-agent-v2 `internal/sessionio` | 直接参考（同じレイアウト） |
| stdio 1MB バッファ + JSON-RPC | `util-series/mcp-guardian/internal/transport/process.go` | 直接流用 |
| shell mock テストパターン | `util-series/mcp-guardian/internal/proxy/proxy_test.go` | 流用（より高度な対話に拡張） |
| BurntSushi/toml + env vars config | `util-series/data-agent/internal/config/config.go` | 直接コピー |
| Sandbox integration test 構造 | `util-series/shell-agent-v2/app/internal/sandbox/integration_test.go` | 流用 |

## 7. Estimated effort (粗見積もり)

トラック別の工数感覚（時間ではなく相対比較として）:

- Track A (scaffold): S
- Track B (mcp framework): M (mcp-guardian 流用で軽減)
- Track C (workspace + Podman): L (パストラバーサル防御・冪等性・孤児検出が地味に重い)
- Track D (3 tools): M (B + C ができていれば各ツールはアダプター化)
- Track E (runtime image): S
- Track F (e2e harness): M-L (シナリオ網羅性次第)

レビューチェックポイント: A 完了時、B+C+E 揃った時、D 完了時、F 完了時 = Phase 1 完成。

## See also

- ADR-0001: workspace_id によるスコープとライフサイクル
- ADR-0002: Podman 採用（抽象化なし）
- ADR-0003: Python のみのランタイム
- ADR-0004: stdio トランスポートのみ
- `architecture.ja.md`: アーキテクチャ全体設計
- `_wip/data-toolbox-mcp/docs/ja/data-toolbox-mcp-rfp.ja.md`: RFP 全体
