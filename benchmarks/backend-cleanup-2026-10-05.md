# Backend cleanup: 2026-10-05

Windows/amd64, Go 1.27.1, GOMAXPROCS=12. Before and after use the same nine workloads
and benchmark flags: `-phase vm -samples 3 -mintime 100ms`. Every result is checked.
Times are medians for an entire workload, usually with 1,000 iterations.

| Workload | Before (ms) | After (ms) |
| --- | ---: | ---: |
| Lists | 0.716 | 0.716 |
| Maps | 0.942 | 0.895 |
| Empty program | 0.000234 | 0.000218 |
| Arithmetic | 0.091 | 0.092 |
| Function calls | 0.301 | 0.298 |
| Nested value copies | 0.982 | 1.009 |
| Shared references | 0.367 | 0.369 |
| Interface dispatch | 0.382 | 0.396 |
| Allocation | 0.382 | 0.382 |

Raw results: [before](backend-cleanup-before-windows-amd64.json),
[after](backend-cleanup-after-windows-amd64.json).

These small samples show broadly similar warmed costs, with run-to-run variation;
they do not establish a speedup. Allocation counts remain the same except for one
fewer allocation per list/map workload from removing the constructor's string tag.
Per-operation allocated bytes remain in the same range.

This phase excludes preparation. Structural validation now adds work at first
execution, outside warmed timing. Do not infer unchanged load/startup costs from
this comparison; the harness's `load_execute` phase includes that work.

Validation: all Go tests and `go vet` passed, as did 64 historical native CLI cases
and the imported task-store demo. New malformed-artifact tests cover direct and
JSON-decoded instructions and check rejection before guest side effects. Existing
tests cover shared mutation, nested copies, method argument effects, generic
specialization, historical artifacts, concurrent execution, and partial budgets.
