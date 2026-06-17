# CONTEXT.md — tetherdb

Session handoff file. Updated at the end of every session.
Read at the start of the next session alongside CLAUDE.md, MEMORY.md, and DECISIONS.md.

Every session has a name and a state: open | closed.
A session is closed only after CONTEXT.md is committed and pushed.

---

## SESSION 1 — 2026-06-17 — Context Engineering Setup — closed

Branch: claude/ecstatic-archimedes-w7640y

### WHAT WAS DONE

Set up the full context engineering system for tetherdb. Created CLAUDE.md, MEMORY.md,
CONTEXT.md, DECISIONS.md, CHANGELOG.md, .llmignore, PRPs/TEMPLATE.md, PRPs/DISCOVERY.md,
docs/CODE_STYLE.md, and the docs/source/ folder structure. The project is brand new with
only a LICENSE and README.md present — no stack, no source code. All key architectural
questions have been captured in DECISIONS.md as open items.

### FILES CREATED OR MODIFIED

CLAUDE.md                  — Behavioral rules for all AI sessions
MEMORY.md                  — Resolved decisions register (empty — no decisions yet)
CONTEXT.md                 — This file
DECISIONS.md               — Open architectural decisions register
CHANGELOG.md               — Shipping history
.llmignore                 — Protected files list
PRPs/TEMPLATE.md           — PRP template
PRPs/DISCOVERY.md          — Discovery interview protocol
docs/CODE_STYLE.md         — Code documentation rules
docs/source/               — Raw human context folder (meetings, research, stakeholder, constraints)

### TESTS WRITTEN

None — no code exists yet.

### DECISIONS MADE

None resolved. All architectural questions are open and recorded in DECISIONS.md.

### PENDING DECISIONS OPENED

- DECISION-001: Implementation language and runtime
- DECISION-002: Database sync architecture (push/pull/bidirectional)
- DECISION-003: Supported database backends
- DECISION-004: Distribution model (library vs daemon vs CLI)
- DECISION-005: Error handling strategy

### STILL OPEN AT CLOSE

Everything. No implementation has started. Awaiting first PRP and resolution of DECISION-001 through DECISION-005.

---

## SESSION 2 — 2026-06-17 — SQL Server Connector Implementation — open

Branch: claude/quirky-edison-kne9yf

### WHAT WAS DONE

(in progress)

---

## NEXT SESSION START POINT

Step 1: append a new session entry to CONTEXT.md with state `open` and the current branch name. Commit it before anything else.

Then read CLAUDE.md, MEMORY.md, DECISIONS.md in that order.

Before writing any code: resolve DECISION-001 (language/runtime) and DECISION-004 (distribution model) with the user — these block all other work. Then write the first PRP for whatever feature is being built.
