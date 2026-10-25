# ADR-0007: Extend runtime container package scope (fonts + plotting tools)

- Status: Accepted
- Date: 2026-06-05
- Driver: magi
- Revisions: 2026-10-25 — pyarrow takes a floor (`>=18.0`) instead of `~=18.0`; Brotli copies not yet fixed upstream recorded (v0.8.3)
- Generalises to: other Python-sandbox projects in nlink-jp (mcp-skeleton derivatives, etc.)

---

## Context

The v0.1.0 runtime container:

```dockerfile
FROM python:3.13-slim
RUN pip install --no-cache-dir \
      duckdb~=1.1 pandas~=2.2 polars~=1.8 pyarrow~=18.0
```

This handles "load data + SQL + tabular Python analysis" fine. But **plotting fails** at `import matplotlib` with `ImportError`, and **even if matplotlib were present, Japanese labels would render as ☐☐☐ tofu**. The typical LLM-driven data-analysis loop — "visualize the result" — is broken.

The predecessor [shell-agent-v2](https://github.com/nlink-jp/shell-agent-v2) (`app/internal/sandbox/imagebuild/bundle.go`) already solved this:

1. **`python:3.12-slim`** as the base (proven stable, not the newer 3.13)
2. **`fonts-noto-cjk` + `fonts-noto-cjk-extra`** via `apt install` (essential for CJK glyph rendering)
3. **`/etc/matplotlib/matplotlibrc`** auto-placed:
   ```
   font.family: sans-serif
   font.sans-serif: DejaVu Sans, Noto Sans CJK JP, Arial, Liberation Sans
   axes.unicode_minus: False
   ```
   Caveat: matplotlib 3.10's Agg backend renders **all** text with the first font that loads (no per-glyph fallback). Putting `DejaVu Sans` first triggers `UserWarning: Glyph ... missing` on CJK characters, so this project flips the order and puts `Noto Sans CJK JP` first (it covers Latin glyphs too, so there's no side effect).
4. **`MATPLOTLIBRC=/etc/matplotlib/matplotlibrc`** pinned via env var
5. **`matplotlib` + `numpy` + `scipy` + `scikit-learn` + `graphviz`** via pip

See shell-agent-v2 ADR-0004 and `history/sandbox-image-build.md` for the background.

## Decision

v0.2.0 adopts:

### 1. Switch Python base image to `python:3.12-slim`

`3.13-slim` → `3.12-slim`. Proven in shell-agent-v2. The 3.12-vs-3.13 feature delta is irrelevant for this project's use cases.

### 2. Add OS packages (apt)

```
fonts-noto-cjk         # essential: CJK label rendering
ca-certificates        # for TLS when network=bridge (insurance for future pip installs)
```

`fonts-noto-cjk-extra` adds rare-character / variant coverage but >50MB; skip — regular CJK characters (the JIS L1/L2 range) work with `fonts-noto-cjk` alone.

### 3. Add Python packages (pip)

```
matplotlib~=3.10       # the de facto plotting library
Pillow~=11.0           # matplotlib's dependency, plus standalone image-processing use cases
```

Matplotlib pulls Pillow in as a dep automatically, but pinning it explicitly makes intent visible.

### 4. Ship matplotlib configuration

Adopt the shell-agent-v2 pattern but **flip the font order to put CJK first**:

- `/etc/matplotlib/matplotlibrc` with
  ```
  font.family: sans-serif
  font.sans-serif: Noto Sans CJK JP, DejaVu Sans, Arial, Liberation Sans
  axes.unicode_minus: False
  ```
- `MATPLOTLIBRC=/etc/matplotlib/matplotlibrc` env var

**Why CJK-first**: matplotlib 3.10's Agg backend renders all text with the first loadable font in `font.sans-serif` (no per-glyph fallback). With `DejaVu Sans` first, CJK characters produce `UserWarning: Glyph 37329 (\N{CJK UNIFIED IDEOGRAPH-91D1}) missing from font(s) DejaVu Sans`. `Noto Sans CJK JP` covers Latin glyphs too, so putting it first has no side effect on English labels.

This way, `import matplotlib.pyplot as plt; plt.title("売上推移")` renders Japanese with no extra setup.

**Real-machine verification (2026-06-05)**: `python -W error::UserWarning -c "...savefig with Japanese labels..."` completes with no UserWarning.

### 5. Backend stays at Agg (default)

The container is headless; `matplotlib.use("Agg")` is unnecessary (slim images lack Tk/Qt, so Agg auto-selects). If an LLM accidentally calls `plt.show()` it is a no-op. Output flows through `plt.savefig("/work/foo.png")` to the host.

### 6. Explicitly out of scope for v0.2.0

| Package | Reason |
|---------|--------|
| `numpy` | pandas / matplotlib pull it as a dep. No explicit pin needed. |
| `scipy` | +60MB, stats/linear algebra. Defer until demand surfaces (separate ADR). |
| `scikit-learn` | +80MB, ML. Defer (separate ADR). |
| `seaborn` | depends on matplotlib. Reachable via `pip install` under `network=bridge`. Pin-management cost outweighs ambient demand. |
| `plotly` | +30MB, interactive HTML. No browser path from MCP, so the benefit is small. |
| `graphviz` (apt + pip) | shell-agent-v2 ships it for `.dot` diagrams; this project is data-analysis-focused and doesn't need it. |
| `openpyxl` / `xlsxwriter` | Excel I/O. pandas has its own readers for basic Excel input. Defer (separate ADR). |

### 7. Image size budget

v0.1.0: 692MB → v0.2.0 estimate: 850–900MB (`fonts-noto-cjk` ~150MB + matplotlib + Pillow + deps). **<900MB** is the budget line. If it exceeds, the levers are: trim `fonts-noto-cjk` to a subset, or skip matplotlib font-cache pre-warming.

## Consequences

**Positive:**

- LLM can plot with matplotlib via `execute_code` → completes the typical data-sandbox loop.
- Japanese labels render with no special setup → removes friction for the project's primary user audience (Japanese users).
- Borrowing shell-agent-v2's solution avoids re-inventing the "Japanese fonts in matplotlib" problem.
- Plots land at `/work/<filename>.png` and surface to the host via the existing mount design — no architecture change needed (architecture.md §3 / §4 unchanged).
- Sharing the `python:3.12-slim` base across nlink-jp's sandbox projects creates layer-cache reuse opportunities.

**Negative:**

- Image grows from 692MB to ~850–900MB (~30%). First-time `build-runtime` goes from 1–2 minutes to 2–3 minutes.
- One additional `apt install` step in the Dockerfile increases complexity (but shell-agent-v2 prior art means maintainers won't get lost).
- Python 3.13 → 3.12 downgrade: user code can't use 3.13-only syntax (generator-expression rule relaxations, etc.). Practically zero impact.
- Future requests to add scipy / scikit-learn / seaborn each incur a fresh ADR because v0.2.0 says no to everything else.

**Amendment (2026-10-25, v0.8.3): pyarrow takes a floor.**

- CVE-2026-111497 is an out-of-bounds write in the Brotli decoder (affected 1.0.3–1.2.0). `load_data` reads user-supplied `.parquet` through DuckDB's `read_parquet`, and `execute_code` can read one with pyarrow or pandas, so a Brotli-coded parquet reaches a decoder.
- Arrow 18 bundles Brotli 1.0.9 (`cpp/thirdparty/versions.txt` at `apache-arrow-18.1.0`). Apache Arrow ships fixes only in new major versions — the Brotli update (apache/arrow#55167, unmerged on 2026-10-25) targets main — so `pyarrow~=18.0` could never take it. pyarrow is now `>=18.0`, and a rebuild takes whatever major Arrow has released. At v0.8.3 that is 26.0.0, which is not yet fixed either; the floor is what lets the next rebuild take the fix.
- The other packages keep `~=`. pandas 3, polars 2, DuckDB 2 and Pillow 13 were all on PyPI on 2026-10-25; moving to any of them changes the API that LLM-written analysis code uses and is not a patch-release change. pyarrow is consumed through stable interfaces (DataFrame ⇄ Arrow, parquet I/O): with pyarrow 26.0.0, pandas 2.3.3, polars 1.44.2 and DuckDB 1.5.6, `pip check` is clean and parquet / DataFrame / DuckDB round-trips behave as with pyarrow 18.1.0 (measured in both images).
- **Not fixed by this release:** DuckDB vendors Brotli 1.1.0 in `third_party/brotli` for parquet (v1.1.3, v1.4.1 and v1.5.1 alike); the fix (duckdb/duckdb#27791) was unmerged on 2026-10-25. `duckdb~=1.1` already floats to the newest 1.x, so a rebuild after a fixed 1.x release takes it. Pillow 11.3.0 bundles `libbrotlidec` 1.1.0 for WOFF2 fonts; this image renders only the installed Noto CJK fonts, so the path is not reached in practice, and whether a newer Pillow bundles a fixed Brotli was not checked.

## Alternatives Considered

### A1: Status quo (no matplotlib, no fonts)

- Pros: image stays small, Dockerfile simple
- Cons: claiming "data analysis" while you cannot visualize is incomplete
- Rejected because the absent feature is foundational, not optional

### A2: matplotlib only, no fonts

- Pros: minimal image growth (+50MB)
- Cons: Japanese labels become tofu (☐☐☐), and every LLM output requires a "font problem" explanation forever
- Rejected because the friction is unacceptable for Japanese users

### A3: matplotlib + scipy + scikit-learn + seaborn + plotly

- Pros: full data-science kit
- Cons: image exceeds 1.5GB, first build >5 minutes, pin-management surface explodes, security-patch follow-up grows
- Rejected because predictively bundling packages without proven demand is bloat. Adding via separate ADRs on demand is healthier.

### A4: Mirror shell-agent-v2 exactly (numpy + scipy + scikit-learn + graphviz)

- Pros: full borrow, less testing
- Cons: graphviz is off-scope, scipy/scikit-learn add +200MB without clear demand, pushing v0.1.0's 692MB past 1GB
- Rejected because shell-agent-v2's use case is different (LLM-in-the-loop GUI vs. MCP tool surface). Keep this project to a "minimal visualization set": fonts + matplotlib + Pillow.

### A5: Let users define matplotlib config / packages in config.toml

- Pros: users can declare their own fonts and packages
- Cons: design balloons, dynamic Dockerfile assembly conflicts with `go:embed` Dockerfile (ADR-0005), base-image-rebuild detection becomes complex
- Rejected because it breaks the Phase 0 covenant that the Dockerfile is a frozen `go:embed` asset (ADR-0005). New packages go through ADRs.

## See also

- ADR-0003: Python-only in-container runtime
- ADR-0005: Local-build distribution of the runtime container image
- shell-agent-v2 `docs/en/history/sandbox-image-build.md`
- shell-agent-v2 ADR-0004: Sandbox UID mapping (background for `--userns keep-id`)
- Memory: `feedback_security_first` (adding packages widens the security surface; keep scope narrow)
