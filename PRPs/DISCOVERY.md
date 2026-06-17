# Discovery Interview Protocol

Use when a feature request arrives without a PRP.

## When to Run a Discovery Interview

Any time a feature is described in one or two sentences without specifying:
- What triggers it and what it produces
- Who is affected and how
- What the error and edge-case behavior should be
- Which existing files it touches
- What it must not touch
- Which open decisions in DECISIONS.md are relevant

## Question Sequence

Ask one question at a time. Do not batch questions. Wait for the answer before continuing.

Cover in order:
1. What does it do — input, processing, output
2. Who uses it and when
3. What happens when it fails — user-facing error on each failure path
4. Edge cases — empty states, concurrent requests, invalid input
5. Which existing files it reads from or writes to
6. What it must never modify
7. Are there any open entries in DECISIONS.md this feature depends on?
8. What are the security implications — does it accept external input, touch auth, handle credentials?
9. What does rollback look like if this needs to be abandoned?
10. How success is verified — what commands prove it works?

## After the Interview

Write the completed PRP to `/PRPs/[feature-name].md` using the template in `PRPs/TEMPLATE.md`.
Present it to the user.
Wait for explicit approval before writing any code.
