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

## SESSION 2 — 2026-06-17 — SQL Server Connector Implementation — closed

Branch: claude/quirky-edison-kne9yf

### WHAT WAS DONE

Implemented the SQL Server connector (Reader) per PRP sqlserver-connector.md. Initialised
go.mod as `github.com/massa-platform/tetherdb`. Created the shared connector interface
package and the full sqlserver connector package with CDC and Change Tracking support.
All 12 PRP-specified tests pass. `go build ./...`, `go test ./internal/connector/...`,
and `go vet ./...` all pass clean with CGO_ENABLED=0.

### FILES CREATED OR MODIFIED

go.mod                                         — module init, go-mssqldb dependency
internal/connector/connector.go                — Op, Change, Row, InitialCursor, ChangeCursor, RowStream, ChangeStream, Reader, Writer
internal/connector/sqlserver/errors.go         — ConnectorError, ErrorKind, connErr helper
internal/connector/sqlserver/querier.go        — querier interface, dbQuerier adapter, namedArg helper
internal/connector/sqlserver/sqlserver.go      — Config, Connector struct, New(), Close(), validateConfig, buildDSN, retryWithBackoff
internal/connector/sqlserver/probe.go          — Probe(), tableExists, detectMechanism, isCDCEnabled, isCTEnabled, splitTable
internal/connector/sqlserver/initialsync.go    — InitialSync(), rowStream, primaryKeyColumns, scanTable
internal/connector/sqlserver/changes.go        — Changes(), changeStream, fetchCDC, fetchCT, CDC/CT scan helpers
internal/connector/sqlserver/sqlserver_test.go — 12 unit tests (fakeQuerier-based, no real DB required)

### TESTS WRITTEN

- TestProbe_MissingTable
- TestProbe_NoCDCNoCT
- TestProbe_PrefersCDCOverCT
- TestProbe_FallsBackToCT
- TestInitialSync_EmptyTable
- TestInitialSync_FullTable
- TestInitialSync_ResumesFromCursor
- TestChanges_Insert
- TestChanges_Update
- TestChanges_Delete
- TestChanges_CursorAdvances
- TestProbe_RedactsPasswordInLogs

### DECISIONS MADE

None new — all architectural decisions were already resolved in Session 1.

### KNOWN LIMITATIONS (not scope of this PRP)

- CT fetchCTForTable join ON clause uses a placeholder — real PK-join requires knowing
  the PK columns at query time. Integration test required to validate.
- CDC before-image (op=3) is skipped; Update changes carry After only for now.
- Multi-column PK resume in InitialSync uses single-column fast path only.

### STILL OPEN AT CLOSE

Nothing from this PRP's scope. Next PRP should address the PostgreSQL Writer connector.

---

## NEXT SESSION START POINT

Step 1: append a new session entry to CONTEXT.md with state `open` and the current branch name. Commit it before anything else.

Then read CLAUDE.md, MEMORY.md, DECISIONS.md in that order.

The SQL Server connector (Reader) is complete on branch `claude/quirky-edison-kne9yf`.
Next feature to build is the PostgreSQL Writer connector — write a PRP first.
