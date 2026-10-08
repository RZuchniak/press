# WebAssembly build and API

The existing Go compiler and VM compile to `js/wasm`. This repository provides
the module and an editor-independent JSON boundary. The website owns its worker,
editor, asset hosting, deadlines, and UI. No Node.js dependencies are required here.

## Build

From the repository root on Windows:

```powershell
.\scripts\build-wasm.ps1
# Optional output directory, relative to this repository:
.\scripts\build-wasm.ps1 -OutputDirectory bin/website-assets
```

The default output is `bin/wasm/press.wasm` and `bin/wasm/wasm_exec.js`. Generated
files are ignored by Git. The script restores `GOOS` and `GOARCH` after building
and copies the support script from the same Go toolchain as the module.

On Linux/macOS with Go on PATH:

```sh
mkdir -p bin/wasm
GOOS=js GOARCH=wasm go build -trimpath -o bin/wasm/press.wasm ./cmd/presswasm
goroot=$(go env GOROOT)
if [ -f "$goroot/lib/wasm/wasm_exec.js" ]; then
  cp "$goroot/lib/wasm/wasm_exec.js" bin/wasm/
else
  cp "$goroot/misc/wasm/wasm_exec.js" bin/wasm/
fi
```

Always pair the module with its matching `wasm_exec.js`. This target is `js/wasm`,
not the WASI target `wasip1/wasm`. The module built with Go 1.27.1 was 5,548,638
bytes uncompressed; size varies with Go versions and builds.

## Host contract

The host loads `wasm_exec.js`, creates `Go`, instantiates the module using
`go.importObject`, then starts `go.run(instance)`. Define `pressWasmReady` before
starting Go to receive the initialization callback. The Go process stays alive
for subsequent requests, so do not await `go.run` as a readiness signal.

The module exports `pressWasm(requestJSONString)`, returning a JSON string:

```js
const response = JSON.parse(pressWasm(JSON.stringify({
  action: "run", // "check", "compile", or "run"
  source: "fn main() { println(42); }",
  maxSteps: 1000000, // optional; zero uses the default
  maxDepth: 256     // optional; zero uses the default
})));
// { ok: true, stage: "execute", output: ["42"] }
```

Run the module in a dedicated worker to keep the editor responsive. Calls are
synchronous within that worker; cancellation and wall-clock deadlines require
the host to terminate it and create a fresh instance. The language repository
does not implement that hosting layer.

Responses include `ok`, `stage`, and an output array. Failures include `error`;
partial output is preserved. Stages are `request`, `compile`, `execute`, or
`internal`. Compile returns a portable artifact as a JSON **string** in `bytecode`,
preserving i64 literals as decimal strings. Check and Compile do not execute code.
Diagnostics are currently formatted messages rather than structured editor ranges.

The Go API limits requests to 1 MiB, printed output to 64 KiB, execution to
1,000,000 instructions and 256 calls. Lower execution limits are accepted. These
are usability bounds, not a memory quota or a bytecode security verifier. The
host must also bound loading, compilation, and execution time.

The API accepts one source string and exposes no filesystem operations, host
callbacks, or arbitrary bytecode loading. Imports require the file-based compiler;
virtual multi-file projects will need a future source-provider abstraction.

## Validation

`go test ./...` covers JSON precision, error handling, limits, and recovery. CI
tests the native backend and builds the WebAssembly target. Before moving the
website prototype to its own project, the actual WASM module passed all 64
historical fixtures and 7 browser integration checks, including cancellation and
timeout recovery. Browser/editor tests belong to the website project.
