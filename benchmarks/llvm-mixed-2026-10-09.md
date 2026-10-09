# LLVM mixed-function experiment — 2026-10-09

Windows/amd64, Ryzen 5 3600X, Go 1.27.2, LLVM 23.1.3, GOMAXPROCS 12.
Three samples per case, 150 ms minimum per sample. Programs are compiled once;
timings measure warm execution and exclude source compilation and LLVM setup.
Raw results: [llvm-mixed-windows-amd64.txt](llvm-mixed-windows-amd64.txt).

The new case creates a referenced Box, sums 0 through 999 in a numeric loop,
then prints the sum and the Box's field. Output callbacks discard the text in
this benchmark. The object remains in Go's root frame throughout native execution.

| Backend | Median execution | Allocations per run |
| --- | ---: | ---: |
| Go VM | 73.450 µs | 3 |
| LLVM mixed function | 7.981 µs | 8 |

LLVM is about 9.2 times faster here. This supports keeping Go-managed memory for
native numeric loops that coexist with objects. It does not demonstrate fast
native object access: field and collection operations still use Go helpers.

An initial broader selection also lowered arithmetic and counter operations
inside collection loops. Those short runs added helper boundaries and made
list/map cases slower in this experiment. Automatic mixed selection now requires
a loop whose repeated body can stay native. Other functions keep the existing
batched helper path. The full workload suite remains in the raw results to make
the limits visible; object-heavy execution still trails the VM.

Validation compares output and errors with the VM over instruction/depth limits,
including mixed object parameters, branch merges, collections, overflow and
scope exits. Forced-GC callbacks check that objects survive numeric execution.
Native tests also passed three runs with Go checkptr enabled.
