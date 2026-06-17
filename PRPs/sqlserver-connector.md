# PRP — SQL Server Connector (Reader)

**Status:** pending approval
**Spec reference:** docs/specs/tetherdb.md §5.4, §5.3
**Implements:** `Reader` interface (first connector)

---

## FEATURE

A SQL Server connector that implements the `Reader` interface — auto-detects CDC or Change Tracking, performs a full initial sync of configured tables, then streams ongoing changes, with resumable state stored in SQLite.

---

## OBJECTIVE

When a tetherdb node is configured with a SQL Server connector, it connects to the SQL Server instance, validates the configured tables exist, detects whether CDC or Change Tracking is available, performs a full initial sync of each table (resumable on restart), then continuously streams inserts, updates, and deletes as `Change` values to the pipeline engine. The connector handles reconnection automatically with exponential backoff. The rest of the system receives a `RowStream` or `ChangeStream` and has no knowledge of SQL Server internals.

---

## CONTEXT

- Starting state: no source code exists; `go.mod` does not exist yet
- Ending state: `internal/connector/sqlserver/` package implementing `Reader`; `internal/connector/` package defining the shared interfaces and types; `go.mod` initialised
- Related spec: `docs/specs/tetherdb.md`
- Open decisions blocking this PRP: none — all resolved
- Must not touch: `.env`, `go.sum`, `LICENSE`

---

## INTERFACES TO DEFINE (internal/connector/connector.go)

These are defined once here and implemented by every connector. No other package defines them.

```go
type Op int

const (
    Insert Op = iota
    Update
    Delete
)

type Change struct {
    Schema string
    Table  string
    Op     Op
    PK     map[string]any
    Before map[string]any  // nil on Insert
    After  map[string]any  // nil on Delete
}

type Row map[string]any

type InitialCursor struct {
    LastPK map[string]any  // nil means start from beginning
}

type ChangeCursor struct {
    Value string  // CDC LSN or CT version number, opaque string
}

type RowStream interface {
    Next(ctx context.Context) (Row, error)
    Close() error
}

type ChangeStream interface {
    Next(ctx context.Context) (Change, error)
    Close() error
}

type Reader interface {
    Probe(ctx context.Context) error
    InitialSync(ctx context.Context, table string, cursor InitialCursor) (RowStream, error)
    Changes(ctx context.Context, tables []string, from ChangeCursor) (ChangeStream, error)
    Close() error
}

type Writer interface {
    Probe(ctx context.Context) error
    Apply(ctx context.Context, batch []Change) error
    Close() error
}
```

---

## IMPLEMENTATION REQUIREMENTS

### Must Do

- Initialise `go.mod` as `github.com/massa-platform/tetherdb` before writing any Go files
- Define the `Reader`, `Writer`, `Change`, `RowStream`, `ChangeStream`, `InitialCursor`, `ChangeCursor` types in `internal/connector/connector.go`
- Implement `Reader` in `internal/connector/sqlserver/`
- Support SQL Server login (username + password) and Windows Authentication — controlled by `auth` field in config (`"sqlserver"` or `"windows"`)
- On `Probe()`: verify connection succeeds; verify each configured table exists — if any table is missing, return a descriptive error naming the missing table; detect CDC or CT availability and record which will be used
- Auto-detect change mechanism priority: CDC first, Change Tracking second, error if neither
- On `InitialSync()`: read the full table in batches of 1000 rows ordered by PK; stream rows as `Row` values; accept an `InitialCursor` to resume mid-table (skip rows with PK ≤ `LastPK`)
- On `Changes()`: after initial sync, stream inserts/updates/deletes using the detected mechanism; return a `ChangeCursor` that advances only when the caller calls `Next()`
- Replicate deletes — a deleted row in SQL Server must produce a `Change{Op: Delete}` with the PK populated
- On connection loss to SQL Server: retry with exponential backoff (1s, 2s, 4s, 8s, 16s, cap at 60s); log each retry attempt with elapsed time
- `CGO_ENABLED=0` — use `go-mssqldb` which is pure Go
- Every exported symbol must have a doc comment per `docs/CODE_STYLE.md`

### Must NOT Do

- Do not import `pgx`, `modernc.org/sqlite`, or any non-SQL Server driver — this package imports only `go-mssqldb`
- Do not implement state persistence (SQLite cursors) — the connector receives and returns cursors; persistence is the pipeline engine's responsibility
- Do not implement schema change handling — if a column is added or removed mid-sync, log a warning and continue; do not error or attempt to adapt
- Do not hardcode any host, port, database name, username, or password
- Do not implement the pipeline engine, transport, or any other component — this PRP is the connector only
- Do not panic on connection errors — return errors to the caller

---

## ERROR HANDLING REQUIREMENTS

| Situation | Behaviour |
|-----------|-----------|
| SQL Server unreachable on startup | `Probe()` returns descriptive error; node aborts |
| Configured table does not exist | `Probe()` returns error naming the missing table; node aborts |
| Neither CDC nor CT available | `Probe()` returns error explaining both checks failed |
| Connection lost during initial sync | `RowStream.Next()` returns error; caller retries from last `InitialCursor` |
| Connection lost during change stream | `ChangeStream.Next()` returns error; caller retries from last `ChangeCursor` |
| Row decode error (unexpected column type) | `Next()` returns error wrapping the offending column name and value |
| Auth failure | `Probe()` returns error; do not log the password |

All errors must be wrapped with `fmt.Errorf("sqlserver: <context>: %w", err)` — never return raw driver errors.

---

## SECURITY CONSIDERATIONS

- Passwords must never appear in log output — log connection string as `sqlserver://<user>@<host>/<db>` with password redacted
- Host and port from config must be validated before passing to the driver (non-empty, valid hostname format, port 1–65535)
- Table names from config must be validated against the SQL Server metadata — never interpolated into query strings directly; use the driver's parameterised query API
- Windows Authentication does not involve a password field — do not require or log one when `auth = "windows"`

---

## FILE STRUCTURE

```
go.mod
internal/
  connector/
    connector.go          ← shared interfaces and types (Reader, Writer, Change, etc.)
    sqlserver/
      sqlserver.go        ← Connector struct, New(), Probe(), Close()
      initialsync.go      ← InitialSync() implementation
      changes.go          ← Changes() implementation, CDC and CT sub-implementations
      probe.go            ← CDC/CT detection logic
      errors.go           ← domain error types
```

---

## TESTS TO WRITE

All tests go in `internal/connector/sqlserver/` as `_test.go` files. Tests use an interface-compatible fake/stub — no real SQL Server connection required for unit tests.

- [ ] `TestProbe_MissingTable` — `Probe()` returns error when a configured table does not exist
- [ ] `TestProbe_NoCDCNoCT` — `Probe()` returns error when neither CDC nor CT is enabled
- [ ] `TestProbe_PrefersCDCOverCT` — when both are available, CDC is selected
- [ ] `TestProbe_FallsBackToCT` — when CDC is unavailable but CT is, CT is selected
- [ ] `TestInitialSync_FullTable` — streams all rows from a table with no cursor (start from beginning)
- [ ] `TestInitialSync_ResumesFromCursor` — when `InitialCursor.LastPK` is set, rows at or before that PK are skipped
- [ ] `TestInitialSync_EmptyTable` — empty table produces zero rows, no error
- [ ] `TestChanges_Insert` — an inserted row produces `Change{Op: Insert}` with correct PK and After values
- [ ] `TestChanges_Update` — an updated row produces `Change{Op: Update}` with correct Before and After values
- [ ] `TestChanges_Delete` — a deleted row produces `Change{Op: Delete}` with PK populated and After nil
- [ ] `TestChanges_CursorAdvances` — cursor value increases monotonically across successive changes
- [ ] `TestProbe_RedactsPasswordInLogs` — log output does not contain the password string

---

## ROLLBACK PLAN

- Branch to return to: `main`
- No migrations — SQLite schema is not part of this PRP
- State to return to: delete `internal/` directory and `go.mod`

---

## ACCEPTANCE CRITERIA

- [ ] `go build ./...` passes with `CGO_ENABLED=0`
- [ ] `go test ./internal/connector/...` passes
- [ ] `go vet ./...` passes with no warnings
- [ ] No password or connection string appears in any log output in tests
- [ ] `internal/connector/sqlserver` imports no driver other than `go-mssqldb`
- [ ] Every exported symbol in `internal/connector/connector.go` and `internal/connector/sqlserver/` has a doc comment
- [ ] `InitialCursor` resume test passes — rows before the cursor are not returned
- [ ] Delete changes carry the PK and have `After == nil`

## VALIDATION

```bash
CGO_ENABLED=0 go build ./...
go test ./internal/connector/...
go vet ./...
```
