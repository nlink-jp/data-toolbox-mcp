# ADR-0005: Distribute the runtime container image via local build

- Status: Accepted
- Date: 2026-06-05
- Driver: magi
- Revisions: 2026-10-25 — builds bypass the layer cache, pull a newer base, and upgrade the base's packages, so re-running `build-runtime` takes security updates (v0.8.3)
- Generalises to: other container-based tools in nlink-jp

---

## Context

The Python-only runtime container locked in by ADR-0003 runs on the end user's machine. The distribution options are:

- (A) **Registry push**: distribute via a registry like `ghcr.io/nlink-jp/data-toolbox-runtime:vX.Y.Z`
- (B) **Local build**: ship the Dockerfile with the project and have users `podman build` locally
- (C) **Image tar attachments**: attach a `podman save` tar to GitHub Releases

Registry push offers convenient distribution but adds cost:

- Credentials management for ghcr.io / Docker Hub (personal PATs that expire)
- Operationalizing image signing and vulnerability scanning (cosign, trivy)
- nlink-jp's release pipeline is unified around local builds (memory `feedback_no_github_actions_ci`) — registry push introduces a CI/CD dependency
- Pushing to a public registry creates an artifact that cannot be retracted (deleting later breaks downstream users)

Meanwhile, our runtime image is:

- Small in content (python:3.13-slim + 4 packages, around 500MB)
- Light to build (just `pip install`, taking 1–2 minutes)
- Composed of a base image and packages only — no proprietary binaries

So building locally on the user's machine is not very high friction.

## Decision

Phase 1 adopts **local-build distribution**, with the build itself driven by a **dedicated subcommand of the `data-toolbox-mcp` binary**:

- The runtime Dockerfile is **embedded into the `data-toolbox-mcp` binary via `go:embed`** (sparing users from managing a separate file)
- A **`data-toolbox-mcp build-runtime` subcommand** unpacks the embedded Dockerfile into a temp directory and runs `podman build -t localhost/data-toolbox-runtime:vX.Y.Z -t localhost/data-toolbox-runtime:latest`
- Companion commands: `data-toolbox-mcp doctor` (verify Podman state and presence of the runtime image), `data-toolbox-mcp version`
- Default `container.image` is `localhost/data-toolbox-runtime:latest`
- README spells out a four-step setup: (1) `make build` for the binary → (2) `data-toolbox-mcp build-runtime` for the runtime image → (3) `data-toolbox-mcp doctor` to verify → (4) wire it into Claude Desktop
- A `make runtime-image` target is provided as a developer-facing wrapper around `data-toolbox-mcp build-runtime` (optional)
- No registry-push targets in Phase 1

This design ships the MCP server, the build tool, and the diagnostic tool as "one binary, one version," internalizing version-consistency responsibility (memory `feedback_single_binary_subcommand`).

If, in Phase 2+, multiple users ask for lower setup friction, a separate ADR will revisit registry push. That ADR must require cosign signing and SBOM attachment.

## Consequences

**Positive:**

- No credentials management (PATs / accounts)
- Works in restricted-network environments (corporate LANs) as long as the initial `pip install` can complete
- Avoids creating an unretirable public artifact (eases changing project direction)
- Consistent with nlink-jp's local-build approach
- The Dockerfile is right there for users to read, making the contents transparent

**Negative:**

- Setup is two steps (build + image-build)
- The first build needs network access for `pip install` plus a couple of minutes
- Podman build-cache management becomes the user's responsibility
  - **Amendment (2026-10-25, v0.8.3): the build no longer uses the cache, and re-running `build-runtime` is how security updates are taken.** Through v0.8.2 `build-runtime` ran a plain `podman build`. The embedded Dockerfile does not change between rebuilds, so the apt and pip layers came from the cache and nothing was updated; and because the base is the tag `python:3.12-slim`, podman kept the copy it had pulled first (on the maintainer's machine, three months old — Python itself included). After CVE-2026-111497 (Brotli decoder out-of-bounds write) a rebuild could therefore never take a fix. The build is now `podman build --no-cache --pull=newer`, and the Dockerfile runs `apt-get upgrade -y` before its install, because installing two packages never upgrades what the base already contains. Measured after the change: the base was refreshed to a 4-day-old image, and `apt list --upgradable` in the rebuilt image lists nothing. `TestBuildArgsBypassCacheAndRefreshBase` and `TestDockerfileUpgradesBaseBeforeInstall` hold both
  - Every rebuild now takes the network for the base pull as well as `pip install` (which already needed it), and gets a new image ID; the previous image stays behind untagged until `podman image prune`. The layer-cache sharing that ADR-0007 lists as a positive no longer applies to this image's own layers
- Keeping versions aligned between `make build` and `make runtime-image` becomes the user's responsibility (the README must establish the convention)

## Alternatives Considered

### A1: Push to ghcr.io

- Pros: users only need `podman pull`
- Cons: credentials ops, mandatory signing/SBOM, retirement cost, CI/CD dependency
- Rejected because these costs don't fit Phase 1 scope and conflict with nlink-jp policy (`feedback_no_github_actions_ci`)

### A2: Push to Docker Hub

- Pros: more familiar than ghcr
- Cons: rate limits, organization-plan costs, same operational burden as A1
- Rejected for the same reasons as A1, plus Docker Hub rate limits can hurt personal-machine usage

### A3: Image tar attached to GitHub Releases

- Pros: not pull-in-one-step, but no registry credentials needed
- Cons: Release assets take ~500MB; extra `podman load` step; multi-arch becomes complex
- Rejected — the benefit is thin and user burden is no lower than local build

### A4: Both (local build + optional registry push)

- Pros: maximum user choice
- Cons: documentation, test, and release-pipeline branching
- Rejected — Phase 1 stays focused on local build, and registry can be reconsidered when there's pressure

## See also

- ADR-0003: Python-only in-container runtime
- Memory: `feedback_no_github_actions_ci` (local-build policy without CI)
- `_wip/data-toolbox-mcp/docs/en/reference/phase1-plan.md` Track E
