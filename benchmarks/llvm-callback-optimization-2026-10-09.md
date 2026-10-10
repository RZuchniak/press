# LLVM callback optimization: 2026-10-09

Windows/amd64, Ryzen 5 3600X, Go 1.27.2, LLVM 23.1.3, GOMAXPROCS=12.
Three samples per benchmark at `-benchtime=150ms`; medians below measure warmed
execution of a whole workload, usually 1,000 loop iterations. Outputs are checked
on every execution. The earlier JIT values are from the [initial baseline](llvm-jit-2026-10-09.md).
Different sampling runs introduce noise; these figures are workload observations,
not general performance guarantees.

| Workload | Earlier JIT (ms) | Current JIT (ms) | Current VM (ms) |
| --- | ---: | ---: | ---: |
| Lists | 9.743 | 2.152 | 0.655 |
| Maps | 10.426 | 2.365 | 0.814 |
| Empty program | 0.001258 | 0.001136 | 0.000208 |
| Arithmetic | 0.008881 | 0.008905 | 0.093414 |
| Function calls | 6.061 | 0.015252 | 0.288769 |
| Nested value copies | 7.348 | 2.702 | 0.996 |
| Shared references | 6.167 | 1.919 | 0.334 |
| Interface dispatch | 7.015 | 3.025 | 0.360 |
| Allocation | 5.180 | 1.013 | 0.371 |

Raw output: [current benchmark samples](llvm-callback-optimization-windows-amd64.txt).

Primitive calls now stay in native code across arguments, returns and recursion.
The function-call workload is about 397x faster than the earlier callback-heavy
JIT, and 18.9x faster than the current VM sample. It also drops from roughly 4,012
Go allocations per workload to nine. This benchmark's entire call graph is scalar;
it does not predict performance for calls involving objects or interfaces.

Straight-line helper instructions are batched into one native/Go crossing, with
one frame-registry lookup per batch. Original per-instruction budgets, operation
ordering, errors and cleanup remain intact. The object-heavy workloads improve
approximately 2.3–5.1x over the earlier JIT, but still take approximately 2.7–8.4x
the VM's time in these samples.

CompileLLVM plus Close took a median of 15.36 ms for the counter loop, excluding
Press source compilation. This includes preparation, LLVM library/JIT setup,
emission, verification, code materialization and disposal. Small one-shot programs
may lose overall time despite better warmed execution. Go allocation statistics
exclude LLVM/native heap and executable memory; process RSS was not measured.

## Implementation

- Scalar eligibility checks direct-call signatures and removes callers of
  unsupported bodies to a fixed point. Mutual recursion remains eligible.
- Internal scalar functions exchange arguments/results through native scalar
  slots. Public entries import Go values and publish results once.
- A pinned, pointer-free execution buffer holds instruction and depth counters.
  Native calls preserve the VM's call-depth and error ordering, including failure
  propagation and restoration of depth during returns.
- Helper batches contain at most sixteen original instructions, do not cross
  branch targets, and terminate at control-flow/call boundaries. Failures abort
  the current batch immediately.
- Guest objects remain rooted in Go storage. Neither direct calls nor batching
  introduces native-held Go heap pointers. The default VM is unchanged.

## Validation

All tests and go vet passed, including repeated LLVM checks with
`-gcflags=all=-d=checkptr=2`. Expanded differential tests cover nested multi-argument
calls, boolean and void results, self/mutual recursion, pruning through unsupported
callees, mixed native/helper calls, per-step budgets, depth limits, arithmetic
failures, short-circuiting, enums and propagation. Forced-GC runs verify live roots
across callbacks, and registry checks verify cleanup after nested errors.

These results support continuing the optional backend. Native scalar execution
inside mixed object functions and helper-frame reuse are the next performance
targets.
