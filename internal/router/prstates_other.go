//go:build !windows

package router

import "os/exec"

func hideWindow(*exec.Cmd) {}
