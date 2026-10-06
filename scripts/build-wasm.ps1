param([string]$OutputDirectory = 'bin/wasm')
$ErrorActionPreference = 'Stop'
$pressRoot = Split-Path -Parent $PSScriptRoot
$pressOutput = [IO.Path]::GetFullPath((Join-Path $pressRoot $OutputDirectory))
[IO.Directory]::CreateDirectory($pressOutput) | Out-Null
$pressPreviousOS = $env:GOOS
$pressPreviousArch = $env:GOARCH
try {
    $env:GOOS = 'js'
    $env:GOARCH = 'wasm'
    & (Join-Path $PSScriptRoot 'go.ps1') build -trimpath -o (Join-Path $pressOutput 'press.wasm') ./cmd/presswasm
    $pressGoRoot = & (Join-Path $PSScriptRoot 'go.ps1') env GOROOT
    $pressSupport = Join-Path $pressGoRoot 'lib/wasm/wasm_exec.js'
    if (-not (Test-Path -LiteralPath $pressSupport)) { $pressSupport = Join-Path $pressGoRoot 'misc/wasm/wasm_exec.js' }
    Copy-Item -LiteralPath $pressSupport -Destination (Join-Path $pressOutput 'wasm_exec.js')
} finally {
    $env:GOOS = $pressPreviousOS
    $env:GOARCH = $pressPreviousArch
}
Get-Item -LiteralPath (Join-Path $pressOutput 'press.wasm') | Select-Object Name,Length
