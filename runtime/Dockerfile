FROM python:3.12-slim

# OS packages:
#   fonts-noto-cjk  — CJK glyph rendering for matplotlib (ADR-0007)
#   ca-certificates — TLS roots, harmless under network=none, needed when
#                     [container.limits] network is flipped to "bridge"
#
# Upgrade before installing: installing the two packages above never touches
# what python:3.12-slim already contains, so without it a rebuild keeps the
# base's libraries at whatever versions the pulled image had (ADR-0005,
# amended v0.8.3).
RUN export DEBIAN_FRONTEND=noninteractive \
    && apt-get update \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends \
        fonts-noto-cjk \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Python data-analysis stack + plotting (ADR-0003 + ADR-0007).
# Pillow is pulled in transitively by matplotlib, but pinned explicitly
# so describe_runtime can advertise it as guaranteed.
#
# pyarrow and Pillow take a floor, not a compatible-release pin: both ship
# fixes only in their newest release line, so ~=18.0 / ~=11.0 could never take
# one (CVE-2026-111497 in Arrow's bundled Brotli; CVE-2026-25990, fixed in
# Pillow 12.1.1). ADR-0007, amended v0.8.3.
RUN pip install --no-cache-dir \
        duckdb~=1.1 \
        pandas~=2.2 \
        polars~=1.8 \
        'pyarrow>=18.0' \
        matplotlib~=3.10 \
        'Pillow>=12.1.1'

# A floor lets a rebuild resolve a combination nobody has run, and pip check
# does not catch a pyarrow that pandas 2.x no longer works with. Fail the build
# rather than ship it: exercise the paths the tools use (parquet in and out,
# DataFrame <-> Arrow, DuckDB over both, a matplotlib PNG through Pillow).
RUN pip check && python -c "\
import duckdb, pandas as pd, polars as pl, pyarrow as pa, pyarrow.parquet as pq; \
import matplotlib; matplotlib.use('Agg'); import matplotlib.pyplot as plt; from PIL import Image; \
pq.write_table(pa.table({'a': [1, 2, 3]}), '/tmp/c.parquet', compression='brotli'); \
assert duckdb.sql(\"SELECT sum(a) FROM read_parquet('/tmp/c.parquet')\").fetchone()[0] == 6; \
df = pd.read_parquet('/tmp/c.parquet'); assert pa.Table.from_pandas(df).num_rows == 3; \
assert duckdb.sql('SELECT count(*) FROM df').fetchone()[0] == 3; \
assert pl.read_parquet('/tmp/c.parquet').height == 3 and pl.from_arrow(pq.read_table('/tmp/c.parquet')).height == 3; \
plt.plot([1, 2]); plt.savefig('/tmp/c.png'); assert Image.open('/tmp/c.png').size[0] > 0" \
    && rm -f /tmp/c.parquet /tmp/c.png

# Pin matplotlib's font fallback chain so Japanese labels render with
# Noto Sans CJK JP without any user setup. CJK font MUST be first: matplotlib
# uses the first font that loads for ALL text (no per-glyph fallback in the
# Agg backend), and Noto Sans CJK JP also covers Latin glyphs.
RUN mkdir -p /etc/matplotlib && \
    printf 'font.family: sans-serif\nfont.sans-serif: Noto Sans CJK JP, DejaVu Sans, Arial, Liberation Sans\naxes.unicode_minus: False\n' \
        > /etc/matplotlib/matplotlibrc
ENV MATPLOTLIBRC=/etc/matplotlib/matplotlibrc

RUN useradd -m -u 1000 toolbox
USER 1000:1000
WORKDIR /work
CMD ["sleep", "infinity"]
