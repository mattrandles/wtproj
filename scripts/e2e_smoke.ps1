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
    $json = (Read-TextFile $Path) | ConvertFrom-Json
    return $json.$Key
}

function Read-TextFile([string] $Path) {
    if (-not (Test-Path -LiteralPath $Path)) {
        Fail "missing expected output file $Path"
    }
    return [System.IO.File]::ReadAllText($Path)
}

function Write-NativeOutput([object[]] $Output, [string] $Path) {
    $text = ($Output | ForEach-Object { $_.ToString() }) -join [Environment]::NewLine
    [System.IO.File]::WriteAllText($Path, $text)
}

function Assert-Contains([string] $Needle, [string] $Path) {
    $content = Read-TextFile $Path
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
        & $binaryPath help | Out-Null
        if ($LASTEXITCODE -ne 0) {
            exit $LASTEXITCODE
        }
        if (Test-Path ".wtp") {
            Fail "help initialized .wtp storage"
        }
        & $binaryPath schema | Out-Null
        if ($LASTEXITCODE -ne 0) {
            exit $LASTEXITCODE
        }
        if (Test-Path ".wtp") {
            Fail "schema initialized .wtp storage"
        }

        $invalidShowOutput = @(& $binaryPath task show wtp-9999 --unknown 2>&1)
        $invalidShowExitCode = $LASTEXITCODE
        Write-NativeOutput $invalidShowOutput $errOut
        if ($invalidShowExitCode -ne 1) {
            Fail "unknown show option exited $invalidShowExitCode, want 1"
        }
        Assert-Contains 'unknown option "--unknown"' $errOut

        & $binaryPath --json task create `
            --title "Bootstrap provider" `
            --description "Initial task for smoke testing" > $taskAJson
        if ($LASTEXITCODE -ne 0) {
            exit $LASTEXITCODE
        }

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
            --depends-on $taskAShortID > $taskBJson
        if ($LASTEXITCODE -ne 0) {
            exit $LASTEXITCODE
        }

        $taskBShortID = Extract-JsonString $taskBJson "shortId"
        if (-not $taskBShortID) {
            Fail "could not extract task B shortId"
        }

        & $binaryPath task list > $listOut
        if ($LASTEXITCODE -ne 0) {
            exit $LASTEXITCODE
        }
        Assert-Contains $taskAShortID $listOut
        Assert-Contains $taskBShortID $listOut

        $startOutput = @(& $binaryPath task start $taskBShortID --agent Bob 2>&1)
        $startExitCode = $LASTEXITCODE
        Write-NativeOutput $startOutput $errOut
        if ($startExitCode -eq 0) {
            Fail "blocked dependency start unexpectedly succeeded"
        }
        Assert-Contains "blocked by unresolved dependencies" $errOut

        & $binaryPath --json task next --agent Alice > $taskOut
        if ($LASTEXITCODE -ne 0) {
            exit $LASTEXITCODE
        }
        Assert-Contains ('"shortId": "{0}"' -f $taskAShortID) $taskOut
        Assert-Contains '"status": "inProgress"' $taskOut
        Assert-Contains '"assignee": "Alice"' $taskOut

        & $binaryPath task comment $taskAShortID --agent Alice --message "smoke progress" | Out-Null
        & $binaryPath task done $taskAShortID --agent Alice | Out-Null

        & $binaryPath --json --get-next-task --agent Bob > $taskOut
        if ($LASTEXITCODE -ne 0) {
            exit $LASTEXITCODE
        }
        Assert-Contains ('"shortId": "{0}"' -f $taskBShortID) $taskOut
        Assert-Contains '"status": "inProgress"' $taskOut
        Assert-Contains '"assignee": "Bob"' $taskOut

        & $binaryPath task pause $taskBShortID | Out-Null
        & $binaryPath --json task next --agent Bob > $taskOut
        if ($LASTEXITCODE -ne 0) {
            exit $LASTEXITCODE
        }
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
