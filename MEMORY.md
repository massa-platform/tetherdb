# MEMORY.md — tetherdb

Records resolved architectural decisions and current project state.
Read this at the start of every session before writing any code.

---

## ARCHITECTURAL DECISIONS

### 1. Source and target databases

**Decision:** Microsoft SQL Server is the source database. PostgreSQL is the target. These are the only supported backends at launch.

**Why:** User-specified initial scope. Keeping source and target narrow allows deep feature coverage (SQL Server change tracking/CDC; Postgres as a well-understood write target) before adding more connectors.

**Rules out:** MySQL, SQLite, Oracle, MongoDB as sources or targets in v1. A pluggable connector interface must be designed so adding them later does not require restructuring the core engine.

---

## CURRENT PROJECT STATE

### Fully Working
- Context engineering system (CLAUDE.md, MEMORY.md, CONTEXT.md, DECISIONS.md, CHANGELOG.md, .llmignore, PRPs/, docs/)

### In Progress
- Nothing — waiting for DECISION-001 (language) and DECISION-004 (distribution model) to be resolved before any code is written

### Not Started
- Everything else

---

## NEXT SESSION START POINT

Read CLAUDE.md, MEMORY.md, DECISIONS.md, CONTEXT.md in that order.
Resolve DECISION-001 (language/runtime) and DECISION-004 (distribution model) with the user before writing any code.
Once those are resolved, update this file and CLAUDE.md with the stack details, then write the first PRP.
