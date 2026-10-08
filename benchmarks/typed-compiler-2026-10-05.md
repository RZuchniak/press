# Structured types and compiler instructions: 2026-10-05

The compiler now interns named/generic/reference types, checks and rewrites their
structures, and emits typed instruction nodes. It constructs JSON instruction
arrays only at serialization. The VM and portable artifact versions are unchanged.
Twenty-eight complete artifact snapshots captured before the refactor match the
current output, including method/generic names, branch PCs, and literal encoding.

Windows/amd64, Go 1.27.1, GOMAXPROCS=12. Before and after use the same workloads
and `-samples 3 -mintime 100ms`. Results below are median sample values. Compile
times are microseconds for a whole source; execution times are milliseconds for
the whole workload (usually 1,000 iterations), not individual instructions.

| Workload | Compile before/after (µs) | Compile allocated B before/after | VM before/after (ms) | Load + execute before/after (ms) |
| --- | ---: | ---: | ---: | ---: |
| Lists | 114.1 / 111.2 | 43,524 / 44,948 | 0.741 / 0.696 | 0.786 / 0.836 |
| Maps | 99.9 / 106.6 | 43,669 / 45,002 | 0.939 / 1.001 | 0.970 / 1.047 |
| Empty program | 12.8 / 12.6 | 6,465 / 7,062 | 0.000223 / 0.000223 | 0.00829 / 0.00832 |
| Arithmetic | 43.8 / 49.3 | 20,895 / 21,655 | 0.090 / 0.094 | 0.123 / 0.126 |
| Function calls | 61.1 / 63.4 | 24,571 / 25,547 | 0.291 / 0.297 | 0.347 / 0.337 |
| Nested value copies | 131.6 / 126.1 | 43,170 / 44,750 | 0.888 / 1.041 | 0.969 / 0.979 |
| Shared references | 85.8 / 88.7 | 34,692 / 35,329 | 0.369 / 0.384 | 0.446 / 0.427 |
| Interface dispatch | 105.8 / 109.1 | 45,456 / 46,543 | 0.390 / 0.371 | 0.462 / 0.431 |
| Allocation | 62.2 / 64.7 | 26,638 / 27,661 | 0.387 / 0.386 | 0.435 / 0.431 |

Compilation adds roughly 0.6–1.6 KB of allocation traffic in these small workloads.
Warm VM allocation counts and fresh load allocation traffic remain essentially
unchanged. These samples do not establish a speedup or a performance guarantee.

The nested-copy warmed timing differed enough to justify a follow-up check. Five
additional samples of the final build had a median of 0.795 ms, with the same
8,008 allocations and about 512.6 KB allocated per workload. That spread, alongside
the unchanged artifact representation, argues for treating timing differences as
measurement variability rather than attributing them to this compiler refactor.
No runtime performance improvement is claimed.

An initial broad instruction struct increased compile allocation substantially.
The final implementation uses compact typed nodes for each operand category;
the table reports that final implementation, not the intermediate version.

Raw results: [before](typed-compiler-before-windows-amd64.json),
[after](typed-compiler-after-windows-amd64.json),
[nested-copy follow-up](typed-compiler-nested-repeat-windows-amd64.json).

Validation: all Go tests and `go vet`; all 64 historical native CLI fixtures;
the imported task-store demo; 28 complete artifact comparisons; new nested generic
inference, immutable substitution, type identity, mixed reference/value shapes,
short-circuit evaluation and control-flow checks. Existing tests continue covering
partial instruction budgets, receiver effects and ownership semantics.
