# PRP: PostgreSQL Writer Connector

**Status:** approved
**Spec ref:** §5.5, implementation plan step 2
**Branch:** claude/quirky-edison-kne9yf

---

## Goal

Implement the `Writer` interface for PostgreSQL. The writer receives batches of `Change`
values from the pipeline engine and applies them to a target Postgres database as a mirror
of the SQL Server source. Phase 1 scope: prove the connector compiles, connects, and
applies changes correctly — no transport or pipeline wiring yet.

---

## Behaviour

### Apply(ctx, batch []Change)

Each call applies all changes in `batch` inside a single database transaction.

| Change.Op | SQL                                    | Notes                                    |
|-----------|----------------------------------------|------------------------------------------|
| Insert    | `INSERT ... ON CONFLICT (pk) DO UPDATE SET ...` | Upsert — idempotent on redelivery  |
| Update    | Same as Insert                         | Reuses upsert path                       |
| Delete    | `DELETE FROM t WHERE pk_col = $1 ...`  | Missing row → no-op (idempotent)         |

- Conflict key is always the columns present in `Change.PK`.
- If any statement in the batch fails (e.g. FK violation, type mismatch), the transaction
  is rolled back and `Apply` returns an error. The source will not be ACKed and will retry
  the batch.
- Column names and values come from `Change.After` (for Insert/Update) and `Change.PK`
  (for Delete). Column names are used directly as Postgres identifiers — they are quoted
  with `pgx`'s identifier quoting to prevent SQL injection.

### Probe(ctx)

Verifies the database connection is reachable and the credentials are valid. Does not
check table existence — that is deferred (see Open Decisions). Returns a typed
`ConnectorError` on failure.

### Close()

Closes the underlying `pgxpool` connection pool.

---

## Config

Two modes — both are supported, individual fields take priority over DSN if both are set.

**Explicit fields:**
```toml
[sink]
driver   = "postgres"
host     = "pg.internal"
port     = 5432          # optional, defaults to 5432
database = "erp_mirror"
username = "${PG_USER}"
password = "${PG_PASS}"
sslmode  = "require"     # optional, defaults to "require"
```

**Raw DSN:**
```toml
[sink]
driver = "postgres"
dsn    = "postgres://user:pass@pg.internal:5432/erp_mirror?sslmode=require"
```

The `sink` TOML section is a new optional top-level key in `Config`. It follows the same
pointer pattern as `connector` — `nil` means no sink configured.

---

## Package layout

```
internal/connector/postgres/
    postgres.go        — Config, Connector struct, New(), Close(), Probe(), Apply()
    apply.go           — buildUpsert(), buildDelete(), applyBatch() helpers
    errors.go          — ConnectorError, ErrorKind, connErr helper (mirrors sqlserver pattern)
    postgres_test.go   — unit tests (fakePool interface, no real DB required)
```

No file may exceed 300 lines. If `postgres.go` approaches the limit, split helpers into
`apply.go` first.

---

## Interface wiring

`internal/connector/connector.go` already defines the `Writer` interface. The Postgres
connector must satisfy it exactly — no new methods on the interface.

`internal/config/config.go` gains a `SinkConfig` struct and `Sink *SinkConfig` field.
`HasSink()`, `SinkDSN()`, and `RedactedSinkDSN()` helper methods follow the same pattern
as the connector helpers.

`cmd/tetherdb/main.go` does NOT wire the sink yet — phase 1 is connector-only. The
`program.Start()` method probes the sink if `cfg.HasSink()` using the same pattern as
the connector probe, then returns. No data flows yet.

---

## Driver

`github.com/jackc/pgx/v5` — specifically `pgxpool` for connection pooling. Use
`pgxpool.New(ctx, dsn)` to open. Queries use `pgx`'s native `pgconn` error types for
error classification.

Do NOT use `database/sql` — pgx native API is used throughout for proper type handling
and named parameter support.

---

## Error kinds

Reuse the same `ErrorKind` pattern as the sqlserver connector:

| Kind              | When                                              |
|-------------------|---------------------------------------------------|
| ErrConnection     | Cannot reach Postgres, TLS failure                |
| ErrAuth           | Login failed                                      |
| ErrMissingTable   | Table not found during Apply (deferred to apply time) |
| ErrDecode         | Cannot map Change value to Postgres column type   |
| ErrInvalidConfig  | Missing required config field                     |

---

## SQL generation

Column names from `Change.After` / `Change.PK` are used as dynamic identifiers.
Use `pgx`'s `pgconn` quoting (`pgx/v5/pgconn` `QuoteIdentifier`) to prevent injection.
Never concatenate user-supplied column names without quoting.

Upsert template (generated dynamically from the column set):
```sql
INSERT INTO "schema"."table" ("col1", "col2", ...)
VALUES ($1, $2, ...)
ON CONFLICT ("pk_col") DO UPDATE SET "col1" = EXCLUDED."col1", ...
```

Delete template:
```sql
DELETE FROM "schema"."table" WHERE "pk_col" = $1
```

For composite PKs, the WHERE clause uses AND across all PK columns.

---

## Tests to write (TDD order)

Write each test before its implementation.

1. `TestProbe_Success` — fakePool returns nil on Ping → Probe returns nil
2. `TestProbe_ConnectionFailure` — fakePool returns error → Probe returns ErrConnection
3. `TestApply_Insert` — single Insert change → correct upsert SQL executed
4. `TestApply_Update` — single Update change → same upsert SQL as Insert
5. `TestApply_Delete` — single Delete change → correct DELETE SQL executed
6. `TestApply_DeleteMissingRow` — DELETE affects 0 rows → Apply returns nil (no-op)
7. `TestApply_BatchTransaction` — mixed batch → all executed in one transaction
8. `TestApply_RollbackOnError` — one statement fails → transaction rolled back, error returned
9. `TestApply_CompositePK` — Change with 2-column PK → WHERE clause uses both columns
10. `TestApply_SpecialCharsInPassword` — DSN built with special chars → no URL parse error
11. `TestConfig_ExplicitFields` — host/port/user/pass → DSN built correctly
12. `TestConfig_RawDSN` — dsn field present → used as-is

---

## Open decisions logged

- **DECISION-009 (open):** Should `Probe()` verify target table existence at startup, or
  defer to first apply? Arguments: eager check surfaces misconfiguration early; lazy check
  avoids requiring all tables to exist before first change arrives. Deferred to post-phase-1.

- **DECISION-010 (open):** Schema sync — should tetherdb optionally create or migrate
  target Postgres tables to match the SQL Server schema? Out of scope for v1 per spec §4
  (non-goals), but operator demand is expected. Deferred.

---

## Out of scope for this PRP

- Transport (WebSocket) wiring
- Pipeline engine wiring (Reader → Writer)
- ACK protocol
- SQLite state layer
- Management API sink status endpoint
