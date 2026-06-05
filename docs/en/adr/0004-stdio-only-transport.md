# ADR-0004: Support stdio transport only in Phase 1

- Status: Accepted
- Date: 2026-06-05
- Driver: magi
- Generalises to: none

---

## Context

The MCP protocol supports multiple transports:

- **stdio**: the client spawns the MCP server process and exchanges JSON-RPC over stdin/stdout
- **SSE / HTTP**: the client connects to an HTTP endpoint. Enables multi-client and remote hosting

The lab-series predecessor `mcp-skeleton` supports both (`server/app.py` and `server/sse.py`). However, the `data-toolbox-mcp` RFP §1 pins "use from MCP clients like Claude Desktop / Cursor over stdio, on a personal machine" as the primary scenario.

Supporting HTTP/SSE would require:

- Authentication / authorization (who can access)
- TLS configuration (certificate management, HTTPS listener)
- SSRF / request-smuggling defense
- More elaborate workspace concurrency control for multiple simultaneous clients
- Interop with reverse proxies / load balancers

This exceeds the scope and bandwidth of Phase 1.

## Decision

Phase 1 supports **stdio transport only**:

- Entrypoint is `data-toolbox-mcp serve` (or argument-less invocation → stdio mode)
- HTTP / SSE-mode flags are not implemented in Phase 1
- Borrow the pattern from mcp-guardian's `internal/transport/process.go` (`bufio.Scanner` with a 1MB buffer, reading one line at a time)

When HTTP/SSE support is added later, a separate ADR will weigh:

- Authentication scheme (API key / OAuth / mTLS)
- Workspace exclusion strategy
- Whether to front the server with mcp-guardian or have this server listen on HTTP directly

## Consequences

**Positive:**

- Auth, TLS, SSRF, and similar security concerns are deferred past Phase 1
- The mcp-guardian transport code is a usable reference; implementation feasibility is clear
- The test harness stays simple — shell mock server + `exec.Command`, reusing mcp-guardian's `proxy_test.go` pattern
- Focus stays on the primary scenario of running locally from Claude Desktop / Cursor

**Negative:**

- Team sharing / remote hosting is not supported. Team needs must wait until Phase 2
- A single-process stdio connection means multiple LLM clients can't use it simultaneously (the same client must switch `workspace_id` to use multiple workspaces)
- The "another process on the same machine accessing via MCP" use case forces each process to spawn its own server (which may conflict with DuckDB's single-writer constraint → handled by ADR-0001's serialization stance)

## Alternatives Considered

### A1: Both stdio and HTTP/SSE (the mcp-skeleton path)

- Pros: wider use cases, more future flexibility
- Cons: forces auth, TLS, and multi-client control into Phase 1. Far outside scope
- Rejected because the Phase 1 primary scenario is fixed (personal machine + stdio) and additional implementation ROI is low

### A2: HTTP/SSE only

- Pros: team-centric usage, monitoring, audit logs are easier to build
- Cons: Claude Desktop / Cursor — the primary MCP clients — are stdio-first. We'd miss the main users
- Rejected because it doesn't fit the primary scenario

## See also

- `_wip/data-toolbox-mcp/docs/en/data-toolbox-mcp-rfp.md` §1 / §3 / §7-A
- Memory: `feedback_mcp_no_protocol_cancel` (MCP has no cancellation notification)
- Memory: `feedback_child_process_exit_status` (child process exit status handling)
- Memory: `feedback_mcp_proxy_always_responds` (a relay must always answer)
- Reference implementation: `util-series/mcp-guardian/internal/transport/process.go`
