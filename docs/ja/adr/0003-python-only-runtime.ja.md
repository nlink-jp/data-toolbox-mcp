# ADR-0003: コンテナ内ランタイムを Python のみに限定する

- Status: Accepted
- Date: 2026-06-05
- Driver: magi
- Generalises to: なし

---

## Context

`execute_code(workspace_id, language, code)` の `language` パラメータには Python / Bash / Node / R など複数言語を受ける選択肢がある。多言語サポートは柔軟性を提供する一方で、以下のコストが発生する:

- コンテナイメージのサイズ膨張（Node + Python + R で 1.5GB を超える可能性）
- 各言語の依存パッケージのバージョン管理・脆弱性追跡
- セキュリティサーフェスの拡大（言語ランタイムごとにエスケープ経路が増える）
- ドキュメントとテストの量が言語数に比例

RFP §1 で「データ分析サンドボックス」を主要シナリオとして確定しており、データ分析用途では Python が事実上のデファクトである。DuckDB を内蔵する意図とも合致する（`duckdb-python` の API が成熟している）。

## Decision

Phase 1 ではコンテナランタイムを **Python 3 のみ** に限定する:

- ベースイメージは `python:3.13-slim` (安定版) を採用
- 同梱パッケージ: `duckdb` / `pandas` / `polars` / `pyarrow`（データ分析の標準スタック）
- `execute_code` の `language` パラメータは将来拡張のためのプレースホルダーとして残すが、Phase 1 では `"python"` 以外を渡されたら `unsupported_language` エラーで明示拒否
- 追加パッケージのインストールは Phase 1 では不可（コンテナ起動後の `pip install` を許容するか否かは未決定 → Open Question として Phase 1 計画に記載）

将来 R / Node を追加する要求が出てきた場合は、「同じコンテナにマルチランタイムを詰める」のか「言語ごとに別コンテナイメージを切り替える」のかを別 ADR で検討する。

## Consequences

**Positive:**

- コンテナイメージサイズが約 500MB（slim ベース + 主要パッケージ）に収まる見込み。pull コストが許容範囲
- セキュリティサーフェスが Python ランタイムに限定され、脆弱性監視（pip-audit など）が単純化
- `language` のディスパッチ実装が不要、コードがシンプルに
- DuckDB → pandas → polars → pyarrow の Python データ分析エコシステムが揃っており、LLM が知っているパターンで書きやすい

**Negative:**

- 「ちょっと curl したい」「shell ワンライナーで前処理」が直接できない（Python の `subprocess.run` 経由になる）
- R で統計分析したい層（lme4 / brms 等）はカバーできない。R 専用 MCP サーバーが別途必要
- データサイエンスでよく使う Node 系ツール（puppeteer, playwright）は使えない（→ そもそも `network=none` 既定なのでブラウザ系は対象外）

## Alternatives Considered

### A1: Python + Bash

- Pros: shell ワンライナー利用が直接できる
- Cons: bash 経由でファイルシステム探索・curl 試行などをされるリスク。Python の `subprocess` 経由で同じことは可能なので必須ではない
- 却下理由: セキュリティサーフェス増 vs 表現力増のトレードオフで前者が勝る

### A2: Python + Bash + Node

- Pros: フロントエンド系データ加工、npm エコシステム利用
- Cons: Node のセキュリティアドバイザリ追従コスト、イメージサイズ激増
- 却下理由: データ分析シナリオで Node 必須のケースは少なく、ROI が低い

### A3: Python + Bash + R

- Pros: 統計分析の選択肢が広がる
- Cons: R イメージは重い（500MB+）、依存ライブラリの ABI 互換性問題が頻発
- 却下理由: R 需要は専用 MCP サーバーで吸収すべき

## See also

- `_wip/data-toolbox-mcp/docs/ja/data-toolbox-mcp-rfp.ja.md` §2 / §3
- メモリ: `feedback_security_first`（セキュリティサーフェス縮小の方針）
