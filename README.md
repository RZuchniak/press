# Press language prototype

A working first slice of the language discussed in this project. **Press** and
`.press` are provisional names. This is a language experiment, not a production
runtime or a performance demonstration.

The compiler and VM are written in Go, using only its standard library. Building
requires Go 1.22 or newer. The compiled CLI needs no Node.js or installed Go runtime.

```powershell
go test ./...
go build -o bin/press.exe ./cmd/press
.\bin\press.exe run examples/counter.press
.\bin\press.exe run examples/results.press
.\bin\press.exe run examples/interfaces.press
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
New artifacts use bytecode version 3; versions 1 and 2 remain executable.

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

## Next stages

1. Collections, modules, diagnostics with source locations preserved in bytecode,
   and a stronger bytecode verifier.
2. Lightweight tasks, locks, cancellation and a small HTTP/JSON library.
3. Measure startup, memory, throughput and latency of the Go-hosted VM. Evaluate
   LLVM JIT/AOT if those measurements justify the additional runtime/GC work.

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
- `internal/language/types.go`: methods, interfaces, generic validation and specialization.
- `internal/language/vm.go`: value copying, shared references and execution.
- `internal/language/testdata/regressions.json`: 64 source cases captured from the
  original implementation, covering output, compile errors and runtime errors.
- `internal/language/testdata/node-*.json`: original compiled artifacts retained
  to test execution compatibility without needing Node.

Go tests also exercise receiver mutation, interface dispatch, generic specialization,
template validation, arithmetic boundaries, runaway recursion, malformed artifacts,
and CLI compilation followed by execution after deleting the original source.
