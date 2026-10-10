package router

import (
	"os/exec"
	"syscall"
)

// hideWindow keeps gh from flashing a console window on Windows.
func hideWindow(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
