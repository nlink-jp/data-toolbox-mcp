# ADR-0006: workspace 管理 + ランタイム情報開示 MCP ツール (list_workspaces / delete_workspace / describe_runtime)

- Status: Accepted
- Date: 2026-06-05
- Revisions:
  - 2026-06-05: v0.2.1 で `list_workspaces` / `execute_code` 戻り値に `host_work_dir` を追加。`describe_runtime` の notes / mount_points 文言を拡充して artifact exchange convention を LLM に明示。理由は本文 §Decision (v0.2.1 amendment) 参照
- Driver: magi
- Generalises to: なし

---

## Context

v0.1.0 リリース後、実利用シナリオで 3 つの摩擦が観察された:

- LLM クライアントは複数チャットセッションを跨いで `workspace_id` を覚えていない。「先週どの workspace で何を分析していたか」を辿る手段がない
- ADR-0001 で「workspace の garbage collection は Phase 2 で TTL ベースの cleanup を別 ADR にて検討」と deferred されたが、その前段として手動で削除する手段すら存在しない
- 既存ワークスペースの存在を確認するには `ls ~/.data-toolbox/` をホスト側で実行するしかなく、MCP クライアント (sandboxed である Claude Desktop 等) からは到達不能
- **LLM はコンテナに何が同梱されているかを知らない**。matplotlib があるのか、Pillow が使えるのか、フォントは何が入っているのか、ネットワークは通るのか — `execute_code` を投機的に走らせて ImportError や接続失敗を見るまで分からない。これは `network=none` 環境では特に痛い (試行錯誤の余地がない)

つまり、LLM 駆動の運用で **workspace の発見・整理** と **コンテナ機能の発見** の両方ができない。

## Decision

MCP ツールを 3 つ追加する:

### `list_workspaces`

- **引数**: なし
- **戻り値**:
  ```json
  {
    "workspaces": [
      {
        "id": "samples",
        "last_used": "2026-06-05T14:30:00Z",
        "container_state": "running",
        "host_work_dir": "/Users/magi/.data-toolbox/samples/work/"
      }
    ]
  }
  ```
- **真実の源**: ディスク (`<workspace_dir>/<id>/` ディレクトリの存在)
- **`last_used`**: `<workspace_dir>/<id>/work/analysis.duckdb` の `os.Stat().ModTime()`。ファイル不在なら親ディレクトリの ModTime にフォールバック
- **`container_state`**: `podman ps -a --filter name=data-toolbox-mcp-<id> --format {{.State}}` 結果を `"running"` / `"stopped"` / `"absent"` の 3 値に正規化
- **`host_work_dir`** (v0.2.1): コンテナ内 `/work/` がホスト側で実際にどこにマウントされているかの絶対パス。`filepath.Join(workspace_dir, id, "work")` で計算。LLM はこれを見て生成 artifact (PNG/CSV/Parquet 等) のホスト側パスをユーザーに伝えられる

### `delete_workspace`

- **引数**: `{workspace_id: string}`
- **戻り値**: `{deleted: true, workspace_id: "..."}`
- **動作**:
  1. `workspace.ValidateID` で `workspace_id` 構文検証 (ADR-0001 と同じ正規表現)
  2. 計算した `<workspace_dir>/<workspace_id>` が `<workspace_dir>` の直接の子であることを `filepath.Clean` で defense-in-depth 再検証 (path traversal の二重防御)
  3. コンテナがあれば `podman rm -f` で除去（在席不問）
  4. in-memory `Manager.workspaces` から削除
  5. `os.RemoveAll(<workspace_dir>/<workspace_id>/)` でディスク状態を完全削除（DuckDB ファイル + work/ + _upload + _code 全部）

破壊的操作だが、MCP クライアント (Claude Desktop / Cursor) はツール呼び出し前にユーザー承認を取るため、プロトコルレベルで「明示的同意」が得られている。`confirm: true` のような追加引数は付けない (UX 悪化のみ)。

### `describe_runtime`

- **引数**: なし
- **戻り値**:
  ```json
  {
    "python_version": "3.12",
    "container_image": "localhost/data-toolbox-runtime:latest",
    "packages": [
      {"name": "duckdb", "version_constraint": "~=1.1"},
      {"name": "pandas", "version_constraint": "~=2.2"},
      {"name": "polars", "version_constraint": "~=1.8"},
      {"name": "pyarrow", "version_constraint": "~=18.0"},
      {"name": "matplotlib", "version_constraint": "~=3.10"},
      {"name": "Pillow", "version_constraint": "~=11.0"}
    ],
    "fonts": ["Noto Sans CJK JP"],
    "network": "none",
    "mount_points": {"/work": "container directory bind-mounted to the host; files here appear on the host (see notes for the artifact-exchange pattern)"},
    "notes": [
      "matplotlibrc preconfigured with Noto Sans CJK JP first; Japanese labels render without extra setup.",
      "DuckDB file lives at /work/analysis.duckdb inside the container.",
      "Container runs as uid 1000; host bind-mounts via --userns keep-id:uid=1000,gid=1000.",
      "ARTIFACT EXCHANGE: anything you write to /work/<name> inside the container appears on the host at <workspace_dir>/<workspace_id>/work/<name>. The exact host path for the workspace you are using is returned as host_work_dir in the execute_code result and in each list_workspaces item. Use this to hand files back to the user (PNG plots, exported CSV / Parquet, generated reports — anything). Do NOT base64-encode and embed in the response: it wastes the response budget and the user can open the file directly."
    ]
  }
  ```
- **データソース**: コンパイル時定数 (`internal/runtime/manifest.go`) で記述。Dockerfile を更新するときは同じコミットで manifest も更新する規律 (ADR-0005 と同様に `go:embed` 同期と同じ責務範囲)
- **`network` フィールドのみ実行時に config から取得** (`config.Container.Limits.Network`)、`packages` 等の静的情報は構造体定数
- **正確性方針**: "ある" と宣言したものは確実に動く / "ない" ものが追加 install 経由で実は使えるケース (`network=bridge` 時の `pip install`) はカバーしない (それは `execute_code` で試せばいい)

`describe_runtime` の意義は、LLM が **最初に 1 回呼ぶ** ことを想定。ユーザーがツール呼び出しを承認するのは初回のみ、それ以降は LLM がコンテキストに保持する。

### v0.2.1 amendment: `execute_code` 戻り値に `host_work_dir` を追加

実機検証 (2026-06-05 Claude Desktop) で、LLM が matplotlib plot を生成した後にホスト側にどう渡すかを把握できず、**戻り値の stdout に base64 で埋め込んで返そうとする** 挙動が観測された。LLM はコンテナ内の `/work/foo.png` がホスト側のどこに対応するかを知らないことが原因。

対策として `execute_code` の戻り値スキーマを拡張する:

```diff
 {
   "stdout": "...",
   "stderr": "...",
-  "exit_code": 0
+  "exit_code": 0,
+  "host_work_dir": "/Users/magi/.data-toolbox/<workspace_id>/work/"
 }
```

- 値は `<workspace_dir>/<workspace_id>/work/`（実際の絶対パス、`filepath.Join` で計算）
- LLM はこのパスを見て「`{host_work_dir}foo.png` に書きました」とユーザーに案内できる
- 後方互換: 既存クライアントは新フィールドを無視するだけ。破壊変更なし

これは ADR-0003 で定義された `execute_code` の戻り値スキーマへの追加であり、本来なら ADR-0003 を改訂する範囲だが、文脈が「LLM への情報開示」(ADR-0006 の主題) と一致するため、本 ADR の v0.2.1 amendment として記録する。

## Consequences

**Positive:**

- LLM が `list_workspaces` で過去の workspace を発見・選択できる → セッション跨ぎの作業継続が成立
- `delete_workspace` で不要な workspace を片付けられる → ディスク占有問題の応急処置
- `describe_runtime` で LLM が「matplotlib 使える」「Noto Sans CJK で日本語通る」「network=none なので pip install 不可」を初回 1 回で把握 → 投機的 ImportError / `pip install` 試行 / 「フォントがないので英語ラベルにします」みたいな無駄な迂回が消える
- 将来の TTL ベース GC (Phase 3 以降の検討) はこれら workspace ツールの上に構築できる (例: 90 日未使用 workspace の自動 list → 削除候補レビュー)
- ディスク真実の源モデルは `Ensure` の冪等性とも整合 (ADR-0001 の "サーバー再起動を跨いで永続" の補完)

**Negative:**

- ツール表面が 3 → 6 に増加。inputSchema 定義とドキュメントの維持工数が上がる
- `list_workspaces` 1 回ごとに `podman ps` 呼び出しが入る (workspace 数だけ) → 多数 workspace 環境では数百 ms オーダーになり得るが、典型的個人利用 (< 20 workspace) では問題なし
- `delete_workspace` は不可逆。LLM の誤動作リスクは MCP クライアント側のユーザー承認に依存
- `describe_runtime` の manifest と実 Dockerfile が drift し得る (二重管理リスク)。CI / 統合テストで「manifest と実コンテナの整合性」を検証する仕組みが必要 (Phase 1 の e2e ハーネスで `pip list` 結果と比較)

## Alternatives Considered

### A1: list_workspaces のみ、delete は手動で

- Pros: 表面が小さい、破壊的操作の責任が分離
- Cons: LLM 駆動で完結しない (人間がターミナルに戻る必要がある)
- 却下理由: 「LLM 駆動で workspace 整理を完結させる」のがこの ADR の目的

### A2: Container state を含めず、ディスク存在のみ返す

- Pros: podman 呼び出し不要、軽量
- Cons: 「コンテナが既に動いている / 停止している / 不在 (未初回 ensure)」の判別ができない → LLM が「この workspace は今すぐクエリ叩いていいのか」を判断できない
- 却下理由: container_state は LLM の判断材料として重要、コストは許容範囲

### A3: list_workspaces を `list_artifacts` まで拡張 (DuckDB のテーブル一覧も同時に返す)

- Pros: 一発でリッチな情報
- Cons: 各 workspace の DuckDB を都度 open する必要 → 高コスト、エラーパス複雑化、container_state="absent" の場合の挙動が定義しづらい
- 却下理由: 用途が広がりすぎ。テーブル一覧は `query_data` で `SHOW TABLES` を叩けば取得可能なので冗長

### A4: `describe_runtime` を実装せず inputSchema description に詰め込む

- Pros: ツール表面が増えない
- Cons: tools/list の description は LLM が "暗黙的に" 読むだけで、構造化されない。バージョン情報を更新するたび description を編集する手間は同じ。pip list 等から動的に取れる利点も失う
- 却下理由: 構造化された静的データの方が LLM プロンプトに使いやすい。`describe_runtime` を 1 回呼ばせるコストは小さい

### A5: `describe_runtime` で `pip list` を実行時に叩いて返す

- Pros: 真の動的情報、drift しない
- Cons: 毎回 podman exec、平均 1-2 秒。manifest と Dockerfile を分離管理する罠 (実は network=bridge にすると user が pip install して結果が変わる) も持ち込む
- 却下理由: 静的 manifest + 統合テストでの drift 検証のほうが運用が単純で速い

### A6: TTL ベース自動 GC を先に実装

- Pros: 完全自動化
- Cons: ポリシー (何日? どこに退避?) の設計が重い、Phase 3 級の工数
- 却下理由: 手動制御を先に出して使用感を見てから自動化を検討する方が段階的に安全

## See also

- ADR-0001: workspace_id によるスコープとライフサイクル
- ADR-0004: stdio トランスポートのみ
- メモリ: `feedback_structured_mcp_tool_errors` (delete 失敗時のエラーは `{code: "workspace_failed", ...}` 形式で返す)
- メモリ: `feedback_mcp_client_validates_input_schema_enum` (delete_workspace は workspace_id を string で受けるだけなので enum 検証は対象外)
