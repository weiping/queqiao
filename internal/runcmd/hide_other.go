//go:build !windows

package runcmd

import "os/exec"

func hide(*exec.Cmd) {}
