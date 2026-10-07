# First VM optimization pass

Measured October 5, 2026 on the same Windows/amd64 host, Go 1.27.1,
GOMAXPROCS=12. These are a fresh pre-change run and the final optimized run,
not comparisons against the October 3 timing samples. The pre-change run used
three calibrated samples; the optimized run used five. Each operation executes
the same complete 1,000-step workload and verifies its result. Median timing
samples and their corresponding allocation figures are shown below.

| Workload | Before ms/op | After ms/op | Speedup | Before allocated B/op | After allocated B/op |
|---|---:|---:|---:|---:|---:|
| Arithmetic | 0.583 | 0.092 | 6.35x | 70,196 | 57 |
| Function calls | 1.216 | 0.295 | 4.12x | 477,484 | 64 |
| Nested value copies | 7.445 | 0.831 | 8.96x | 4,690,245 | 448,526 |
| Shared references | 2.249 | 0.365 | 6.17x | 1,141,380 | 160 |
| Interface dispatch | 2.577 | 0.380 | 6.78x | 1,221,443 | 156 |
| Allocation | 1.403 | 0.379 | 3.70x | 758,595 | 96,114 |

Nested-copy allocated bytes fell by approximately 90%, and allocations fell from
41,748 to 8,008 per workload. Reference/interface workloads allocate about 160
bytes per workload rather than over 1 MB. Allocated bytes represent allocation
traffic, not peak memory consumption or the application's entire live heap.

The warmed empty-program median remained approximately 238 ns (245 ns before).
An initial scratch-buffer regression was addressed before collecting final results.

## Runtime changes

- Prepare portable bytecode once into numeric instructions, pre-parsed literals,
  resolved call targets, lexical local slots and indexed struct layouts.
- Use tagged values with inline integers/Booleans instead of boxed Go interfaces.
- Reuse frame/local storage rather than allocating environment maps for calls and
  loop scopes. Clear expired scope slots to release guest references promptly.
- Copy only the result of a pure field read, not the entire enclosing value.
- Move already-owned temporary values into declarations, assignments, constructor
  fields, function arguments and returns instead of repeatedly copying them.
- Fuse primitive load/arithmetic/store and compare/branch sequences. Original PCs
  and step counts remain valid; fallbacks preserve partial instruction execution
  and error ordering near the instruction budget.
- Pool cleared scratch buffers, with a retention cap for oversized arenas.

Value structs remain independent copies; explicit references remain shared. Receiver
capture and argument snapshots retain their prior evaluation order. The serialized
bytecode format remains version 3, and earlier versions remain executable.

Prepared metadata is immutable after first execution. Concurrent executions share
only that metadata and use independent guest state. The Go-facing Program object
must not be mutated after its first Execute; creating another Program through Compile
or Decode creates a new plan. This does not impose a restriction on Press guest data.

## Cold path and measurement scope

Warmed `vm` samples exclude one-time bytecode preparation. The new `load_execute`
phase includes fresh JSON decoding, plan construction and execution every time.
Representative median timings after optimization were:

| Workload | Fresh load + execution ms/op |
|---|---:|
| Arithmetic | 0.124 |
| Function calls | 0.332 |
| Nested value copies | 1.050 |
| Shared references | 0.416 |
| Interface dispatch | 0.452 |
| Allocation | 0.421 |

The empty-program load+execute median was about 8.2 microseconds. This does not
include OS process launch. Scratch buffers may be reused between operations in
both phases; a process-start benchmark is the separate `launch_exec` phase.

Raw samples:

- `optimization-before-windows-amd64.json`
- `optimization-after-windows-amd64.json`
- `optimization-load-execute-windows-amd64.json`

Reproduce with a built benchmark executable:

```powershell
.\scripts\go.ps1 build -o bin/pressbench.exe ./cmd/pressbench
.\bin\pressbench.exe -phase vm -samples 5 -json .cache/optimized-vm.json
.\bin\pressbench.exe -phase load_execute -samples 3 -json .cache/load-execute.json
```

Validation: the full Go suite and Go vet passed. Added tests cover snapshots across
side effects, enum/return copy independence, exact fused instruction budgets and
error ordering, concurrent execution, and cleanup on errors/scope exit. All 64
historical and 44 feature cases also passed through the native CLI, and the language
tour produced the same output.

The VM remains substantially slower than native Go on these tiny workloads. Native
Go compiles arithmetic, field access and calls into optimized machine code; this
runtime still interprets instructions and performs dynamic checks. The results
justify further representation/dispatch work and eventual JIT evaluation, but do
not establish server throughput or a memory advantage over Java.
