package runcmd

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000 // CREATE_NO_WINDOW

var getConsoleWindow = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleWindow")

// hasConsole: one run in a terminal lets its children share that console.
var hasConsole = func() bool {
	h, _, _ := getConsoleWindow.Call()
	return h != 0
}

func hide(cmd *exec.Cmd) {
	if hasConsole() {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
