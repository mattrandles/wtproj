//go:build windows

package updater

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf16"
)

// Windows keeps a running executable locked. A detached, encoded PowerShell
// helper waits for this process to exit, moves the old binary aside, installs
// the verified staged file, and restores the old binary if installation fails.
const windowsReplacementScript = `$ErrorActionPreference = 'Stop'
$source = $env:WTP_UPDATE_SOURCE
$target = $env:WTP_UPDATE_TARGET
$backup = $env:WTP_UPDATE_BACKUP
$errorPath = $env:WTP_UPDATE_ERROR
$processId = [int]$env:WTP_UPDATE_PID
try {
    Wait-Process -Id $processId -ErrorAction SilentlyContinue
    if (Test-Path -LiteralPath $errorPath) { Remove-Item -Force -LiteralPath $errorPath }
    Move-Item -LiteralPath $target -Destination $backup
    try {
        Move-Item -LiteralPath $source -Destination $target
    }
    catch {
        if ((Test-Path -LiteralPath $backup) -and -not (Test-Path -LiteralPath $target)) {
            Move-Item -LiteralPath $backup -Destination $target
        }
        throw
    }
    Remove-Item -Force -LiteralPath $backup
}
catch {
    Set-Content -LiteralPath $errorPath -Value $_.Exception.Message
}`

func replaceExecutable(source, target string) (bool, error) {
	encodedScript := encodePowerShell(windowsReplacementScript)
	command := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-EncodedCommand", encodedScript)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	command.Env = updateEnvironment(os.Environ(), map[string]string{
		"WTP_UPDATE_SOURCE": source,
		"WTP_UPDATE_TARGET": target,
		"WTP_UPDATE_BACKUP": target + ".wtp-update-backup",
		"WTP_UPDATE_ERROR":  target + ".wtp-update-error.txt",
		"WTP_UPDATE_PID":    strconv.Itoa(os.Getpid()),
	})
	if err := command.Start(); err != nil {
		return false, fmt.Errorf("start Windows replacement helper: %w", err)
	}
	// The helper has already inherited everything it needs. Failure to release
	// our local process handle must not remove the staged file out from under it.
	_ = command.Process.Release()
	return true, nil
}

func encodePowerShell(script string) string {
	encoded := utf16.Encode([]rune(script))
	data := make([]byte, len(encoded)*2)
	for index, value := range encoded {
		data[index*2] = byte(value)
		data[index*2+1] = byte(value >> 8)
	}
	return base64.StdEncoding.EncodeToString(data)
}

func updateEnvironment(current []string, replacements map[string]string) []string {
	result := make([]string, 0, len(current)+len(replacements))
	for _, entry := range current {
		name, _, _ := strings.Cut(entry, "=")
		if _, replaced := replacements[strings.ToUpper(name)]; !replaced {
			result = append(result, entry)
		}
	}
	for name, value := range replacements {
		result = append(result, name+"="+value)
	}
	return result
}
