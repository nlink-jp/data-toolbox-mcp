# ADR-0008: attach_files — a dedicated tool for returning artifacts as MCP content

- Status: Accepted
- Date: 2026-06-06
- Driver: magi
- Generalises to: other MCP servers that need to surface artifacts to the LLM

---

## Context

In v0.2.1 we added `host_work_dir` to the `execute_code` and `list_workspaces` results so the LLM could tell the user the **absolute host path** of generated artifacts. Real-machine verification on 2026-06-06 (Claude Desktop) surfaced a remaining friction:

- To **inline-display** a generated PNG in the chat, Claude Desktop requires the user to have connected the folder containing the file as a "connected folder."
- Without that, the LLM falls back to "I saved it to `{host_work_dir}/sales.png`, open it in Finder," and the interaction loses a beat.
- The MCP protocol itself allows `tools/call` to return **multiple content blocks of mixed type** (text / image / resource) in `result.content`. MCP clients (Claude Desktop) inline-render image content.

So "tell the user where the file is" (solved by v0.2.1) and "actually return the file" (this ADR) are separate concerns.

## Decision

Add a new tool, **`attach_files`**.

### `attach_files`

- **Arguments**: `{workspace_id: string, paths: [string]}`
  - Each `paths` entry is either a container-absolute path under `/work/...` (e.g. `"/work/sales.png"`) or a path relative to `/work` (e.g. `"sales.png"`).
  - Between 1 and 16 entries.
- **Result**: a rich `result.content` array:
  - First block: a text content with a summary of the N files attached, their on-host locations, and detected kinds.
  - Subsequent blocks: dispatched by file extension into image / text / metadata-only:

| Extension (lowercase) | Block kind | Detail |
|-----------------------|------------|--------|
| `.png` `.jpg` `.jpeg` `.gif` `.webp` `.bmp` | MCP image content | `{type: "image", data: <base64>, mimeType: "image/png"}` etc. Client renders inline |
| `.svg` | MCP image content | `mimeType: "image/svg+xml"` |
| `.csv` `.tsv` `.json` `.jsonl` `.ndjson` `.txt` `.md` `.log` `.yaml` `.yml` `.toml` | MCP text content | `{type: "text", text: "...file body..."}`. Over the size cap, head + tail ellipsis |
| Other (`.parquet` `.pdf` `.zip`, ...) | Metadata-only | A text content with `"file at <host_work_dir>/<name>, <size> bytes, sha256: <hash>"`. No base64 embedding |

### Size caps (initial)

- **Per-file: 10 MiB** (10 × 1024 × 1024 bytes)
- **Total response: 20 MiB**
- On overflow:
  - Per-file overflow: that file is downgraded to metadata-only
  - Total overflow: process in order, files past the cap are downgraded to metadata-only
- Configurable via `config.toml` `[attach] max_single_size_bytes` / `[attach] max_total_size_bytes` (defaults as above).

### Path resolution

- If a `paths` entry starts with `/work/`, treat it as a path under `<host_work_dir>`.
- Otherwise, treat it as relative to `<host_work_dir>` (so "sales.png" also works).
- After resolution, re-verify with `filepath.Clean` + prefix check that the absolute path is in the `<host_work_dir>` subtree (path-traversal defense-in-depth — same pattern as `delete_workspace` in ADR-0006).

### MCP protocol path

- The current `mcpserver.handleToolsCall` always **JSON-marshals the handler's return value and wraps it in a single text content block**.
- Extend it: if the handler returns a new **`mcpserver.RawResult`** value, take its content blocks verbatim instead.
- Existing tools (load_data, etc.) keep returning a single text content block — backward compatible.

## Consequences

**Positive:**

- Claude Desktop can inline-display plots **with no connected-folder setup** — a major UX win (the LLM itself flagged this as the top Phase 2 follow-up).
- The LLM **explicitly chooses what to return**, avoiding the pitfalls of auto-scanning or monkey-patching (no accidental file leaks, no change to `execute_code`'s semantics).
- Non-PNG text returns (CSV snippets, generated markdown) go through the same tool — usable for "give the user this table" or "show this report" too.
- The metadata-only fallback keeps parquet/PDF/zip artifacts visible ("here's where it is, here's how big it is") without bloating the response.

**Negative:**

- Tool surface grows 6 → 7. LLM inputSchema-recognition load increases slightly.
- The MCP-content array gets assembled across multiple blocks; `internal/mcpserver` gains a small branch in result handling.
- Base64-encoding large files adds response latency (rough est. ~30ms per 10 MiB on M-series Macs — acceptable).
- Extension-based dispatch means a binary named `.csv` or a text file named `.png` won't behave intuitively, but the inputs come from the sandbox so deliberate abuse isn't really a vector.

## Alternatives Considered

### A1: `execute_code` auto-scans `<host_work_dir>` and returns new files

- Pros: no extra API protocol for the LLM
- Cons: **returns files we didn't intend** (prior plots, user-placed images), needs filename/mtime heuristics, response size becomes unpredictable
- Rejected because explicit selection wins on predictability and auditability

### A2: Sentinel comment / env var in the Python code declares the return set

- Pros: stays within `execute_code`, no new tool
- Cons: forces the LLM to memorize a **bespoke syntax**, the parser becomes a leaky contract, hard to extend
- Rejected because an explicit independent tool aligns better with MCP design

### A3: Monkey-patch `savefig` in the runtime to auto-collect outputs

- Pros: plain `plt.savefig("/work/foo.png")` "just works"
- Cons: **opaque side effects** are hard to debug, doesn't cover non-matplotlib outputs (Pillow saves, polars `write_csv`), erodes the sandbox boundary
- Rejected — transparent monkey patches conflict with this project's predictability priority

### A4: Split into `attach_image` + `attach_text`

- Pros: explicit APIs, simpler per-tool schemas
- Cons: tools 6 → 8, LLM has to dispatch by extension itself, responsibility is essentially the same
- Rejected — one tool with extension-based dispatch is a cleaner UX

## See also

- ADR-0006 (v0.2.1 amendment): how `host_work_dir` solved "tell where the file is"
- ADR-0009: the dual problem — load files directly from sandbox `/work` into DuckDB (`load_from_work`)
- Memory: `feedback_structured_mcp_tool_errors` (attach failures also return `{code, message, details}` JSON)
