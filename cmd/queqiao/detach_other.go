//go:build !windows

package main

// detachConsole: launchd and systemd give queqiaod no console.
func detachConsole() {}
