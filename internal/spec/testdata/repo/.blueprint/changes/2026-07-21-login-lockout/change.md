+++
id = "2026-07-21-login-lockout"
title = "Add login lockout"
type = "feat"
tier = "full"
status = "verified"
scenario = "brownfield-feature"

[loop]
predicate = "blueprint verify 2026-07-21-login-lockout"
max_iterations = 12
max_minutes = 90
max_usd = 15.0

[loop.breaker]
repeat_action_n = 3
repeat_error_n = 3
no_diff_delta_n = 3
oscillation_n = 2
monologue_tokens = 4000

[loop.boundaries]
writable = ["src/**", "tests/**"]
readonly = [".blueprint/specs/**"]
+++

# Add login lockout

## Delta

### ADDED REQ-auth-003 (unwanted)

If five consecutive login attempts fail for one account, then the system shall lock that account for 15 minutes.

verify:
- test: TestLoginLockout

### MODIFIED REQ-auth-001 (event-driven)

When a login request carries invalid credentials, the system shall respond 401 within 200 ms without setting a session cookie.

verify:
- test: TestLoginRejectsInvalid

### REMOVED REQ-auth-002

## Tasks

- [x] T1: Write TestLoginLockout covering the five-failure lock in tests/auth/lockout_test.go
  - Consumes: .blueprint/specs/auth/spec.md
  - Produces: tests/auth/lockout_test.go
- [ ] T2: Implement the lockout counter in src/auth/lockout.go keyed by account ID
  - Consumes: tests/auth/lockout_test.go
  - Produces: src/auth/lockout.go

## Design

Lockout state lives in the session store keyed by account ID with a 15-minute TTL, so no schema migration is needed.
