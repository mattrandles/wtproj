//go:build windows

package prerelease

import (
	"fmt"
	"os/exec"
)

func prepareCommand(command *exec.Cmd) {}

func terminateCommand(command *exec.Cmd) {
	if command.Process == nil {
		return
	}
	_ = exec.Command("taskkill", "/PID", fmt.Sprint(command.Process.Pid), "/T", "/F").Run()
}
