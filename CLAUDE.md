# CLAUDE.md — tetherdb

Behavioral instructions for AI coding assistants. Each rule exists to prevent a specific mistake.

---

## SESSION HANDOFF RULE — NON-NEGOTIABLE

Every session in `CONTEXT.md` must have:
- A **name** — short descriptive title of what the session accomplished
- A **state** — `open` while work is in progress, `closed` once handoff is done
- A **branch** — the git branch this session's work lives on

Format:
  ## SESSION {n} — {YYYY-MM-DD} — {Name} — {state}
  Branch: {branch-name}

Rules:
- **Step 1 of every session, no exceptions:** append a new session entry to `CONTEXT.md` with state `open` and the current branch name. Do this before reading any other file, before planning, before writing any code. The entry must exist and be committed before any other work begins.
- Mark it `closed` only after CONTEXT.md is updated and committed and pushed.
- Never leave a session `open` at the end of a turn.
- Never start a new session without closing the previous one first.
- The NEXT SESSION START POINT block is always rewritten at the end of every session.
- Sessions are never deleted — the full history stays in this file.

The open entry looks like this — write it immediately:

  ## SESSION {n} — {YYYY-MM-DD} — {Name} — open
  Branch: {branch-name}

Replace `{Name}` with a short title for what this session intends to do.
Commit this entry before proceeding.

---

## PRP RULE — NON-NEGOTIABLE

Never write code for a new feature without a PRP file in /PRPs.

If a feature request is given without a PRP:
1. Do not write any code.
2. Run a discovery interview — one question at a time.
3. Cover: what it does, who it affects, edge cases, error states, what files it touches, what it must not touch.
4. Write the PRP to /PRPs/[feature-name].md.
5. Present it to the user for approval.
6. Only build after explicit approval.

A vague prompt is not a starting point. It is the beginning of a discovery.

---

## SCOPE RULE — NON-NEGOTIABLE

One PRP at a time. Never implement more than one feature's scope in a single session.

If mid-implementation you discover the scope is larger than the PRP described:
1. Stop immediately. Do not continue implementing.
2. Document what was discovered.
3. Update or create a new PRP for the expanded scope.
4. Get approval before continuing.

The model does not decide that something is "small enough to add." The human decides.

---

## TESTING RULE — NON-NEGOTIABLE

Write the test before writing the implementation. No exceptions.

Rules:
- For every new function, write a failing test first. Then write the minimum code to make it pass.
- Tests mirror the source tree (exact paths TBD once stack is chosen — see DECISIONS.md DECISION-001).
- What to test: behavior visible to callers — inputs, outputs, and error paths.
- What NOT to test: implementation internals, private functions, third-party library behavior, framework wiring.
- Every new exported function must have at least one test covering its happy path and one covering each error path.
- After any non-trivial change, run the full test suite before considering the task done.

If the PRP does not describe what to test, add the test cases to the PRP before writing any code.

---

## SECURITY RULE — NON-NEGOTIABLE

Never implement the following without explicit human review and approval:
- Authentication or session management
- Authorization / permission checks
- Cryptographic operations (hashing, signing, encrypting)
- Secrets or credential handling
- Network protocol parsing (untrusted input from remote databases)

For everything else:
- Never hardcode credentials, connection strings, or secrets. Not even in example values.
- Never construct queries by string concatenation — use parameterized queries or the driver's safe API.
- Never trust connection parameters from an untrusted source without validation.
- Never log passwords, tokens, connection strings, or PII.
- Never expose internal error details externally — map them to a generic message.
- When generating code that accepts external input (database host, port, credentials), validate before use.

If you are unsure whether something has a security implication, stop and ask before implementing.

---

## CODE DOCUMENTATION RULE — NON-NEGOTIABLE

Read `docs/CODE_STYLE.md` before writing any function, class, or module.

Every exported function, class, and type must have a documentation block. Every non-obvious decision inside a function must have an inline comment explaining *why*, not what.

---

## PENDING DECISIONS RULE — NON-NEGOTIABLE

Before writing any code that depends on an unresolved architectural question, check `DECISIONS.md`.

- If the decision is listed as `open`, stop. Do not implement. Ask the user to resolve it first.
- If the decision is listed as `resolved`, follow the outcome recorded there — do not re-litigate it.
- If you encounter a new unresolved question mid-implementation, add it to `DECISIONS.md` as `open` and stop.

Never make an architectural choice silently. If you are guessing, you are making a decision that belongs in DECISIONS.md.

---

## FILE SIZE RULE

No file exceeds 300 lines.

When a file reaches the limit:
1. Stop before adding more code.
2. Propose a specific split to the user — show the proposed new file names and what moves where.
3. Wait for approval.
4. Split, then continue.

---

## PROTECTED FILES — NON-NEGOTIABLE

Never read, modify, or delete the following under any circumstances:

- `.env` and `.env.*`
- Any lockfile: `package-lock.json`, `yarn.lock`, `pnpm-lock.yaml`, `Cargo.lock`, `go.sum`
- Migration files (once stack is chosen, specific paths will be added here)
- `LICENSE`

See also: `.llmignore` at the project root.

---

## COMMANDS

```bash
# Stack not yet chosen — commands will be filled in once DECISION-001 is resolved.
# See DECISIONS.md.
```

---

## STACK

- **Project:** tetherdb — tether and sync databases
- **License:** GPL v3
- **Language/Runtime:** TBD — see DECISIONS.md DECISION-001
- **Distribution:** TBD — see DECISIONS.md DECISION-004

---

## ARCHITECTURE RULES

Architecture is not yet decided. See DECISIONS.md for all open questions.

Once DECISION-001 through DECISION-005 are resolved, this section will be populated with:
- API/interface shape
- Error handling approach
- Where shared types live
- Which layer connects to databases
- Module boundaries

---

## ERROR HANDLING — NON-NEGOTIABLE

This project uses **return-based error handling**. Do not throw exceptions for expected failures.

The exact type signature depends on DECISION-001 (language choice), but the principle is universal:

```
Expected failure  →  return an error value the caller must handle
Truly unexpected  →  let it propagate (programmer error, unrecoverable state)
```

Once the language is chosen, specific Result/Either/error-return patterns will be documented here.

Anti-patterns (language-agnostic):
- Never use exceptions/panics for expected failures (connection refused, table not found, auth failed)
- Never return null/nil/undefined to signal failure — the caller cannot distinguish "not found" from "returned nothing"
- Never expose raw library error messages to callers — wrap them in domain error types
- Never swallow errors silently

---

## FILE ORGANIZATION

```
tetherdb/
├── CLAUDE.md
├── MEMORY.md
├── CONTEXT.md
├── DECISIONS.md
├── CHANGELOG.md
├── .llmignore
├── PRPs/
│   ├── TEMPLATE.md
│   ├── DISCOVERY.md
│   └── [feature].md
├── docs/
│   ├── source/
│   │   ├── meetings/
│   │   ├── research/
│   │   ├── stakeholder/
│   │   └── constraints/
│   ├── CODE_STYLE.md
│   └── specs/
└── [src/ — structure TBD after DECISION-001]
```

---

## ANTI-PATTERNS

1. **Never connect to a database without validating the connection parameters first.** Invalid input reaching the driver can produce confusing errors or, in adversarial cases, SSRF.
2. **Never assume a sync operation is idempotent without proving it.** Double-applying a sync can corrupt data.
3. **Never hardcode a database port, host, or credential.** All connection config must come from environment or config file.
4. **Never treat a failed sync as a no-op.** Every failed sync must be surfaced to the caller with enough detail to diagnose the failure.
5. **Never make an architectural choice silently.** Open DECISIONS.md first.

---

## KNOWN ISSUES — DO NOT FIX

None yet — project has not started.
