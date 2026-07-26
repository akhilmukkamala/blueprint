---
reviewed:            # YYYY-MM-DD — set when a human curates this file (knowledge lint enforces freshness, max-age 90 days)
owner:               # who answers questions about this map
status: draft
---

# Architecture — boundary map

## Modules

| Module   | Responsibility                     | May depend on |
| -------- | ---------------------------------- | ------------- |
| _core_   | _shared types, no business logic_  | _nothing_     |
| _..._    | _one line each; delete these rows_ | _..._         |

## Dependency direction

_State the one-sentence rule an agent can check a diff against, e.g. "cli ->
service -> store; nothing imports upward; core imports nothing." A boundary
map without a direction rule is a drawing, not a contract._

## Dataflow trace: <name one critical path>

_Walk one request or job end to end: entry point -> module -> module -> side
effect, naming the actual files. One honest trace beats ten abstract diagrams._

See also: [glossary](glossary.md), [tech debt](debt.md).
