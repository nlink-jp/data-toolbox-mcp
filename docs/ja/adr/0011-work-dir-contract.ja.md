# ADR-0011: ワークスペースは呼び出し側の `work_dir` 配下に置き、`allowed_paths` を廃止する

- Status: Accepted
- Date: 2026-09-13
- Amends: [ADR-0001](0001-workspace-id-lifecycle.ja.md) のワークスペース所在（`workspace_dir` 配下）と、`allowed_paths` による入力制限

## Context

組織 ADR-021（ファイル渡し MCP サーバーの work dir 契約）をこのサーバーに適用する。
形は pcap-analyzer-mcp（同 ADR-0008）と同じにする —— 利用者の指示。

このサーバーの出力は**呼び出し側が開けない場所**にあった。ワークスペースは
サーバー所有の `workspace_dir`（既定 `~/.data-toolbox`）配下にあり、`execute_code` が
`/work` に書いたファイルのホストパス `host_work_dir` を結果に載せていたが、
呼び出し側のファイルツールはそこを読めない。`attach_files` が中身をインラインで
返せるから死んでいなかっただけで、設計がそう言っていたわけではない。

入力側の `allowed_paths` も、表現したいことを表現できない機構だった —— 照合は
解決後パスの前方一致で、リポジトリ単位の粒度が無い。work root 全体を賄うには
ホームを挙げるしかなく、その瞬間 `.ssh` も `.aws` も通る。

## Decision

1. **全ツールが `work_dir` を必須で取る。** ワークスペースは
   `<work_dir>/<workspace_id>/`、コンテナの `/work` にマウントされるのは
   `<work_dir>/<workspace_id>/work`。**`host_work_dir` は呼び出し側が開ける場所を指す**
   ようになる。
2. **解決順は 引数 → `_meta["jp.nlink/work_dir"]` → エラー**、検証は閉じた一覧
   （絶対 / `~` 無し / `..` 無し / 存在する dir / 書込可 / システム・資格情報の位置でない）。
3. **`workspace.workspace_dir` と `workspace.allowed_paths` を削除する。**
   キーが残った config は起動時に名指しで落とす —— 運用者が書いたつもりの
   封じ込めが無言で消えるのが最悪の結果だからである。
4. **`load_data` の `file_path` はブラックリストだけで守る。** 資格情報・エージェント
   制御ファイルの位置（`~/.ssh`、`~/.aws` 等）を拒否し、それ以外は読む。判定は
   パスの両方の綴り × 項目側の両方の綴り。ブラックリストは**床であって境界ではない**。
5. **コンテナ名に work dir のダイジェストを含める** —— 同じ `workspace_id` が異なる
   `work_dir` の下にあれば別のワークスペースであり、1 つの常駐コンテナが両方を
   兼ねることはできない。
6. `list_workspaces` / `delete_workspace` も `work_dir` を取る（走査対象は
   呼び出し側のディレクトリ）。

## Consequences

- **破壊的。** 全ツールのスキーマが変わり、`workspace_dir` / `allowed_paths` を
  持つ config は起動しない。既存の `~/.data-toolbox` 配下のワークスペースは
  参照されなくなる（中身はそのまま残る。不要なら手で削除する）
- `host_work_dir` が**開けるパスになる** —— これが本 ADR の主目的
- `attach_files` は引き続き有用（画像やテキストをインラインで返す道は残る）が、
  「開けないから必要」ではなくなった
- 既存の常駐コンテナ（`data-toolbox-mcp-<id>` 名）は再利用されない。不要なものは
  `podman rm` で掃除する

## References

- 組織 ADR-021、pcap-analyzer-mcp ADR-0008（同じ形の先行例）、
  voice-scribe ADR-0010（参照実装）
