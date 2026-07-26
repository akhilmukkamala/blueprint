# Add rate limiting to the ingest API

```toml
[loop]
predicate = "go version"
max_iterations = 5
max_minutes = 90
max_usd = 10.0
[loop.breaker]
repeat_action_n  = 3
repeat_error_n   = 3
no_diff_delta_n  = 3
oscillation_n    = 2
monologue_tokens = 4000
on_trip = "park"
[loop.boundaries]
writable = ["src/**", "tests/**"]
readonly = [".blueprint/specs/**"]
```

## Delta

ADDED REQ-ingest-004: WHEN a client exceeds 100 requests per minute the
ingest API SHALL respond 429 with a Retry-After header.

## Tasks

- [x] T1: add token bucket limiter to ingest middleware
- [ ] T2: return 429 with Retry-After on limit breach
- [ ] T3: integration test for burst traffic
