// Package runcmd makes the commands mbridge runs (magpie's CLI, gh), as
// magpie's internal/proc does for magpie: a context that ends closes the
// command's pipes shortly after, and on Windows a mbridge with no console
// (started by the scheduled task) starts them without a window of their own.
package runcmd

import (
	"context"
	"os/exec"
	"time"
)

// WaitDelay is how long a command's pipes stay open after its context ends.
const WaitDelay = 2 * time.Second

// CommandContext is exec.CommandContext with WaitDelay and, on Windows, no
// window of its own.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = WaitDelay
	hide(cmd)
	return cmd
}
