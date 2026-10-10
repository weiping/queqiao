package main

import "syscall"

// detachConsole lets go of the console Task Scheduler opened for
// queqiao.exe, which closes its window.
func detachConsole() {
	syscall.NewLazyDLL("kernel32.dll").NewProc("FreeConsole").Call()
}
