package runcmd

import (
	"context"
	"testing"
)

// A command started through runcmd closes its pipes shortly after its
// context ends, even when a grandchild still holds them (magpie's proc
// learned this in #123: a CLI that is a script running node).
func TestCommandContextSetsWaitDelay(t *testing.T) {
	cmd := CommandContext(context.Background(), "true")
	if cmd.WaitDelay != WaitDelay || WaitDelay <= 0 {
		t.Fatalf("WaitDelay = %v, want %v", cmd.WaitDelay, WaitDelay)
	}
}
