FROM python:3.12-slim

# OS packages:
#   fonts-noto-cjk  — CJK glyph rendering for matplotlib (ADR-0007)
#   ca-certificates — TLS roots, harmless under network=none, needed when
#                     [container.limits] network is flipped to "bridge"
RUN apt-get update && apt-get install -y --no-install-recommends \
        fonts-noto-cjk \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Python data-analysis stack + plotting (ADR-0003 + ADR-0007).
# Pillow is pulled in transitively by matplotlib, but pinned explicitly
# so describe_runtime can advertise it as guaranteed.
RUN pip install --no-cache-dir \
        duckdb~=1.1 \
        pandas~=2.2 \
        polars~=1.8 \
        pyarrow~=18.0 \
        matplotlib~=3.10 \
        Pillow~=11.0

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
