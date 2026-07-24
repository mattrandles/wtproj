$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

& (Join-Path $PSScriptRoot "verify.ps1") -Mode commit
exit $LASTEXITCODE
