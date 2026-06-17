## FEATURE: [one sentence]

## OBJECTIVE
[2–3 sentences describing what "done" looks like from a user perspective]

## CONTEXT

- Starting state: [which files currently exist and are relevant]
- Ending state: [which files will be created or modified]
- Related existing code: [specific file paths to read before starting]
- Open decisions that must be resolved first: [list any DECISIONS.md entries that block this feature]

## IMPLEMENTATION REQUIREMENTS

### Must Do
- [specific requirement]
- [specific requirement]

### Must NOT Do
- [explicit exclusion — be specific about why]
- [explicit exclusion]

## ERROR HANDLING REQUIREMENTS

- [Which errors this feature must surface and how]
- [Which errors it can silently ignore and why]
- [What the caller receives on each failure path]

## SECURITY CONSIDERATIONS

- [Input validation requirements — what must be validated before processing]
- [Auth requirements — which operations require authentication]
- [Data exposure risks — what must never appear in logs or client responses]
- [If any of the restricted categories apply (auth, crypto, secrets), note that human review is required before merging]

## TESTS TO WRITE

List the specific test cases before any implementation begins:
- [ ] Happy path: [describe]
- [ ] Error path: [describe each error variant the caller can receive]
- [ ] Edge case: [describe]

## ROLLBACK PLAN

If this feature needs to be abandoned mid-implementation:
- Branch to return to: [branch name]
- Migration to reverse: [migration name, or "none"]
- State the codebase should be in: [describe]

## ACCEPTANCE CRITERIA
- [ ] [testable criterion]
- [ ] [testable criterion]
- [ ] All existing tests pass
- [ ] New tests written and passing
- [ ] Typecheck / lint passes
- [ ] No undocumented exports
- [ ] CHANGELOG.md updated

## VALIDATION
Run these commands to verify completion:
- [typecheck/lint command — TBD after DECISION-001]
- [test command — TBD after DECISION-001]
- [any feature-specific check]
