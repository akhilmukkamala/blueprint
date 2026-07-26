---
id: auth
status: approved
owner: alice
reviewed: 2026-07-21
---

# auth

### REQ-auth-001 (event-driven)

When a login request carries invalid credentials, the system shall respond 401 without setting a session cookie.

verify:
- test: TestLoginRejectsInvalid

### REQ-auth-002 (ubiquitous)

The system shall store passwords only as argon2id hashes.

verify:
- test: TestPasswordHashing
- check: gitleaks
