# DECISIONS.md — tetherdb

Tracks architectural and design questions that are open, deferred, or resolved.

Rules:
- Every open decision blocks implementation of the code it affects.
- The AI must not implement anything that depends on an open decision.
- When a decision is resolved, move it to the RESOLVED section and record the outcome.
- Once resolved, copy the outcome to MEMORY.md as an architectural decision.

---

## OPEN — Requires human input before implementation

### DECISION-001 — Implementation language and runtime

**Status:** open
**Raised:** 2026-06-17 — Session 1
**Resolved by:** human
**Blocks:** All implementation. Cannot write any code without knowing the language.

**Question:** What language and runtime will tetherdb be written in?

**Options:**
- A) Rust — zero-cost abstractions, memory safety, strong async story (tokio), ideal for a database tool that must be reliable and fast; steeper learning curve, slower iteration
- B) Go — simple concurrency model (goroutines), fast compile times, strong stdlib networking, widely understood; GC pauses possible in latency-sensitive paths
- C) TypeScript/Node.js — fast iteration, large ecosystem; not ideal for a systems tool, GC, single-threaded by default
- D) Python — fastest prototyping; not suitable for a production database sync tool

**Notes:** Given the project description ("tether and sync databases"), this is a systems-level tool. Rust or Go are the most natural choices. The decision will determine the error handling pattern, module structure, test framework, and build toolchain.

---

### DECISION-002 — Database sync architecture

**Status:** open
**Raised:** 2026-06-17 — Session 1
**Resolved by:** human
**Blocks:** Core sync engine design, PRP for sync feature

**Question:** How does tetherdb synchronize databases — what is the fundamental sync model?

**Options:**
- A) Change Data Capture (CDC) — subscribe to the source database's replication stream (WAL for Postgres, binlog for MySQL); low latency, complex to implement, requires elevated DB permissions
- B) Polling / snapshot diff — periodically query both databases and diff the results; simple, higher latency, works on any database without special permissions
- C) Event-sourced — the user's application emits events that tetherdb applies to multiple databases; application must be modified
- D) Hybrid — CDC when available, polling as fallback

**Notes:** This is the most consequential architectural decision. It determines the complexity of the core engine, the permissions required, and which databases can be supported.

---

### DECISION-003 — Supported database backends

**Status:** resolved
**Raised:** 2026-06-17 — Session 1
**Resolved:** 2026-06-17 — Session 1

**Question:** Which database systems will tetherdb support at launch?

**Outcome:** Microsoft SQL Server (source) → PostgreSQL (target). SQL Server is the only supported source at launch. PostgreSQL is the only supported target. The architecture must be designed with a pluggable source connector interface so additional sources can be added later without restructuring.

**Rationale:** User-specified. Narrow initial scope reduces driver complexity and allows deep feature coverage (SQL Server CDC via change tracking or CT/CDC features; Postgres as a well-understood write target).

**Copied to MEMORY.md:** yes

---

### DECISION-004 — Distribution model

**Status:** open
**Raised:** 2026-06-17 — Session 1
**Resolved by:** human
**Blocks:** Public API design, entry point structure

**Question:** How is tetherdb distributed and consumed?

**Options:**
- A) Library / SDK — imported as a dependency into the user's application; no separate process
- B) Standalone daemon / server — runs as a background service, exposes an HTTP or gRPC API
- C) CLI tool — invoked from the command line, configuration via file or flags; one-shot or watch mode
- D) Multiple: library + CLI that embeds the library

**Notes:** This determines whether tetherdb has a public API surface (library), a network protocol (daemon), or a CLI interface. Each implies a very different architecture. Option D is common for mature tools but is a larger scope.

---

### DECISION-005 — Error handling strategy

**Status:** open
**Raised:** 2026-06-17 — Session 1
**Resolved by:** human (informed by DECISION-001)
**Blocks:** Cannot finalize until language is chosen (DECISION-001)

**Question:** What is the specific error handling pattern for this project?

**Options:**
- A) Rust: `Result<T, E>` with a domain error enum; `?` operator for propagation; `thiserror` for error types
- B) Go: explicit `(T, error)` return; custom error types wrapping stdlib errors; no panics in library code
- C) TypeScript: `Result<T, E>` discriminated union; no throws in service layer

**Notes:** Depends on DECISION-001. Once language is chosen, this decision is straightforward — follow the language's idiomatic error handling. Document the specific pattern here so all sessions follow the same approach.

---

## DEFERRED — Acknowledged, not yet needed

### DECISION-006 — Conflict resolution strategy

**Status:** deferred
**Raised:** 2026-06-17 — Session 1
**Revisit when:** Sync architecture (DECISION-002) is resolved and first sync feature is being designed

**Question:** When two databases have diverged (e.g., both modified the same row), how does tetherdb resolve the conflict?

**Notes:** Last-write-wins is the simplest but loses data. Custom merge functions are powerful but complex. This is deferred because the answer depends heavily on the sync architecture chosen in DECISION-002.

---

### DECISION-007 — Observability and telemetry

**Status:** deferred
**Raised:** 2026-06-17 — Session 1
**Revisit when:** First working sync implementation exists

**Question:** How does tetherdb expose sync metrics, logs, and traces?

**Notes:** Structured logging is a baseline requirement. Metrics (Prometheus? OpenTelemetry?) and distributed tracing are deferred until there is something worth observing.

---

## RESOLVED

No decisions resolved yet.
