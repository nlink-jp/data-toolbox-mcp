# ADR-0002: コンテナエンジンとして Podman を採用し抽象化を行わない

- Status: Accepted
- Date: 2026-06-05
- Driver: magi
- Generalises to: なし

---

## Context

`execute_code` 経由でユーザー（LLM）コードを安全に実行するには、ホストから隔離された実行環境が不可欠である。母体である shell-agent-v2 では `app/internal/sandbox/engine.go` に `Engine` インターフェースを定義し、`app/internal/sandbox/cli.go` の `cliEngine` 内で `resolveEngine("auto"|"podman"|"docker")` により Podman / Docker 両対応している。

抽象化の対価として:

- engine 切替ロジック・テストマトリクス（Podman × Docker × macOS × Linux）が肥大化
- Podman 特有の `--userns keep-id:uid=1000` 対応（shell-agent-v2 ADR-0004）と Docker の `--user $(id -u)` の差を engine 層が吸収する必要がある
- Docker daemon 起動状態・Podman machine 起動状態の検査ロジックが engine ごとに必要

data-toolbox-mcp の Phase 1 は「個人マシン上で MCP クライアントから安全にサンドボックス実行を提供する」ことが目的であり、エンジン選択肢の幅は本質的価値ではない。一方、エンジンを 1 つに固定すればテスト経路は半分、コードは大幅に簡素化できる。

## Decision

Phase 1 では **Podman に固定する**:

- `exec.Command` 経由で `podman run / podman exec / podman stop / podman rm` を直接呼び出す（shell-agent-v2 `internal/sandbox/cli.go` の構造をそのまま流用）
- エンジン抽象化のための interface 化は **行わない**（YAGNI）
- 設定ファイルにエンジン選択フラグは出さない（将来追加時に backward-compatible に変更可能なので問題なし）

選定理由:

- **rootless**: 通常ユーザー権限で動作。ホスト側のセキュリティ境界が強い
- **daemon-less**: 常駐デーモン不要、systemd への依存なし
- **macOS で `podman machine` 起動済みなら CLI 互換性が高い**: README で手順を明示
- **nlink-jp 内に Podman 採用の前例あり**: shell-agent-v2、cclaude（コンテナ運用ノウハウのメモリあり）

Docker 対応の要求が複数件出てきた段階で、改めて別 ADR を起こしてエンジン抽象化を検討する。

## Consequences

**Positive:**

- 実装・テスト範囲が縮小し、Phase 1 のスコープが現実的になる
- Podman の `network=none` を default にすることで、サンドボックス境界をデフォルトで強くできる
- shell-agent-v2 と同じエンジン前提のため、コンテナ運用上の既知問題（メモリ `Podman Machine on macOS` 等）の対処を流用できる

**Negative:**

- Docker のみがセットアップ済みのユーザーは Podman を別途インストールする必要がある
- macOS では `podman machine init && podman machine start` が前提条件。README とエラーメッセージで「動いていないとどう失敗するか」を明示する必要がある
- 将来 Docker 対応を追加するとき、`exec.Command` 直接呼び出しを Engine interface 化するリファクタが必要になる（その時点では実装の差異が明確化されているので、抽象化の妥当性も判断しやすい）

## Alternatives Considered

### A1: Docker 単独

- Pros: 利用者の母数が大きく、Docker Desktop / colima が一般的
- Cons: daemon 必須、rootless ではない（Docker Desktop は VM 内 root）。サンドボックス境界が Podman より弱い
- 却下理由: セキュリティを Phase 1 から優先する方針（メモリ `feedback_security_first`）と整合しない

### A2: shell-agent-v2 と同様にエンジン抽象化（Podman + Docker 両対応）

- Pros: ユーザーが選べる、移行コストが低い
- Cons: 上記の通り、テスト範囲と実装複雑度が倍増。Phase 1 のスコープに合わない
- 却下理由: 「実装と同時にセキュリティを組み込む」前提では Podman 単独の方がレビュー対象が明確

### A3: コンテナを使わず subprocess + chroot/jail

- Pros: 依存ゼロ、軽量
- Cons: macOS で chroot は限定的、jail は FreeBSD 専用、Linux Namespace を生で扱うのは保守困難。「コンテナ化仮想マシン」という RFP 要件と不一致
- 却下理由: 要件不一致

## See also

- `_wip/data-toolbox-mcp/docs/ja/data-toolbox-mcp-rfp.ja.md` §3 / §7-B
- shell-agent-v2 ADR-0004 (Sandbox UID mapping)
- メモリ: `feedback_podman_machine`（macOS での既知問題）
- メモリ: `feedback_security_first`（セキュリティは実装と同時に）
