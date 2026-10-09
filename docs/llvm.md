# Experimental LLVM backend

Press now has an optional native LLVM ORC LLJIT backend while retaining Go-managed
guest memory. The first adapter supports Windows/amd64 and has been tested with
the official LLVM 23.1.3 distribution and Go 1.27.2. The default VM remains the
normal execution backend. The portable .pbc format is unchanged.

## Run

Build the CLI with the existing Go helper:

```powershell
.\scripts\go.ps1 build -o bin/press.exe ./cmd/press
.\bin\press.exe run-llvm examples/counter.press
.\bin\press.exe compile examples/counter.press counter.pbc
.\bin\press.exe exec-llvm counter.pbc
.\bin\press.exe llvm-ir examples/counter.press counter.ll
```

This workspace has a portable LLVM library under .tools/llvm/bin. From the
repository root, the CLI finds it automatically. For another installation or
working directory, select the DLL explicitly:

```powershell
$env:PRESS_LLVM_DLL = 'C:\path\to\LLVM\bin\LLVM-C.dll'
```

For a fresh workspace, extract the x86_64 Windows LLVM distribution from the
[official LLVM release](https://github.com/llvm/llvm-project/releases/tag/llvmorg-23.1.3)
with an archive tool supporting its compression format. Preserve required DLL
dependencies beside LLVM-C.dll. This prototype uses the current ORC context API;
older libraries may lack required exports and will be rejected with a diagnostic.
LLVM executables such as lli are not required at runtime. No C compiler, cgo build
or global LLVM installation is required for this Windows adapter.

Native compilation runs LLVM's O2 IR optimization pipeline by default, verifying
each pass. The CLI accepts PRESS_LLVM_OPT=none, O1 or O2 to compare execution and
compilation costs. Optimization runs inside the DLL; no opt subprocess is needed.
llvm-ir continues to emit the original lowering for inspection.

llvm-ir only writes textual LLVM IR and does not load LLVM. It works on the other
Go-supported platforms. Native execution on those platforms returns an explicit
unsupported-platform error. The browser retains its existing Go VM.

## Execution and memory

```text
Press source / portable artifact
    -> unoptimized prepared instructions
    -> LLVM basic blocks -> ORC LLJIT -> machine code
         |                          |
         | scalar functions         | object-bearing functions
         v                          v
     native scalar slots        Go callbacks -> Go stack/local roots
                                              -> Go objects -> Go GC
```

The backend checks control-flow stack shapes and initialized local types to
identify functions containing only i64/bool operations, branches, local bindings,
printing, returns and direct calls to other eligible functions. Call-graph pruning
removes callers of unsupported bodies; recursive scalar components remain native.
Those functions keep locals, stack operands, arguments and returns in native
storage. They charge the original instruction budget directly through a pinned,
pointer-free counter buffer. Arithmetic preserves overflow traps, division
behavior and the special MIN_INT64 remainder case. Direct guest calls use an
internal ABI and share native instruction/depth counters. The public bridge
imports Go parameters and publishes the final result only at the outer boundary.
Large scalar frames use Go helpers until platform stack-probe support is added.

Selected mixed functions keep proven i64/bool loops native while retaining their
objects in Go. A pinned, pointer-free numeric exchange buffer holds primitive
locals and stack slots. Go helpers materialize these slots at boundaries and
export primitive results back into the buffer; all object references remain in
Go's root frame. Control-flow merges that lose type information use helpers.
Selection currently requires a loop with no helper operations in its repeated
body. Short numeric runs around object operations retain the general helper path,
because splitting those runs increased crossing overhead in the measurements.
Dynamic method dispatch also retains that path.

Other functions still compile into native basic blocks, but their object, string,
collection and call operations invoke Go helpers. Straight-line sequences of up
to sixteen original instructions share one callback. Each instruction still
charges its own budget and stops immediately on an error. Batches end at branch,
call, return and propagation boundaries and do not span branch targets. Mixed
functions without an eligible numeric loop perform arithmetic in the Go helper
as part of the batch. This path
remains slower than the VM, but has fewer foreign calls than the initial prototype.

An execution registry holds actual Go references while native code carries
one integer execution ID. Each execution reuses frames and numeric buffers by
call depth; callbacks resolve its current frame. Returned frames clear their Go
references immediately. Locals, stack values and object fields retain Go's normal
representation. Overwrites and scope/frame exits clear obsolete roots; no table
keeps every guest allocation alive until the run ends. Execution IDs are removed on
success and failure. Native code never dereferences guest heap pointers or writes
Go references. Go continues to handle allocation, write barriers and collection
of the guest object graph.

The Windows adapter uses syscall.SyscallN and syscall.NewCallback for foreign
runtime transitions. This replaces the originally proposed cgo.Handle transport
on this platform with a Go-owned integer registry. Callback errors are caught
before returning to native code; generated code returns normally, then Go
propagates the error and performs frame cleanup.

The helper instruction switch is generated from the existing VM source, avoiding
manual drift while leaving the VM's hot loop and escape behavior intact. Regenerate
it after changing VM instruction semantics:

```powershell
.\scripts\go.ps1 generate ./internal/language
```

## Go API

```go
compiled, err := language.CompileLLVM(program, language.LLVMOptions{
    LibraryPath: `C:\path\to\LLVM-C.dll`, // optional: environment/default lookup
    Optimization: language.LLVMOptO2, // default; also LLVMOptO1 or LLVMOptNone
})
if err != nil { return err }
defer compiled.Close()
return compiled.Execute(language.Options{MaxSteps: 1_000_000, MaxDepth: 256})
```

Compile once and reuse the compiled program to measure warmed execution.
Concurrent executions have independent guest state. Close waits for active
executions and must be called after they finish, outside execution callbacks.
Only trusted compiler-produced artifacts are supported, as with the VM.

## Validation and measurements

```powershell
.\scripts\go.ps1 test ./...
.\scripts\go.ps1 vet ./...
.\scripts\go.ps1 test ./internal/language -run LLVM -count=1 -gcflags=all=-d=checkptr=2
.\scripts\go.ps1 test ./internal/language -run '^$' -bench 'BenchmarkLLVMWorkloads|BenchmarkLLVMMixedNumericLoop|BenchmarkLLVMCompile|BenchmarkLLVMOptimizationLevels' -benchtime=150ms -count=3
```

Native tests run when the repository-local DLL exists or PRESS_LLVM_DLL is set.
Otherwise they explicitly skip; portable IR tests and VM tests still run. An
explicitly configured but broken library fails native tests rather than skipping.

Coverage includes historical execution fixtures, example programs with imports,
methods, interfaces, generics, collections and enums, forced GC during native
execution, release of dead roots before execution ends, concurrency, callback
errors, closed-program rejection and standalone artifact execution. Differential
tests compare output and errors at each partial budget across scalar and helper
paths, including overflow, zero division, short-circuiting and depth limits.

See [callback optimization measurements](../benchmarks/llvm-callback-optimization-2026-10-09.md)
and the [initial baseline](../benchmarks/llvm-jit-2026-10-09.md). Scalar arithmetic
and direct primitive calls show warmed improvements in the measured workloads;
object-heavy workloads remain slower than the VM. Compilation overhead matters. These results
do not justify selecting LLVM as the default backend.

The [mixed-function measurements](../benchmarks/llvm-mixed-2026-10-09.md) show a
numeric loop holding a Go object executing about nine times faster than the VM.
This is warm execution and excludes compilation; object-heavy workloads remain
slower than the VM.

LLVM optimization and frame reuse are now implemented. The
[optimization measurements](../benchmarks/llvm-optimization-2026-10-09.md)
compare warmed execution with compilation costs and record frame reuse savings.
Next work should reduce synchronization costs, investigate specialized numeric
collection storage, improve mixed region selection, add a portable native adapter and measure sustained allocation
behavior. The [memory research](llvm-jit-memory.md) records alternative heap models
if the bridge cannot deliver useful performance for the intended workloads.
The [follow-up LLVM research](llvm-options-2026-10-09.md) compares optimization,
lazy compilation, specialized numeric buffers and native-heap alternatives,
with a measured O2 experiment and helper profiles.
