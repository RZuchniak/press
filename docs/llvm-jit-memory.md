# LLVM JIT execution with Go-managed memory

Research date: 2026-10-09. This note records the initial architectural proposal.
An optional prototype is now implemented; see [the current backend](llvm.md) and
[measurements](../benchmarks/llvm-jit-2026-10-09.md). The Windows prototype uses
integer frame IDs and Go-owned stack/local roots instead of per-object IDs and
cgo.Handle, and adds a native scalar path. The remaining proposals below are not
all implemented.

## Conclusion

Press can retain the standard Go garbage collector while adding LLVM JIT
execution. The most practical first design keeps guest objects in Go and exposes
them to generated code through integer IDs and runtime callbacks. It does not
make Go's collector scan LLVM-generated stack frames.

Direct integration of arbitrary LLVM code with the standard Go runtime is a
different, much larger project. If callback costs prevent useful speedups, a
separate native guest heap with tracing GC is the preferred next investigation.
Neither approach inherently requires changing Press's source-level value and
shared-reference semantics.

## Existing backend

The CLI calls CompileFile and Execute; it does not compile guest programs to Go.
Compiler instructions serialize into portable Press artifacts. Execute prepares
numeric instructions and runs a stack VM. Integers and booleans are inline;
strings, objectValue fields, lists and maps use Go-managed storage.

Relevant implementation: cmd/press/main.go, internal/language/compiler_ir.go,
internal/language/values.go, internal/language/collections_runtime.go and
internal/language/vm.go. copyValue recursively copies value contents while
preserving explicit reference identity. Existing execution cleanup clears pooled
stack/local buffers.

## Proposed bridge

```text
Press source / .pbc
        |
        v
LLVM lowering -> ORC LLJIT -> native guest functions
                                  |
                      C ABI runtime helper calls
                                  |
                                  v
                       Go execution context
                       live root slots -> Go objects
                                  |
                                  v
                         Go garbage collector
```

LLVM supplies a C API for LLJIT, including creation, module submission and symbol
lookup. A C shim can invoke the generated entry point, and exported Go callbacks
can implement object operations. Runtime symbols must be explicitly resolved;
the JIT must call cgo-generated callback entry points rather than arbitrary Go
function addresses. See the [LLJIT C API](https://llvm.org/docs/doxygen/group__LLVMCExecutionEngineLLJIT.html)
and [cgo documentation](https://pkg.go.dev/cmd/cgo).

Proposed execution contract:

1. Go creates an execution context with root slots containing real Go values.
2. One runtime/cgo.Handle represents that context. The C shim and generated code
   receive its integer representation, not a Go pointer.
3. Generated arithmetic, comparisons, branches and primitive calls operate on
   native scalars. Object-bearing values are represented by IDs for rooted slots.
4. Allocation, copying, field access, strings, collections and object mutation
   use callbacks. A callback resolves the context and slot IDs, performs the
   operation in Go, and roots an object result before returning its ID.
5. Object fields retain real Go references, so the Go collector follows the
   guest object graph, including cycles. Pointer-bearing writes occur in Go and
   therefore receive the normal Go write barriers.
6. Generated code releases obsolete root slots through helpers. Function returns
   transfer or duplicate roots before caller/callee cleanup. Final cleanup clears
   remaining roots and deletes the context handle after native execution ends.

Go documents runtime/cgo.Handle as an integer transport for Go values across C
boundaries, with explicit deletion required. The root-slot scheme above is our
proposed design built on that mechanism, not an existing LLVM integration.
See [runtime/cgo.Handle](https://pkg.go.dev/runtime/cgo#Handle).

### Root lifetime is essential

An ID on a native stack is not a GC root by itself. Its corresponding Go slot
must remain populated until every native use is complete, including uses after
callbacks that trigger GC. Overwriting a slot while another native value still
uses its ID is invalid. Slot reuse needs an explicit ownership protocol; generation
checks can detect stale IDs but cannot replace correct lifetime management.

A table retaining every allocation until execution ends is acceptable only as
a bounded experiment. It would retain dead objects indefinitely in a long-running
program. Root slots must be cleared when values are overwritten, scopes exit,
temporaries die and frames return. This tracks native roots, not reference counts
through the entire guest graph; unreachable object cycles can still be collected.

Expected errors should be caught inside Go callbacks and returned as status
codes. Generated functions propagate status and clean up frames explicitly; do
not depend on panics unwinding generated frames. Enforce instruction and depth
limits and preserve VM error ordering before claiming backend equivalence.

### Expected tradeoffs

Primitive kernels can stay in native code. Object-heavy workloads may spend much
of their time in callbacks and lose to the current VM. Fuse or batch helpers where
semantics allow, then measure. No speedup is established by this research.

Keep the LLVM backend optional. Native builds gain LLVM and a compatible C
toolchain dependency. The existing Go VM remains the browser execution path;
this native cgo bridge is not a replacement for the js/wasm target.

## Why the alternatives are harder

### Pinning the existing object graph

runtime.Pinner supports retained native pointers under the cgo rules, but pinning
an outer object is not a general solution for its mutable pointer-rich graph.
Our objects contain strings, slices, maps and nested objects. Reachable memory,
new allocations and pointer writes would all need careful handling. Pinning also
does not install LLVM stack maps into Go's collector. Restrict possible future
uses to audited pointer-free buffers; keep object graphs behind callbacks.
See [cgo pointer rules](https://pkg.go.dev/cmd/cgo#hdr-Passing_pointers).

### Direct Go runtime integration

LLVM statepoints and stack maps provide compiler mechanisms, not an automatic
adapter to the standard Go collector. Direct integration would require matching
Go's calling conventions, stack/runtime metadata, barriers, safepoints and
scheduler expectations, including the treatment of dynamically generated code.

There is relevant precedent: TangoLLVM translates selected Go compiler SSA
functions to LLVM and patches generated code plus metadata into Go object files.
This demonstrates feasibility with compiler integration, but does not provide
an off-the-shelf Press JIT. Its approach reuses Go compiler analysis and metadata
that Press does not currently produce. See the [2025 LLVM presentation](https://llvm.org/devmtg/2025-10/slides/quick_talks/gu.pdf).

TinyGo also demonstrates LLVM plus GC, but uses its own runtime/collector choices;
it is not a drop-in bridge to the standard Go collector. See [TinyGo GC options](https://tinygo.org/docs/reference/usage/important-options/).

## If a native guest heap is needed

| Option | Assessment for Press |
| --- | --- |
| Precise, nonmoving mark/sweep with explicit roots | Preferred long-term investigation for direct native object access; requires allocator, object layouts, root management and a collector. |
| Boehm conservative GC | Candidate for a faster native prototype; still requires native object representation and correct JIT/thread/root integration. Conservative retention must be measured. |
| Reference counting | Requires retain/release and cycle handling; does not automatically preserve collection of shared cycles. |
| Per-execution arena | Useful for bounded runs; unsuitable alone for sustained execution with allocation churn. |

LLVM does not include a collector. Its shadow-stack/statepoint facilities can
support a collector we choose or build. See [LLVM GC integration](https://llvm.org/docs/GarbageCollection.html)
and the [Boehm collector](https://www.hboehm.info/gc/).

A native guest heap can coexist with a Go compiler/host. Keep heap ownership
explicit at the boundary; do not assume Go traces native allocations or a native
collector traces Go allocations.

## Experiment and decision criteria

Start with primitive functions, then add one shared object type through rooted
IDs. Exercise allocations followed by forced Go GC while values are live only
through the bridge. Verify aliases, nested copies, unreachable cycles, root-slot
reuse, returns, recursion, errors and concurrent independent executions. Check
that discarded allocations can be reclaimed before a long execution ends.

Use cgo pointer checking, including GOEXPERIMENT=cgocheck2 where supported, but
also test lifetime correctness: pointer checks cannot detect all stale IDs or
excessive retention. Compare output, traps and execution budgets against the VM.

Benchmark JIT compilation separately from warm execution. Include arithmetic,
field-heavy and collection-heavy workloads and root-table memory behavior.
Continue the Go-managed design only if it delivers useful improvements on
representative workloads. Otherwise investigate a native tracing heap rather
than coupling Press to Go runtime internals.
