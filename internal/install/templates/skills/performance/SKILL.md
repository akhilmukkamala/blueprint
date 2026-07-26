---
name: performance
description: Blueprint performance playbook — measure a baseline, set an explicit threshold, gate the change on the benchmark.
---

# Performance

1. Route it: `blueprint new "perf: <hotspot>" --type perf` (light tier plus a threshold file).
2. Measure first: run the benchmark on main and commit the baseline numbers plus the target threshold to the change contract. No baseline, no perf change.
3. `blueprint approve <id>`, then optimize until the benchmark beats the threshold and the suite stays green.
4. Gate it: wire github-action-benchmark per the commented `bench-threshold` example in .blueprint/verifiers.toml (per-PR alert-threshold + fail-on-alert), so regressions fail CI, not code review.
5. `blueprint verify <id>` and `blueprint close <id>` when green.

Report numbers, not adjectives: before, after, threshold — in the change.md.
