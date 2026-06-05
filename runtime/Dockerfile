FROM python:3.13-slim

RUN pip install --no-cache-dir \
      duckdb~=1.1 \
      pandas~=2.2 \
      polars~=1.8 \
      pyarrow~=18.0

RUN useradd -m -u 1000 toolbox
USER 1000:1000
WORKDIR /work
CMD ["sleep", "infinity"]
