# ADR-0007: ランタイムコンテナパッケージのスコープ拡張 (フォント + 描画ツール)

- Status: Accepted
- Date: 2026-06-05
- Driver: magi
- Generalises to: nlink-jp 内で Python サンドボックスを持つ他プロジェクト (mcp-skeleton 派生等)

---

## Context

v0.1.0 のランタイムコンテナは:

```dockerfile
FROM python:3.13-slim
RUN pip install --no-cache-dir \
      duckdb~=1.1 pandas~=2.2 polars~=1.8 pyarrow~=18.0
```

これで「データのロードと SQL クエリと Python での表形式分析」は動く。しかし、**プロットを描こうとすると `import matplotlib` で ImportError**、**仮に matplotlib があっても日本語ラベルが ☐☐☐ (tofu) になる**ため、LLM 駆動データ分析の典型シナリオである「結果を可視化する」が不成立。

先行プロジェクト [shell-agent-v2](https://github.com/nlink-jp/shell-agent-v2) のサンドボックス (`app/internal/sandbox/imagebuild/bundle.go`) では以下の対策が既に取られている:

1. **`python:3.12-slim`** をベースに採用（3.13 ではなく安定マイナーの 3.12）
2. **`fonts-noto-cjk` + `fonts-noto-cjk-extra`** を `apt install` （CJK 文字描画必須）
3. **`/etc/matplotlib/matplotlibrc`** を自動配置:
   ```
   font.family: sans-serif
   font.sans-serif: DejaVu Sans, Noto Sans CJK JP, Arial, Liberation Sans
   axes.unicode_minus: False
   ```
   ※ ただし matplotlib 3.10 の Agg バックエンドは **font.sans-serif の最初に見つかったフォントで全文字を描画** (per-glyph fallback なし)。`DejaVu Sans` を先頭にすると CJK で `UserWarning: Glyph ... missing` が出るので、本プロジェクトでは順序を `Noto Sans CJK JP` 先頭にする (Noto Sans CJK JP は Latin も覆うので副作用なし)
4. **`MATPLOTLIBRC=/etc/matplotlib/matplotlibrc`** を環境変数で固定
5. **`matplotlib` + `numpy` + `scipy` + `scikit-learn` + `graphviz`** を pip install

shell-agent-v2 ADR-0004 / history/sandbox-image-build にも経緯あり。

## Decision

v0.2.0 で以下を採用する:

### 1. Python ベースイメージを `python:3.12-slim` に変更

`3.13-slim` → `3.12-slim`。shell-agent-v2 で実証済の安定版。3.12 と 3.13 の機能差は本プロジェクトの用途では無視できる。

### 2. OS パッケージ追加 (apt)

```
fonts-noto-cjk         # 必須: 日本語 / 中国語 / 韓国語ラベル描画
ca-certificates        # network=bridge 時の TLS 用 (将来の pip install への保険)
```

`fonts-noto-cjk-extra` は冠字・異体字対応だが +50MB 超のため見送り。CJK 通常文字 (JIS 第一・第二水準相当) は `fonts-noto-cjk` だけで描画可能。

### 3. Python パッケージ追加 (pip)

```
matplotlib~=3.10       # 描画ツールのデファクト
Pillow~=11.0           # matplotlib の依存兼、画像処理単体ユースケース
```

`matplotlib` を入れると `Pillow` は依存として自動で入るが、明示しておくほうが意図が明確。

### 4. matplotlib 設定の自動配置

shell-agent-v2 のパターンを採用するが、**フォント順序は CJK 先頭** に修正:

- `/etc/matplotlib/matplotlibrc` に
  ```
  font.family: sans-serif
  font.sans-serif: Noto Sans CJK JP, DejaVu Sans, Arial, Liberation Sans
  axes.unicode_minus: False
  ```
- 環境変数 `MATPLOTLIBRC=/etc/matplotlib/matplotlibrc`

**順序が CJK 先頭である理由**: matplotlib 3.10 の Agg バックエンドは font.sans-serif リストの **最初に見つかった有効なフォントで全文字を描画** する (per-glyph fallback ではない)。`DejaVu Sans` を先頭にすると CJK 文字で `UserWarning: Glyph 37329 (\N{CJK UNIFIED IDEOGRAPH-91D1}) missing from font(s) DejaVu Sans` が出る。`Noto Sans CJK JP` は Latin 文字もカバーするため、先頭に置いても英語ラベルへの副作用なし。

これにより LLM が `import matplotlib.pyplot as plt; plt.title("売上推移")` と書いても、特殊設定なしで日本語が描画される。

**実機検証 (2026-06-05)**: `python -W error::UserWarning -c "...日本語ラベル付きで savefig..."` が UserWarning 無く成功することを確認済み。

### 5. backend は Agg (デフォルト)

ヘッドレスコンテナなので `matplotlib.use("Agg")` は不要 (Python:slim には Tk/Qt がそもそも入らないため Agg が自動選択)。LLM が誤って `plt.show()` を叩いた場合は no-op。`plt.savefig("/work/foo.png")` でホスト側に出力。

### 6. スコープ外 (v0.2.0 では入れない)

| パッケージ | 理由 |
|----------|------|
| `numpy` | `pandas` / `matplotlib` が依存として入れる。明示不要 |
| `scipy` | +60MB、統計・線形代数用。需要が出てから別 ADR |
| `scikit-learn` | +80MB、機械学習用。需要が出てから別 ADR |
| `seaborn` | matplotlib 依存。pip install (network=bridge) で都度導入可。pin の管理コストに見合う需要なし |
| `plotly` | +30MB、対話型 HTML 出力。MCP 経由でブラウザを開く経路がないので恩恵小 |
| `graphviz` (apt + pip) | shell-agent-v2 は `.dot` 図描画用に採用、本プロジェクトはデータ分析特化のため不要 |
| `openpyxl` / `xlsxwriter` | Excel 入出力。pandas は別途自前 reader を持つので最低限の Excel ロードは可能。需要が出てから別 ADR |

### 7. イメージサイズ目処

v0.1.0: 692MB → v0.2.0: 推定 850-900MB (fonts-noto-cjk ~150MB + matplotlib + Pillow + 依存)。**900MB 以下** を許容ラインとする。これを超えそうなら `fonts-noto-cjk` を最小 subset に絞るか、`matplotlib` のフォントキャッシュ事前生成を見送る等で調整。

## Consequences

**Positive:**

- LLM が `execute_code` で `matplotlib` プロットを描けるようになる → データ分析サンドボックスの典型シナリオ完成
- 日本語ラベルが特殊設定なしで描画される → 日本語ユーザー (本プロジェクトの第一想定ユーザー層) の摩擦解消
- shell-agent-v2 の知見を借りるので「日本語フォント問題」を再発明しない
- 描画結果は `/work/<filename>.png` 経由でホスト側に出力 → 既存のマウント設計と整合 (architecture.md §3 / §4 のまま)
- ベースイメージ統一 (`python:3.12-slim`) で nlink-jp 内のサンドボックス系プロジェクトのレイヤキャッシュ共有可能性

**Negative:**

- イメージサイズが 692MB → 推定 850-900MB に増加 (約 30%)。初回 `build-runtime` 所要時間が 1-2 分 → 2-3 分に延びる
- `apt install` ステップ追加で Dockerfile の複雑度が上がる (ただし shell-agent-v2 の前例があるので保守者は迷わない)
- Python 3.13 → 3.12 ダウングレード: ユーザー Python コードが 3.13 専用構文 (生成式の制限緩和等) を使えなくなる。実用上の影響はほぼゼロ
- 今後 scipy / scikit-learn / seaborn の追加要求が出るたびに ADR を起こす運用負荷 (v0.2.0 で全部入れない方針なので)

## Alternatives Considered

### A1: 現状維持 (matplotlib も fonts も入れない)

- Pros: イメージサイズ維持、Dockerfile シンプル
- Cons: 「データ分析」を謳いつつ可視化できないのは不完全
- 却下理由: 利用シナリオの基本機能を欠く

### A2: matplotlib のみ追加、フォントは入れない

- Pros: イメージサイズ最小限の増加 (+50MB 程度)
- Cons: 日本語ラベルが tofu (☐☐☐) になる。LLM 出力チェックの度に「フォント問題ですね」と説明する手間が永続発生
- 却下理由: 日本語ユーザー向けにこの摩擦は許容できない

### A3: matplotlib + scipy + scikit-learn + seaborn + plotly すべて入れる

- Pros: 「データサイエンスフル装備」を主張可能
- Cons: イメージサイズが 1.5GB 超え、初回 build が 5 分超、pin 管理対象が増える、セキュリティパッチ追従コスト上昇
- 却下理由: 必要性が証明されていないパッケージを predictively 入れると Bloat に陥る。需要が出てから別 ADR で追加するほうが健全

### A4: shell-agent-v2 と完全に同じ構成 (numpy + scipy + scikit-learn + graphviz も全部)

- Pros: 知見の完全コピー、テスト負担減
- Cons: graphviz は本プロジェクトのスコープ外、scipy/scikit-learn は需要不明確で +200MB の無駄、結果 v0.1.0 の 692MB が 1GB 超え
- 却下理由: shell-agent-v2 とは利用シナリオが違う (LLM ループ全部 vs MCP 経由ツール提供) ので、フォント・matplotlib・Pillow までの "可視化最小セット" に留める

### A5: matplotlib 設定をユーザー config.toml で受け取る

- Pros: フォント追加・パッケージ追加をユーザーが宣言可能
- Cons: 設計が肥大化、ベースイメージ自動再ビルドの判定が複雑、`go:embed` Dockerfile との相互作用が複雑 (ユーザー設定で動的に Dockerfile を組み立てる必要)
- 却下理由: Phase 0 で確立した「Dockerfile は go:embed の固定資産」(ADR-0005) を破壊する。今後追加するパッケージは ADR で議論して固定する方針を維持

## See also

- ADR-0003: コンテナ内ランタイムを Python のみに限定する
- ADR-0005: ランタイムコンテナイメージはローカルビルド配布
- shell-agent-v2 `docs/ja/history/sandbox-image-build.ja.md`
- shell-agent-v2 ADR-0004: Sandbox UID mapping (`--userns keep-id` の経緯)
- メモリ: `feedback_security_first` (パッケージ追加はセキュリティサーフェス拡大に直結するため scope を絞る)
