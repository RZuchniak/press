# Interpreter baseline

The [backend cleanup comparison](backend-cleanup-2026-10-05.md) records warmed
execution before and after separating transport, preparation, values and execution.
The [typed compiler comparison](typed-compiler-2026-10-05.md) records compilation,
fresh loading and execution after adding structured types and typed compiler IR.

Build the CLI and standalone benchmark executable first. There is no Node dependency.

```powershell
.\scripts\go.ps1 build -o bin/press.exe ./cmd/press
.\scripts\go.ps1 build -o bin/pressbench.exe ./cmd/pressbench
.\bin\pressbench.exe -iterations 20 -samples 3 -cli .\bin\press.exe -json .cache/baseline.json
```

On Linux/macOS build `bin/press` and `bin/pressbench` with `go build`, then use
`./bin/pressbench -cli ./bin/press`. JSON output paths must have an existing parent
directory. Run the built executable rather than `go run` when collecting results.

Each operation is one complete workload, normally 1,000 loop iterations. The empty
program is the exception and prints only `0`. The harness verifies every result,
checks a JSON bytecode round trip, warms the workload, and separates:

- `compile`: source parsing, type checking and bytecode generation.
- `vm`: execution of already-compiled bytecode, including frames, scopes, value
  copying, formatting and checksum verification; console output is suppressed.
- `load_execute`: decoding a fresh bytecode artifact, preparing its executable
  plan, and execution. This includes costs excluded from warmed VM timing.
- `native_go`: the equivalent algorithm using native typed Go values. Compiler
  optimizations remain enabled. Selected call/allocation boundaries use `noinline`
  so they actually exercise the intended operation. These are comparison points,
  not proof of equivalent runtime representations or attainable VM throughput.
- `launch_exec`: optional child-process launch, bytecode loading, execution and
  output capture. The empty-program row approximates warmed launch overhead; this
  is not cold-disk startup. Its memory fields belong to the parent benchmark
  process, **not** the child, and are labeled accordingly in JSON.

`-iterations` is a minimum. Each sample automatically increases the count until it
runs for at least `-mintime` (default `100ms`), preventing fast workloads from being
reported below clock resolution. Calibration batches are excluded from reported
samples. A GC precedes each batch, outside the timer. GC work during execution
remains included. Results include `ns/op`, allocated `B/op`, `allocs/op`, GC cycles,
GC pause time and live heap bytes at the end of a batch. Allocations are truncated
to integer averages. Heap size is not per-operation memory or peak process RSS.

## Collection baseline: 2026-10-05

Collection workloads added on 2026-10-05 insert 1,000 values, then look them up
and sum them. Three calibrated samples on Windows/amd64, Go 1.27.1, GOMAXPROCS=12:

| Workload | Warm VM median (ms) | Native Go median (ms) | VM allocated B/op | VM allocs/op |
| --- | ---: | ---: | ---: | ---: |
| List operations | 0.722 | 0.016 | 202,017 | 2,017 |
| Map operations | 1.018 | 0.099 | 539,106 | 3,028 |

Raw results: [lists](collections-list-windows-amd64.json) and
[maps](collections-map-windows-amd64.json). These use explicit shared collections
to avoid deep copies on every lookup. The VM still allocates `Option` wrappers for
lookups and map replacement results. These figures measure allocation traffic,
not a memory ceiling or server performance. Native algorithms use Go slices/maps
with primitive elements, rather than the VM's tagged values and Option objects.

## Original baseline: 2026-10-03

Windows/amd64, Go 1.27.1, GOMAXPROCS=12, three calibrated samples per phase. Raw
samples and the actual iteration counts are in `baseline-windows-amd64.json`.
The table uses median execution time and the allocation figures from that sample.
Times vary with scheduling, hardware, Go version and profiling overhead.

| Workload | VM ms/op | Allocated bytes/op | Allocations/op | Native Go ns/op |
|---|---:|---:|---:|---:|
| Arithmetic | 0.572 | 70,193 | 3,727 | 303 |
| Function calls | 1.160 | 477,457 | 9,606 | 2,023 |
| Nested value copies | 5.818 | 4,689,940 | 41,747 | 2,043 |
| Shared references | 1.880 | 1,141,378 | 13,501 | 1,739 |
| Interface dispatch | 1.983 | 1,221,405 | 16,501 | 2,582 |
| Allocation | 1.265 | 758,611 | 9,727 | 9,317 |

The empty bytecode program took approximately 240 ns inside an existing VM host
and 19.6 ms for child-process launch plus execution on this machine. These describe
different scopes and should not be compared as the same measurement.

Nested value copying is the clearest initial optimization candidate. Ordinary
arithmetic also allocates substantially, which suggests examining boxed VM values
and frame/scope handling before pursuing a JIT. These measurements do not establish
server throughput, idle server memory, tail latency or a lower memory ceiling than
Java. Collections, concurrency and HTTP functionality are still needed for that.
In a separate allocation profile of the nested-copy workload, `copyValue` accounted
for about 85% of sampled allocated bytes. This supports targeting copy representation
and redundant copies first; profiling overhead is excluded from the timing table.

## Profiling

```powershell
.\bin\pressbench.exe -workload nested_value_copies -phase vm -samples 1 -mintime 2s -cpuprofile .cache/nested.cpu.pprof -heapprofile .cache/nested.heap.pprof
.\scripts\go.ps1 tool pprof -top .cache/nested.cpu.pprof
.\scripts\go.ps1 tool pprof -top -alloc_space .cache/nested.heap.pprof
```

Profiles cover the selected benchmark process, including warmup, calibration and
explicit pre-batch GC. Profiling changes timing; compare unprofiled samples for
performance conclusions. The heap profile supports both live-heap and cumulative
allocation views through pprof. Binary profiles remain in the ignored cache.

The October 5 optimization report is in [optimization-2026-10-05.md](optimization-2026-10-05.md).

Validation: all workload checksums and bytecode round trips passed; 64 historical
and 44 feature cases passed through the native CLI. Go vet passed. Windows
Application Control blocked some generated Go unit-test executables locally; CI
runs the full suite on Linux, where that local restriction does not apply. A pushed
workflow is not itself evidence that CI has passed.
