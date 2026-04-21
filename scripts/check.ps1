$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
Set-Location $repoRoot

$unformatted = & gofmt -l ./cmd ./internal
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}
if ($unformatted) {
    Write-Error ("gofmt needed for:`n{0}" -f ($unformatted -join [Environment]::NewLine))
}

& go test ./...
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}

& (Join-Path $PSScriptRoot "e2e_smoke.ps1")
exit $LASTEXITCODE
