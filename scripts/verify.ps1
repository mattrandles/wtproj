[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet("commit", "release")]
    [string] $Mode = "commit"
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
Set-Location $repoRoot

function Fail([string] $Message) {
    throw "verification failed: $Message"
}

function Require-Command([string] $Name, [string] $Scope) {
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        Fail "$Scope requires '$Name'; install it and retry"
    }
}

function Invoke-Checked([string] $Description, [scriptblock] $Command) {
    & $Command
    if ($LASTEXITCODE -ne 0) {
        Fail "$Description exited with code $LASTEXITCODE"
    }
}

Require-Command "go" "commit verification"
Require-Command "git" "commit verification"

$unformatted = & gofmt -l ./cmd ./internal
if ($LASTEXITCODE -ne 0) {
    Fail "gofmt -l failed with code $LASTEXITCODE"
}
if ($unformatted) {
    Write-Error ("gofmt needed for:`n{0}" -f ($unformatted -join [Environment]::NewLine))
    exit 1
}

Invoke-Checked "go test ./..." { & go test ./... }
Invoke-Checked "go vet ./..." { & go vet ./... }
Invoke-Checked "PowerShell smoke test" { & (Join-Path $PSScriptRoot "e2e_smoke.ps1") }

if ($Mode -eq "commit") {
    Write-Host "commit verification passed"
    exit 0
}

Require-Command "goreleaser" "release verification"

$workDir = Join-Path ([System.IO.Path]::GetTempPath()) ("wtp-verify-" + [System.Guid]::NewGuid().ToString())
$config = Join-Path $workDir "goreleaser.yaml"
$dist = Join-Path $workDir "dist"
$assetDirectory = Join-Path $workDir "assets"
$snapshotVersion = "0.0.0-verify"

function Find-ReleaseAsset([string] $Name) {
    $direct = Join-Path $dist $Name
    if (Test-Path -LiteralPath $direct -PathType Leaf) {
        return (Resolve-Path -LiteralPath $direct).Path
    }

    $archiveName = [System.IO.Path]::GetFileNameWithoutExtension($Name)
    $binaryName = if ($Name.EndsWith(".exe")) { "wtp.exe" } else { "wtp" }
    $matches = @(
        Get-ChildItem -LiteralPath $dist -Recurse -File |
            Where-Object {
                $_.Name -eq $binaryName -and
                $_.Directory.Name.StartsWith($archiveName + "_", [System.StringComparison]::Ordinal)
            }
    )
    if ($matches.Count -ne 1) {
        Fail "expected one $Name under $dist, found $($matches.Count)"
    }
    return $matches[0].FullName
}

function Copy-ReleaseAssets {
    New-Item -ItemType Directory -Force -Path $assetDirectory | Out-Null
    $assets = @(
        "wtp_darwin_amd64",
        "wtp_darwin_arm64",
        "wtp_linux_amd64",
        "wtp_linux_arm64",
        "wtp_windows_amd64.exe",
        "wtp_windows_arm64.exe",
        "checksums.txt"
    )
    foreach ($asset in $assets) {
        Copy-Item -LiteralPath (Find-ReleaseAsset $asset) -Destination (Join-Path $assetDirectory $asset)
    }
}

try {
    New-Item -ItemType Directory -Force -Path $workDir | Out-Null
    Copy-Item -LiteralPath (Join-Path $repoRoot ".goreleaser.yaml") -Destination $config
    Add-Content -LiteralPath $config -Value ("`ndist: {0}" -f $dist)

    $previousSnapshotVersion = $env:WTP_QA_SNAPSHOT_VERSION
    $previousReleaseDist = $env:WTP_RELEASE_DIST
    $env:WTP_QA_SNAPSHOT_VERSION = $snapshotVersion
    try {
        Invoke-Checked "goreleaser check" { & goreleaser check --config $config }
        Invoke-Checked "goreleaser release snapshot" { & goreleaser release --snapshot --clean --skip=publish --config $config }
        $env:WTP_RELEASE_DIST = $dist
        Invoke-Checked "release asset contract test" { & go test ./internal/releaseasset -run '^TestGoReleaserSnapshotMatchesPlatformContract$' -count=1 }
    } finally {
        if ($null -eq $previousSnapshotVersion) { Remove-Item Env:WTP_QA_SNAPSHOT_VERSION -ErrorAction SilentlyContinue } else { $env:WTP_QA_SNAPSHOT_VERSION = $previousSnapshotVersion }
        if ($null -eq $previousReleaseDist) { Remove-Item Env:WTP_RELEASE_DIST -ErrorAction SilentlyContinue } else { $env:WTP_RELEASE_DIST = $previousReleaseDist }
    }

    Copy-ReleaseAssets
    Invoke-Checked "PowerShell release QA" { & (Join-Path $PSScriptRoot "release_qa.ps1") -AssetDirectory $assetDirectory -ExpectedVersion $snapshotVersion }
    Write-Host "release verification passed"
} finally {
    if (Test-Path -LiteralPath $workDir) {
        Remove-Item -LiteralPath $workDir -Recurse -Force
    }
}
