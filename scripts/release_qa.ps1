[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string] $AssetDirectory,

    [Parameter(Mandatory = $true)]
    [string] $ExpectedVersion
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

function Fail([string] $Message) {
    throw "release QA failed: $Message"
}

$assets = @(
    "wtp_darwin_amd64",
    "wtp_darwin_arm64",
    "wtp_linux_amd64",
    "wtp_linux_arm64",
    "wtp_windows_amd64.exe",
    "wtp_windows_arm64.exe"
)
$checksumsPath = Join-Path $AssetDirectory "checksums.txt"
if (-not (Test-Path -LiteralPath $checksumsPath -PathType Leaf)) {
    Fail "missing checksums.txt"
}
$checksumLines = Get-Content -LiteralPath $checksumsPath

foreach ($asset in $assets) {
    $path = Join-Path $AssetDirectory $asset
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        Fail "missing release asset $asset"
    }
    $actual = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
    $matches = @($checksumLines | Where-Object { $_ -ceq "$actual  $asset" })
    if ($matches.Count -ne 1) {
        Fail "checksum mismatch or malformed entry for $asset"
    }
}

$windowsAssets = @("wtp_windows_amd64.exe", "wtp_windows_arm64.exe")
foreach ($asset in $windowsAssets) {
    $bytes = [System.IO.File]::ReadAllBytes((Join-Path $AssetDirectory $asset))
    if ($bytes.Length -lt 2 -or $bytes[0] -ne 0x4d -or $bytes[1] -ne 0x5a) {
        Fail "$asset is not a Windows PE executable"
    }
}

if (-not $IsWindows) {
    Write-Host "release QA PowerShell: Windows assets validated; Windows execution requires a Windows host"
    exit 0
}

$hostAsset = if ($env:PROCESSOR_ARCHITECTURE -match "ARM64") { "wtp_windows_arm64.exe" } else { "wtp_windows_amd64.exe" }
$workDir = Join-Path ([System.IO.Path]::GetTempPath()) ("wtp-release-qa-" + [System.Guid]::NewGuid().ToString())
try {
    $project = Join-Path $workDir "project"
    $projectBin = Join-Path $project ".tools\bin"
    $userBin = Join-Path $workDir "user-bin"
    $globalBin = Join-Path $workDir "global-bin"
    foreach ($bin in @($projectBin, $userBin, $globalBin)) {
        New-Item -ItemType Directory -Force -Path $bin | Out-Null
        Copy-Item -LiteralPath (Join-Path $AssetDirectory $hostAsset) -Destination (Join-Path $bin "wtp.exe")
        $version = & (Join-Path $bin "wtp.exe") version
        if ($LASTEXITCODE -ne 0 -or -not ($version -match [regex]::Escape("wtp $ExpectedVersion"))) {
            Fail "installed $hostAsset did not report $ExpectedVersion"
        }
    }
    New-Item -ItemType Directory -Force -Path $project | Out-Null
    & git -C $project init -q
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    Push-Location $project
    try {
        $env:Path = "$projectBin;$env:Path"
        $task = & wtp.exe --json task create --title "Windows direct-download task" --description "release QA" | ConvertFrom-Json
        if (-not $task.shortId) { Fail "Windows create did not return shortId" }
        $claim = & wtp.exe --json task next --agent "release-qa" | ConvertFrom-Json
        if ($claim.status -ne "inProgress") { Fail "Windows claim did not start task" }
        & wtp.exe export --out exported | Out-Null
        if (-not (Test-Path -LiteralPath (Join-Path $project "exported\$($task.id).json"))) { Fail "Windows export did not write task" }
    } finally {
        Pop-Location
    }
    Write-Host "release QA PowerShell: Windows install and workflow passed"
} finally {
    if (Test-Path -LiteralPath $workDir) {
        Remove-Item -LiteralPath $workDir -Recurse -Force
    }
}
