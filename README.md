# Press language prototype

A working first slice of the language discussed in this project. **Press** and
`.press` are provisional names. This is a language experiment, not a production
runtime or a performance demonstration.

The current priority is a small, cohesive compiler and runtime: portable artifacts,
predictable value semantics, clear internal contracts, and measured performance.
Java-inspired portability does not require a bundled web framework. Feature growth
follows backend quality rather than a checklist of application libraries.

The compiler and VM are written in Go, using only its standard library. Building
requires Go 1.22 or newer. The compiled CLI needs no Node.js or installed Go runtime.

```powershell
go test ./...
go build -o bin/press.exe ./cmd/press
.\bin\press.exe run examples/counter.press
.\bin\press.exe run examples/results.press
.\bin\press.exe run examples/interfaces.press
.\bin\press.exe run examples/tasks/main.press
.\bin\press.exe check examples/counter.press
.\bin\press.exe compile examples/counter.press counter.pbc
.\bin\press.exe exec counter.pbc
```

On this workspace, an official Go toolchain is available locally in `.tools/go`.
If Go is not on PATH, use the helper for build and test commands:

```powershell
.\scripts\go.ps1 test ./...
.\scripts\go.ps1 build -o bin/press.exe ./cmd/press
.\scripts\verify-fixtures.ps1
```

The helper uses the local toolchain when available, otherwise Go on PATH, and keeps
the build cache in `.cache/go-build`. Both directories and built executables are
ignored by Git. On Linux/macOS use `go build -o bin/press ./cmd/press` and `./bin/press`.
The fixture verification script exercises the historical cases through the built
native CLI, supplementing the Go unit tests.

## Benchmarking

The Go compiler and VM also build for the browser. See the
[WebAssembly build and API instructions](docs/webassembly.md). This repository
provides the Go WebAssembly target and JSON API; website and editor integration
belong in the separate website project.

Build `cmd/pressbench` to measure compilation separately from VM execution,
allocations and native Go reference workloads. Optional CLI subprocess measurements
cover launch plus execution. See [benchmark instructions and the recorded baseline](benchmarks/README.md)
for commands, profiling and the limits of these measurements.

`run` compiles and executes source. `compile` writes a versioned JSON instruction
artifact; `exec` executes it without parsing or compiling the original source.
The artifact contains typed function signatures, struct layouts and stack-machine
instructions. Integer constants are decimal strings to preserve all 64 bits.
It can run on supported platforms with a native Press CLI built for that platform.
Application bytecode remains portable; the CLI executable is platform-specific.
It is not JVM or
LLVM bytecode, and compatibility across future format versions is not promised.

## Implemented

- Lexer, parser, compile-time type checking, bytecode emitter and stack VM.
- `fn`, typed parameters, recursion and return values; required `fn main()` entry.
- `let` bindings permit reassignment; `const` bindings forbid it.
- `i64`, `String`, `bool`; no null literal or implicitly nullable references.
- Structs with `let` and `const` fields and complete field initialization.
- Value-copy semantics for structs, including nested value fields.
- `&S` shared, GC-managed references, constructed with `&S { ... }`.
- Automatic reference following for fields and shared field mutation.
- Lexical block scopes, `if`/`else`, `while`, arithmetic, comparisons, short-circuit
  Boolean operators and primitive `println`.
- Exact signed 64-bit arithmetic with overflow traps and division toward zero.
- Typed `List<T>` and `Map<K, V>`, with value copies or explicit shared references.
- Relative file imports linked into a standalone artifact.
- Custom tagged enums with zero or multiple payload values.
- Built-in `Option<T>` and `Result<T, E>`, including nested types.
- Exhaustive match expressions with scoped, const payload bindings.
- `?` propagation for both `Result` errors and `Option` absence.
- Struct methods using implicit `self`, with explicit `mut fn` receivers.
- Explicit interfaces, multiple `implements`, and dispatch through shared references.
- Generic functions and structs, interface bounds, inference and bytecode specialization.

See `examples/counter.press` for a runnable example. Its output is:

```text
1
0
0
2
4
Shared mutation works.
```

## Collections and file imports

```rust
import "store.press";

fn main() {
    let values = List<i64>(10, 20);
    values.push(30);
    println(match (values.get(0)) { Some(n) => n, None => -1 });

    const scores = &Map<String, i64>();
    scores.set("Ada", 42);
    println(scores.len());
}
```

Collection constructors require explicit type arguments. `List<T>(...)` accepts
initial elements; `Map<K, V>()` constructs an empty map. Map keys currently support
`String`, `i64`, and `bool`. Collections are invariant, like other generic types.

| Method | List<T> | Map<K, V> |
| --- | --- | --- |
| `len()` | `i64` | `i64` |
| `get(key)` | `Option<T>`; index is `i64` | `Option<V>` |
| `set(key, value)` | `bool`; false for an invalid index | `Option<V>` containing the replaced value |
| `push(value)` | `void` | — |
| `pop()` | `Option<T>` | — |
| `contains(key)` | — | `bool` |
| `remove(key)` | — | `Option<V>` containing the removed value |

Indices start at zero. Negative or out-of-range list lookups return `None`.
Assignment and value parameters recursively copy collection contents, preserving
the identity of explicit references inside them. Lookup returns a value copy;
update the collection with `set` to save changes to a retrieved value. Use
`&List<T>(...)` or `&Map<K, V>()` when shared storage is intended or copying a large
collection would be expensive. `const` forbids mutating a value collection;
a const binding to a shared collection still allows shared mutation.

Imports are relative to the importing file. `run`, `check`, and `compile` load the
whole dependency graph; an imported file needs no `main`. Each normalized path is
loaded once (case-insensitively on Windows), import cycles are rejected, and up to
256 files are supported. All declarations currently share one namespace, with
duplicates rejected. Namespaces, aliases, private declarations, package management,
collection iteration, and map enumeration are deferred.

For Go integrations, use `language.CompileFile(path)` to resolve imports;
`language.Compile(source)` remains available for a single source string without
imports. Linked bytecode includes dependencies, so execution needs no source files.
See `examples/tasks/main.press` and `examples/tasks/store.press` for a runnable
in-memory task store using imports, collections, structs, and `Option` propagation.

## Methods, interfaces and generics

```rust
interface Identifiable {
    fn identity() -> i64;
}

struct Counter implements Identifiable {
    const id: i64;
    let count: i64;

    fn identity() -> i64 {
        return self.id;
    }

    mut fn increment() {
        self.count += 1;
    }
}

fn sameIdentity<T: Identifiable>(left: T, right: T) -> bool {
    return left.identity() == right.identity();
}

fn printIdentity(item: &Identifiable) {
    println(item.identity());
}
```

Methods access the original receiver, not a hidden copy. `mut fn` can run on a `let`
value binding, a mutable nested value field, or a shared reference. A `const` value
cannot call a mutating method. Methods cannot replace or shadow `self`.
Ordinary `fn` methods cannot mutate the receiver's direct value fields or invoke mut
methods on those fields. They may still mutate explicitly shared objects or perform
I/O; `fn` is not a purity guarantee. Reading or returning `self` follows normal value
copy rules.

Interfaces require exact method signatures, including the `mut` qualifier. Structs
declare `implements InterfaceA, InterfaceB`; matching methods alone do not confer
implementation. An `&Counter` can implicitly convert to `&Identifiable` in a binding,
argument, field, constructor payload or return. It retains the same shared object.
Interface calls dispatch to that object's concrete method, and interface values
expose only the declared methods. Bare interface values, class inheritance, interface
inheritance and default methods are not supported. Use composition for data reuse.

```rust
struct Pair<T> {
    const first: T;
    const second: T;

    fn firstValue() -> T {
        return self.first;
    }
}

fn identity<T>(value: T) -> T {
    return value;
}

fn main() {
    const pair = Pair<i64> { first: 1, second: 2 };
    println(pair.firstValue());
    println(identity("hello"));
    println(identity<i64>(3));
}
```

Generic function arguments are inferred from argument types or specified explicitly.
Generic struct arguments can be explicit, inferred from initializer fields, or
supplied by an expected type annotation/return/argument. Generic types are invariant:
`Pair<A>` does not convert to `Pair<B>` when A and B differ. Multiple bounds use
`T: Identifiable + Printable`; bounds also work on generic structs.

The compiler checks generic bodies using their declared bounds even when no calls
instantiate them. Unconstrained `T` does not provide arbitrary fields, methods or
arithmetic operations. Concrete instantiations produce separate bytecode functions
and struct layouts; templates are not interpreted at runtime. This is specialization
of bytecode, not a machine-code JIT. Specialization limits catch unbounded expansion.

Method overloading, generic methods, associated types and custom specialization
overrides are deferred. Inference currently requires enough argument/field context;
if it cannot determine a type, supply explicit type arguments. Built-in enum
constructors continue to use expected types rather than explicit constructor arguments.

`examples/interfaces.press` demonstrates value mutation, shared interface dispatch,
multiple bounds, inferred and explicit function calls, and generic struct methods.
Artifacts containing collection instructions use bytecode version 4; other programs
still emit version 3. Versions 1 through 4 remain executable with the current CLI.

## Absence, errors and tagged enums

`Option<T>` has `Some(T)` and `None` variants. `Result<T, E>` has `Ok(T)`
and `Err(E)` variants. Constructors receive their expected type from variable
annotations, return signatures, function parameters, struct fields or match arms.
`Some(value)` can also infer an Option type directly; `None`, `Ok` and `Err` need
context because they do not supply all type arguments.

```rust
enum LookupError {
    Unavailable(String),
    InvalidId,
}

fn findUser(id: i64) -> Result<Option<User>, LookupError> {
    // See examples/results.press for the implementation.
}

fn userName(id: i64) -> Result<String, LookupError> {
    const user = findUser(id)?;
    return Ok(match (user) {
        Some(found) => found.name,
        None => "Anonymous",
    });
}
```

Custom enum constructors use qualified names, e.g. `LookupError::InvalidId`
or `LookupError::Unavailable("offline")`. Matches require parentheses around
their subject and expression-valued arms separated by commas. All variants must
appear exactly once, every arm must return the same type, and payload bindings
exist only within their arm. `_` discards an individual payload; catch-all arms,
nested patterns, guards and statement blocks in arms are not implemented yet.

`?` unwraps `Some`/`Ok`. On `None`/`Err` it immediately returns from the enclosing
function, even inside a loop or match arm. An Option operand requires an Option
return type; a Result operand requires a Result return type with exactly the same
error type. The success types can differ. No implicit error conversion is performed.

Enum payloads follow the existing value-copy rules: copying an enum copies nested
value structs and enums, while its explicit references preserve shared identity.
Recursive value layouts are rejected; references to structs can break layout cycles.

The complete lookup example prints:

```text
Alice
Anonymous
Invalid user ID
Database unavailable
```

Enum metadata and instructions were introduced in bytecode version 2. The runtime
continues to accept those artifacts and version 1 artifacts from the original prototype.

## Semantics selected for the prototype

Assignments, arguments and returns copy struct values. Copies preserve the identity
of any reference fields. Copying an `&S` shares its target. No borrow checker or
lifetime annotations are involved. `&existingVariable` is intentionally unsupported
until address-taking semantics are decided.

`const` is not deep immutability. A const value struct's direct value fields cannot
be assigned through that binding. A const reference can mutate `let` fields on its
target. A field declared `const` cannot be replaced, but if it contains a reference,
the referenced object's mutable fields can still change. Function parameters are
const bindings; copy a value parameter into a local `let` to modify the local copy.

The VM represents shared objects as ordinary Go pointers, so Go's runtime and GC
manage their memory. There is no extracted collector or separate collector integration.
Execution is single-threaded. There are configurable instruction and call-depth limits to catch runaway
programs. These limits are debugging aids, not a security boundary. Only execute
trusted compiler-produced bytecode: the loader is not an untrusted-code verifier.

The runtime prepares each Program once into numeric instructions, local slots and
indexed struct layouts. Primitive stack values avoid Go interface boxing. Field
reads copy only the selected result, and owned temporary values move between stack,
locals, arguments and returns without redundant copies. Ordinary value assignment
still produces independent nested value fields; explicit references still share.
Short primitive instruction sequences are fused internally while keeping original
bytecode PCs and instruction-limit/error ordering through a fallback path.

Scratch arenas are pooled, cleared after execution (including errors), and capped
to avoid retaining oversized buffers. Concurrent executions share immutable prepared
metadata but have independent guest state. For Go callers, do not mutate a Program's
executable metadata after its first Execute. Preparation is outside warmed VM timing;
`load_execute` benchmarks include JSON decoding, preparation and execution.

## Next stages

1. Strengthen artifact validation with control-flow stack and type checks, building
   on the interned type graph and typed compiler instructions.
2. Preserve source locations through bytecode and improve runtime diagnostics.
3. Measure startup, preparation and execution independently as internals evolve.
   Evaluate LLVM JIT/AOT only if measurements justify the additional runtime/GC work.

Collection iteration, cleanup syntax, namespaces and concurrency remain possible
extensions. They are not prerequisites for the next backend milestone. See the
[backend architecture notes](docs/backend.md) for current boundaries and invariants.

There is no server framework, JIT or performance claim yet.
Strings support concatenation and standard quoted escapes, not interpolation.
Conditions require parentheses. Non-void functions currently must end with an
explicit return; full control-flow return analysis is deferred. Integer overflow
traps are a provisional choice. Compile-time constants and deep immutable types
are also separate future decisions.
User-defined enums are not generic yet; generic functions and structs are supported.

## Implementation and migration checks

- `cmd/press`: native CLI and file-based integration tests.
- `internal/language/parser.go`: lexer, syntax tree and parser.
- `internal/language/compiler.go`: type checker and bytecode generation.
- `internal/language/type_ref.go`: immutable interned types, structural substitution and inference.
- `internal/language/compiler_ir.go`: typed compiler instructions and portable serialization.
- `internal/language/types.go`: methods, interfaces, generic validation and specialization.
- `internal/language/collections.go`: type checking and emission for collection operations.
- `internal/language/collection_ops.go`: shared collection operation contracts.
- `internal/language/collections_runtime.go`: collection storage and numeric dispatch.
- `internal/language/modules.go`: file imports and linking before type checking.
- `internal/language/bytecode.go`: portable artifact schema and JSON decoding.
- `internal/language/validate.go`: instruction representation and link validation.
- `internal/language/values.go`: copying, shared references and primitive operations.
- `internal/language/vm.go`: frames, execution limits and instruction execution.
- `internal/language/plan.go`: typed internal execution representation.
- `internal/language/prepare.go`: cached instruction lowering, layouts and slot resolution.
- `internal/language/optimize.go`: local optimization with original instruction budgets.
- `internal/language/operators.go`: arithmetic identities shared across compiler and runtime.
- `internal/language/testdata/compiler-artifacts.json`: 28 complete artifacts captured
  before the type/IR refactor, checked against current compiler output.
- `internal/language/testdata/regressions.json`: 64 source cases captured from the
  original implementation, covering output, compile errors and runtime errors.
- `internal/language/testdata/node-*.json`: original compiled artifacts retained
  to test execution compatibility without needing Node.

Go tests also exercise receiver mutation, interface dispatch, generic specialization,
template validation, arithmetic boundaries, runaway recursion, malformed artifacts,
and CLI compilation followed by execution after deleting the original source.
