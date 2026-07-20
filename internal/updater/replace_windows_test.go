//go:build windows

package updater

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestWindowsReplacementScriptWaitsAndRollsBack(t *testing.T) {
	for _, required := range []string{"Wait-Process", "Move-Item -LiteralPath $target -Destination $backup", "Move-Item -LiteralPath $backup -Destination $target", "Set-Content -LiteralPath $errorPath"} {
		if !strings.Contains(windowsReplacementScript, required) {
			t.Fatalf("Windows replacement script missing %q", required)
		}
	}

	decodedBytes, err := base64.StdEncoding.DecodeString(encodePowerShell(windowsReplacementScript))
	if err != nil {
		t.Fatalf("decode encoded PowerShell: %v", err)
	}
	units := make([]uint16, len(decodedBytes)/2)
	for index := range units {
		units[index] = uint16(decodedBytes[index*2]) | uint16(decodedBytes[index*2+1])<<8
	}
	if decoded := string(utf16.Decode(units)); decoded != windowsReplacementScript {
		t.Fatal("PowerShell script did not survive UTF-16LE base64 encoding")
	}
}

func TestUpdateEnvironmentReplacesInheritedHelperValues(t *testing.T) {
	got := updateEnvironment([]string{"PATH=C:\\Windows", "wtp_update_target=stale"}, map[string]string{
		"WTP_UPDATE_TARGET": `C:\\Program Files\\wtp.exe`,
	})
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "stale") || !strings.Contains(joined, `WTP_UPDATE_TARGET=C:\\Program Files\\wtp.exe`) {
		t.Fatalf("updated environment = %v", got)
	}
}
