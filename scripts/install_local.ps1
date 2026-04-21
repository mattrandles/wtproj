$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
Set-Location $repoRoot

$installDir = if ($env:WTP_INSTALL_DIR) { $env:WTP_INSTALL_DIR } else { Join-Path $HOME ".local/bin" }
$binaryName = if ($env:WTP_INSTALL_NAME) { $env:WTP_INSTALL_NAME } else { "wtp.exe" }
$targetPath = Join-Path $installDir $binaryName

New-Item -ItemType Directory -Force -Path $installDir | Out-Null
$tmpName = ".{0}.tmp.{1}" -f $binaryName, ([guid]::NewGuid().ToString("N"))
$tmpPath = Join-Path $installDir $tmpName

try {
    & go build -trimpath -o $tmpPath ./cmd/wtp
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }

    Move-Item -Force -Path $tmpPath -Destination $targetPath
    Write-Output ("installed {0}" -f $targetPath)
}
finally {
    if (Test-Path $tmpPath) {
        Remove-Item -Force $tmpPath
    }
}
