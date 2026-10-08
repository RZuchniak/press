# Backend architecture

Press prioritizes a small, maintainable compiler and runtime. Portability and a
cohesive programming model are the Java-inspired goals; building an HTTP framework
is not a backend milestone. Add language features when their semantics and internal
representation can be explained and tested, rather than expanding a library list.

## Pipeline

```text
source files -> parser / module linking -> structured type checking
             -> typed function IR -> serialization -> portable artifact
             -> structural + link validation -> typed preparation
             -> local optimization -> VM execution
```

| Responsibility | Files | Contract |
| --- | --- | --- |
| Source syntax and linking | `parser.go`, `modules.go` | Link declarations before checking; retain tokens for compile diagnostics |
| Type representation | `type_ref.go` | Intern immutable named types, generic children and references within one compilation |
| Checking and emission | `compiler.go`, `types.go`, `collections.go` | Check types through interned nodes; emit typed instructions; preserve evaluation order |
| Compiler IR | `compiler_ir.go` | Typed function signatures and instruction payloads; named branch destinations; serialize once at the output boundary |
| Portable representation | `bytecode.go` | Versioned JSON schema; decimal strings for i64 literals |
| Artifact validation | `validate.go` | Check operand shapes, known operations, collection call contracts, call targets/counts, complete struct initializers and method links |
| Execution representation | `plan.go`, `prepare.go` | Resolve operands, slots, layouts and targets once; retain original PCs |
| Optimization | `optimize.go` | Fuse only supported local sequences; retain budget fallbacks and error order |
| Value semantics | `values.go` | Primitive inline values; recursively copy owned objects; preserve explicit references |
| Collections | `collection_ops.go`, `collections_runtime.go` | Shared operation contracts; numeric runtime dispatch; immutable list/map layouts |
| Execution | `vm.go` | Own frames and scratch buffers; execute prepared instructions |
| Arithmetic semantics | `operators.go` | Shared operator identities, independent of compiler and execution instruction formats |

These boundaries stay in one Go package for now. Moving code into packages or
introducing interfaces does not by itself improve the architecture; do that when
independent consumers or test boundaries need it.

## Invariants

- Raw `[]any` instructions belong to the portable format and serialization
  boundary. Compiler emission and branch patching use typed instruction nodes.
  Operand casts belong to artifact validation/preparation, not compiler passes
  or the hot VM loop.
- A type node's identity is meaningful within its compilation only. References
  point to their target nodes, and generic types point to their argument nodes.
  Substitution creates/interns changed shapes and preserves unchanged children;
  it never mutates a template. Inference, signature equality, assignability and
  value-layout checks use the same graph.
- Source declarations and portable metadata still carry canonical string keys.
  Small adapters resolve those keys through the compilation's graph; repeated
  checks do not reparse them. Template variable bounds remain in the active
  checker environment, rather than being attached to shared named type nodes.
- Compiler function parameters, results, receivers and local bindings hold type
  nodes. Expression checker APIs still pass canonical type keys between routines;
  this boundary can be migrated independently of type semantics or the wire format.
- Compiler IR instructions store their own operand category. Only branch nodes
  have mutable destinations. Serialization preserves instruction order and PCs;
  the prepared VM still applies its own optimizations afterward.
- Collection kind is layout metadata. Enum variant names describe enum variants;
  they are not reused as collection storage tags.
- Collection arity, result presence, supported kinds and receiver mutability are
  defined together. Both the source checker and artifact loader use that contract.
- User and collection methods capture receivers before argument evaluation and
  use one shared checker for receiver mutation permissions.
- Stack values own their value payloads. Reading a binding copies a value;
  transferring a temporary into a local, parameter, field or return does not
  duplicate ownership. Retrieving collection elements produces value copies.
- Prepared plans and shared collection layouts are immutable. Each execution owns
  its guest objects, locals and stack. Scratch buffers are cleared on completion
  or errors; oversized buffers are not pooled.
- Optimizations preserve original PCs, charged instruction counts, evaluation
  order and errors at partial instruction budgets.
- Artifact metadata must not be mutated after the first Execute. The cached plan
  and cached preparation errors depend on that contract.

## Validation limits

JSON decoding checks syntax and trailing data. First execution validates the
artifact representation and links before any guest instruction runs, then prepares
and caches its plan. Invalid instructions identify the function and zero-based
instruction index. Lexical slots, scope balance and jump ranges are also checked
during preparation.

This is structural validation, not a complete bytecode type or control-flow
verifier. It does not prove stack height/types on every branch, all enum payload
uses, definite initialization, or every dynamic method call's signature. Execute
still requires trusted compiler-produced artifacts. Execution limits remain
debugging aids, not a sandbox.

## Remaining architecture work

1. Extend validation to control-flow stack state and type consistency using the
   structured representations; keep validation independent of optimizations.
2. Carry source locations through emission and preparation so runtime errors can
   identify source frames without making normal execution format strings.
3. Consider a binding pass that resolves source declarations and checker APIs to
   type nodes directly. The current adapters keep parser and wire schemas stable;
   remove them only when doing so makes the frontend easier to maintain.

Keep compile, fresh load/prepare/execute, and warmed execution measurements
separate. A successful refactor needs semantic compatibility and reasonable
performance, not a new feature or an asserted speedup.

The [cleanup measurements](../benchmarks/backend-cleanup-2026-10-05.md) compare
warmed execution before and after this separation.
The [typed compiler measurements](../benchmarks/typed-compiler-2026-10-05.md) also
cover compilation and fresh artifact loading. Twenty-eight full artifact snapshots
captured before that migration check that serialization has not changed.
