# Check the historical language cases through the native CLI as a second,
# independent path in addition to go test. Requires a built bin/press.exe.
param([string]$FixturePath = 'internal\language\testdata\regressions.json')
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$pressRoot = Split-Path -Parent $PSScriptRoot
$pressCLI = Join-Path $pressRoot 'bin\press.exe'
if (!(Test-Path -LiteralPath $pressCLI)) { throw 'Build bin/press.exe first.' }
$pressDirectory = Join-Path $pressRoot '.cache\verify'
New-Item -ItemType Directory -Force -Path $pressDirectory | Out-Null
$pressSource = Join-Path $pressDirectory 'case.press'
$pressArtifact = Join-Path $pressDirectory 'case.pbc'
$pressCases = Get-Content -Raw -LiteralPath (Join-Path $pressRoot $FixturePath) | ConvertFrom-Json
$pressCount = 0
foreach ($pressCase in $pressCases) {
    $pressCount++
    [System.IO.File]::WriteAllText($pressSource, $pressCase.source)
    $pressCompileOutput = @(& $pressCLI compile $pressSource $pressArtifact 2>&1 | ForEach-Object { $_.ToString() })
    $pressCompileCode = $LASTEXITCODE
    if ($pressCase.compileError) {
        $pressExpected = $pressCase.compileError
        if ($pressExpected.Contains('Recursive value layout')) { $pressExpected = 'Recursive value layout' }
        if ($pressCompileCode -eq 0 -or !([string]::Join("`n", $pressCompileOutput).Contains($pressExpected))) {
            throw "Case $pressCount compile mismatch: $pressCompileOutput"
        }
        continue
    }
    if ($pressCompileCode -ne 0) { throw "Case $pressCount failed compilation: $pressCompileOutput" }
    foreach ($pressExecution in $pressCase.executions) {
        $pressActual = @(& $pressCLI exec $pressArtifact 2>&1 | ForEach-Object { $_.ToString() })
        $pressExitCode = $LASTEXITCODE
        if ($pressExecution.runtimeError) {
            if ($pressExitCode -eq 0 -or $pressActual.Count -eq 0 -or !($pressActual[-1].Contains($pressExecution.runtimeError))) {
                throw "Case $pressCount runtime error mismatch: $pressActual"
            }
            $pressActual = @($pressActual | Select-Object -SkipLast 1)
        } elseif ($pressExitCode -ne 0) { throw "Case $pressCount runtime failed: $pressActual" }
        $pressExpectedOutput = @($pressExecution.output)
        if ([string]::Join("`n",$pressActual) -cne [string]::Join("`n",$pressExpectedOutput)) {
            throw "Case $pressCount output mismatch: $pressActual"
        }
    }
}
Write-Output "$pressCount native CLI regression cases passed."
