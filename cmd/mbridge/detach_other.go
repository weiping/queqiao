//go:build !windows

package main

// detachConsole: launchd and systemd give mbridge no console.
func detachConsole() {}
