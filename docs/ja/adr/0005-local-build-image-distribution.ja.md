# ADR-0005: ランタイムコンテナイメージはローカルビルド配布とする

- Status: Accepted
- Date: 2026-06-05
- Driver: magi
- Generalises to: nlink-jp の他コンテナベースツール

---

## Context

Phase 0 で確定した「Python のみのランタイムを内蔵したコンテナ」（ADR-0003）は、エンドユーザーマシン上で実行される。配布形態として以下が考えられる:

- (A) **Registry push**: `ghcr.io/nlink-jp/data-toolbox-runtime:vX.Y.Z` のようなレジストリ経由で配布
- (B) **ローカルビルド**: Dockerfile を本プロジェクトに同梱し、ユーザーが手元で `podman build` する
- (C) **Image tar 配布**: GitHub Releases に `podman save` した .tar を添付

Registry push は配布性が高い反面、以下のコストがある:

- ghcr.io / Docker Hub の認証情報管理（個人 PAT → 失効リスク）
- イメージの署名・脆弱性スキャン（cosign, trivy）の運用追加
- nlink-jp のリリースパイプラインはローカルビルド方式で統一されている（メモリ `feedback_no_github_actions_ci`）— レジストリ push は CI/CD への依存を持ち込む
- パブリックレジストリへの push は「廃止できないアーティファクト」を生む（後から消すとユーザーが困る）

一方、本プロジェクトのランタイムイメージは:

- 内容が小さい（python:3.13-slim + 4 パッケージ、〜500MB）
- ビルドが軽量（`pip install` のみ、所要 1-2 分）
- ベースイメージ + パッケージのみで、独自バイナリは含まない

そのため、ユーザー側で手元 build しても摩擦が小さい。

## Decision

Phase 1 では **ローカルビルド配布** を採用し、build 自体は **`data-toolbox-mcp` バイナリの専用サブコマンド** で実行する:

- ランタイム用 Dockerfile は **`data-toolbox-mcp` バイナリに `go:embed` で同梱**（ユーザーが別ファイルを管理する手間を排除）
- **`data-toolbox-mcp build-runtime` サブコマンド** が、埋め込み Dockerfile を一時ディレクトリに展開して `podman build -t localhost/data-toolbox-runtime:vX.Y.Z -t localhost/data-toolbox-runtime:latest` を実行
- 補助コマンド: `data-toolbox-mcp doctor`（Podman 状態とランタイム image の有無を検査）、`data-toolbox-mcp version`
- `container.image` の default は `localhost/data-toolbox-runtime:latest`
- セットアップ手順を README で明示: 「(1) `make build` でバイナリ → (2) `data-toolbox-mcp build-runtime` でランタイム image → (3) `data-toolbox-mcp doctor` で確認 → (4) Claude Desktop 設定」の 4 ステップ
- `make runtime-image` は開発者向けに `data-toolbox-mcp build-runtime` を呼ぶラッパーとして提供（任意）
- レジストリ push 用ターゲットは Phase 1 では用意しない

この設計により、MCP 本体・ビルドツール・診断ツールが「1 バイナリ・1 バージョン」で同梱され、バージョン整合性の責任を内部化できる（メモリ `feedback_single_binary_subcommand`）。

将来の Phase 2 以降で「セットアップ摩擦を下げたい」という要求が複数件出てきたら、別 ADR で registry push を検討する。その際は cosign 署名と SBOM 添付を前提とする。

## Consequences

**Positive:**

- 認証情報管理（PAT / アカウント）が不要
- ネットワーク制限環境（社内 LAN 等）でも、初回 `pip install` さえ通れば build 可能
- 廃止できない公開アーティファクトを作らない（プロジェクト方向性の変更に追従しやすい）
- nlink-jp のローカルビルド方式と一貫
- ユーザーが Dockerfile を読めるため、何が入っているかが透明

**Negative:**

- セットアップ手順が 2 ステップ（build + image-build）になる
- 初回 build に `pip install` 用ネットワーク + 数分が必要
- Podman の build cache 管理がユーザー責任になる
- バージョン整合の責任がユーザーに移る（`make build` のバイナリと `make runtime-image` のイメージのバージョンを揃える慣習を README で明示する必要）

## Alternatives Considered

### A1: ghcr.io へ push

- Pros: ユーザーは `podman pull` のみで完結
- Cons: 認証情報運用、署名・SBOM 必須化、廃止コスト、CI/CD 依存
- 却下理由: 上記コストが Phase 1 スコープに合わない。nlink-jp 方針 (`feedback_no_github_actions_ci`) とも整合しない

### A2: Docker Hub へ push

- Pros: ghcr より広く知られている
- Cons: rate limit、organization plan のコスト、A1 と同じ運用負担
- 却下理由: A1 と同じ理由 + Docker Hub の rate limit が個人マシンで実害になりうる

### A3: GitHub Releases に image tar 添付

- Pros: pull 一発ではないが registry credentials 不要
- Cons: Release アセットサイズが 500MB を占める、`podman load` 手順が増える、複数 arch 対応が複雑
- 却下理由: 利点が薄く、ローカル build と比較してユーザー負担も変わらない

### A4: 両対応（ローカル build + registry push 選択可）

- Pros: ユーザーの選択肢が広い
- Cons: ドキュメント・テスト・リリースパイプラインの分岐が増える
- 却下理由: Phase 1 スコープではローカル build に絞り、registry が必要になった段階で別途検討

## See also

- ADR-0003: コンテナ内ランタイムを Python のみに限定する
- メモリ: `feedback_no_github_actions_ci`（CI 不使用のローカルビルド方針）
- `_wip/data-toolbox-mcp/docs/ja/reference/phase1-plan.ja.md` Track E
