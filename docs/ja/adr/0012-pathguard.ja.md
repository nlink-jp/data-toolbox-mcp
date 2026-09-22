# ADR-0012: パスの判定は nlink-jp/pathguard に任せる — 写しを持たない

- Status: Accepted
- Date: 2026-09-22

## Context

ADR-0011 以来、`work_dir` の検証と、読み取りのブラックリスト（`workdir.Sensitive`）は
`internal/mcp/workdir` にあった。voice-scribe（組織 ADR-021 の参照実装）からの写しで、同じ写しが
ほかの 7 サーバーにもあった。どの写しも場所を**名前で**比べていた。APFS は既定で大文字小文字を区別しないので、
`~/.SSH`、`.ENV`、`/USR/local` が同じ場所を指しながら検査を通った。ホームディレクトリが分からない
ときは `Sensitive` が "" を返し、すべてを通した。

組織はこの判定を 1 つのモジュールにまとめた（`nlink-jp/pathguard`、lib-series）。場所を
ファイルの実体と、ディスクと同じやり方で同一視した名前の両方で比べ、まだ存在しない場所も
その親の実体で捕まえる。一覧は gem-agent・lagent と同じものを 1 つ持つ。

## Decision

- `github.com/nlink-jp/pathguard` v0.1.0 を依存に加える。この org の外のコードは入らない。
- `internal/mcp/workdir` は**薄いアダプタ**にする。持つのは次だけ:
  - リクエストの `_meta` を文脈から取り出して `pathguard/workdir` の `Resolve` に渡すこと、
  - その `*workdir.Error` を `toolerr` の同じ code・message・details に移すこと、
  - `NewResolver(serverDirs...)` —— このサーバー自身のディレクトリ（今は設定ディレクトリ
    `~/.config/data-toolbox-mcp` だけ）を守る場所（`pathguard.ServerDir`）として渡し、`work_dir_required` の
    1 文を `RequiredHint` で添える。空のパスは何も守らないのではなく、すべての呼び出しを拒ませる
    （設定ディレクトリが決まらないのはホームが分からないときで、そのときは pathguard もすべてを拒む）、
  - `Sensitive` —— `pathguard/workdir.Sensitive`（Local の方針）をそのまま出す。
- 呼び出し箇所（`Resolve`・`Validate`・`Sensitive`）は変えない。変わるのは組み立ての 1 行
  （`internal/tools/workdir.go` の `workDirResolver`）と、ゼロ値で組み立てていたテストだけである。
- `TestOnlyOnePlaceConstructsAResolver` は、`workdir.go` の `workdir.NewResolver` の呼び出しがただ 1 つであることと、
  `workdir.Resolver` のリテラルがどこにも無いこと（ゼロ値はすべての呼び出しを拒む）を構文木で確かめるようにする。
- 判定そのもののテストは pathguard にある。ここに残すのはアダプタのテスト（`_meta` の取り出し、
  エラーの写し、守る場所、ゼロ値が拒むこと）と、既存の契約テストである。

## Consequences

`load_data` と `work_dir` の検査が変わる（CHANGELOG に書く）:

- **新たに拒む**: ランタイムと同じ一覧のうち、自分のホームにある本物の場所
  （`~/.kube`、`~/.config/gh`、`~/.azure`、`~/.terraform.d`、`~/.gemini`、`~/.config/mcp-bridge`、
  `~/.netrc`、`~/.npmrc`、`~/.pypirc`、`~/.git-credentials`、`~/.vault-token`、`~/.docker/config.json`、
  `~/.claude.json`、`~/.bash_history`、`~/.zsh_history`）。床のどの場所についても、大文字小文字の違い・
  リンク・ファームリンクなど、あらゆる綴り。それらのディレクトリの直下にあるリンクの指す先（同期フォルダへの
  リンクになった `~/.ssh/config` なら、その指す先のファイル）。`$HOME` がアカウントのホームと違うときは、
  両方を守る。Linux の `/etc` を `work_dir` にすること。
- **新たに通す**: `.env.example`、`.env.sample`、`.env.template`、`.env.dist`（ひな形であって秘密ではない）。
- **ホームが分からなければ、`load_data` のパスもどの `work_dir` も拒む**。以前はすべてを通していた。
- `work_dir_denied` の `details` に `reason` が加わる。
- 1 回の検査は約 2 ms（pathguard の実測）。読み込みの時間に比べて無視できる。

写しを持たないので、判定の修正は pathguard のリリースと、ここでの依存の更新 1 行になる。

## Amendment (2026-09-22): 実際に使うディレクトリも判定する

`work_dir` だけを検査していたので、`work_dir=~/.config` と `workspace_id=gh` でワークスペースが `~/.config/gh`
になり、コンテナの `/work` としてマウントされた（`load_from_work`・`query_data`・スクリプトがそこの資格情報を
読んで返せ、`delete_workspace` は消せた）。ADR-0011 の頃からの穴で、image-forge の独立レビューで見つかった。
`workspace.NewManager(cfg, podman, check)` は判定を必須の引数として受け取り、`Ensure`・`PreviewDelete`・`Delete` は
`<work_dir>/<workspace_id>` を最初に `workdir.Resolver.CheckBeneath`（pathguard v0.2.0）で判定する。サーバーは
`tools.WorkspaceCheck` を渡し、配線は `newWorkspaceManager` 1 か所。判定の無い Manager はすべてのワークスペースを
拒む。pathguard v0.2.0 は NUL バイトを含むパスも拒む。

## Amendment (2026-09-22, v0.8.1): ファイルの有無で答えを変えない

`load_data` の `file_path` は `filepath.EvalSymlinks` で解決してから床に掛けていた。そのため資格情報の位置に
あるファイルは、あれば `path_not_allowed`、無ければ `invalid_arguments` になり、答えがどの秘密が存在するかを
呼び出し側に教えていた。`attach_files` は `/work` 内のファイルに床を掛けておらず、そこにある `.env` や、
ワークスペースがその同期フォルダ内にあるときの `~/.ssh` 内のリンクの行き先を、あれば返していた（拡張子が
テキストか画像のもの — `.txt`・`.json`・`.png` など — は中身をそのまま、それ以外は大きさ・時刻・ハッシュ）。
slack-mcp-extender と chrome-pilot-mcp の独立レビューで見つかった型で、ここでは HOME を一時ディレクトリにした
テストで実測した（`load_data` は 7 組すべて、`attach_files` は 4 組中 2 組で答えが違った。仕掛けたリンクで
外へ出る 2 組は `os.Root` が存在に関係なく拒んでいた）。

- `ResolveInput` はまず置き場所を決め（`workdir.Where` = pathguard の `Forms` の末尾。リンクはすべて辿り、
  宙に浮いたリンクはその先で。存在するパスなら `EvalSymlinks` と同じ）、渡された綴りと置き場所の両方で床に
  掛けてから、置き場所に対して存在を問う（`EvalSymlinks(where)`）。解決先が置き場所と違えば（その間に
  変わった）、そこでもう一度判定する。
- 拒否が名指すのは渡されたとおりのパスだけで、details から `resolved` を外した。置き場所は途中の項目がリンクなら
  変わる（同期フォルダへの `~/.ssh/config`、dotfiles へリンクした `~/.aws`）ので、宙に浮いたリンクと項目なしで
  値が違い、どの項目があってどこを指すかを示していた（この変更の独立レビューが見つけた）。
- `attach_files` は各パスをその置き場所で床に掛けてから `root.Stat` する。読むのは従来どおり `os.Root` 経由。
- 解決できないパスに専用の分岐を作らない。
- `TestExistenceIsNotRevealedByLoadData` と `…ByAttachFiles` は、同じパスをファイルがある状態と消した状態で
  呼び、答え全体を比べる（リンクである資格情報の項目と、リンクである資格情報ディレクトリを含む）。6 つの変異
  （判定の順序を戻す・`load_data` の床を外す・綴りから辿り直す・`attach_files` の床を外す・置き場所を決めない・
  拒否に置き場所を戻す）はすべてアサーションで落ちた。
- 限界: `attach_files` の床は `/work` の境界ではない。`execute_code` は `/work` をすべて読めるので、保護された
  場所を**含む**ワークスペース（`~/.ssh` 内のリンクが指す同期フォルダなど）はサンドボックスに見えたままになる。
- pathguard 側の既知の限界（次のリリースに向けて記録）:
  - 資格情報ディレクトリの項目を通って `..` で抜けるパス（パス自体でも、仕掛けたリンクの行き先でも）は、通った場所
    ではなく行き着く場所で判定されるので、その項目がリンクか・行き先がどこかが答えに出うる。pathguard が判定するのは
    Clean した形で、歩いた途中のディレクトリではない。
  - 置き場所は pathguard の形の末尾。リンクの連鎖が既に出た綴りに戻ると、それは終点ではなく途中の段になる。どの段も
    判定済みなので判定していないものは開かないが、その経路のファイルは見つからないと答えるか、途中の段から読まれうる。
    pathguard は終点を返さない。
  - `work_dir` は pathguard/workdir が組織 ADR-022 §4 の順序（not found が denied より先）で検証するので、資格情報の
    ディレクトリを指す `work_dir` は、存在するかどうかで答えが変わる。
  - 非 ASCII 名のリンク先を別の Unicode 正規化で綴ると、同一性で拒むのはそれが存在するときだけになる（pathguard は
    正規化しない）。別の場所に作ったハードリンクを拒むのは、床の場所そのものであるファイル（`~/.netrc`、
    `~/.docker/config.json` など）へのものだけで、それも存在するときだけ。資格情報ディレクトリの中のファイル
    （`~/.ssh/id_rsa`）や `.env` へのハードリンクは拒まない —— ディレクトリはそれ自身の同一性で比べ、中のファイルでは比べない。
- 判定と読み取りは 2 段で、その間にすり替えたリンクは辿られる（check-to-use の競合。ここでは閉じていない。閉じるには、
  開いたものを記述子から判定する必要がある）。

## References

- 組織 ADR-021（ファイル渡し MCP サーバーの work dir 契約）
- ADR-0011（work dir 契約）: 検証の閉じた一覧と、読み取りのブラックリスト —— その実装をここで置き換える
- nlink-jp/pathguard の RFP（`docs/ja/pathguard-rfp.ja.md`）
