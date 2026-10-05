# Use raw arguments so Go flags such as -o are not bound as PowerShell parameters.
$pressGoArguments = $args
$ErrorActionPreference = 'Stop'
$pressRoot = Split-Path -Parent $PSScriptRoot
$pressLocalGo = Join-Path $pressRoot '.tools\go\bin\go.exe'
$pressGoCommand = Get-Command go -ErrorAction SilentlyContinue
if (Test-Path -LiteralPath $pressLocalGo) { $pressGo = $pressLocalGo }
elseif ($pressGoCommand) { $pressGo = $pressGoCommand.Source }
else { throw 'Install Go 1.22 or newer, or place the official Go archive in .tools/go.' }
$pressPreviousCache = $env:GOCACHE
$pressPreviousTelemetry = $env:GOTELEMETRY
$pressPreviousToolchain = $env:GOTOOLCHAIN
Push-Location $pressRoot
try {
    $env:GOCACHE = Join-Path $pressRoot '.cache\go-build'
    $env:GOTELEMETRY = 'off'
    $env:GOTOOLCHAIN = 'local'
    & $pressGo @pressGoArguments
    if ($LASTEXITCODE -ne 0) { throw "Go exited with code $LASTEXITCODE" }
} finally {
    Pop-Location
    $env:GOCACHE = $pressPreviousCache
    $env:GOTELEMETRY = $pressPreviousTelemetry
    $env:GOTOOLCHAIN = $pressPreviousToolchain
}
