# Code Style — tetherdb

Documentation rules for all code in this project.
Read this before writing any function, class, or module.

Note: Specific syntax below uses Rust as a placeholder. Once DECISION-001 is resolved,
update this file with the language-specific doc comment format.

---

## The Two Layers of Documentation

### Layer 1 — Block documentation (what this is)

Every exported function, struct/type, trait/interface, and constant gets a documentation block.
Internal (non-exported) items get a block only if their purpose is not immediately obvious.

**Rust format:**

```rust
/// Connects to a database at the given address and returns a verified connection handle.
///
/// Validates the address format before attempting the connection. Returns `Err(ConnectError::InvalidAddress)`
/// if the address cannot be parsed, and `Err(ConnectError::Refused)` if the database rejects the connection.
///
/// # Arguments
///
/// * `addr` - Database connection string (e.g. `postgres://user:pass@host:5432/db`)
/// * `opts` - Connection options including timeout and TLS settings
///
/// # Returns
///
/// `Result<Connection, ConnectError>` — see `ConnectError` for all failure variants.
///
/// # Example
///
/// ```
/// let conn = connect("postgres://localhost/mydb", ConnectOptions::default()).await?;
/// ```
pub async fn connect(addr: &str, opts: ConnectOptions) -> Result<Connection, ConnectError> {
```

**Go format (if DECISION-001 resolves to Go):**

```go
// Connect establishes a connection to the database at addr.
//
// It validates the address before dialing. Returns ConnectError with
// kind=InvalidAddress if the address cannot be parsed, or kind=Refused
// if the database rejects the connection.
//
// Example:
//
//	conn, err := Connect(ctx, "postgres://localhost/mydb", DefaultOptions())
func Connect(ctx context.Context, addr string, opts Options) (*Connection, error) {
```

Rules:
- The first line is always a single sentence. No "This function...". Start with the verb.
- Parameters, return values, and error variants are always documented.
- One example is required on every public API function.
- Never document what the code does mechanically — document what it means to the caller.

### Layer 2 — Inline comments (why this decision was made)

Inline comments explain decisions, not code.

**Good:** `// Retry once — the upstream driver returns a transient error on first connection ~15% of the time`
**Bad:** `// Increment retry counter`

Rules:
- Comment above the line it explains, not at the end.
- Use inline comments when: a magic value appears, a library is used non-obviously, a performance trade-off was made, a guard clause prevents a non-obvious bug, a workaround exists for a known issue.
- Never comment what the code does. If the code needs a comment to explain what it does, rewrite the code.
- Known issues: `// TODO(#123): Remove after upstream fixes their connection pooling`

---

## Module-Level Documentation

Every file gets a top-of-file comment:

```rust
//! sync — core synchronization engine
//!
//! Responsible for applying change events from a source database to one or more targets.
//! Does NOT handle conflict detection — that is in conflict.rs.
//!
//! Depends on: connection, schema, error
//! Used by: main, cli
```

---

## Documentation Anti-Patterns

1. **Describing the code.** The code is the description. Comments explain what the code cannot.
2. **Stale comments.** A comment that contradicts the code is worse than no comment. Update comments in the same edit as the code.
3. **`// TODO` without a ticket.** Every TODO gets a ticket reference or a date.
4. **Over-documenting internals.** Not every helper function needs a block.
5. **Under-documenting the error contract.** Every public function's documentation must describe every failure variant the caller can receive. This is the most important part for callers.
6. **Undocumented public exports.** Every exported item must have a doc block. No exceptions.
