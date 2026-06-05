# RFP: data-toolbox-mcp

> Generated: 2026-06-05
> Status: Draft
> Author: magi
> Phase: Planning (Phase 1 of CONVENTIONS.md)

## 1. Problem Statement

shell-agent-v2 が内蔵していた DuckDB データ分析エンジンとコンテナ化コード実行環境を LLM 非依存の MCP サーバーとして抽出し、Claude Desktop / Cursor 等の MCP クライアントから stdio 経由で再利用可能にするツール。DuckDB を内蔵したコンテナを実行基盤として共有し、`load_data` / `query_data` / `execute_code` の 3 ツールを公開する。対象ユーザーは、MCP クライアント上で自分専用エージェントを組みたいが、データ分析サンドボックス（DuckDB + Python ランタイム + ファイル授受）を都度構築するコストを払いたくない個人開発者・データ分析者。

## 2. Functional Specification

### Commands / API Surface

MCP ツールとして以下 3 つを公開する:

| ツール | 引数 | 戻り値 |
|--------|------|--------|
| `load_data` | `workspace_id: str`, `file_path: str`, `table_name: str` | `{rows_loaded: int, schema: object}` |
| `query_data` | `workspace_id: str`, `sql: str` | JSON 配列 `[{col: val, ...}, ...]` + LIMIT 警告（default LIMIT 20000、`[query] default_row_limit` で変更可）|
| `execute_code` | `workspace_id: str`, `language: "python"`, `code: str` | `{stdout: str, stderr: str, exit_code: int}` |

`workspace_id` は LLM/クライアントが明示指定する文字列キー。同じ `workspace_id` を指定する限り、コンテナと DuckDB ファイルが共有される。

### Input / Output

- **Transport**: MCP stdio（JSON-RPC over stdio）
- **load_data の file_path**: ホスト絶対パス。`allowed_paths` 設定のホワイトリスト配下のみ許可。MCP サーバーがホストファイルを読み込み、コンテナ内の作業領域に供給
- **query_data の出力**: JSON 配列形式 `[{col: val, ...}, ...]`。default LIMIT 20000 を自動付加し、超過時は明示的に警告（`[query] default_row_limit` で変更可）。MCP サーバーとしては「言われた範囲を可能な限り素直に返す」のがベースライン姿勢で、巨大結果のストリーミングやファイル経由受け渡しはクライアント側のエージェント実装に委ねる
- **execute_code の出力**: stdout / stderr / exit_code を返却
- **コンテナ → ホストの生成物**: コンテナ内 `/work` ボリュームをホスト側 `workspace_dir/<workspace_id>/work/` にマウントすることで自動同期。LLM が `/work/foo.png` に書けばホスト側で読める

### Configuration

`config.toml` セクション分割（nlink-jp 慣習に従う）+ 環境変数で上書き可。

```toml
[server]
log_level = "info"
log_file = "~/.data-toolbox/logs/server.log"

[workspace]
workspace_dir = "~/.data-toolbox"  # 変更可
allowed_paths = ["~/data", "~/Downloads"]

[container]
image = "localhost/data-toolbox-runtime:latest"  # ADR-0005: ローカル build

[container.limits]
cpu = "1.0"
memory = "2GB"
timeout_seconds = 60
network = "none"

[query]
default_row_limit = 20000  # query_data の自動 LIMIT デフォルト
```

### External Dependencies

**ランタイム時（MCP サーバー実行時）**:

- 外部 API / LLM / クラウド一切なし、すべてローカル
- Podman ソケットへのアクセス（rootless 前提）
- ホストファイルシステム読み書き（allowed_paths と workspace_dir のみ）

**ビルド時（コンテナイメージ生成時、エンドユーザーマシンで実行）**:

- Container registry (Docker Hub) からの base image (python:3.13-slim) pull
- PyPI からの duckdb / pandas / polars / pyarrow 取得
- OS パッケージマネージャー (apt) からの base OS パッケージ取得

ADR-0005 により、本プロジェクトは registry push を行わない。エンドユーザーが `make runtime-image` で各自 build する方式（ローカルビルド配布）。初回 build にはネットワーク + 数分の待ち時間が必要。

## 3. Design Decisions

**実装言語: Go**

- mcp-guardian / nlk と同じ路線。外部依存ゼロを目指す（Podman は `exec.Command` で駆動、MCP SDK は最小限の依存のみ）
- シングルバイナリ配布、cross-compile 容易
- 当初 Rust を候補として検討したが、nlink-jp 内に Rust 実績がなく、保守コスト・CONVENTIONS.md テンプレート未整備の懸念から Go に変更

**コンテナエンジン: Podman 前提**

- rootless、デーモン不要（systemd / Docker daemon 不要）
- macOS では `podman machine` 起動が前提（後述 External Constraints 参照）

**ランタイム: Python のみ**

- `duckdb / pandas / polars / pyarrow` を同梱
- データ分析用途に絞り、Bash / Node / R は明示的にスコープ外

**ライフサイクル: 明示的な workspace_id でスコープ**

- shell-agent-v2 の per-session DuckDB モデルとは異なる方向
- LLM/クライアントが状態管理を明示的に行うことで、複数クライアント共存と予測可能性を両立

**既存 nlink-jp ツールとの関係**:

- **shell-agent-v2**: 将来的に `data-toolbox-mcp` を参照する形にリファクタ可能（ただし本プロジェクトの責務外、shell-agent-v2 側の判断）
- **mcp-guardian**: ガバナンスプロキシとして前段に配置可能（権限・監査ログ）
- **mcp-skeleton**: stdio MCP の参考実装として流用
- **cclaude**: コンテナ運用 Tips を参考にする

**配布形態: 単一バイナリ + サブコマンド構成**

メモリ `feedback_single_binary_subcommand`（shell-agent-v2 で実証済み）に従い、`data-toolbox-mcp` を 1 つの Go バイナリとして配布し、サブコマンドで機能を切り分ける:

- `data-toolbox-mcp serve` — MCP stdio サーバー（メインモード、引数なしでも同じ）
- `data-toolbox-mcp build-runtime` — Podman 経由でランタイムコンテナイメージを build（ADR-0005 の「ローカル build 配布」を実現する手段）
- `data-toolbox-mcp doctor` — 環境診断（Podman 状態、ランタイム image の有無、config 検証）
- `data-toolbox-mcp version` — バージョン表示

ランタイム用 Dockerfile はバイナリに `go:embed` で同梱し、ユーザーが Dockerfile を別ファイルとして管理する手間を排除する。これにより、MCP 本体とビルドツールとセットアップ診断ツールが「1 バイナリ・1 バージョン」で揃い、整合性管理が楽になる。

**明示的にスコープ外**:

- HTTP / SSE transport（Phase 1 は stdio のみ）
- LLM 連携機能一切（思想として LLM 非依存を維持）
- shell-agent-v2 の 4-facility memory / System Rules / Global Memory
- 認証・認可（個人マシン stdio 前提。必要なら mcp-guardian を前段に）
- GUI / Web UI
- 非 Python ランタイム（R / Node / Bash）

## 4. Development Plan

### Phase 0: 設計文書化（実装着手前）

「ADR before impl」の原則に従い、実装前に以下を完成させレビューを受ける:

- **ADR-0001**: workspace_id モデルとライフサイクル
- **ADR-0002**: Podman 選定（コンテナエンジン抽象化を Phase 1 では作らない理由）
- **ADR-0003**: Python 限定ランタイムの選定
- **ADR-0004**: stdio 限定（HTTP/SSE をスコープ外とする理由）
- **アーキテクチャ全体設計書**: `docs/{ja,en}/reference/architecture.md`（プロセス境界・データフロー・状態遷移）
- **開発計画書**: Phase 1-3 の TODO ブレークダウンと各 Phase の完成条件

### Phase 1: Core MVP（Security & Testability as Day-1）

- Podman ラッパー（起動・停止・exec、終了ステータス可視化）
- workspace_id ベースの状態管理（コンテナ ID + DuckDB ファイルパスの永続化）
- MCP stdio サーバー骨格（mcp-skeleton 参考）
- `load_data` / `query_data` / `execute_code` の 3 ツール実装
- config.toml + env vars 読み込み
- ランタイムコンテナ Dockerfile（python + duckdb + pandas + polars + pyarrow）
- **セキュリティを実装と同時に組み込む**:
  - `allowed_paths` のパストラバーサル / シンボリックリンク防御
  - リソース制限（CPU / memory / timeout、network=none default）
  - コンテナ強制停止のフォールバック
  - エラー時に確実にコンテナがリークしないライフサイクル管理
- **ダミー MCP クライアントによる自動 E2E テストハーネス**:
  - JSON-RPC over stdio のメッセージシーケンス検証
  - workspace_id ライフサイクル全パス（init → load → query → execute → cleanup）
  - エラーパス・タイムアウト・コンテナ強制停止の網羅
- ユニットテスト + Podman 統合テスト

**Phase 1 完成条件**: 全 ADR レビュー済 + テストハーネスで主要シナリオ通過 + Podman 環境で動作確認

### Phase 2: UX 改善・実機補完

- `query_data` の LIMIT 自動付加 + 大結果サイズ警告
- 構造化エラーメッセージ（LLM が原因を判別しやすい形）
- ログローテーション
- Claude Desktop / Cursor での最終実機 E2E（自動 E2E でカバーしきれない LLM 駆動シナリオの確認）

### Phase 3: Release

- README.md / README.ja.md / CHANGELOG.md / AGENTS.md
- リリースパイプライン（make build / build-all、cross-compile）
- アンブレラ submodule 統合（util-series）
- nlink-jp/.github プロファイル更新（catalog sync メモリに従う）

**Phase の独立レビュー可否**:

- Phase 0 は文書のみ → 単独で完結レビュー可能
- Phase 1 は MVP として独立で動作確認可能
- Phase 2 は Phase 1 完了が前提
- Phase 3 はリリースのため Phase 1 + 2 完了が前提

## 5. Required API Scopes / Permissions

### ランタイム時（MCP サーバー実行時）

**外部 API: None**（LLM / クラウド一切なし）

**ローカル権限要件**:

- ホストファイルシステム読み込み権限（`allowed_paths` 内）
- Podman ソケットアクセス（rootless ユーザー権限）
- `workspace_dir` 書き込み権限

### ビルド時（コンテナイメージ生成時）

**ネットワーク依存**:

- Container registry: base image (python) の pull
- PyPI: duckdb / pandas / polars / pyarrow の取得
- OS package mirror (apt / apk): base OS パッケージ取得

ビルドはプロジェクトのリリースパイプラインで実行し、エンドユーザーには事前ビルド済イメージを配布する。エンドユーザーは `podman pull` 権限のみ必要。

## 6. Series Placement

**Series: util-series**

**理由**:

- shell-agent-v2 が既に util-series にあり、本プロジェクトはその LLM 非依存部分の派生物のため、同じシリーズに配置するのが自然
- util-series の本質は「パイプフレンドリーなデータ変換 CLI」だが、MCP サーバーも「ツール提供の発想」として近く、整合する
- lab-series は実験性が強い段階向けだが、本プロジェクトは shell-agent-v2 という先行実証があるため初期から util-series で扱える

## 7. External Platform Constraints

### A. MCP プロトコル制約

- MCP 仕様バージョン依存（現時点 2024-11-05 ベース）。本プロトコルにはキャンセル通知が存在しないため、長時間実行のキャンセルは「子プロセス kill」を採用（メモリの `MCP no protocol cancel` / `child process exit status` 教訓に従う）
- stdio transport のメッセージサイズに実質的な上限あり。大きな query_data 結果は LIMIT で抑える設計
- ツールパラメータの JSON Schema 表現力に制約あり（複雑なユニオン型は避ける）
- プロキシ / 中継として動作する場合、リクエストを未応答で放置しない（`MCP proxy always responds` メモリに従う。本プロジェクトは中継ではないが、コンテナ実行失敗時も必ず JSON-RPC エラーを返す）

### B. Podman 制約（macOS 特有）

- `podman machine` の起動が前提
- 既知問題: virtiofs ソケット不可、gvproxy ポート保持、VM OOM、sshd ENV、`sed -i` 互換性（メモリ `Podman Machine on macOS` 参照）
- ユーザーは事前に Podman をセットアップしている必要あり。README に手順記載

### C. DuckDB 制約

- メモリ駆動。大規模データ時のディスクスピル設定（`memory_limit` PRAGMA）が必要
- 単一プロセスのみが同一 DuckDB ファイルに書き込み可能。これは workspace_id モデル設計と整合（1 workspace = 1 コンテナ = 1 DuckDB writer）
- 異なる workspace_id 間でデータ共有したい場合は parquet/csv export → load_data 経由で行う

### D. クライアント側設定パス

- Claude Desktop: `claude_desktop_config.json` の `mcpServers` 配下
- Cursor: `.cursor/mcp.json` または settings 経由
- 本プロジェクトとしてはバイナリパスと config.toml パスを案内するだけで、クライアント側設定の自動化はスコープ外

---

## Discussion Log

### 命名

- 候補: `agent-tools-mcp`, `shell-agent-tools`, `data-sandbox-mcp`, `duckbox-mcp`, ユーザー指定 `data-toolbox-mcp`
- 採用: `data-toolbox-mcp`（中立的・拡張余地あり）

### 利用シーンの絞り込み

- 「Claude Desktop / Cursor 等の MCP クライアントから stdio で使う、個人マシン中心」に絞り込み
- これにより HTTP/SSE transport や team hosting を Phase 1 スコープ外と整理

### ツール表面の設計

- 当初構想: 「DuckDB を内蔵したコンテナとして構築し、コンテナ内コードが DB を直接駆動」
- 検討した代替案:
  1. `execute_code` 中心のミニマリスト表面（すべてコード経由）
  2. `load_data / query_data / execute_code` の 3 ツール（採用）
  3. `setup_workspace / execute_code` の 2 ツール
- 採用理由: LLM にとって取り回しやすい中庸の表面（実装は内部でコンテナとランタイムを共有）

### ライフサイクルモデル

- 検討した代替案:
  1. 1 MCP サーバー = 1 コンテナ = 1 DuckDB（生存中永続）
  2. MCP セッションごとに生成・破棄（shell-agent-v2 路線）
  3. **明示的 workspace_id でスコープ**（採用）
  4. ホストに DuckDB 永続、コンテナはエフェメラル
- 採用理由: 複数クライアント共存と予測可能性を両立。LLM に状態管理の明示性を要求する代わりに、API の単純性と耐久性を得る

### コンテナエンジン選定

- 検討した代替案:
  1. Docker
  2. **Podman**（採用）
  3. 複数エンジン抽象化
  4. subprocess + chroot/jail（コンテナを使わない）
- 採用理由: rootless、デーモン不要、セキュリティ境界が強い

### 実装言語選定

- 初期選択: Rust（高性能・安全性）
- 懸念提示: nlink-jp に Rust 実績なし、CONVENTIONS.md / Makefile テンプレ未整備、保守コスト
- 最終決定: **Go**（mcp-guardian と同じ路線、シングルバイナリ、外部依存軽量）

### Development Plan の方針

- 「ADR / アーキテクチャ全体設計を文書化してから実装に着手」する Phase 0 を明示化
- セキュリティを後工程の "Hardening" にすると "security at implementation time" 原則に反するため、Phase 1 に組み込み
- **ダミー MCP クライアントによる自動 E2E テストハーネス** を Phase 1 に含めることで、LLM 実機テストに依存しないテスタビリティを確保

### API Scopes の区別

- 当初「外部 API なし」と整理したが、ユーザー指摘により **ランタイム時 vs ビルド時** で外部依存が異なることを明示化
- ランタイム: なし / ビルド時: registry + PyPI + OS package mirror

### 設定項目

- `allowed_paths`、`container_image`、`resource_limits`、`log_level/file` を採用
- `workspace_dir` は当初固定の予定だったが、ユーザー指摘により変更可能とする

### Phase 0 Open Questions の解決 (2026-06-05)

Phase 0 文書化で残った Open Questions が以下のとおり解決:

- **Q5-1 (Podman macOS gvproxy)**: `network=none` をデフォルトとし、gvproxy 経路を回避する。実機検証は Phase 1 で実施
- **Q5-2 (large query streaming)**: default LIMIT を 1000 → **20000** に引き上げ、設定 `[query] default_row_limit` で変更可能とする。「MCP サーバーは言われた範囲で素直に返す」のがベースライン姿勢で、巨大結果のストリーミングやファイル経由受け渡しはクライアント側エージェント実装の責務
- **Q5-3 (container image hosting)**: ghcr / Docker Hub への push を行わず、ローカル build 配布とする。**ADR-0005 を新規制定**
- **Q5-4 (pip install in container)**: `network` 設定で自然に分岐させる方針。`network=allow` であればコンテナ内 `pip install` は LLM が自由に実行可能、`network=none` ではそもそも pip 接続が失敗する。特定プロセスのみ許可するような複雑な ACL は Phase 1 では実装しない
- **Q5-5 (DuckDB version pinning)**: Phase 1 開始時の安定マイナーで pin（`duckdb~=1.1` 形式）。pandas / polars / pyarrow も同様に pin する
