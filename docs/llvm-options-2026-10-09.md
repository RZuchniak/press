# LLVM integration options — 2026-10-09

Follow-up: optimization and execution-frame reuse are now implemented. See
[implementation measurements](../benchmarks/llvm-optimization-2026-10-09.md).
The research experiment below records the earlier investigation.

Recommendation: add an explicit LLVM optimization pipeline, then reduce helper
frame/registry churn. Keep the Go collector while testing these changes. Consider
a native guest heap if direct object access becomes the main requirement and the
bridge still loses on representative workloads.

## What the current implementation leaves open

The Windows adapter parses/verifies IR and submits it to ORC LLJIT. It does not
invoke an explicit middle-end IR optimization pipeline. That is distinct from
LLVM's machine-code generation passes. LLVM exposes LLVMRunPasses through its
C API, accepting pipelines such as default<O2>, so this experiment can become an
in-process option without an external opt dependency. See the
[LLVM C pass API](https://llvm.org/docs/doxygen/group__LLVMCCoreNewPM.html).

I tested externally optimized IR with the existing native adapter. Windows/amd64,
Ryzen 5 3600X, Go 1.27.2, LLVM 23.1.3; three 150 ms samples per case. Programs were
compiled once. These are warmed execution medians, excluding optimization,
compilation and subprocess startup. Both variants discarded output through the
same callback, so these results should be compared within this table.

| Workload | Current LLVM | LLVM with O2 IR passes | Speedup |
| --- | ---: | ---: | ---: |
| Numeric loop holding an object | 7.962 µs | 4.633 µs | 1.72× |
| Arithmetic | 8.822 µs | 4.924 µs | 1.79× |
| Direct scalar calls | 14.227 µs | 6.060 µs | 2.35× |
| List operations | 2.189 ms | 2.225 ms | No improvement |

All shared benchmark workloads plus mixed-loop, overflow, short-circuiting,
remainder and recursion probes matched VM output/errors at step budgets 1–150,
depth limits 1–8 and normal execution. This is preliminary validation, not the
complete qualification of a production optimization option. The implementation
still needs compile-latency measurements and the full GC/concurrency/fixture
checks with optimization enabled. No production backend behavior changed.

[Raw timings](../benchmarks/llvm-optimization-probe-windows-amd64.txt) and
[reproducible probe source](../benchmarks/llvm-optimization-probe-test.go.txt)
are saved. To reproduce on the supported Windows setup, copy the probe to
internal/language/llvm_opt_probe_test.go, run its test and benchmark using
scripts/go.ps1, then remove that temporary copy. It uses repository-local opt.exe.

## What profiling suggests

A separate one-second-per-case CPU/allocation profile sampled shared-reference
and allocation workloads. About 38% of sampled allocated objects were attributed
to windowsLLVM.run and sync.Map entry creation combined. Helper instruction
dispatch was the largest flat CPU entry (27%); pop was another 7%. These are
sampled profiles of two cases, not a universal cost breakdown.

The cumulative 94% under runtime.cgocall includes nested callback/guest work; it
must not be interpreted as 94% call-boundary overhead. Go profiling also does not
fully resolve generated LLVM code. There is no evidence in this sample that GC
collection itself is the principal bottleneck. Frame registration/allocation and
generic helper dispatch are concrete candidates before replacing the collector.
[Profile summaries](../benchmarks/llvm-helper-profile-windows-amd64.txt).

## Alternatives and proposed experiments

| Option | Benefit to investigate | Limits / next experiment |
| --- | --- | --- |
| Explicit O1/O2 passes with ORC LLJIT | Improve numeric code using the existing architecture | Measure compile latency and run full differential/GC tests. O2 already improved these numeric probes. |
| Reuse Go frames and one execution-level registry handle | Remove repeated frame/registry allocation | Preserve recursion, concurrent executions and prompt clearing of dead roots; benchmark method-heavy programs. This is a Press design proposal. |
| Specialized pinned primitive buffers | Native access to numeric list data while Go owns allocation | Prototype List<i64> storage separately from pointer-bearing values. Growth requires repinning/reloading, aliases and bounds must remain correct. |
| VM plus selective/lazy JIT | Avoid compilation and callbacks for cold or object-heavy functions | Begin with function selection and VM fallback. LLLazyJIT offers function-at-first-call compilation; hotness-driven tiering still needs our own policy. |
| Native runtime with a native tracing heap | Direct native fields, collections and calls | Major representation/runtime work. Compare one object/collection kernel first, then validate aliases, copies, cycles and roots. |
| LLVM object output / AOT | Remove runtime compilation for shipped applications | Still requires a compatible runtime and linking. Does not remove Go helper costs by itself. |

ORC supports LLJIT and function-lazy LLLazyJIT. Our current single-module submission
and lookup of every public function does not exploit function-level laziness;
module partitioning or a compile-on-demand layer is needed. Lazy compilation and
hotness-based tiering are different features. See
[ORC design](https://llvm.org/docs/ORCv2.html). LLVM can also emit object files;
see its [AOT tutorial](https://llvm.org/docs/tutorial/MyFirstLanguageFrontend/LangImpl08.html).

Pinned numeric backing arrays are a narrower proposal than exposing Go object
graphs. Go's foreign-pointer rules require accessible Go memory to stay pinned
and restrict unpinned pointers inside it. Press's current List uses []value,
whose elements contain strings and object pointers; it is not a pointer-free
numeric buffer. A specialized representation would therefore be required.
See [Go's pointer rules](https://pkg.go.dev/cmd/cgo#hdr-Passing_pointers).

A small cgo C/C++ shim is another adapter option for portability and access to
ORC's richer C++ APIs. It does not inherently eliminate Go/native transitions or
make the Go collector scan arbitrary LLVM frames. Treat its speed as unmeasured;
switching adapters alone is not the proposed performance fix.

For a native heap, a precise nonmoving mark/sweep collector with explicit roots
is the long-term candidate; Boehm is an existing conservative collector worth
comparing for a prototype. Neither is implemented here. Boehm must see the guest
roots through an appropriate native integration; integer IDs retained only in
Go are not automatically discoverable pointers. LLVM supplies GC integration
mechanisms rather than a collector. See
[LLVM GC integration](https://llvm.org/docs/GarbageCollection.html) and the
[Boehm project](https://www.hboehm.info/gc/).

The immediate decision does not require choosing a new heap: numeric O2 gains
are measurable, and helper overhead remains addressable. A native-heap experiment
becomes justified if optimizing the bridge still cannot improve the object-heavy
programs Press is intended to run.
