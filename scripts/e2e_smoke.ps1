$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$workDir = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString())
$binaryPath = Join-Path $workDir "wtp.exe"
$testRepo = Join-Path $workDir "repo"

function Fail([string] $Message) {
    throw "smoke test failed: $Message"
}

function Extract-JsonString([string] $Path, [string] $Key) {
    $json = Get-Content -Raw -Path $Path | ConvertFrom-Json
    return $json.$Key
}

function Assert-Contains([string] $Needle, [string] $Path) {
    $content = Get-Content -Raw -Path $Path
    if (-not $content.Contains($Needle)) {
        Fail "expected '$Needle' in $Path"
    }
}

try {
    New-Item -ItemType Directory -Path $workDir | Out-Null
    New-Item -ItemType Directory -Path $testRepo | Out-Null

    & git -C $testRepo init -q
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }

    & go build -o $binaryPath (Join-Path $repoRoot "cmd/wtp")
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }

    $taskAJson = Join-Path $workDir "task_a.json"
    $taskBJson = Join-Path $workDir "task_b.json"
    $listOut = Join-Path $workDir "list.txt"
    $errOut = Join-Path $workDir "error.txt"
    $taskOut = Join-Path $workDir "task.txt"

    Push-Location $testRepo
    try {
        & $binaryPath --json task create `
            --title "Bootstrap provider" `
            --description "Initial task for smoke testing" | Set-Content -NoNewline -Path $taskAJson

        $taskAShortID = Extract-JsonString $taskAJson "shortId"
        if (-not $taskAShortID) {
            Fail "could not extract task A shortId"
        }
        if (-not (Test-Path (Join-Path ".wtp/todo" "$taskAShortID.json"))) {
            Fail "task A filename was not written with shortId"
        }

        & $binaryPath --json task create `
            --title "Follow-up task" `
            --description "Depends on task A" `
            --depends-on $taskAShortID | Set-Content -NoNewline -Path $taskBJson

        $taskBShortID = Extract-JsonString $taskBJson "shortId"
        if (-not $taskBShortID) {
            Fail "could not extract task B shortId"
        }

        & $binaryPath task list | Set-Content -NoNewline -Path $listOut
        Assert-Contains $taskAShortID $listOut
        Assert-Contains $taskBShortID $listOut

        $startSucceeded = $true
        try {
            & $binaryPath task start $taskBShortID --agent Bob 2> $errOut | Out-Null
            if ($LASTEXITCODE -ne 0) {
                $startSucceeded = $false
            }
        } catch {
            $startSucceeded = $false
        }
        if ($startSucceeded) {
            Fail "blocked dependency start unexpectedly succeeded"
        }
        Assert-Contains "blocked by unresolved dependencies" $errOut

        & $binaryPath --json task next --agent Alice | Set-Content -NoNewline -Path $taskOut
        Assert-Contains ('"shortId": "{0}"' -f $taskAShortID) $taskOut
        Assert-Contains '"status": "inProgress"' $taskOut
        Assert-Contains '"assignee": "Alice"' $taskOut

        & $binaryPath task comment $taskAShortID --agent Alice --message "smoke progress" | Out-Null
        & $binaryPath task done $taskAShortID --agent Alice | Out-Null

        & $binaryPath --json --get-next-task --agent Bob | Set-Content -NoNewline -Path $taskOut
        Assert-Contains ('"shortId": "{0}"' -f $taskBShortID) $taskOut
        Assert-Contains '"status": "inProgress"' $taskOut
        Assert-Contains '"assignee": "Bob"' $taskOut

        & $binaryPath task pause $taskBShortID | Out-Null
        & $binaryPath --json task next --agent Bob | Set-Content -NoNewline -Path $taskOut
        Assert-Contains ('"shortId": "{0}"' -f $taskBShortID) $taskOut
        Assert-Contains '"status": "inProgress"' $taskOut

        & $binaryPath task done $taskBShortID --agent Bob | Out-Null
        & $binaryPath export --out exported | Out-Null

        if (-not (Test-Path ".wtp/meta/index.json")) {
            Fail "missing index file"
        }

        $taskAID = Extract-JsonString $taskAJson "id"
        $taskBID = Extract-JsonString $taskBJson "id"
        if (-not (Test-Path (Join-Path "exported" "$taskAID.json"))) {
            Fail "missing export for task A"
        }
        if (-not (Test-Path (Join-Path "exported" "$taskBID.json"))) {
            Fail "missing export for task B"
        }
    } finally {
        Pop-Location
    }

    Write-Host "smoke test passed"
} finally {
    if (Test-Path $workDir) {
        Remove-Item -Recurse -Force $workDir
    }
}
