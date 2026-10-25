# Pinned by digest, not by tag: a tag moves, and what it moves to was never
# checked by anyone. This is the multi-arch index digest (amd64 and arm64
# both resolve through it). A new base reaches the image only when this line
# is changed and released (ADR-0005, amended v0.8.4).
FROM python:3.12-slim@sha256:a0f57064cbdcd6285fa45f51237ab0b35ea8ebaed06a1f5973e89e19dfb86704

# OS packages:
#   fonts-noto-cjk  — CJK glyph rendering for matplotlib (ADR-0007)
#   ca-certificates — TLS roots, harmless under network=none, needed when
#                     [container.limits] network is flipped to "bridge"
#
# Upgrade before installing: installing the two packages above never touches
# what the base already contains. apt verifies the Debian archive's
# signatures, so this takes Debian's security fixes without trusting an
# unverified download (ADR-0005, amended v0.8.3 and v0.8.4).
RUN export DEBIAN_FRONTEND=noninteractive \
    && apt-get update \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends \
        fonts-noto-cjk \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Python data-analysis stack + plotting (ADR-0003 + ADR-0007).
#
# Installed only from requirements.lock: every package, dependencies
# included, at one version, checked against its SHA256. --require-hashes
# refuses anything whose hash is not in the lock, and --no-deps keeps pip from
# fetching a dependency the lock does not name. The lock is regenerated from
# requirements.in with `make runtime-lock`, reviewed, and released — a rebuild
# never resolves versions by itself (ADR-0007, amended v0.8.4).
COPY requirements.lock /tmp/requirements.lock
RUN pip install --no-cache-dir --require-hashes --no-deps -r /tmp/requirements.lock \
    && rm -f /tmp/requirements.lock

# Fail the build if the locked combination does not work: exercise the paths
# the tools use (parquet in and out, DataFrame <-> Arrow, DuckDB over both, a
# matplotlib PNG through Pillow). This is the check a regenerated lock must
# pass before it is released.
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
