# ADR-0008: attach_files — artifact を MCP コンテンツとして返す専用ツール

- Status: Accepted
- Date: 2026-06-06
- Driver: magi
- Generalises to: 他の MCP サーバー (LLM への artifact 返却が必要なもの)

---

## Context

v0.2.1 で `host_work_dir` を `execute_code` / `list_workspaces` 戻り値に追加したことで、LLM は生成 artifact の **ホスト側絶対パス** をユーザーに案内できるようになった。しかし Claude Desktop 実機検証 (2026-06-06) で次の摩擦が判明:

- LLM が生成した PNG プロットを **チャットにインライン表示するには、ユーザーがそのフォルダを Claude Desktop に「接続済みフォルダ」として登録しておく必要がある**
- 接続が無い場合、LLM は「`{host_work_dir}sales.png` に保存しました、Finder で開いてください」と案内するだけになり、対話が一段スムーズではない
- MCP プロトコル自体は `tools/call` の `result.content` が **複数の content block (text / image / resource)** を返せる仕様。MCP クライアント (Claude Desktop) は image content をインライン表示する

つまり「ファイルの場所を伝える」(v0.2.1 で解決) と「ファイル自体を返す」(本 ADR の主題) は別の関心事。

## Decision

新ツール **`attach_files`** を追加する。

### `attach_files`

- **引数**: `{workspace_id: string, paths: [string]}`
  - `paths` の各要素は `/work/...` または workspace の `/work` 配下相対パス (例: `"/work/sales.png"` または `"sales.png"`)
  - 1 要素以上、最大 16 要素
- **戻り値**: MCP `tools/call` の `content` 配列をリッチに構成
  - 先頭: text content block で「N 個のファイルを attach した、各々の host 上の場所と種別」を summary 表示
  - 続き: ファイル拡張子で自動判定して image / text / metadata-only に振り分け:

| 拡張子 (lowercase) | 戻り種別 | 詳細 |
|--------------------|---------|------|
| `.png` `.jpg` `.jpeg` `.gif` `.webp` `.bmp` | MCP image content | `{type: "image", data: <base64>, mimeType: "image/png"}` 等。クライアントがインライン描画 |
| `.svg` | MCP image content | `mimeType: "image/svg+xml"` |
| `.csv` `.tsv` `.json` `.jsonl` `.ndjson` `.txt` `.md` `.log` `.yaml` `.yml` `.toml` | MCP text content | `{type: "text", text: "...file body..."}`。サイズ上限超過時は先頭部分 + 末尾省略マーカー |
| その他 (`.parquet` `.pdf` `.zip` 等) | metadata-only | text content で `"file at <host_work_dir>/<name>, <size> bytes, sha256: <hash>"` を返す。base64 埋め込みはしない |

### サイズ上限 (Phase 1)

- **単体ファイル: 10 MiB** (10 × 1024 × 1024 bytes)
- **合計レスポンス: 20 MiB**
- 超過時:
  - 単体超過: そのファイルは metadata-only に降格
  - 合計超過: 処理順で先のファイルから返し、上限到達後は残りを metadata-only に降格
- 上限値は `config.toml` `[attach] max_single_size_bytes` / `[attach] max_total_size_bytes` で上書き可能 (デフォルト上記)

### パス解決

- `paths` の各要素が `/work/` で始まれば、`<host_work_dir>` 配下のサブパスとして解決
- `/work/` で始まらなければ、`<host_work_dir>` に対する相対パスとして解決 (LLM が "sales.png" だけ書いても通る)
- 解決後の絶対パスが `<host_work_dir>` の subtree であることを `filepath.Clean` + prefix check で再検証 (path traversal 二重防御 — ADR-0006 `delete_workspace` と同じパターン)

### MCP プロトコル経路

- 既存 `mcpserver.handleToolsCall` は **tool handler の戻り値 (any) を JSON.Marshal して単一 text content block に詰める** 設計だった
- これを拡張: tool handler が **`mcpserver.RawResult` 型** (新規) を返した場合は、その content block 配列をそのまま使う
- 既存ツール (load_data 等) は今まで通り単一 text content として返却される (後方互換)

## Consequences

**Positive:**

- Claude Desktop で **接続済みフォルダの設定無しに plot をインライン表示できる** → 体感効率が大幅向上 (LLM 自身が Phase 2 補完の優先度 1 と評価)
- LLM が **何を返すかを明示的に指定** する設計のため、自動 scan / monkey patch 系の罠を回避 (意図しないファイル返却なし、execute_code の挙動も無変更)
- PNG 以外 (CSV / JSON / markdown) の text 返却も同じツールで完結 → 「結果テーブルを返す」「生成 markdown を返す」用途にも転用可能
- メタデータ only の fallback により、parquet / pdf 等の大きなバイナリも「場所と要約は返す」程度の支援は受けられる

**Negative:**

- ツール表面が 6 → 7 に増加 (`attach_files` 追加)。LLM の inputSchema 認識コスト微増
- MCP `content` 配列を複数 block で組み立てるため、`internal/mcpserver` の result handling に分岐が増える
- 大きいファイルの base64 化が response time に響く可能性 (10 MiB PNG で ~30ms 程度の想定、許容範囲)
- 拡張子ベースの種別判定なので、`.csv` という名前の binary や `.png` という名前の text を返すと挙動が直感的でない (ただし対象がサンドボックス内のため意図的な悪用は基本起きない)

## Alternatives Considered

### A1: `execute_code` の戻り値で auto-scan + 自動返却

- 実装案 (a): exec 後に `<host_work_dir>/*.png` 等を scan して新規追加分を全て image content として返す
- Pros: LLM に追加の API お作法を要求しない
- Cons: **意図しないファイル (前回の plot、user が手で置いた画像) も返してしまう** / scan のしきい値設定 (filename / mtime) が必要 / レスポンスサイズが予測不能
- 却下理由: 「LLM が明示的に指定する」設計のほうが、結果の予測可能性と監査性が高い

### A2: Python 側で sentinel コメント or 環境変数経由でリスト宣言

- 実装案 (b): `# data-toolbox: attach /work/sales.png` のような sentinel を Python コード末尾に書かせて、exec 後にパースして返す
- Pros: execute_code 内で完結、ツール追加なし
- Cons: LLM に **特殊な記法** を覚えさせるオーバーヘッド / sentinel の parse 仕様が漏洩しやすい / 拡張困難
- 却下理由: 明示的な独立ツールのほうが MCP 思想と整合する

### A3: `savefig` を hook して内部リストに自動追記する monkey-patch を runtime に inject

- 実装案 (c): matplotlib の `Figure.savefig` を runtime レベルで wrap し、書き出した画像を session list に積む
- Pros: LLM のコードが Plain な `plt.savefig("/work/foo.png")` のままで動く
- Cons: **副作用が透過** すぎて debug 困難 / matplotlib 以外の出力 (Pillow, polars の write_csv 等) はカバーできない / runtime とサンドボックス境界を緩める方向
- 却下理由: 透過的な monkey patch は data-toolbox-mcp の「予測可能性」と相性が悪い

### A4: `attach_image` (画像専用) + `attach_text` (text 専用) の 2 ツール分割

- Pros: API が明示的、各々の inputSchema が単純
- Cons: ツール 6 → 8、LLM が拡張子から正しいツールを選ぶ手間、本質的な責務が同じ
- 却下理由: 1 ツールで拡張子から自動振り分けのほうが UX が良い

## See also

- ADR-0006 (v0.2.1 amendment): `host_work_dir` で「場所を伝える」を解決した経緯
- ADR-0009: 対をなす「サンドボックス内ファイルの直接 DuckDB ロード」(`load_from_work`)
- メモリ: `feedback_structured_mcp_tool_errors` (attach 失敗時のエラーも `{code, message, details}` JSON で返す)
