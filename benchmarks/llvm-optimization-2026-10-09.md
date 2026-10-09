# LLVM optimization and frame reuse — 2026-10-09

Windows/amd64, Ryzen 5 3600X, Go 1.27.2, LLVM 23.1.3, GOMAXPROCS 12.
Three 150 ms samples per case; table entries are medians. Benchmarks ran after
other verification work finished. Raw results:
[llvm-optimization-windows-amd64.txt](llvm-optimization-windows-amd64.txt).

Compilation runs LLVMRunPasses inside the loaded LLVM library with verification
after each pass. O2 is the default; O1 and none are selectable through the API or
the CLI's PRESS_LLVM_OPT environment variable. LLVMIR remains the unoptimized
lowering. No external optimizer process is used by the backend.

## Warm execution

Source is compiled once and compiled code reused. Output callbacks discard text.
These timings exclude both source and LLVM compilation.

| Workload | none | O1 | O2 |
| --- | ---: | ---: | ---: |
| Numeric loop holding an object | 7.927 µs | 4.796 µs | 5.301 µs |
| Arithmetic | 7.576 µs | 4.590 µs | 4.471 µs |
| Scalar calls | 11.662 µs | 5.277 µs | 5.247 µs |
| List operations | 2.590 ms | 2.547 ms | 2.240 ms |
| Shared references / method calls | 1.783 ms | 1.793 ms | 1.785 ms |

O2 improved arithmetic by 1.69× and scalar calls by 2.22× over none in this run.
O1 is competitive and faster for the mixed-loop case. These small samples do not
establish O2 as best for every program. Object-heavy execution still trails the VM.

## LLVM compilation plus disposal

These timings start from an already compiled Press Program and include prepared
plan generation, IR lowering, DLL/JIT setup, optimization, native compilation,
symbol lookup and Close. They exclude Press source compilation and process launch.

| Workload | none | O1 | O2 |
| --- | ---: | ---: | ---: |
| Numeric loop holding an object | 4.727 ms | 10.457 ms | 13.080 ms |
| Arithmetic | 4.819 ms | 14.257 ms | 15.614 ms |
| Scalar calls | 7.266 ms | 22.079 ms | 24.262 ms |
| List operations | 3.785 ms | 5.490 ms | 4.964 ms |
| Shared references / method calls | 2.597 ms | 3.781 ms | 4.014 ms |

Optimization pays off through repeated execution. For the arithmetic example,
about 11 ms additional O2 compile/disposal cost buys about 3 µs per warm execution;
roughly 3,500 executions are needed to offset that difference. This is a workload
estimate, not a universal threshold. For short one-shot programs the VM or none
can be preferable; tiered/lazy compilation remains future work.

## Frame reuse

Each execution registers one Go-owned context. Synchronous guest calls reuse
frames and pinned numeric storage by depth rather than allocating/registering a
new frame every call. On return, each frame clears all references and numeric
contents; its backing numeric buffer can serve a later call. Recursive calls use
separate frames, and concurrent executions have separate contexts.

The shared-reference workload now reports 2,014 allocations and about 113 KB per
run. The [previous mixed-function run](llvm-mixed-windows-amd64.txt) reported 6,014
allocations and about 329 KB: approximately two thirds fewer allocations and
216 KB less allocated per execution. This is a comparison with an earlier run,
not a simultaneous controlled latency comparison. Remaining object/value copies
still allocate. Scalar-only runs add two small context/frame bookkeeping
allocations; reuse chiefly benefits frequent helper calls.

## Validation

All tests and vet passed. Native tests passed three times with checkptr enabled,
including historical fixtures, examples, overflow/division, instruction/depth
limits, callbacks, concurrency, Close and forced GC. Separate mode tests compare
none/O1/O2 against the VM on benchmark sources and numeric regressions. A lifetime
test keeps an outer execution alive while confirming returned child-frame objects
can be collected and caller frames are restored correctly.
