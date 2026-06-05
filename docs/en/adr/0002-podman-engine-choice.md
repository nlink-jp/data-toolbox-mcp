# ADR-0002: Adopt Podman as the container engine without an abstraction

- Status: Accepted
- Date: 2026-06-05
- Driver: magi
- Generalises to: none

---

## Context

Safely running user (LLM) code through `execute_code` requires a sandbox isolated from the host. The predecessor `shell-agent-v2` defines an `Engine` interface in `app/internal/sandbox/engine.go` and supports both Podman and Docker via `resolveEngine("auto"|"podman"|"docker")` inside `app/internal/sandbox/cli.go`'s `cliEngine`.

The abstraction comes at a cost:

- Engine-switching logic and the test matrix (Podman × Docker × macOS × Linux) balloons
- The engine layer has to absorb the Podman-specific `--userns keep-id:uid=1000` handling (shell-agent-v2 ADR-0004) versus Docker's `--user $(id -u)`
- Detecting Docker daemon liveness vs. Podman machine state requires per-engine logic

For `data-toolbox-mcp` Phase 1, the goal is to provide safe sandbox execution from MCP clients on a personal machine — engine choice breadth is not the value being delivered. Pinning one engine halves the test paths and considerably simplifies the code.

## Decision

Phase 1 pins **Podman**:

- Call `podman run / podman exec / podman stop / podman rm` directly via `exec.Command` (reusing the structure from shell-agent-v2 `internal/sandbox/cli.go`)
- **Do not** introduce an engine-abstraction interface (YAGNI)
- Do not surface an engine-selection flag in the config file (a future addition stays backward compatible)

Rationale:

- **rootless**: runs under a normal user. Strong host-side security boundary
- **daemon-less**: no long-running daemon, no systemd dependency
- **High CLI compatibility on macOS** once `podman machine` is up: instructions go in the README
- **Precedent inside nlink-jp**: shell-agent-v2 and cclaude (operational memory exists)

When multiple Docker-support requests appear, we will open a new ADR and reconsider engine abstraction.

## Consequences

**Positive:**

- Implementation and test surface shrink, making Phase 1 scope realistic
- Defaulting Podman's `network=none` gives a strong sandbox boundary by default
- Sharing the engine assumption with shell-agent-v2 lets us reuse remediation for known issues (memory `Podman Machine on macOS`, etc.)

**Negative:**

- Users with only Docker set up must install Podman separately
- On macOS, `podman machine init && podman machine start` is a prerequisite. The README and error messages must make "what happens when it isn't running" explicit
- Adding Docker support later requires refactoring the direct `exec.Command` calls into an Engine interface (by that point the implementation differences will be visible, so the validity of abstraction will be easier to judge)

## Alternatives Considered

### A1: Docker only

- Pros: wider user base; Docker Desktop / colima are common
- Cons: requires a daemon; not rootless (Docker Desktop is root inside the VM). Weaker sandbox boundary than Podman
- Rejected because it conflicts with the "security from Phase 1" stance (memory `feedback_security_first`)

### A2: Engine abstraction like shell-agent-v2 (Podman + Docker)

- Pros: user choice, easy migration
- Cons: as noted above, test surface and complexity roughly double — incompatible with Phase 1 scope
- Rejected because Podman alone gives a clearer review target when security is being built in alongside implementation

### A3: No container — subprocess + chroot/jail

- Pros: zero dependencies, lightweight
- Cons: chroot on macOS is limited, jail is FreeBSD only, raw Linux Namespaces are hard to maintain. Conflicts with the RFP requirement of "containerized virtual machine"
- Rejected because it doesn't meet the requirement

## See also

- `_wip/data-toolbox-mcp/docs/en/data-toolbox-mcp-rfp.md` §3 / §7-B
- shell-agent-v2 ADR-0004 (Sandbox UID mapping)
- Memory: `feedback_podman_machine` (known macOS issues)
- Memory: `feedback_security_first` (security goes in alongside the implementation)
