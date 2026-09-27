# Test Dataset Construction Reference

How to build comprehensive test datasets that systematically exercise boundary
conditions, edge cases, and error scenarios. Use this reference when producing
test dataset tables for a spec written by plan-spec. These datasets specify
what qa-lead's RED step must cover — they are not the tests themselves.

## Dataset Table Format

```
#### Dataset: [Context — e.g., "Email Address Validation"]

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | ""    | Empty         | Error: required | BDD Scenario: [title] | Zero-length |
| 2 | "a"   | Min           | Error: invalid  | BDD Scenario: [title] | Below min format |
```

Every row MUST have a `Traces to` value linking it to a specific BDD scenario.
Every `Expected Output` MUST come from this spec's Behavioral Contract, the
cited contract schema (`contracts/components/schemas/`), or the ADR — never
from reading an existing implementation.

## Boundary Condition Categories

Systematically generate test data from these categories. Not every category
applies to every input — select the ones relevant to the domain.

### Numeric Inputs

| Boundary Type | Example Values | Purpose |
|---------------|---------------|---------|
| Zero          | `0`           | Additive identity, division edge |
| One           | `1`           | Multiplicative identity, off-by-one |
| Negative      | `-1`          | Sign handling |
| Min           | Smallest valid value | Lower bound |
| Min - 1       | One below smallest valid | Just outside lower bound |
| Max           | Largest valid value | Upper bound |
| Max + 1       | One above largest valid | Just outside upper bound |
| Float precision | `0.1 + 0.2` | Floating-point rounding |
| Very large    | `2^53`, `MAX_INT` | Overflow risk |
| NaN / Infinity | `NaN`, `Inf` | Special IEEE 754 values |

### String Inputs

| Boundary Type | Example Values | Purpose |
|---------------|---------------|---------|
| Empty         | `""`          | Zero-length string |
| Null / nil    | `null`, `nil` | Absence of value |
| Single char   | `"a"`         | Minimum meaningful content |
| Max length    | `"a" * MAX`   | Upper bound |
| Max + 1       | `"a" * (MAX+1)` | Just over limit |
| Whitespace only | `"   "`, `"\t\n"` | Invisible but non-empty |
| Leading/trailing spaces | `" hello "` | Trim handling |
| Unicode       | `"café"`, `"..."` | Multi-byte, combining chars |
| Special chars | `"<script>alert(1)</script>"` | Injection, escaping |
| Emoji         | `"Hello \U0001F600"` | Multi-codepoint characters — note: Omnipus itself bans emoji in stored data and UI chrome (root `CLAUDE.md`), so this boundary is about input handling, never a UI output expectation |
| RTL text      | Arabic/Hebrew strings | Bidirectional text |
| Newlines      | `"line1\nline2"` | Embedded line breaks |
| Very long     | 10KB+ string  | Memory, truncation |

### Collection Inputs

| Boundary Type | Example Values | Purpose |
|---------------|---------------|---------|
| Empty         | `[]`, `{}` | No items |
| Single item   | `[x]` | Minimum non-empty |
| Max size      | Collection at limit | Upper bound |
| Max + 1       | One over limit | Overflow |
| Duplicates    | `[a, a, a]` | Uniqueness handling |
| Nested        | `[[[]]]` | Deep nesting |
| Mixed types   | `[1, "two", null]` | Heterogeneous content |
| Sorted / unsorted | Both orders | Order assumptions |

### Date and Time Inputs

| Boundary Type | Example Values | Purpose |
|---------------|---------------|---------|
| Epoch         | `1970-01-01T00:00:00Z` | Zero time |
| Pre-epoch     | `1969-12-31` | Negative timestamps |
| Far future    | `9999-12-31` | Year overflow |
| Leap year     | `2024-02-29` | Feb 29 handling |
| Non-leap year | `2023-02-29` | Invalid date |
| DST transition | Spring forward/fall back | Lost/repeated hour |
| Timezone boundaries | `23:59:59 UTC` vs local | Day boundary differences |
| Midnight      | `00:00:00` | Start of day |
| End of day    | `23:59:59.999` | Last moment |

### File Inputs

| Boundary Type | Example Values | Purpose |
|---------------|---------------|---------|
| Empty file    | 0 bytes | No content |
| Very large    | > available memory | Resource limits |
| Binary content | Random bytes | Non-text handling |
| Missing file  | Non-existent path | File-not-found |
| No permissions | Read-protected | Permission denied |
| Locked file   | In-use by another process | Concurrent access |
| Symlink       | Link to file/directory | Resolution handling |
| Special name  | Spaces, unicode, `..` | Path parsing |

## Edge Case Categories

Beyond boundary values, consider these situational edge cases:

### Concurrency

- Simultaneous identical requests
- Read during write
- Double-submit / duplicate actions
- Race between create and delete

### State

- Uninitialised / first-time use
- Partially completed previous operation
- Corrupted or inconsistent stored state
- Cache stale while source updated

### Network and I/O

- Connection timeout
- Partial response (truncated body)
- Connection refused (service down)
- DNS resolution failure
- Slow response (latency > timeout threshold)
- Retry after transient failure

### Resource Exhaustion

- Out of memory
- Disk full
- File descriptor exhaustion
- Rate limit exceeded
- Connection pool exhausted

## Error Scenario Categories

Build test data that triggers these categories of errors:

### Input Validation

| Error Type | Example | Expected Response |
|-----------|---------|-------------------|
| Missing required field | Omit `name` | 400 / validation error naming the field |
| Wrong type | String where int expected | 400 / type error |
| Out of range | Negative age | 400 / range error |
| Malformed format | Invalid email | 400 / format error |
| Injection attempt | SQL/XSS payloads | 400 / sanitised, no execution |

### Authentication / Authorisation

| Error Type | Example | Expected Response |
|-----------|---------|-------------------|
| Missing credentials | No token | 401 |
| Expired credentials | Expired JWT | 401 |
| Insufficient permissions | User accesses admin resource | 403 |
| Revoked access | Deleted user's token | 401 |
| Tool policy denies the call | Agent's effective policy resolves to `deny` (two-layer model, Hard Constraint #6) | Caller sees a clear denial, not a silent no-op |

### Dependency Failures

| Error Type | Example | Expected Response |
|-----------|---------|-------------------|
| Service unavailable | Upstream 503 | Graceful error message |
| Timeout | Upstream > threshold | Timeout error, no hang |
| Version mismatch | Breaking API change | Clear incompatibility error |
| Partial failure | 2 of 3 calls succeed | Consistent state, clear report |

### Data Integrity

| Error Type | Example | Expected Response |
|-----------|---------|-------------------|
| Missing foreign key | Reference to deleted record | Error or graceful handling |
| Duplicate unique value | Insert with existing key | Conflict error |
| Stale data | Concurrent update | Conflict detection |
| Corrupted payload | Truncated JSON | Parse error |

## Regression Dataset Requirements

When the feature modifies existing functionality, add a regression dataset:

```
#### Regression Dataset: [What existing behaviour is being protected]

| # | Input | Previous Behaviour | Must Still Produce | Traces to |
|---|-------|-------------------|--------------------|-----------|
| 1 | [existing valid input] | [what it used to return] | [same result] | Regression: [area] |
```

This dataset is run BEFORE making changes (to confirm the baseline) and
AFTER (to confirm nothing broke). Every row represents a contract with
existing users or systems.

## Completeness Checklist

For each input domain in the feature, verify:

- [ ] At least one boundary value at each end (min, max)
- [ ] At least one value just outside each boundary (min-1, max+1)
- [ ] At least one empty/null/zero test
- [ ] At least one valid representative value (happy path)
- [ ] At least one value triggering each distinct error message
- [ ] Edge cases relevant to the domain (see categories above)
- [ ] Every row traces to a BDD scenario
- [ ] Every `Expected Output` cites its source (this spec / a named contract
      schema / the ADR) — never "as implemented"
