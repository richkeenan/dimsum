# dimsum

A hand-built, performance-focused DNS ad blocker in Go, with a React, shadcn/ui, and Tailwind administration interface.

**Status: implementation paused at the reviewed task-11 checkpoint, awaiting owner instruction.** The service supports validated UDP/TCP forwarding, filtering/list updates, watched configuration, local DNS, background client naming, and upstream health/fallback. The byte-bounded cache is implemented as a library but is not yet connected to the resolver. Statistics, full CLI/API, frontend, packaging, and release qualification remain pending. See [implementation status](docs/implementation-status.md) and the [protocol support and test-port guide](docs/milestone-a-forwarding.md).

## Read in this order

1. [Product scope and architecture](docs/01-product-and-architecture.md)
2. [Research: existing DNS implementations](docs/02-dns-implementation-research.md)
3. [Your network: Pi-hole observations](docs/03-network-assessment.md)
4. [DNS engine, cache, and memory layout](docs/04-dns-engine-and-performance.md)
5. [Filtering, lists, and custom DNS](docs/05-filtering-and-configuration.md)
6. [Statistics and storage decision](docs/06-statistics-and-storage.md)
7. [Web application and API](docs/07-web-and-api.md)
8. [Testing, benchmarks, and rollout](docs/08-testing-and-rollout.md)
9. [Implementation plan](docs/09-implementation-plan.md)
10. [Sources and decision register](docs/10-sources-and-decisions.md)
11. [Agent-first control, configuration, and deployment](docs/11-agent-control-and-deployment.md)

## Recommended direction

Build an independently owned Go forwarding/filtering engine, rather than a Pi-hole wrapper or a new recursive resolver. **Forwarding to configurable upstream DNS providers is owner-approved.** Use compact immutable filtering indexes, byte-oriented cache storage, bounded request and statistics queues, and a separate control path. Ship one Go binary containing the compiled web assets; Node is a build dependency only.

The primary machine is a **Raspberry Pi 4 Model B with 4 GB RAM and a 64-bit OS**, confirmed through the existing Pi-hole host. The initial compatibility workload is its current filtering setup, including stale caching, exact rules, an allow-regex, CNAME inspection, and modern HTTPS/SVCB queries.

The first release replaces that Pi-hole for technically comfortable users. The same portable application supports shared Linux arm64/amd64 machines, including Intel servers and private cloud instances. Package a native Linux service, standalone binary, and Docker/Compose deployment; use GoReleaser for release automation. Native service installation is the recommended primary experience.

**Agents are first-class users:** laptop-based agents use SSH and a discoverable CLI with structured output to do everything the UI can do. Configuration is authoritative, comment-preserving, Git-friendly text with automatic validated reload, optional grouped changes, and protection against stale UI saves. dimsum contains no AI; MCP is deferred.

The desktop-first dashboard emphasizes metrics, top ten clients by request count, and top ten blocked exact domains. Friendly client names are a priority and are discovered asynchronously where possible, with manual overrides.

First release uses one network-wide policy, one administrator, and LAN/private-network access. Home Assistant integration is excluded. Per-device policies, encrypted upstreams, general conditional forwarding, general migration tooling, multi-instance management, and upgrade machinery are deferred. Configuration backup and a focused Pi-hole move are sufficient initially.

The owner-approved statistics default is **SQLite WAL with asynchronous batched writes, seven days of detailed history, and longer-lived aggregate charts**. Retention remains configurable.

The owner-approved cache default is **immediate responses from eligible expired cache entries, with background refresh and a maximum stale age of one hour**. Stricter freshness modes remain configurable.

The owner-approved dependency policy is to **hand-build DNS request handling, filtering, caching, and fallback**, use established supporting libraries for TLS, SQLite, regex, and the frontend, and use existing DNS libraries as test oracles. The four initial design decisions are resolved.

“Fastest” is an engineering objective, not a result this research establishes. Acceptance requires reproducible comparisons against Pi-hole, AdGuard Home, Blocky, and CoreDNS on the same Raspberry Pi, with equivalent behavior and statistics enabled.
