# ADR-0003: Restrict the in-container runtime to Python only

- Status: Accepted
- Date: 2026-06-05
- Driver: magi
- Generalises to: none

---

## Context

The `language` parameter of `execute_code(workspace_id, language, code)` could accept Python / Bash / Node / R, etc. Multi-language support gives flexibility but at a price:

- Container image bloat (Node + Python + R could exceed 1.5GB)
- Per-language dependency version management and vulnerability tracking
- Larger security surface (every language runtime adds escape vectors)
- Documentation and test volume grows proportionally to language count

RFP §1 pins the "data analysis sandbox" as the primary scenario, and Python is the de facto choice for data analysis. This also matches the intent of bundling DuckDB (the `duckdb-python` API is mature).

## Decision

Phase 1 restricts the in-container runtime to **Python 3 only**:

- Base image: `python:3.13-slim` (stable)
- Bundled packages: `duckdb`, `pandas`, `polars`, `pyarrow` (the standard data-analysis stack)
- The `execute_code` `language` parameter is kept as a placeholder for future extension; Phase 1 rejects any value other than `"python"` with an explicit `unsupported_language` error
- Installing additional packages is not supported in Phase 1 (whether to allow post-startup `pip install` is undecided → recorded as an Open Question in the Phase 1 plan)

If future demand for R / Node appears, a separate ADR will decide between "pack multiple runtimes into one image" and "swap container images per language."

## Consequences

**Positive:**

- Container image stays around 500MB (slim base + key packages). Pull cost is acceptable
- The security surface is bounded to the Python runtime; vulnerability monitoring (e.g. pip-audit) is simpler
- No `language` dispatch implementation, simpler code
- The Python data-analysis ecosystem (DuckDB → pandas → polars → pyarrow) is in place, and LLMs write in patterns they already know

**Negative:**

- "Just want to curl something" / "preprocess with a shell one-liner" can't be done directly (must route through Python `subprocess.run`)
- Users wanting R for statistics (lme4 / brms etc.) are not covered. A separate R MCP server would be needed
- Node-based data-handling tools (puppeteer, playwright) won't work (→ already out of scope because `network=none` is the default)

## Alternatives Considered

### A1: Python + Bash

- Pros: direct shell one-liners
- Cons: filesystem probing / curl attempts via bash. Achievable via Python `subprocess` too, so not necessary
- Rejected because the security-surface increase outweighs the expressiveness gain

### A2: Python + Bash + Node

- Pros: front-end-style data processing, npm ecosystem
- Cons: Node security-advisory tracking, much bigger image
- Rejected because Node-essential cases in data analysis are rare and ROI is low

### A3: Python + Bash + R

- Pros: broader statistical analysis options
- Cons: R image is heavy (500MB+), and ABI compatibility issues with dependent libraries are common
- Rejected because R demand should be served by a dedicated MCP server

## See also

- `_wip/data-toolbox-mcp/docs/en/data-toolbox-mcp-rfp.md` §2 / §3
- Memory: `feedback_security_first` (shrink the security surface)
